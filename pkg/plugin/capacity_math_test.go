package plugin

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nais/grafana-otel-plugin/pkg/plugin/chquery"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/queries"
)

func TestClassifyUsedRatio(t *testing.T) {
	t.Parallel()
	if classifyUsedRatio(0.1, 1) != CapacityBandUnder {
		t.Fatal("under")
	}
	if classifyUsedRatio(0.5, 1) != CapacityBandOK {
		t.Fatal("ok")
	}
	if classifyUsedRatio(0.9, 1) != CapacityBandOver {
		t.Fatal("over")
	}
	if classifyUsedRatio(1.2, 1) != CapacityBandOver {
		t.Fatal("over request")
	}
	if classifyUsedRatio(1, 0) != "" {
		t.Fatal("no capacity")
	}
}

func TestBandFractionsFromCounts(t *testing.T) {
	t.Parallel()
	f := bandFractionsFromCounts(2, 2, 0)
	if f.Under != 0.5 || f.OK != 0.5 || f.Over != 0 {
		t.Fatalf("%+v", f)
	}
	if bandFractionsFromCounts(0, 0, 0).Under != 0 {
		t.Fatal("empty")
	}
}

func TestBuildCapacityPopulationAndNodes(t *testing.T) {
	t.Parallel()
	a := &App{}
	fsUsage := 50.0
	fsLimit := 100.0
	rm := map[string][]queries.PromResult{
		"sumCpuUsage": {{Value: queries.NewPromValue(0, "1")}},
		"sumCpuReq":   {{Value: queries.NewPromValue(0, "4")}},
		"sumMemUsage": {{Value: queries.NewPromValue(0, "1e8")}},
		"sumMemReq":   {{Value: queries.NewPromValue(0, "4e8")}},
		"nsCpuUsage": {
			{Metric: map[string]string{"namespace": "alpha", "k8s_cluster_name": "prod"}, Value: queries.NewPromValue(0, "0.3")},
		},
		"nsCpuReq": {
			{Metric: map[string]string{"namespace": "alpha", "k8s_cluster_name": "prod"}, Value: queries.NewPromValue(0, "1")},
		},
		"nsMemUsage": {
			{Metric: map[string]string{"namespace": "alpha", "k8s_cluster_name": "prod"}, Value: queries.NewPromValue(0, "1e8")},
		},
		"nsMemReq": {
			{Metric: map[string]string{"namespace": "alpha", "k8s_cluster_name": "prod"}, Value: queries.NewPromValue(0, "4e8")},
		},
		"nodeCpuUsage": {
			{Metric: map[string]string{"node": "n1"}, Value: queries.NewPromValue(0, "1")},
		},
		"nodeCpuAlloc": {
			{Metric: map[string]string{"node": "n1"}, Value: queries.NewPromValue(0, "4")},
		},
		"nodeMemUsage": {
			{Metric: map[string]string{"node": "n1"}, Value: queries.NewPromValue(0, "1e8")},
		},
		"nodeMemAlloc": {
			{Metric: map[string]string{"node": "n1"}, Value: queries.NewPromValue(0, "4e8")},
		},
		"nodeFsUsage": {
			{Metric: map[string]string{"node": "n1"}, Value: queries.NewPromValue(0, fmt.Sprintf("%v", fsUsage))},
		},
		"nodeFsLimit": {
			{Metric: map[string]string{"node": "n1"}, Value: queries.NewPromValue(0, fmt.Sprintf("%v", fsLimit))},
		},
	}
	out := a.aggregateCapacity(rm)
	if !out.Available {
		t.Fatal("expected available")
	}
	if out.Population.Namespaces.Total != 1 || out.Population.Nodes.Total != 1 {
		t.Fatalf("population totals: %+v", out.Population)
	}
	if out.Population.Namespaces.Bands.CPU.Under <= 0 {
		t.Fatalf("ns cpu under band: %+v", out.Population.Namespaces.Bands.CPU)
	}
	if len(out.Nodes) != 1 || out.Nodes[0].StorageRatio == nil || *out.Nodes[0].StorageRatio != 0.5 {
		t.Fatalf("nodes storage: %+v", out.Nodes)
	}
	if out.Population.Nodes.Bands.Storage == nil || out.Population.Nodes.Bands.Storage.OK <= 0 {
		t.Fatalf("node storage band (50%% → ok): %+v", out.Population.Nodes.Bands.Storage)
	}
	if out.Population.Namespaces.Bands.Storage != nil {
		t.Fatal("namespace storage must stay null without PVC series")
	}
}

