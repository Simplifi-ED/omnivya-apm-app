package plugin

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Default on-demand unit rates for Capacity USD estimates (rate-based, not billing).
const (
	defaultCPUHourUSD       = 0.0316
	defaultMemoryGiBHourUSD = 0.0042
	hoursPerMonth           = 730.0
	bytesPerGiB             = 1024.0 * 1024.0 * 1024.0
)

// CapacityConfidence tiers for Goldilocks recommendations by lookback window.
const (
	CapacityConfidenceLow    = "low"
	CapacityConfidenceMedium = "medium"
	CapacityConfidenceHigh   = "high"
)

// capacitySafetyMargin widens every percentile and every peak floor, so "15%
// above what we measured" means the same thing in every bound.
const capacitySafetyMargin = 0.15

// The window is clamped before it becomes a confidence multiplier. Below one day
// the multiplier would explode; above thirty days it has saturated.
const (
	capacityMinConfidenceDays = 1.0
	capacityMaxConfidenceDays = 30.0
)

func capacityCostRates(settings queriesCapacityRates) (cpuHour, memGiBHour float64) {
	cpuHour = defaultCPUHourUSD
	memGiBHour = defaultMemoryGiBHourUSD
	if settings.CPUHourUSD > 0 {
		cpuHour = settings.CPUHourUSD
	}
	if settings.MemoryGiBHour > 0 {
		memGiBHour = settings.MemoryGiBHour
	}
	return cpuHour, memGiBHour
}

// queriesCapacityRates is a narrow interface so capacity_math stays free of
// queries package cycles in unit tests.
type queriesCapacityRates struct {
	CPUHourUSD    float64
	MemoryGiBHour float64
}

func monthlyUSD(cpuCores, memBytes, cpuHourUSD, memGiBHourUSD float64) float64 {
	memGiB := memBytes / bytesPerGiB
	return (cpuCores*cpuHourUSD + memGiB*memGiBHourUSD) * hoursPerMonth
}

func capacityConfidence(days float64) string {
	switch {
	case days < 3:
		return CapacityConfidenceLow
	case days < 7:
		return CapacityConfidenceMedium
	default:
		return CapacityConfidenceHigh
	}
}

// capacityConfidenceDays clamps the lookback window to the range the confidence
// multipliers are defined for, in days.
func capacityConfidenceDays(window time.Duration) float64 {
	d := window.Hours() / 24
	return math.Min(capacityMaxConfidenceDays, math.Max(capacityMinConfidenceDays, d))
}

// capacityConfidenceMultipliers returns the VPA recommender's confidence
// widening. Short history widens the upper bound; the lower bound is effectively
// neutral, because a request that shrinks with less data is how you get an
// unschedulable workload.
func capacityConfidenceMultipliers(days float64) (lower, upper float64) {
	upper = 1 + 1/days
	base := 1 + 0.001/days
	lower = 1 / (base * base)
	return lower, upper
}

// UsageQuantiles is one resource's window summary, in that resource's native
// unit: cores for CPU (5m rate), bytes for memory (working-set gauge).
type UsageQuantiles struct {
	P50     float64
	P90     float64
	P95     float64
	Max     float64
	Present bool
}

// monotoneQuantiles enforces 0 <= P50 <= P90 <= P95 <= Max and maps NaN to 0, so
// a partial or corrupt observation set can never produce an inverted triple. It
// clamps upward, taking the running max, so garbage input over-estimates rather
// than under-estimates.
func monotoneQuantiles(p50, p90, p95, max float64) UsageQuantiles {
	p50, p90, p95, max = safeFloat(p50), safeFloat(p90), safeFloat(p95), safeFloat(max)
	p90 = math.Max(p90, p50)
	p95 = math.Max(p95, p90)
	max = math.Max(max, p95)
	return UsageQuantiles{P50: p50, P90: p90, P95: p95, Max: max, Present: true}
}

// ResourceBounds is the VPA-shaped triple for one resource, in that resource's
// native unit, already quantized. Burstable reads Lower as the request and Upper
// as the limit. Guaranteed reads Target for both.
type ResourceBounds struct {
	Lower  float64 `json:"lower"`
	Target float64 `json:"target"`
	Upper  float64 `json:"upper"`
}

// quantized applies the resource's ceiling function to all three bounds.
// Ceiling is monotone, so lower <= target <= upper survives.
func (b ResourceBounds) quantized(f func(float64) float64) ResourceBounds {
	return ResourceBounds{Lower: f(b.Lower), Target: f(b.Target), Upper: f(b.Upper)}
}

