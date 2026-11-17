package chquery

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCapacityUsageProbeSQL_ForbidsProcessAndMatchesFamilies(t *testing.T) {
	sql := CapacityUsageProbeSQL(time.Now().Add(-time.Hour), time.Now())
	for _, want := range []string{
		"otel_metrics_sum",
		"otel_metrics_gauge",
		MetricContainerCPUUsage,
		MetricContainerMemoryWorkingSet,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("usage probe missing %q\n%s", want, sql)
		}
	}
	if !CapacityProbeForbidsProcess(sql) {
		t.Fatal("usage probe must not mention process_")
	}
}

func TestCapacityKSMProbeSQL(t *testing.T) {
	sql := CapacityKSMProbeSQL(time.Now().Add(-time.Hour), time.Now())
	for _, want := range []string{
		"otel_metrics_gauge",
		MetricKubePodResourceRequests,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("KSM probe missing %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "process_") {
		t.Fatal("KSM probe must not mention process_")
	}
}

func TestCapacityLedgerSQL_IdentityAndRate(t *testing.T) {
	to := time.Unix(1_700_000_000, 0).UTC()
	from := to.Add(-5 * time.Minute)
	sql := CapacityLedgerSQL("demo", "prod", from, to)
	for _, want := range []string{
		"Attributes['namespace']",
		"Attributes['pod']",
		"Attributes['container']",
		"ResourceAttributes['cluster']",
		MetricContainerCPUUsage,
		MetricContainerMemoryWorkingSet,
		MetricKubePodResourceRequests,
		MetricKubePodResourceLimits,
		"container != '' AND container != 'POD'",
		"/ 300",
		"otel_metrics_sum",
		"otel_metrics_gauge",
		"demo",
		"prod",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("ledger missing %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "process_") {
		t.Fatal("ledger must not use process_")
	}
}

func TestCapacityRecsBucketSQL_AndHistory(t *testing.T) {
	to := time.Now()
	from := to.Add(-24 * time.Hour)
	recs := CapacityRecsBucketSQL("ns", "", from, to)
	for _, want := range []string{"quantileExactInclusive(0.95)", "cpu_p50", "cpu_p90", "mem_p50", "mem_p90", MetricContainerCPUUsage, MetricKubePodResourceRequests} {
		if !strings.Contains(recs, want) {
			t.Errorf("recs SQL missing %q", want)
		}
	}
	owner := CapacityPodOwnerSQL("ns", "", from, to)
	if !strings.Contains(owner, MetricKubePodOwner) {
		t.Fatal("owner SQL missing kube_pod_owner")
	}
	hist := CapacityHistorySeriesSQL("ns", "app", "", "", from, to, 60)
	for _, want := range []string{"bucket_ms", MetricContainerCPUUsage, MetricKubePodRestarts, "app", "k8s_cluster_name"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history SQL missing %q", want)
		}
	}
}

func TestCapacityResetAwareIncrease(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur float64
		hasPrev   bool
		want      float64
	}{
		{"no previous sample is zero", 0, 1000, false, 0},
		{"monotonic step", 1000, 1010, true, 10},
		{"reset adds current value", 1010, 0, true, 0},
		{"first sample after reset", 0, 10, true, 10},
		{"flat sample adds nothing", 42, 42, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := capacityResetAwareIncrease(tc.prev, tc.cur, tc.hasPrev); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}

	series := []float64{1000, 1010, 0, 10}
	var total float64
	for i, v := range series {
		hasPrev := i > 0
		var prev float64
		if hasPrev {
			prev = series[i-1]
		}
		total += capacityResetAwareIncrease(prev, v, hasPrev)
	}
	if total != 20 {
		t.Fatalf("series total = %v, want 20", total)
	}
}

func TestCapacityCounterSQL_UsesResetAwareLagNotExtrema(t *testing.T) {
	to := time.Unix(1_700_000_000, 0).UTC()
	from := to.Add(-5 * time.Minute)
	cpu := q(MetricContainerCPUUsage)
	thr := q(MetricContainerCPUThrottled)

	ledger := CapacityLedgerSQL("demo", "prod", from, to)
	for _, old := range []string{
		fmt.Sprintf("maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s)", cpu, cpu),
		fmt.Sprintf("maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s)", thr, thr),
	} {
		if strings.Contains(ledger, old) {
			t.Errorf("ledger still uses extrema for counters: %s", old)
		}
	}
	for _, want := range []string{"lagInFrame(Value, 1, nan)", "isNaN(prev_val)", "/ 300"} {
		if !strings.Contains(ledger, want) {
			t.Errorf("ledger missing %q\n%s", want, ledger)
		}
	}

	recs := CapacityRecsBucketSQL("demo", "prod", from, to)
	if strings.Contains(recs, fmt.Sprintf("maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s)", cpu, cpu)) {
		t.Error("recs still uses extrema for cpu counter")
	}
	for _, want := range []string{"lagInFrame(Value, 1, nan)", "isNaN(prev_val)", "AS cpu_rate", "AS thr_rate"} {
		if !strings.Contains(recs, want) {
			t.Errorf("recs missing %q\n%s", want, recs)
		}
	}

	hist := CapacityHistorySeriesSQL("demo", "", "", "", from, to, 60)
	if strings.Contains(hist, "max(Value) - min(Value)") {
		t.Error("history still uses extrema for counter delta")
	}
	for _, want := range []string{"lagInFrame(Value, 1, nan)", "isNaN(prev_val)", "AS inc", "AS dt"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history missing %q\n%s", want, hist)
		}
	}
}