func TestCeilCPUCores(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want float64
	}{
		{0, 0.01},
		{0.005, 0.01},
		{0.123, 0.13},
		{1.0, 1.0},
		{1.001, 1.01},
		// 0.07 is slightly above 7/100 in float64, so a bare math.Ceil yields 8.
		{0.07, 0.07},
		{0.071, 0.08},
	}
	for _, tc := range cases {
		if got := ceilCPUCores(tc.in); got != tc.want {
			t.Errorf("ceilCPUCores(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCeilMemoryBytes(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	if got := ceilMemoryBytes(0); got != 32*mi {
		t.Fatalf("floor: got %v", got)
	}
	if got := ceilMemoryBytes(33 * mi); got != 48*mi {
		t.Fatalf("round up 33Mi → 48Mi: got %v", got)
	}
}

func TestCapacityConfidenceDays(t *testing.T) {
	t.Parallel()
	if got := capacityConfidenceDays(2 * time.Hour); got != 1 {
		t.Fatalf("2h must clamp to 1 day: %v", got)
	}
	if got := capacityConfidenceDays(365 * 24 * time.Hour); got != 30 {
		t.Fatalf("365d must clamp to 30 days: %v", got)
	}
	if got := capacityConfidenceDays(7 * 24 * time.Hour); got != 7 {
		t.Fatalf("7d: %v", got)
	}
}

func TestCapacityConfidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		days float64
		want string
	}{
		{2.9, CapacityConfidenceLow},
		{3.0, CapacityConfidenceMedium},
		{6.99, CapacityConfidenceMedium},
		{7.0, CapacityConfidenceHigh},
	}
	for _, tc := range cases {
		if got := capacityConfidence(tc.days); got != tc.want {
			t.Errorf("capacityConfidence(%v) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

func TestCapacityConfidenceMultipliers(t *testing.T) {
	t.Parallel()
	lower, upper := capacityConfidenceMultipliers(7)
	if math.Abs(upper-8.0/7.0) > 1e-12 {
		t.Fatalf("upperMult at 7d = %v, want %v", upper, 8.0/7.0)
	}
	base := 1 + 0.001/7
	if want := 1 / (base * base); math.Abs(lower-want) > 1e-12 {
		t.Fatalf("lowerMult at 7d = %v, want %v", lower, want)
	}
}

func TestMonotoneQuantiles(t *testing.T) {
	t.Parallel()
	q := monotoneQuantiles(0.5, 0.1, 0.2, 0.05)
	if q.P50 != 0.5 || q.P90 != 0.5 || q.P95 != 0.5 || q.Max != 0.5 {
		t.Fatalf("garbage input must saturate every quantile at the max: %+v", q)
	}
	q = monotoneQuantiles(math.NaN(), 0.1, math.NaN(), 0.3)
	if q.P50 != 0 || q.P90 != 0.1 || q.P95 != 0.1 || q.Max != 0.3 {
		t.Fatalf("NaN must map to zero, then clamp upward: %+v", q)
	}
}

// TestCapacityResourceBoundsSpiky walks the spiky service from the design
// through every window length. The peak floor pins Upper at 370m for every
// window, and the grafted target (p90 x margin, no confidence widening) pins
// Target at 140m, so only the tier moves with the picker.
func TestCapacityResourceBoundsSpiky(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	cases := []struct {
		name     string
		window   time.Duration
		wantCPU  ResourceBounds
		wantMem  ResourceBounds
		wantTier string
	}{
		{
			name:     "7d",
			window:   7 * 24 * time.Hour,
			wantCPU:  ResourceBounds{Lower: 0.10, Target: 0.14, Upper: 0.37},
			wantMem:  ResourceBounds{Lower: 80 * mi, Target: 304 * mi, Upper: 304 * mi},
			wantTier: CapacityConfidenceHigh,
		},
		{
			name:     "2d",
			window:   48 * time.Hour,
			wantCPU:  ResourceBounds{Lower: 0.10, Target: 0.14, Upper: 0.37},
			wantMem:  ResourceBounds{Lower: 80 * mi, Target: 304 * mi, Upper: 304 * mi},
			wantTier: CapacityConfidenceLow,
		},
		{
			name:     "1h clamped to 1d",
			window:   time.Hour,
			wantCPU:  ResourceBounds{Lower: 0.10, Target: 0.14, Upper: 0.37},
			wantMem:  ResourceBounds{Lower: 80 * mi, Target: 304 * mi, Upper: 304 * mi},
			wantTier: CapacityConfidenceLow,
		},
		{
			name:     "60d clamped to 30d",
			window:   60 * 24 * time.Hour,
			wantCPU:  ResourceBounds{Lower: 0.10, Target: 0.14, Upper: 0.37},
			wantMem:  ResourceBounds{Lower: 80 * mi, Target: 304 * mi, Upper: 304 * mi},
			wantTier: CapacityConfidenceHigh,
		},
	}
	cpuQ := UsageQuantiles{P50: 0.080, P90: 0.120, P95: 0.140, Max: 0.320, Present: true}
	memQ := UsageQuantiles{P50: 64 * mi, P90: 96 * mi, P95: 112 * mi, Max: 256 * mi, Present: true}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			days := capacityConfidenceDays(tc.window)
			if got := capacityConfidence(days); got != tc.wantTier {
				t.Fatalf("tier = %q, want %q", got, tc.wantTier)
			}
			lowerMult, upperMult := capacityConfidenceMultipliers(days)

			cpu := capacityResourceBounds(cpuQ, lowerMult, upperMult, cpuTargetIgnoresPeak).quantized(ceilCPUCores)
			if cpu != tc.wantCPU {
				t.Fatalf("cpu bounds = %+v, want %+v", cpu, tc.wantCPU)
			}
			mem := capacityResourceBounds(memQ, lowerMult, upperMult, memTargetFloorsAtPeak).quantized(ceilMemoryBytes)
			if mem != tc.wantMem {
				t.Fatalf("mem bounds = %+v, want %+v", mem, tc.wantMem)
			}
		})
	}
}

