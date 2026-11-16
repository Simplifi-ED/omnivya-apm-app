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

func capacityConfidence(window time.Duration) string {
	switch {
	case window < 24*time.Hour:
		return CapacityConfidenceLow
	case window < 7*24*time.Hour:
		return CapacityConfidenceMedium
	default:
		return CapacityConfidenceHigh
	}
}

// ceilCPUCores rounds up to the next 10 millicores (0.01 cores), with a 10m floor.
func ceilCPUCores(cores float64) float64 {
	const floor = 0.01 // 10m
	if cores <= 0 {
		return floor
	}
	rounded := math.Ceil(cores*100) / 100
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

func burstableRecommendation(cpuP95, memP95, memMax float64) (cpuReq, memReq, memLim float64) {
	cpuReq = ceilCPUCores(cpuP95 * 1.15)
	memReq = ceilMemoryBytes(memP95 * 1.15)
	memLim = ceilMemoryBytes(memMax * 1.20)
	return cpuReq, memReq, memLim
}

func guaranteedRecommendation(cpuMax, memMax float64) (cpuReq, cpuLim, memReq, memLim float64) {
	cpu := ceilCPUCores(cpuMax * 1.15)
	mem := ceilMemoryBytes(memMax * 1.20)
	return cpu, cpu, mem, mem
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

func resourcesYAMLPatch(cpuReq float64, cpuLim *float64, memReq, memLim float64) string {
	var b strings.Builder
	b.WriteString("resources:\n")
	b.WriteString("  requests:\n")
	fmt.Fprintf(&b, "    cpu: %q\n", formatCPUCores(cpuReq))
	fmt.Fprintf(&b, "    memory: %q\n", formatMemoryBytes(memReq))
	b.WriteString("  limits:\n")
	if cpuLim != nil {
		fmt.Fprintf(&b, "    cpu: %q\n", formatCPUCores(*cpuLim))
	}
	fmt.Fprintf(&b, "    memory: %q\n", formatMemoryBytes(memLim))
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