func TestCapacityHistorySeriesSQL_ReadsPrecedingSample(t *testing.T) {
	to := time.Unix(1_700_000_000, 0).UTC()
	from := to.Add(-time.Hour)
	step := 60
	hist := CapacityHistorySeriesSQL("demo", "", "", "", from, to, step)

	precedingFrom := from.Add(-time.Duration(step) * time.Second)
	scanFrom := fmt.Sprintf(
		"%s >= fromUnixTimestamp64Milli(%d) AND %s <= fromUnixTimestamp64Milli(%d)",
		ColTimeUnix, precedingFrom.UnixMilli(), ColTimeUnix, to.UnixMilli(),
	)
	if !strings.Contains(hist, scanFrom) {
		t.Fatalf("history must scan one step before from to keep a preceding sample\nwant %s\n%s", scanFrom, hist)
	}
	inRange := fmt.Sprintf("%s >= fromUnixTimestamp64Milli(%d)", ColTimeUnix, from.UnixMilli())
	if !strings.Contains(hist, inRange) {
		t.Errorf("history must attribute increases only at or after from (%s)", inRange)
	}
	if !strings.Contains(hist, "sumIf(dt, MetricName") {
		t.Errorf("history rates must divide by elapsed sample time\n%s", hist)
	}
	if !strings.Contains(hist, "sum(in_range) > 0") {
		t.Error("history must drop buckets holding only the preceding baseline sample")
	}
	if strings.Contains(hist, "max(Value) - min(Value)") {
		t.Error("history must not derive counter increases from extrema")
	}
}

func TestCapacityEnvFilter_CommaSeparated(t *testing.T) {
	single := capacityEnvFilter("prod")
	if strings.Contains(single, "IN (") || !strings.Contains(single, "= 'prod'") {
		t.Fatalf("single: %s", single)
	}
	multi := capacityEnvFilter("prod, staging")
	if !strings.Contains(multi, "IN (") || !strings.Contains(multi, "'prod'") || !strings.Contains(multi, "'staging'") {
		t.Fatalf("multi: %s", multi)
	}
}

func TestCapacityNodeAllocSQL_GroupsByCluster(t *testing.T) {
	sql := CapacityNodeAllocSQL("prod", time.Now().Add(-time.Hour), time.Now())
	if !strings.Contains(sql, "GROUP BY resource, node, k8s_cluster_name") {
		t.Fatalf("missing cluster group: %s", sql)
	}
	if strings.Contains(sql, "LIMIT 500") {
		t.Fatal("node alloc population must not cap at LIMIT 500")
	}
}