func TestCapacityResourceBoundsFlat(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	cpuQ := UsageQuantiles{P50: 0.400, P90: 0.550, P95: 0.600, Max: 0.620, Present: true}
	memQ := UsageQuantiles{P50: 200 * mi, P90: 260 * mi, P95: 300 * mi, Max: 320 * mi, Present: true}

	cases := []struct {
		window  time.Duration
		wantCPU ResourceBounds
		wantMem ResourceBounds
	}{
		{
			window:  48 * time.Hour,
			wantCPU: ResourceBounds{Lower: 0.46, Target: 0.64, Upper: 1.04},
			wantMem: ResourceBounds{Lower: 240 * mi, Target: 368 * mi, Upper: 528 * mi},
		},
		{
			window:  30 * 24 * time.Hour,
			wantCPU: ResourceBounds{Lower: 0.46, Target: 0.64, Upper: 0.72},
			wantMem: ResourceBounds{Lower: 240 * mi, Target: 368 * mi, Upper: 368 * mi},
		},
	}
	for _, tc := range cases {
		days := capacityConfidenceDays(tc.window)
		lowerMult, upperMult := capacityConfidenceMultipliers(days)
		cpu := capacityResourceBounds(cpuQ, lowerMult, upperMult, cpuTargetIgnoresPeak).quantized(ceilCPUCores)
		if cpu != tc.wantCPU {
			t.Errorf("%v cpu bounds = %+v, want %+v", tc.window, cpu, tc.wantCPU)
		}
		mem := capacityResourceBounds(memQ, lowerMult, upperMult, memTargetFloorsAtPeak).quantized(ceilMemoryBytes)
		if mem != tc.wantMem {
			t.Errorf("%v mem bounds = %+v, want %+v", tc.window, mem, tc.wantMem)
		}
	}
}

func TestCapacityResourceBoundsOrdering(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	for range 5000 {
		days := 1 + rng.Float64()*29
		lowerMult, upperMult := capacityConfidenceMultipliers(days)
		q := monotoneQuantiles(
			rng.Float64()*10, rng.Float64()*10, rng.Float64()*10, rng.Float64()*10,
		)
		for _, floorsAtPeak := range []bool{cpuTargetIgnoresPeak, memTargetFloorsAtPeak} {
			b := capacityResourceBounds(q, lowerMult, upperMult, floorsAtPeak)
			if b.Lower < 0 || b.Lower > b.Target || b.Target > b.Upper {
				t.Fatalf("inverted pre-quantization triple %+v for %+v", b, q)
			}
			if b.Upper < q.Max*(1+capacitySafetyMargin)-1e-12 {
				t.Fatalf("upper %v below peak floor for %+v", b.Upper, q)
			}
			quantized := b.quantized(ceilCPUCores)
			if quantized.Lower > quantized.Target || quantized.Target > quantized.Upper {
				t.Fatalf("inverted quantized triple %+v", quantized)
			}
		}
	}
}