// WorkloadBounds holds one entry per observed resource. A nil field means the
// window had no observation for that resource, which is what suppresses the
// resource's keys in the YAML.
type WorkloadBounds struct {
	CPU *ResourceBounds `json:"cpu,omitempty"`
	Mem *ResourceBounds `json:"mem,omitempty"`
}

// Target floors. Memory Target becomes a limit under Guaranteed, and a memory
// limit under the observed working set is an OOMKill. CPU Target is a limit too,
// but a short CPU limit throttles rather than kills, and flooring it would make
// Guaranteed identical to the peak-based Burstable profile for no gain.
const (
	cpuTargetIgnoresPeak  = false
	memTargetFloorsAtPeak = true
)

// capacityResourceBounds is the one place the lower/target/upper triple is
// computed.
func capacityResourceBounds(q UsageQuantiles, lowerMult, upperMult float64, targetFloorsAtPeak bool) ResourceBounds {
	m := 1 + capacitySafetyMargin

	lower := q.P50 * m * lowerMult
	// Upper never sits below an observed peak. The margin is applied after the
	// max, so "15% above what we measured" holds whether the percentile or the
	// peak won.
	upper := math.Max(q.P95*upperMult, q.Max) * m
	target := q.P90 * m
	if targetFloorsAtPeak {
		target = math.Max(target, q.Max*m)
	}
	target = math.Min(target, upper)
	lower = math.Min(lower, target)
	return ResourceBounds{Lower: lower, Target: target, Upper: upper}
}

// applyThrottleGuard refuses to shrink a CPU limit that is already throttling.
// Both Target and Upper take the floor, because Guaranteed's req = lim would
// otherwise land below a limit we know is too small. Lower still drops to the
// p50, so the saving stays available through the request.
func applyThrottleGuard(b ResourceBounds, throttledRate float64, currentCPULim *float64) ResourceBounds {
	if throttledRate <= 0 || currentCPULim == nil {
		return b
	}
	floor := ceilCPUCores(*currentCPULim * (1 + capacitySafetyMargin))
	b.Upper = math.Max(b.Upper, floor)
	b.Target = math.Max(b.Target, floor)
	return b
}

// burstableFromBounds is Goldilocks' burstable profile: the lower bound as the
// request, the upper bound as the limit, for both resources.
func burstableFromBounds(b WorkloadBounds) WorkloadResources {
	var r WorkloadResources
	if b.CPU != nil {
		lim := b.CPU.Upper
		r.CPUReq, r.CPULim = b.CPU.Lower, &lim
	}
	if b.Mem != nil {
		r.MemReq, r.MemLim = b.Mem.Lower, b.Mem.Upper
	}
	return r
}

// guaranteedFromBounds is Goldilocks' guaranteed profile: the target as both
// request and limit. Assigning from one local is what makes req == lim exactly.
func guaranteedFromBounds(b WorkloadBounds) WorkloadResources {
	var r WorkloadResources
	if b.CPU != nil {
		v := b.CPU.Target
		r.CPUReq, r.CPULim = v, &v
	}
	if b.Mem != nil {
		r.MemReq, r.MemLim = b.Mem.Target, b.Mem.Target
	}
	return r
}

// ceilCPUCores rounds up to the next 10 millicores (0.01 cores), with a 10m floor.
// The epsilon absorbs float64 representation error: 0.07 is slightly above 7/100,
// so a bare math.Ceil turns it into 8 millicores of headroom that no one asked for.
func ceilCPUCores(cores float64) float64 {
	const floor = 0.01 // 10m
	if cores <= 0 {
		return floor
	}
	rounded := math.Ceil(cores*100-1e-9) / 100
	if rounded < floor {
		return floor
	}
	return rounded
}

// ceilMemoryBytes rounds up to the next 16MiB, with a 32MiB floor.
func ceilMemoryBytes(bytes float64) float64 {
	const floor = 32 * 1024 * 1024
	const step = 16 * 1024 * 1024
	if bytes <= 0 {
		return floor
	}
	rounded := math.Ceil(bytes/step) * step
	if rounded < floor {
		return floor
	}
	return rounded
}

