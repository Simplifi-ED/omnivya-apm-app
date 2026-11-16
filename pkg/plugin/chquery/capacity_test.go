package chquery

import (
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
	for _, want := range []string{"quantileExact(0.95)", "cpu_p50", MetricContainerCPUUsage, MetricKubePodResourceRequests} {
		if !strings.Contains(recs, want) {
			t.Errorf("recs SQL missing %q", want)
		}
	}
	owner := CapacityPodOwnerSQL("ns", "", from, to)
	if !strings.Contains(owner, MetricKubePodOwner) {
		t.Fatal("owner SQL missing kube_pod_owner")
	}
	hist := CapacityHistorySeriesSQL("ns", "app", "", from, to, 60)
	for _, want := range []string{"bucket_ms", MetricContainerCPUUsage, MetricKubePodRestarts, "app"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history SQL missing %q", want)
		}
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