func TestCapacityThrottleGuard(t *testing.T) {
	t.Parallel()
	cpuQ := UsageQuantiles{P50: 0.080, P90: 0.120, P95: 0.140, Max: 0.320, Present: true}
	days := capacityConfidenceDays(7 * 24 * time.Hour)
	lowerMult, upperMult := capacityConfidenceMultipliers(days)

	lim := 0.500
	b := capacityResourceBounds(cpuQ, lowerMult, upperMult, cpuTargetIgnoresPeak).quantized(ceilCPUCores)
	guarded := applyThrottleGuard(b, 0.01, &lim)
	if guarded != (ResourceBounds{Lower: 0.10, Target: 0.58, Upper: 0.58}) {
		t.Fatalf("guarded = %+v", guarded)
	}
	unguarded := applyThrottleGuard(b, 0, &lim)
	if unguarded != b {
		t.Fatalf("no throttling must not move bounds: %+v", unguarded)
	}
	if unguarded != (ResourceBounds{Lower: 0.10, Target: 0.14, Upper: 0.37}) {
		t.Fatalf("unguarded = %+v", unguarded)
	}
	noLim := applyThrottleGuard(b, 0.01, nil)
	if noLim != b {
		t.Fatalf("no current limit means no guard: %+v", noLim)
	}
}

func TestBurstableAndGuaranteedFromBounds(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	bounds := WorkloadBounds{
		CPU: &ResourceBounds{Lower: 0.10, Target: 0.14, Upper: 0.37},
		Mem: &ResourceBounds{Lower: 80 * mi, Target: 304 * mi, Upper: 304 * mi},
	}
	burstable := burstableFromBounds(bounds)
	if burstable.CPUReq != 0.10 || burstable.CPULim == nil || *burstable.CPULim != 0.37 {
		t.Fatalf("burstable cpu = %+v", burstable)
	}
	if burstable.MemReq != 80*mi || burstable.MemLim != 304*mi {
		t.Fatalf("burstable mem = %+v", burstable)
	}
	guaranteed := guaranteedFromBounds(bounds)
	if guaranteed.CPUReq != 0.14 || guaranteed.CPULim == nil || *guaranteed.CPULim != 0.14 {
		t.Fatalf("guaranteed cpu = %+v", guaranteed)
	}
	if guaranteed.MemReq != 304*mi || guaranteed.MemLim != 304*mi {
		t.Fatalf("guaranteed mem = %+v", guaranteed)
	}

	if !strings.Contains(capacityResourcesYAML(burstable), `cpu: "370m"`) {
		t.Fatal("burstable YAML must carry the CPU limit")
	}
	if !strings.Contains(capacityResourcesYAML(guaranteed), `cpu: "140m"`) {
		t.Fatal("guaranteed YAML must carry the CPU target")
	}
}

func TestGuaranteedQosClassFromBounds(t *testing.T) {
	t.Parallel()
	g := guaranteedFromBounds(WorkloadBounds{
		CPU: &ResourceBounds{Lower: 0.1, Target: 0.14, Upper: 0.37},
		Mem: &ResourceBounds{Lower: 80, Target: 304, Upper: 304},
	})
	if got := qosClass(g.CPUReq, *g.CPULim, g.MemReq, g.MemLim); got != "Guaranteed" {
		t.Fatalf("guaranteed projection must be QoS Guaranteed, got %q", got)
	}
	b := burstableFromBounds(WorkloadBounds{
		CPU: &ResourceBounds{Lower: 0.1, Target: 0.14, Upper: 0.37},
		Mem: &ResourceBounds{Lower: 80, Target: 304, Upper: 304},
	})
	if got := qosClass(b.CPUReq, *b.CPULim, b.MemReq, b.MemLim); got != "Burstable" {
		t.Fatalf("burstable projection must be QoS Burstable, got %q", got)
	}
}

func TestMonthlyUSDAndRates(t *testing.T) {
	t.Parallel()
	cpuH, memH := capacityCostRates(queriesCapacityRates{})
	if cpuH != defaultCPUHourUSD || memH != defaultMemoryGiBHourUSD {
		t.Fatalf("defaults: %v %v", cpuH, memH)
	}
	cpuH, memH = capacityCostRates(queriesCapacityRates{CPUHourUSD: 1, MemoryGiBHour: 2})
	if cpuH != 1 || memH != 2 {
		t.Fatalf("override: %v %v", cpuH, memH)
	}
	usd := monthlyUSD(1, bytesPerGiB, 1, 1)
	want := 2 * hoursPerMonth
	if usd != want {
		t.Fatalf("monthlyUSD = %v, want %v", usd, want)
	}
}

func TestStripReplicaSetHash(t *testing.T) {
	t.Parallel()
	if got := stripReplicaSetHash("api-7d9f8c6b5"); got != "api" {
		t.Fatalf("got %q", got)
	}
	if got := stripReplicaSetHash("api"); got != "api" {
		t.Fatalf("no hash: got %q", got)
	}
}