func formatCPUCores(cores float64) string {
	if cores < 1 {
		return fmt.Sprintf("%dm", int(math.Round(cores*1000)))
	}
	if cores == math.Trunc(cores) {
		return fmt.Sprintf("%d", int(cores))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", cores), "0"), ".")
}

func formatMemoryBytes(bytes float64) string {
	mi := bytes / (1024 * 1024)
	if mi >= 1024 && math.Mod(mi, 1024) == 0 {
		return fmt.Sprintf("%dGi", int(mi/1024))
	}
	return fmt.Sprintf("%dMi", int(math.Round(mi)))
}

// capacityResourcesYAML renders one container resources fragment. A zero request
// means the resource was never observed, which is why absent resources need no
// separate flag: quantization guarantees a real recommendation is never zero.
func capacityResourcesYAML(r WorkloadResources) string {
	includeCPU := r.CPUReq > 0 || r.CPULim != nil
	includeMem := r.MemReq > 0 || r.MemLim > 0
	if !includeCPU && !includeMem {
		return ""
	}
	var b strings.Builder
	b.WriteString("resources:\n")
	b.WriteString("  requests:\n")
	if includeCPU {
		fmt.Fprintf(&b, "    cpu: %q\n", formatCPUCores(r.CPUReq))
	}
	if includeMem {
		fmt.Fprintf(&b, "    memory: %q\n", formatMemoryBytes(r.MemReq))
	}
	writeCPULim := includeCPU && r.CPULim != nil
	writeMemLim := includeMem
	if writeCPULim || writeMemLim {
		b.WriteString("  limits:\n")
		if writeCPULim {
			fmt.Fprintf(&b, "    cpu: %q\n", formatCPUCores(*r.CPULim))
		}
		if writeMemLim {
			fmt.Fprintf(&b, "    memory: %q\n", formatMemoryBytes(r.MemLim))
		}
	}
	return b.String()
}

// stripReplicaSetHash maps a ReplicaSet name to its Deployment by removing the
// trailing 8–10 character pod-template-hash suffix (e.g. api-7d9f8c6b5 → api).
func stripReplicaSetHash(rsName string) string {
	idx := strings.LastIndex(rsName, "-")
	if idx <= 0 {
		return rsName
	}
	suffix := rsName[idx+1:]
	if len(suffix) < 5 || len(suffix) > 10 {
		return rsName
	}
	for _, c := range suffix {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return rsName
		}
	}
	return rsName[:idx]
}

func qosClass(cpuReq, cpuLim, memReq, memLim float64) string {
	hasAll := cpuReq > 0 && cpuLim > 0 && memReq > 0 && memLim > 0
	equal := hasAll && cpuReq == cpuLim && memReq == memLim
	if equal {
		return "Guaranteed"
	}
	if cpuReq > 0 || memReq > 0 {
		return "Burstable"
	}
	return "BestEffort"
}

// Efficiency band thresholds (Grafana Efficiency-style population cards).
const (
	capacityBandUnderMax = 0.40
	capacityBandOverMin  = 0.80
)

// CapacityBandClass is under | ok | over | "" when capacity is missing.
type CapacityBandClass string

// Band class labels used by classifyUsedRatio / population aggregation.
const (
	CapacityBandUnder CapacityBandClass = "under"
	CapacityBandOK    CapacityBandClass = "ok"
	CapacityBandOver  CapacityBandClass = "over"
)

// classifyUsedRatio maps used/capacity into under/ok/over.
// Over when ratio > 80% or used > capacity. Under when ratio < 40%.
func classifyUsedRatio(used, capacity float64) CapacityBandClass {
	if capacity <= 0 || math.IsNaN(used) || math.IsNaN(capacity) {
		return ""
	}
	r := used / capacity
	if used > capacity || r > capacityBandOverMin {
		return CapacityBandOver
	}
	if r < capacityBandUnderMax {
		return CapacityBandUnder
	}
	return CapacityBandOK
}

// EfficiencyBandFractions is under/ok/over share of a population (0..1).
type EfficiencyBandFractions struct {
	Under float64
	OK    float64
	Over  float64
}

func bandFractionsFromCounts(under, ok, over int) EfficiencyBandFractions {
	total := under + ok + over
	if total == 0 {
		return EfficiencyBandFractions{}
	}
	t := float64(total)
	return EfficiencyBandFractions{
		Under: roundTo(float64(under)/t, 4),
		OK:    roundTo(float64(ok)/t, 4),
		Over:  roundTo(float64(over)/t, 4),
	}
}

func accumulateBandClass(c CapacityBandClass, under, ok, over *int) {
	switch c {
	case CapacityBandUnder:
		*under++
	case CapacityBandOK:
		*ok++
	case CapacityBandOver:
		*over++
	}
}