func TestParseCapacityLedgerRows_AndResultMap(t *testing.T) {
	rows := ParseCapacityLedgerRows([]map[string]any{
		{
			"namespace": "demo", "pod": "api-1", "container": "api", "k8s_cluster_name": "prod",
			"usage_cpu": 0.2, "usage_mem": 1e8, "req_cpu": 1.0, "req_mem": 5e8,
			"lim_cpu": 2.0, "lim_mem": 1e9, "thr_rate": 0.01,
			"has_usage_cpu": 1.0, "has_usage_mem": 1.0,
			"has_req_cpu": 1.0, "has_req_mem": 1.0,
			"has_lim_cpu": 1.0, "has_lim_mem": 1.0, "has_throttle": 1.0,
		},
		{
			"namespace": "demo", "pod": "api-2", "container": "api", "k8s_cluster_name": "prod",
			"usage_cpu": 1.5, "usage_mem": 6e8, "req_cpu": 1.0, "req_mem": 5e8,
			"lim_cpu": 0, "lim_mem": 0, "thr_rate": 0,
			"has_usage_cpu": 1.0, "has_usage_mem": 1.0,
			"has_req_cpu": 1.0, "has_req_mem": 1.0,
			"has_lim_cpu": 0.0, "has_lim_mem": 0.0, "has_throttle": 0.0,
		},
		{"namespace": "demo", "pod": "x", "container": "POD", "usage_cpu": 9},
	})
	if len(rows) != 2 {
		t.Fatalf("len=%d", len(rows))
	}
	rm := CapacityLedgerToResultMap(rows, 16, 64e9)
	if len(rm["sumCpuUsage"]) != 1 || rm["sumCpuUsage"][0].Value.Float() < 1.6 {
		t.Fatalf("sumCpuUsage=%v", rm["sumCpuUsage"])
	}
	if len(rm["wasteCpuTop"]) < 1 {
		t.Fatal("expected wasteCpuTop for over-requested container")
	}
	if len(rm["underCpu"]) < 1 {
		t.Fatal("expected underCpu for usage>request")
	}
	if len(rm["gapMissingLimCpu"]) < 1 {
		t.Fatal("expected missing limit gap")
	}
	if len(rm["allocCpu"]) != 1 || rm["allocCpu"][0].Value.Float() != 16 {
		t.Fatalf("allocCpu=%v", rm["allocCpu"])
	}
	if len(rm["throttled"]) != 1 {
		t.Fatalf("throttled=%d", len(rm["throttled"]))
	}
}

func TestLinearForecast(t *testing.T) {
	base := int64(1_700_000_000_000)
	pts := []struct {
		T int64
		V float64
	}{
		{base, 1},
		{base + 3600_000, 2},
		{base + 7200_000, 3},
	}
	pred, slope, ok := LinearForecast(pts, 3600)
	if !ok {
		t.Fatal("expected fit")
	}
	if slope <= 0 {
		t.Fatalf("slope=%v", slope)
	}
	if pred < 3.5 || pred > 4.5 {
		t.Fatalf("pred=%v", pred)
	}
	if _, _, ok := LinearForecast(pts[:1], 3600); ok {
		t.Fatal("single point must fail")
	}
}

func TestCapacityNodeFsSQLAndMerge(t *testing.T) {
	sql := CapacityNodeFsSQL("prod", time.Now().Add(-time.Hour), time.Now())
	for _, want := range []string{MetricContainerFSUsage, MetricContainerFSLimit, "usage_bytes", "limit_bytes", "GROUP BY node, k8s_cluster_name"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("fs SQL missing %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "kube_node_status_capacity") {
		t.Fatal("must not invent usage from capacity-only KSM")
	}
	if strings.Contains(sql, "LIMIT 500") {
		t.Fatal("node fs population must not cap at LIMIT 500")
	}
	rm := emptyCapacityResultMap()
	MergeNodeFsIntoResultMap(rm, []map[string]any{
		{"node": "n1", "usage_bytes": 50.0, "limit_bytes": 100.0},
	})
	if len(rm["nodeFsUsage"]) != 1 || len(rm["nodeFsLimit"]) != 1 {
		t.Fatalf("merge: %+v", rm)
	}
}