func TestCapacityResourcesYAML(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	burstable := WorkloadResources{
		CPUReq: 0.10,
		MemReq: 80 * mi,
		MemLim: 304 * mi,
	}
	cpuLim := 0.37
	burstable.CPULim = &cpuLim
	wantBurstable := `resources:
  requests:
    cpu: "100m"
    memory: "80Mi"
  limits:
    cpu: "370m"
    memory: "304Mi"
`
	if got := capacityResourcesYAML(burstable); got != wantBurstable {
		t.Fatalf("burstable YAML:\ngot:\n%s\nwant:\n%s", got, wantBurstable)
	}

	target := 0.14
	guaranteed := WorkloadResources{
		CPUReq: target,
		CPULim: &target,
		MemReq: 304 * mi,
		MemLim: 304 * mi,
	}
	wantGuaranteed := `resources:
  requests:
    cpu: "140m"
    memory: "304Mi"
  limits:
    cpu: "140m"
    memory: "304Mi"
`
	if got := capacityResourcesYAML(guaranteed); got != wantGuaranteed {
		t.Fatalf("guaranteed YAML:\ngot:\n%s\nwant:\n%s", got, wantGuaranteed)
	}

	// Absent resource: zero request means never observed.
	memOnly := WorkloadResources{MemReq: 80 * mi, MemLim: 304 * mi}
	got := capacityResourcesYAML(memOnly)
	if strings.Contains(got, "cpu:") {
		t.Fatalf("unobserved cpu must not appear: %s", got)
	}
	if capacityResourcesYAML(WorkloadResources{}) != "" {
		t.Fatal("nothing observed must render nothing")
	}
}

func TestQosClass(t *testing.T) {
	t.Parallel()
	if qosClass(1, 1, 100, 100) != "Guaranteed" {
		t.Fatal("guaranteed")
	}
	if qosClass(1, 2, 100, 200) != "Burstable" {
		t.Fatal("burstable")
	}
	if qosClass(0, 0, 0, 0) != "BestEffort" {
		t.Fatal("besteffort")
	}
}

func TestAggregateCapacityNoSeries(t *testing.T) {
	t.Parallel()
	a := &App{}
	out := a.aggregateCapacity(nil)
	if out.Available {
		t.Fatal("empty map must be unavailable")
	}
	if out.Namespaces == nil || out.TopWasteCPU == nil {
		t.Fatal("slices must be non-nil")
	}
}

func TestAggregateCapacityWithWaste(t *testing.T) {
	t.Parallel()
	a := &App{}
	rm := map[string][]queries.PromResult{
		"sumCpuUsage": {{Value: queries.NewPromValue(0, "0.5")}},
		"sumCpuReq":   {{Value: queries.NewPromValue(0, "2")}},
		"sumMemUsage": {{Value: queries.NewPromValue(0, "100000000")}},
		"sumMemReq":   {{Value: queries.NewPromValue(0, "500000000")}},
		"wasteCpuNs": {
			{Metric: map[string]string{"namespace": "alpha"}, Value: queries.NewPromValue(0, "1.5")},
		},
		"wasteMemNs": {
			{Metric: map[string]string{"namespace": "alpha"}, Value: queries.NewPromValue(0, "400000000")},
		},
		"wasteCpuTop": {
			{Metric: map[string]string{"namespace": "alpha", "pod": "p1", "container": "app"}, Value: queries.NewPromValue(0, "1.2")},
		},
	}
	out := a.aggregateCapacity(rm)
	if !out.Available {
		t.Fatal("expected available")
	}
	if len(out.Namespaces) != 1 || out.Namespaces[0].WasteCPU != 1.5 {
		t.Fatalf("namespaces: %+v", out.Namespaces)
	}
	if out.Efficiency.CPUUsedRatio <= 0 {
		t.Fatalf("used ratio: %+v", out.Efficiency)
	}
	if out.Efficiency.RatesAreEstimates != true {
		t.Fatal("rates must be labeled estimates")
	}
}

