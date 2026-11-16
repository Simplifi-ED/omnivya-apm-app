package plugin

import (
	"strings"
	"testing"
	"time"

	"github.com/nais/grafana-otel-plugin/pkg/plugin/chquery"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/queries"
)

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

func TestBurstableAndGuaranteed(t *testing.T) {
	t.Parallel()
	cpuReq, memReq, memLim := burstableRecommendation(0.1, 100*1024*1024, 200*1024*1024)
	if cpuReq < 0.01 {
		t.Fatalf("cpuReq floor: %v", cpuReq)
	}
	if memReq < 32*1024*1024 || memLim < memReq {
		t.Fatalf("memReq=%v memLim=%v", memReq, memLim)
	}
	patch := resourcesYAMLPatch(cpuReq, nil, memReq, memLim)
	if strings.Contains(patch, "limits:\n    cpu:") {
		t.Fatal("burstable YAML must omit cpu limit")
	}
	gCPU, gCPULim, gMem, gMemLim := guaranteedRecommendation(0.2, 200*1024*1024)
	if gCPU != gCPULim || gMem != gMemLim {
		t.Fatalf("guaranteed req must equal lim: cpu %v/%v mem %v/%v", gCPU, gCPULim, gMem, gMemLim)
	}
}

func TestCapacityConfidence(t *testing.T) {
	t.Parallel()
	if capacityConfidence(time.Hour) != CapacityConfidenceLow {
		t.Fatal("expected low")
	}
	if capacityConfidence(48*time.Hour) != CapacityConfidenceMedium {
		t.Fatal("expected medium")
	}
	if capacityConfidence(8*24*time.Hour) != CapacityConfidenceHigh {
		t.Fatal("expected high")
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

func TestResourcesYAMLPatch(t *testing.T) {
	t.Parallel()
	y := resourcesYAMLPatch(0.1, nil, 64*1024*1024, 128*1024*1024)
	if !strings.Contains(y, `cpu: "100m"`) {
		t.Fatalf("cpu: %s", y)
	}
	if strings.Contains(y, "limits:\n    cpu:") {
		t.Fatalf("should omit cpu limit: %s", y)
	}
	lim := 0.2
	y2 := resourcesYAMLPatch(0.2, &lim, 64*1024*1024, 128*1024*1024)
	if !strings.Contains(y2, `cpu: "200m"`) {
		t.Fatalf("guaranteed cpu: %s", y2)
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
			CPUP50: 0.1, CPUP95: 0.2, CPUMax: 0.4,
			MemP95: 1e8, MemMax: 2e8, ReqCPU: 1, ReqMem: 5e8, LimCPU: 2, LimMem: 1e9,
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
	if rec.YAMLPatch == "" || rec.Burstable.CPUReq <= 0 {
		t.Fatalf("goldilocks empty: %+v", rec)
	}
}