func TestCapacityClickHouseLedgerMapsIntoAggregate(t *testing.T) {
	t.Parallel()
	a := &App{}
	rows := []chquery.CapacityContainerIdentity{
		{
			Namespace: "demo", Pod: "api-0", Container: "api", Cluster: "prod",
			UsageCPU: 0.25, UsageMem: 1e8, ReqCPU: 1, ReqMem: 5e8,
			LimCPU: 2, LimMem: 1e9, Throttled: 0.02,
			HasUsageCPU: true, HasUsageMem: true,
			HasReqCPU: true, HasReqMem: true,
			HasLimCPU: true, HasLimMem: true, HasThrottle: true,
		},
		{
			Namespace: "demo", Pod: "api-1", Container: "api", Cluster: "prod",
			UsageCPU: 1.2, UsageMem: 6e8, ReqCPU: 1, ReqMem: 5e8,
			HasUsageCPU: true, HasUsageMem: true,
			HasReqCPU: true, HasReqMem: true,
		},
	}
	rm := chquery.CapacityLedgerToResultMap(rows, 32, 128e9)
	out := a.aggregateCapacity(rm)
	if !out.Available {
		t.Fatal("Available=false")
	}
	if out.GapCounts.OverRequestedCPU < 1 {
		t.Fatalf("over-requested: %+v", out.GapCounts)
	}
	if out.GapCounts.UnderProvisioned < 1 {
		t.Fatalf("under-provisioned: %+v", out.GapCounts)
	}
	if out.GapCounts.Throttled < 1 {
		t.Fatalf("throttled: %+v", out.GapCounts)
	}
	if out.Efficiency.CPUAllocRatio == nil || *out.Efficiency.CPUAllocRatio <= 0 {
		t.Fatalf("alloc ratio: %+v", out.Efficiency)
	}
	if len(out.TopWasteCPU) < 1 || len(out.Namespaces) < 1 {
		t.Fatalf("top/ns empty: waste=%d ns=%d", len(out.TopWasteCPU), len(out.Namespaces))
	}
}

func TestAggregateRecommendationsFromCHResultMap(t *testing.T) {
	t.Parallel()
	a := &App{}
	rm := capacityRecsCHToResultMap(
		[]chquery.CapacityRecRow{{
			Namespace: "demo", Pod: "web-abc", Container: "web", Cluster: "prod",
			CPUP50: 0.080, CPUP90: 0.120, CPUP95: 0.140, CPUMax: 0.320,
			MemP50: 64 * 1024 * 1024, MemP90: 96 * 1024 * 1024, MemP95: 112 * 1024 * 1024, MemMax: 256 * 1024 * 1024,
			ReqCPU: 1, ReqMem: 5e8, LimCPU: 2, LimMem: 1e9,
		}},
		[]chquery.CapacityOwner{{
			Namespace: "demo", Pod: "web-abc", Kind: "ReplicaSet", Name: "web-7d9f8c6b5", Cluster: "prod",
		}},
	)
	out := a.aggregateRecommendations(rm, "prod", 24*time.Hour)
	if !out.Available || len(out.Recommendations) != 1 {
		t.Fatalf("%+v", out)
	}
	rec := out.Recommendations[0]
	if rec.WorkloadKind != "Deployment" || rec.WorkloadName != "web" {
		t.Fatalf("owner mapping: kind=%s name=%s", rec.WorkloadKind, rec.WorkloadName)
	}
	if rec.Bounds.CPU == nil || rec.Bounds.Mem == nil {
		t.Fatalf("bounds must be present when all four quantiles arrived: %+v", rec)
	}
	if rec.YAMLPatch == "" || rec.YAMLPatchGuaranteed == "" {
		t.Fatalf("both YAML patches must render: %+v", rec)
	}
	if *rec.Burstable.CPULim != rec.Bounds.CPU.Upper {
		t.Fatalf("burstable cpu limit %v must be the upper bound %v", *rec.Burstable.CPULim, rec.Bounds.CPU.Upper)
	}
	if rec.Guaranteed.CPUReq != rec.Bounds.CPU.Target || *rec.Guaranteed.CPULim != rec.Bounds.CPU.Target {
		t.Fatalf("guaranteed cpu must be target/target: %+v", rec.Guaranteed)
	}
}

// TestRecommendationsDroppedWhenQuantilesIncomplete pins the Present rule on
// Path A, where an absent series is genuinely absent: a resource missing any of
// p50/p90/p95/max contributes no bounds, because the missing value would
// otherwise enter the math as a zero.
func TestRecommendationsDroppedWhenQuantilesIncomplete(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	a := &App{}
	labels := map[string]string{"namespace": "demo", "pod": "web-abc", "container": "web", "k8s_cluster_name": "prod"}
	rm := map[string][]queries.PromResult{
		"cpuP50": {{Metric: labels, Value: queries.NewPromValue(0, "0.08")}},
		"cpuP90": {{Metric: labels, Value: queries.NewPromValue(0, "0.12")}},
		"cpuP95": {{Metric: labels, Value: queries.NewPromValue(0, "0.14")}},
		"cpuMax": {{Metric: labels, Value: queries.NewPromValue(0, "0.32")}},
		// memP50 and memP90 arrived, memP95 did not: no memory bounds.
		"memP50": {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(64*mi))}},
		"memP90": {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(96*mi))}},
		"memMax": {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(256*mi))}},
	}
	out := a.aggregateRecommendations(rm, "prod", 24*time.Hour)
	if len(out.Recommendations) != 1 {
		t.Fatalf("cpu-only rec must survive: %+v", out)
	}
	rec := out.Recommendations[0]
	if rec.Bounds.CPU == nil {
		t.Fatal("cpu bounds must be present")
	}
	if rec.Bounds.Mem != nil {
		t.Fatalf("memory without p95 must produce no bounds: %+v", rec.Bounds.Mem)
	}
	if strings.Contains(rec.YAMLPatch, "memory:") {
		t.Fatalf("memory must not appear in the CPU-only patch:\n%s", rec.YAMLPatch)
	}
	if len(rec.Notes) == 0 {
		t.Fatal("a dropped resource must disclose why")
	}
	// Missing mem p95 must not treat default 0 as observed usage for USD figures.
	if rec.Observed.MemP95 != 0 {
		t.Fatalf("mem p95 must stay zero when the series was absent: %v", rec.Observed.MemP95)
	}
}

func TestMergeCapacityByWorkloadKeepsMaxCPULim(t *testing.T) {
	t.Parallel()
	low, high := 0.5, 2.0
	obs := map[capacityPodID]*capacityObsRec{
		{ns: "demo", pod: "a", container: "web", cluster: "prod"}: {
			rec: &WorkloadRecommendation{
				Namespace: "demo", Container: "web", Environment: "prod", Replicas: 1,
				Observed: WorkloadObserved{ThrottledRate: 0.01},
				Current:  WorkloadResources{CPULim: &low},
			},
			cpuSeen: capacityQuantilesSeen{p95: true},
		},
		{ns: "demo", pod: "b", container: "web", cluster: "prod"}: {
			rec: &WorkloadRecommendation{
				Namespace: "demo", Container: "web", Environment: "prod", Replicas: 1,
				Observed: WorkloadObserved{ThrottledRate: 0},
				Current:  WorkloadResources{CPULim: &high},
			},
			cpuSeen: capacityQuantilesSeen{p95: true},
		},
	}
	owners := map[string]struct{ kind, name string }{
		"demo\x00prod\x00a": {kind: "Deployment", name: "web"},
		"demo\x00prod\x00b": {kind: "Deployment", name: "web"},
	}
	merged := mergeCapacityByWorkload(obs, owners)
	agg := merged[capacityWkKey{ns: "demo", kind: "Deployment", name: "web", container: "web", cluster: "prod"}]
	if agg == nil || agg.rec.Current.CPULim == nil || *agg.rec.Current.CPULim != high {
		t.Fatalf("merged CPULim must be the max replica limit: %+v", agg)
	}
	if agg.rec.Observed.ThrottledRate != 0.01 {
		t.Fatalf("throttle must survive with the max limit: %+v", agg.rec.Observed)
	}
}

// TestMaxObservedCoversEveryField reflects over WorkloadObserved so a new field
// cannot be forgotten in the merge, which is how ThrottledRate was dropped.
func TestMaxObservedCoversEveryField(t *testing.T) {
	t.Parallel()
	var a, b WorkloadObserved
	va, vb := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	for i := 0; i < va.NumField(); i++ {
		if va.Field(i).Kind() != reflect.Float64 {
			t.Fatalf("unexpected field kind on %s; update this test", va.Type().Field(i).Name)
		}
		va.Field(i).SetFloat(1)
		vb.Field(i).SetFloat(2)
	}
	merged := maxObserved(a, b)
	vm := reflect.ValueOf(merged)
	for i := 0; i < vm.NumField(); i++ {
		if got := vm.Field(i).Float(); got != 2 {
			t.Errorf("maxObserved missed %s: got %v want 2", vm.Type().Field(i).Name, got)
		}
	}
}

// TestPathAPathBParity proves the math is path-independent: the same quantiles
// produce the same recommendation whether they came from Prometheus or
// ClickHouse.
func TestPathAPathBParity(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	labels := map[string]string{"namespace": "demo", "pod": "web-abc", "container": "web", "k8s_cluster_name": "prod"}
	prom := map[string][]queries.PromResult{
		"cpuP50":   {{Metric: labels, Value: queries.NewPromValue(0, "0.08")}},
		"cpuP90":   {{Metric: labels, Value: queries.NewPromValue(0, "0.12")}},
		"cpuP95":   {{Metric: labels, Value: queries.NewPromValue(0, "0.14")}},
		"cpuMax":   {{Metric: labels, Value: queries.NewPromValue(0, "0.32")}},
		"memP50":   {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(64*mi))}},
		"memP90":   {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(96*mi))}},
		"memP95":   {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(112*mi))}},
		"memMax":   {{Metric: labels, Value: queries.NewPromValue(0, fmt.Sprint(256*mi))}},
		"cpuReq":   {{Metric: labels, Value: queries.NewPromValue(0, "1")}},
		"memReq":   {{Metric: labels, Value: queries.NewPromValue(0, "500000000")}},
		"cpuLim":   {{Metric: labels, Value: queries.NewPromValue(0, "2")}},
		"memLim":   {{Metric: labels, Value: queries.NewPromValue(0, "1000000000")}},
		"podOwner": {{Metric: map[string]string{"namespace": "demo", "pod": "web-abc", "owner_kind": "Deployment", "owner_name": "web", "k8s_cluster_name": "prod"}, Value: queries.NewPromValue(0, "1")}},
	}
	ch := capacityRecsCHToResultMap(
		[]chquery.CapacityRecRow{{
			Namespace: "demo", Pod: "web-abc", Container: "web", Cluster: "prod",
			CPUP50: 0.08, CPUP90: 0.12, CPUP95: 0.14, CPUMax: 0.32,
			MemP50: 64 * mi, MemP90: 96 * mi, MemP95: 112 * mi, MemMax: 256 * mi,
			ReqCPU: 1, ReqMem: 5e8, LimCPU: 2, LimMem: 1e9,
		}},
		[]chquery.CapacityOwner{{Namespace: "demo", Pod: "web-abc", Kind: "Deployment", Name: "web", Cluster: "prod"}},
	)
	a := &App{}
	fromProm := a.aggregateRecommendations(prom, "prod", 7*24*time.Hour)
	fromCH := a.aggregateRecommendations(ch, "prod", 7*24*time.Hour)
	if len(fromProm.Recommendations) != 1 || len(fromCH.Recommendations) != 1 {
		t.Fatalf("prom=%d ch=%d", len(fromProm.Recommendations), len(fromCH.Recommendations))
	}
	if !reflect.DeepEqual(fromProm.Recommendations[0], fromCH.Recommendations[0]) {
		t.Fatalf("paths disagree:\nprom %+v\nch   %+v", fromProm.Recommendations[0], fromCH.Recommendations[0])
	}
}

// TestFinalizeCase1Golden pins the end-to-end triple and both YAML patches for
// the spiky service over a 7-day window.
func TestFinalizeCase1Golden(t *testing.T) {
	t.Parallel()
	const mi = 1024 * 1024
	a := &App{}
	rm := capacityRecsCHToResultMap(
		[]chquery.CapacityRecRow{{
			Namespace: "demo", Pod: "web-abc", Container: "web", Cluster: "prod",
			CPUP50: 0.080, CPUP90: 0.120, CPUP95: 0.140, CPUMax: 0.320,
			MemP50: 64 * mi, MemP90: 96 * mi, MemP95: 112 * mi, MemMax: 256 * mi,
			ReqCPU: 1, ReqMem: 5e8, LimCPU: 2, LimMem: 1e9,
		}},
		[]chquery.CapacityOwner{{Namespace: "demo", Pod: "web-abc", Kind: "Deployment", Name: "web", Cluster: "prod"}},
	)
	out := a.aggregateRecommendations(rm, "prod", 7*24*time.Hour)
	rec := out.Recommendations[0]
	if rec.Confidence != CapacityConfidenceHigh {
		t.Fatalf("confidence = %q", rec.Confidence)
	}
	wantBurstable := `resources:
  requests:
    cpu: "100m"
    memory: "80Mi"
  limits:
    cpu: "370m"
    memory: "304Mi"
`
	if rec.YAMLPatch != wantBurstable {
		t.Fatalf("burstable patch:\ngot:\n%s\nwant:\n%s", rec.YAMLPatch, wantBurstable)
	}
	wantGuaranteed := `resources:
  requests:
    cpu: "140m"
    memory: "304Mi"
  limits:
    cpu: "140m"
    memory: "304Mi"
`
	if rec.YAMLPatchGuaranteed != wantGuaranteed {
		t.Fatalf("guaranteed patch:\ngot:\n%s\nwant:\n%s", rec.YAMLPatchGuaranteed, wantGuaranteed)
	}
}
