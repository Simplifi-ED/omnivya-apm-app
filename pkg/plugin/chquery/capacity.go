package chquery

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nais/grafana-otel-plugin/pkg/plugin/queries"
)

// Capacity Path B metric names (Prom-style via prometheus receiver → otel_metrics_*).
// Same families as Path A otelconfig ContainerMetrics. process_* is never usage.
const (
	MetricContainerCPUUsage         = "container_cpu_usage_seconds_total"
	MetricContainerMemoryWorkingSet = "container_memory_working_set_bytes"
	MetricContainerCPUThrottled     = "container_cpu_cfs_throttled_seconds_total"
	MetricContainerFSUsage          = "container_fs_usage_bytes"
	MetricContainerFSLimit          = "container_fs_limit_bytes"
	MetricKubePodResourceRequests   = "kube_pod_container_resource_requests"
	MetricKubePodResourceLimits     = "kube_pod_container_resource_limits"
	MetricKubeNodeAllocatable       = "kube_node_status_allocatable"
	MetricKubePodOwner              = "kube_pod_owner"
	MetricKubePodRestarts           = "kube_pod_container_status_restarts_total"

	// CapacityUsageLookbackSec matches Path A capacityLookback ("5m").
	CapacityUsageLookbackSec = 300
)

// CapacityContainerIdentity is one (namespace, pod, container) ledger row.
type CapacityContainerIdentity struct {
	Namespace   string
	Pod         string
	Container   string
	Cluster     string
	Node        string
	UsageCPU    float64 // cores (rate over lookback)
	UsageMem    float64 // bytes
	ReqCPU      float64
	ReqMem      float64
	LimCPU      float64
	LimMem      float64
	Throttled   float64 // seconds/sec over lookback
	HasUsageCPU bool
	HasUsageMem bool
	HasReqCPU   bool
	HasReqMem   bool
	HasLimCPU   bool
	HasLimMem   bool
	HasThrottle bool
}

func capacityNodeExpr() string {
	return `coalesce(
    nullIf(Attributes['node'], ''),
    nullIf(Attributes['kubernetes_io_hostname'], ''),
    nullIf(ResourceAttributes['k8s.node.name'], ''),
    nullIf(ResourceAttributes['host.name'], ''),
    ''
  )`
}

func capacityClusterExpr() string {
	return `coalesce(
    nullIf(Attributes['k8s_cluster_name'], ''),
    nullIf(ResourceAttributes['k8s.cluster.name'], ''),
    nullIf(ResourceAttributes['cluster'], ''),
    ''
  )`
}

func capacityNamespaceExpr() string {
	return `coalesce(
    nullIf(Attributes['namespace'], ''),
    nullIf(Attributes['exported_namespace'], ''),
    nullIf(ResourceAttributes['k8s.namespace.name'], ''),
    ''
  )`
}

func capacityPodExpr() string {
	return `coalesce(
    nullIf(Attributes['pod'], ''),
    nullIf(ResourceAttributes['k8s.pod.name'], ''),
    ''
  )`
}

func capacityContainerExpr() string {
	return `coalesce(
    nullIf(Attributes['container'], ''),
    nullIf(ResourceAttributes['k8s.container.name'], ''),
    ''
  )`
}

func capacityNamespaceFilter(namespace string) string {
	if namespace == "" {
		return "1"
	}
	return fmt.Sprintf("(%s = %s)", capacityNamespaceExpr(), q(namespace))
}

func capacityEnvFilter(env string) string {
	if env == "" {
		return "1"
	}
	return fmt.Sprintf("(%s = %s)", capacityClusterExpr(), q(env))
}

func capacityContainerExclude() string {
	return fmt.Sprintf(
		"(%s != '' AND %s != 'POD')",
		capacityContainerExpr(), capacityContainerExpr(),
	)
}

// CapacityUsageProbeSQL is true when any cAdvisor usage series exists.
// Does not match process_* (forbidden substitute).
func CapacityUsageProbeSQL(from, to time.Time) string {
	tf := MetricTimeFilter(from, to)
	return fmt.Sprintf(`
SELECT count() AS c FROM (
  SELECT 1
  FROM %s
  WHERE MetricName = %s AND %s
  LIMIT 1
  UNION ALL
  SELECT 1
  FROM %s
  WHERE MetricName = %s AND %s
  LIMIT 1
)
LIMIT 1`,
		table(queries.CHTableMetricsSum), q(MetricContainerCPUUsage), tf,
		table(queries.CHTableMetricsGauge), q(MetricContainerMemoryWorkingSet), tf,
	)
}

// CapacityKSMProbeSQL is true when kube_pod_container_resource_requests exists.
func CapacityKSMProbeSQL(from, to time.Time) string {
	return fmt.Sprintf(`
SELECT count() AS c FROM (
  SELECT 1
  FROM %s
  WHERE MetricName = %s AND %s
  LIMIT 1
)
LIMIT 1`,
		table(queries.CHTableMetricsGauge),
		q(MetricKubePodResourceRequests),
		MetricTimeFilter(from, to),
	)
}

// CapacityLedgerSQL returns one row per (namespace, pod, container) with usage,
// request, limit, and throttle columns over the lookback window ending at `to`.
// from should be to - CapacityUsageLookbackSec for rate deltas.
func CapacityLedgerSQL(namespace, env string, from, to time.Time) string {
	tf := MetricTimeFilter(from, to)
	nsF := capacityNamespaceFilter(namespace)
	envF := capacityEnvFilter(env)
	ctrX := capacityContainerExclude()
	nsE := capacityNamespaceExpr()
	podE := capacityPodExpr()
	ctrE := capacityContainerExpr()
	clE := capacityClusterExpr()
	nodeE := capacityNodeExpr()
	win := float64(CapacityUsageLookbackSec)
	if d := to.Sub(from).Seconds(); d > 1 {
		win = d
	}

	return fmt.Sprintf(`
SELECT
  namespace,
  pod,
  container,
  k8s_cluster_name,
  node,
  greatest(
    maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s),
    0
  ) / %g AS usage_cpu,
  argMaxIf(Value, TimeUnix, MetricName = %s) AS usage_mem,
  argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'cpu') AS req_cpu,
  argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'memory') AS req_mem,
  argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'cpu') AS lim_cpu,
  argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'memory') AS lim_mem,
  greatest(
    maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s),
    0
  ) / %g AS thr_rate,
  countIf(MetricName = %s) > 0 AS has_usage_cpu,
  countIf(MetricName = %s) > 0 AS has_usage_mem,
  countIf(MetricName = %s AND Attributes['resource'] = 'cpu') > 0 AS has_req_cpu,
  countIf(MetricName = %s AND Attributes['resource'] = 'memory') > 0 AS has_req_mem,
  countIf(MetricName = %s AND Attributes['resource'] = 'cpu') > 0 AS has_lim_cpu,
  countIf(MetricName = %s AND Attributes['resource'] = 'memory') > 0 AS has_lim_mem,
  countIf(MetricName = %s) > 0 AS has_throttle
FROM (
  SELECT
    %s AS namespace,
    %s AS pod,
    %s AS container,
    %s AS k8s_cluster_name,
    %s AS node,
    MetricName,
    TimeUnix,
    Value,
    Attributes
  FROM %s
  WHERE MetricName IN (%s, %s)
    AND %s AND (%s) AND (%s) AND (%s)
  UNION ALL
  SELECT
    %s AS namespace,
    %s AS pod,
    %s AS container,
    %s AS k8s_cluster_name,
    %s AS node,
    MetricName,
    TimeUnix,
    Value,
    Attributes
  FROM %s
  WHERE MetricName IN (%s, %s, %s)
    AND %s AND (%s) AND (%s) AND (%s)
)
GROUP BY namespace, pod, container, k8s_cluster_name, node
HAVING container != '' AND container != 'POD'
LIMIT 10000`,
		q(MetricContainerCPUUsage), q(MetricContainerCPUUsage), win,
		q(MetricContainerMemoryWorkingSet),
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceLimits),
		q(MetricKubePodResourceLimits),
		q(MetricContainerCPUThrottled), q(MetricContainerCPUThrottled), win,
		q(MetricContainerCPUUsage),
		q(MetricContainerMemoryWorkingSet),
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceLimits),
		q(MetricKubePodResourceLimits),
		q(MetricContainerCPUThrottled),
		nsE, podE, ctrE, clE, nodeE,
		table(queries.CHTableMetricsSum),
		q(MetricContainerCPUUsage), q(MetricContainerCPUThrottled),
		tf, nsF, envF, ctrX,
		nsE, podE, ctrE, clE, nodeE,
		table(queries.CHTableMetricsGauge),
		q(MetricContainerMemoryWorkingSet), q(MetricKubePodResourceRequests), q(MetricKubePodResourceLimits),
		tf, nsF, envF, ctrX,
	)
}

// CapacityNodeAllocSQL returns per-node allocatable CPU/memory.
func CapacityNodeAllocSQL(env string, from, to time.Time) string {
	return fmt.Sprintf(`
SELECT
  node,
  sumIf(v, resource = 'cpu') AS alloc_cpu,
  sumIf(v, resource = 'memory') AS alloc_mem
FROM (
  SELECT
    Attributes['resource'] AS resource,
    %s AS node,
    argMax(Value, TimeUnix) AS v
  FROM %s
  WHERE MetricName = %s
    AND %s
    AND (%s)
  GROUP BY resource, node
)
WHERE node != ''
GROUP BY node
LIMIT 500`,
		capacityNodeExpr(),
		table(queries.CHTableMetricsGauge),
		q(MetricKubeNodeAllocatable),
		MetricTimeFilter(from, to),
		capacityEnvFilter(env),
	)
}

// CapacityNodeFsSQL returns per-node root-cgroup filesystem usage/limit (when scraped).
// Prefer id="/" (node root); fall back to any empty-container series. Never invent from capacity-only KSM.
func CapacityNodeFsSQL(env string, from, to time.Time) string {
	return fmt.Sprintf(`
SELECT
  node,
  if(usage_root > 0, usage_root, usage_any) AS usage_bytes,
  if(limit_root > 0, limit_root, limit_any) AS limit_bytes
FROM (
  SELECT
    %s AS node,
    argMaxIf(Value, TimeUnix, MetricName = %s AND coalesce(Attributes['id'], '') = '/') AS usage_root,
    argMaxIf(Value, TimeUnix, MetricName = %s AND coalesce(Attributes['id'], '') = '/') AS limit_root,
    argMaxIf(Value, TimeUnix, MetricName = %s) AS usage_any,
    argMaxIf(Value, TimeUnix, MetricName = %s) AS limit_any
  FROM %s
  WHERE MetricName IN (%s, %s)
    AND %s
    AND (%s)
    AND coalesce(Attributes['container'], '') = ''
  GROUP BY node
)
WHERE node != '' AND (usage_bytes > 0 OR limit_bytes > 0)
LIMIT 500`,
		capacityNodeExpr(),
		q(MetricContainerFSUsage), q(MetricContainerFSLimit),
		q(MetricContainerFSUsage), q(MetricContainerFSLimit),
		table(queries.CHTableMetricsGauge),
		q(MetricContainerFSUsage), q(MetricContainerFSLimit),
		MetricTimeFilter(from, to),
		capacityEnvFilter(env),
	)
}

// CapacityAllocatableSQL returns fleet CPU and memory allocatable sums.
func CapacityAllocatableSQL(env string, from, to time.Time) string {
	return fmt.Sprintf(`
SELECT
  sumIf(v, resource = 'cpu') AS alloc_cpu,
  sumIf(v, resource = 'memory') AS alloc_mem
FROM (
  SELECT
    Attributes['resource'] AS resource,
    coalesce(nullIf(Attributes['node'], ''), nullIf(ResourceAttributes['k8s.node.name'], ''), '') AS node,
    argMax(Value, TimeUnix) AS v
  FROM %s
  WHERE MetricName = %s
    AND %s
    AND (%s)
  GROUP BY resource, node
)
LIMIT 1`,
		table(queries.CHTableMetricsGauge),
		q(MetricKubeNodeAllocatable),
		MetricTimeFilter(from, to),
		capacityEnvFilter(env),
	)
}

// CapacityPodOwnerSQL returns latest kube_pod_owner rows.
func CapacityPodOwnerSQL(namespace, env string, from, to time.Time) string {
	return fmt.Sprintf(`
SELECT
  %s AS namespace,
  %s AS pod,
  Attributes['owner_kind'] AS owner_kind,
  Attributes['owner_name'] AS owner_name,
  %s AS k8s_cluster_name,
  argMax(Value, TimeUnix) AS value
FROM %s
WHERE MetricName = %s
  AND %s
  AND (%s)
  AND (%s)
GROUP BY namespace, pod, owner_kind, owner_name, k8s_cluster_name
LIMIT 10000`,
		capacityNamespaceExpr(),
		capacityPodExpr(),
		capacityClusterExpr(),
		table(queries.CHTableMetricsGauge),
		q(MetricKubePodOwner),
		MetricTimeFilter(from, to),
		capacityNamespaceFilter(namespace),
		capacityEnvFilter(env),
	)
}

// CapacityRecsBucketSQL approximates Path A quantile_over_time(rate) via 5m buckets.
// Documented as approximation in docs/api/capacity.md.
func CapacityRecsBucketSQL(namespace, env string, from, to time.Time) string {
	tf := MetricTimeFilter(from, to)
	nsF := capacityNamespaceFilter(namespace)
	envF := capacityEnvFilter(env)
	ctrX := capacityContainerExclude()
	nsE := capacityNamespaceExpr()
	podE := capacityPodExpr()
	ctrE := capacityContainerExpr()
	clE := capacityClusterExpr()
	bucket := CapacityUsageLookbackSec

	return fmt.Sprintf(`
SELECT
  namespace,
  pod,
  container,
  k8s_cluster_name,
  quantileExact(0.50)(cpu_rate) AS cpu_p50,
  quantileExact(0.95)(cpu_rate) AS cpu_p95,
  max(cpu_rate) AS cpu_max,
  quantileExact(0.95)(mem_val) AS mem_p95,
  max(mem_val) AS mem_max,
  max(thr_rate) AS thr_rate,
  max(req_cpu) AS req_cpu,
  max(req_mem) AS req_mem,
  max(lim_cpu) AS lim_cpu,
  max(lim_mem) AS lim_mem
FROM (
  SELECT
    namespace,
    pod,
    container,
    k8s_cluster_name,
    bucket,
    greatest(maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s), 0) / %d AS cpu_rate,
    argMaxIf(Value, TimeUnix, MetricName = %s) AS mem_val,
    greatest(maxIf(Value, MetricName = %s) - minIf(Value, MetricName = %s), 0) / %d AS thr_rate,
    argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'cpu') AS req_cpu,
    argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'memory') AS req_mem,
    argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'cpu') AS lim_cpu,
    argMaxIf(Value, TimeUnix, MetricName = %s AND Attributes['resource'] = 'memory') AS lim_mem
  FROM (
    SELECT
      %s AS namespace,
      %s AS pod,
      %s AS container,
      %s AS k8s_cluster_name,
      toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS bucket,
      MetricName,
      TimeUnix,
      Value,
      Attributes
    FROM %s
    WHERE MetricName IN (%s, %s)
      AND %s AND (%s) AND (%s) AND (%s)
    UNION ALL
    SELECT
      %s AS namespace,
      %s AS pod,
      %s AS container,
      %s AS k8s_cluster_name,
      toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS bucket,
      MetricName,
      TimeUnix,
      Value,
      Attributes
    FROM %s
    WHERE MetricName IN (%s, %s, %s)
      AND %s AND (%s) AND (%s) AND (%s)
  )
  GROUP BY namespace, pod, container, k8s_cluster_name, bucket
  HAVING container != '' AND container != 'POD'
)
GROUP BY namespace, pod, container, k8s_cluster_name
LIMIT 5000`,
		q(MetricContainerCPUUsage), q(MetricContainerCPUUsage), bucket,
		q(MetricContainerMemoryWorkingSet),
		q(MetricContainerCPUThrottled), q(MetricContainerCPUThrottled), bucket,
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceLimits),
		q(MetricKubePodResourceLimits),
		nsE, podE, ctrE, clE, bucket,
		table(queries.CHTableMetricsSum),
		q(MetricContainerCPUUsage), q(MetricContainerCPUThrottled),
		tf, nsF, envF, ctrX,
		nsE, podE, ctrE, clE, bucket,
		table(queries.CHTableMetricsGauge),
		q(MetricContainerMemoryWorkingSet), q(MetricKubePodResourceRequests), q(MetricKubePodResourceLimits),
		tf, nsF, envF, ctrX,
	)
}

// CapacityHistorySeriesSQL returns bucketed fleet aggregates for one namespace.
func CapacityHistorySeriesSQL(namespace, container, env string, from, to time.Time, stepSec int) string {
	if stepSec < 15 {
		stepSec = 15
	}
	tf := MetricTimeFilter(from, to)
	nsF := capacityNamespaceFilter(namespace)
	envF := capacityEnvFilter(env)
	ctrF := "1"
	if container != "" {
		ctrF = fmt.Sprintf("(%s = %s)", capacityContainerExpr(), q(container))
	}
	ctrX := capacityContainerExclude()
	nsE := capacityNamespaceExpr()
	podE := capacityPodExpr()
	ctrE := capacityContainerExpr()

	return fmt.Sprintf(`
SELECT
  bucket_ms,
  greatest(sumIf(delta, MetricName = %s), 0) / %d AS cpu_usage,
  sumIf(latest, MetricName = %s AND resource = 'cpu') AS cpu_request,
  sumIf(latest, MetricName = %s AND resource = 'cpu') AS cpu_limit,
  sumIf(latest, MetricName = %s) AS mem_usage,
  sumIf(latest, MetricName = %s AND resource = 'memory') AS mem_request,
  sumIf(latest, MetricName = %s AND resource = 'memory') AS mem_limit,
  greatest(sumIf(delta, MetricName = %s), 0) / %d AS cpu_throttled,
  greatest(sumIf(delta, MetricName = %s), 0) AS restarts
FROM (
  SELECT
    toUnixTimestamp(toStartOfInterval(TimeUnix, INTERVAL %d SECOND)) * 1000 AS bucket_ms,
    MetricName,
    coalesce(nullIf(Attributes['resource'], ''), '') AS resource,
    %s AS namespace,
    %s AS pod,
    %s AS container,
    greatest(max(Value) - min(Value), 0) AS delta,
    argMax(Value, TimeUnix) AS latest
  FROM (
    SELECT TimeUnix, MetricName, Value, Attributes, ResourceAttributes
    FROM %s
    WHERE MetricName IN (%s, %s, %s)
      AND %s AND (%s) AND (%s) AND (%s) AND (%s)
    UNION ALL
    SELECT TimeUnix, MetricName, Value, Attributes, ResourceAttributes
    FROM %s
    WHERE MetricName IN (%s, %s, %s)
      AND %s AND (%s) AND (%s) AND (%s) AND (%s)
  )
  GROUP BY bucket_ms, MetricName, resource, namespace, pod, container
  HAVING container != '' AND container != 'POD'
)
GROUP BY bucket_ms
ORDER BY bucket_ms
LIMIT 5000`,
		q(MetricContainerCPUUsage), stepSec,
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceLimits),
		q(MetricContainerMemoryWorkingSet),
		q(MetricKubePodResourceRequests),
		q(MetricKubePodResourceLimits),
		q(MetricContainerCPUThrottled), stepSec,
		q(MetricKubePodRestarts),
		stepSec,
		nsE, podE, ctrE,
		table(queries.CHTableMetricsSum),
		q(MetricContainerCPUUsage), q(MetricContainerCPUThrottled), q(MetricKubePodRestarts),
		tf, nsF, envF, ctrF, ctrX,
		table(queries.CHTableMetricsGauge),
		q(MetricContainerMemoryWorkingSet), q(MetricKubePodResourceRequests), q(MetricKubePodResourceLimits),
		tf, nsF, envF, ctrF, ctrX,
	)
}

// Note: restarts counter is queried from otel_metrics_sum (same as CPU counters).

// ParseCapacityProbeCount reads probe SQL `c` column.
func ParseCapacityProbeCount(rows []map[string]any) float64 {
	if len(rows) == 0 {
		return 0
	}
	return ParseFloat(rows[0]["c"])
}

// ParseCapacityLedgerRows maps ledger SQL rows to identity structs.
func ParseCapacityLedgerRows(rows []map[string]any) []CapacityContainerIdentity {
	out := make([]CapacityContainerIdentity, 0, len(rows))
	for _, r := range rows {
		ctr := ParseString(r["container"])
		if ctr == "" || ctr == "POD" {
			continue
		}
		ns := ParseString(r["namespace"])
		if ns == "" {
			continue
		}
		out = append(out, CapacityContainerIdentity{
			Namespace:   ns,
			Pod:         ParseString(r["pod"]),
			Container:   ctr,
			Cluster:     ParseString(r["k8s_cluster_name"]),
			Node:        ParseString(r["node"]),
			UsageCPU:    ParseFloat(r["usage_cpu"]),
			UsageMem:    ParseFloat(r["usage_mem"]),
			ReqCPU:      ParseFloat(r["req_cpu"]),
			ReqMem:      ParseFloat(r["req_mem"]),
			LimCPU:      ParseFloat(r["lim_cpu"]),
			LimMem:      ParseFloat(r["lim_mem"]),
			Throttled:   ParseFloat(r["thr_rate"]),
			HasUsageCPU: ParseFloat(r["has_usage_cpu"]) > 0,
			HasUsageMem: ParseFloat(r["has_usage_mem"]) > 0,
			HasReqCPU:   ParseFloat(r["has_req_cpu"]) > 0,
			HasReqMem:   ParseFloat(r["has_req_mem"]) > 0,
			HasLimCPU:   ParseFloat(r["has_lim_cpu"]) > 0,
			HasLimMem:   ParseFloat(r["has_lim_mem"]) > 0,
			HasThrottle: ParseFloat(r["has_throttle"]) > 0,
		})
	}
	return out
}

func promVal(v float64) queries.PromValue {
	return queries.NewPromValue(0, strconv.FormatFloat(v, 'f', -1, 64))
}

func promMetric(labels map[string]string, v float64) queries.PromResult {
	m := make(map[string]string, len(labels))
	for k, val := range labels {
		if val != "" {
			m[k] = val
		}
	}
	return queries.PromResult{Metric: m, Value: promVal(v)}
}

type capacityNsAgg struct {
	nsName                   string
	wasteCPU, wasteMem       float64
	usageCPU, usageMem       float64
	reqCPU, reqMem           float64
	missReqCPU, missReqMem   int
	missLimCPU, missLimMem   int
	reservedCPU, reservedMem int
	cluster                  string
}

type capacityWasteRow struct {
	ns, pod, ctr, cluster string
	waste                 float64
}

func emptyCapacityResultMap() map[string][]queries.PromResult {
	return map[string][]queries.PromResult{
		"wasteCpuNs": {}, "wasteMemNs": {}, "wasteCpuTop": {}, "wasteMemTop": {},
		"sumCpuUsage": {}, "sumMemUsage": {}, "sumCpuReq": {}, "sumMemReq": {},
		"allocCpu": {}, "allocMem": {},
		"nsCpuUsage": {}, "nsMemUsage": {}, "nsCpuReq": {}, "nsMemReq": {},
		"nodeCpuUsage": {}, "nodeMemUsage": {}, "nodeCpuAlloc": {}, "nodeMemAlloc": {},
		"nodeFsUsage": {}, "nodeFsLimit": {},
		"gapMissingReqCpu": {}, "gapMissingReqMem": {},
		"gapMissingLimCpu": {}, "gapMissingLimMem": {},
		"gapReservedCpu": {}, "gapReservedMem": {},
		"underCpu": {}, "underMem": {}, "throttled": {},
	}
}

func accumulateCapacityRow(
	r CapacityContainerIdentity,
	ns *capacityNsAgg,
	rm map[string][]queries.PromResult,
	topCPU, topMem *[]capacityWasteRow,
) (sumCPUUsage, sumMemUsage, sumCPUReq, sumMemReq float64) {
	labels := map[string]string{
		"namespace": r.Namespace, "pod": r.Pod, "container": r.Container, "k8s_cluster_name": r.Cluster,
	}
	sumCPUUsage = r.UsageCPU
	sumMemUsage = r.UsageMem
	ns.usageCPU += r.UsageCPU
	ns.usageMem += r.UsageMem
	if r.HasReqCPU {
		sumCPUReq = r.ReqCPU
		ns.reqCPU += r.ReqCPU
	}
	if r.HasReqMem {
		sumMemReq = r.ReqMem
		ns.reqMem += r.ReqMem
	}
	hasUsage := r.HasUsageCPU || r.HasUsageMem || r.UsageCPU > 0 || r.UsageMem > 0
	if r.HasReqCPU {
		w := math.Max(r.ReqCPU-r.UsageCPU, 0)
		ns.wasteCPU += w
		if w > 0 {
			*topCPU = append(*topCPU, capacityWasteRow{r.Namespace, r.Pod, r.Container, r.Cluster, w})
		}
	}
	if r.HasReqMem {
		w := math.Max(r.ReqMem-r.UsageMem, 0)
		ns.wasteMem += w
		if w > 0 {
			*topMem = append(*topMem, capacityWasteRow{r.Namespace, r.Pod, r.Container, r.Cluster, w})
		}
	}
	if hasUsage && !r.HasReqCPU {
		ns.missReqCPU++
	}
	if hasUsage && !r.HasReqMem {
		ns.missReqMem++
	}
	if hasUsage && !r.HasLimCPU {
		ns.missLimCPU++
	}
	if hasUsage && !r.HasLimMem {
		ns.missLimMem++
	}
	if r.HasReqCPU && !r.HasUsageCPU && r.UsageCPU == 0 {
		ns.reservedCPU++
	}
	if r.HasReqMem && !r.HasUsageMem && r.UsageMem == 0 {
		ns.reservedMem++
	}
	if r.HasReqCPU && r.UsageCPU > r.ReqCPU {
		rm["underCpu"] = append(rm["underCpu"], promMetric(labels, r.UsageCPU-r.ReqCPU))
	}
	if r.HasReqMem && r.UsageMem > r.ReqMem {
		rm["underMem"] = append(rm["underMem"], promMetric(labels, r.UsageMem-r.ReqMem))
	}
	if r.HasThrottle && r.Throttled > 0 {
		rm["throttled"] = append(rm["throttled"], promMetric(labels, r.Throttled))
	}
	return sumCPUUsage, sumMemUsage, sumCPUReq, sumMemReq
}

func emitCapacityNamespaceGaps(rm map[string][]queries.PromResult, nsMap map[string]*capacityNsAgg) {
	for _, a := range nsMap {
		nsName := a.nsName
		labels := map[string]string{"namespace": nsName, "k8s_cluster_name": a.cluster}
		appendIfPos := func(key string, v float64) {
			if v > 0 {
				rm[key] = append(rm[key], promMetric(labels, v))
			}
		}
		appendIfPos("wasteCpuNs", a.wasteCPU)
		appendIfPos("wasteMemNs", a.wasteMem)
		appendIfPos("nsCpuUsage", a.usageCPU)
		appendIfPos("nsMemUsage", a.usageMem)
		appendIfPos("nsCpuReq", a.reqCPU)
		appendIfPos("nsMemReq", a.reqMem)
		appendIfPos("gapMissingReqCpu", float64(a.missReqCPU))
		appendIfPos("gapMissingReqMem", float64(a.missReqMem))
		appendIfPos("gapMissingLimCpu", float64(a.missLimCPU))
		appendIfPos("gapMissingLimMem", float64(a.missLimMem))
		appendIfPos("gapReservedCpu", float64(a.reservedCPU))
		appendIfPos("gapReservedMem", float64(a.reservedMem))
	}
}

func emitCapacityTopWaste(rm map[string][]queries.PromResult, topCPU, topMem []capacityWasteRow) {
	sort.Slice(topCPU, func(i, j int) bool { return topCPU[i].waste > topCPU[j].waste })
	sort.Slice(topMem, func(i, j int) bool { return topMem[i].waste > topMem[j].waste })
	const topN = 50
	if len(topCPU) > topN {
		topCPU = topCPU[:topN]
	}
	if len(topMem) > topN {
		topMem = topMem[:topN]
	}
	for _, w := range topCPU {
		rm["wasteCpuTop"] = append(rm["wasteCpuTop"], promMetric(map[string]string{
			"namespace": w.ns, "pod": w.pod, "container": w.ctr, "k8s_cluster_name": w.cluster,
		}, w.waste))
	}
	for _, w := range topMem {
		rm["wasteMemTop"] = append(rm["wasteMemTop"], promMetric(map[string]string{
			"namespace": w.ns, "pod": w.pod, "container": w.ctr, "k8s_cluster_name": w.cluster,
		}, w.waste))
	}
}

// CapacityLedgerToResultMap builds the PromResult job map aggregateCapacity expects.
func CapacityLedgerToResultMap(rows []CapacityContainerIdentity, allocCPU, allocMem float64) map[string][]queries.PromResult {
	rm := emptyCapacityResultMap()
	nsMap := map[string]*capacityNsAgg{}
	var sumCPUUsage, sumMemUsage, sumCPUReq, sumMemReq float64
	var topCPU, topMem []capacityWasteRow

	type nodeAgg struct{ cpu, mem float64 }
	nodeUsage := map[string]*nodeAgg{}

	for _, r := range rows {
		key := r.Namespace
		if r.Cluster != "" {
			key = r.Cluster + "\x00" + r.Namespace
		}
		ns := nsMap[key]
		if ns == nil {
			ns = &capacityNsAgg{nsName: r.Namespace, cluster: r.Cluster}
			nsMap[key] = ns
		}
		if r.Cluster != "" {
			ns.cluster = r.Cluster
		}
		uCPU, uMem, rCPU, rMem := accumulateCapacityRow(r, ns, rm, &topCPU, &topMem)
		sumCPUUsage += uCPU
		sumMemUsage += uMem
		sumCPUReq += rCPU
		sumMemReq += rMem
		if r.Node != "" {
			na := nodeUsage[r.Node]
			if na == nil {
				na = &nodeAgg{}
				nodeUsage[r.Node] = na
			}
			na.cpu += r.UsageCPU
			na.mem += r.UsageMem
		}
	}

	emitCapacityNamespaceGaps(rm, nsMap)
	emitCapacityTopWaste(rm, topCPU, topMem)

	for node, na := range nodeUsage {
		labels := map[string]string{"node": node}
		if na.cpu > 0 {
			rm["nodeCpuUsage"] = append(rm["nodeCpuUsage"], promMetric(labels, na.cpu))
		}
		if na.mem > 0 {
			rm["nodeMemUsage"] = append(rm["nodeMemUsage"], promMetric(labels, na.mem))
		}
	}

	rm["sumCpuUsage"] = []queries.PromResult{promMetric(nil, sumCPUUsage)}
	rm["sumMemUsage"] = []queries.PromResult{promMetric(nil, sumMemUsage)}
	rm["sumCpuReq"] = []queries.PromResult{promMetric(nil, sumCPUReq)}
	rm["sumMemReq"] = []queries.PromResult{promMetric(nil, sumMemReq)}
	if allocCPU > 0 {
		rm["allocCpu"] = []queries.PromResult{promMetric(nil, allocCPU)}
	}
	if allocMem > 0 {
		rm["allocMem"] = []queries.PromResult{promMetric(nil, allocMem)}
	}
	return rm
}

// MergeNodeAllocIntoResultMap adds per-node allocatable series from CapacityNodeAllocSQL rows.
func MergeNodeAllocIntoResultMap(rm map[string][]queries.PromResult, rows []map[string]any) {
	if rm == nil {
		return
	}
	for _, r := range rows {
		node := ParseString(r["node"])
		if node == "" {
			continue
		}
		labels := map[string]string{"node": node}
		if cpu := ParseFloat(r["alloc_cpu"]); cpu > 0 {
			rm["nodeCpuAlloc"] = append(rm["nodeCpuAlloc"], promMetric(labels, cpu))
		}
		if mem := ParseFloat(r["alloc_mem"]); mem > 0 {
			rm["nodeMemAlloc"] = append(rm["nodeMemAlloc"], promMetric(labels, mem))
		}
	}
}

// MergeNodeFsIntoResultMap adds per-node filesystem usage/limit from CapacityNodeFsSQL rows.
func MergeNodeFsIntoResultMap(rm map[string][]queries.PromResult, rows []map[string]any) {
	if rm == nil {
		return
	}
	for _, r := range rows {
		node := ParseString(r["node"])
		if node == "" {
			continue
		}
		labels := map[string]string{"node": node}
		if usage := ParseFloat(r["usage_bytes"]); usage > 0 {
			rm["nodeFsUsage"] = append(rm["nodeFsUsage"], promMetric(labels, usage))
		}
		if limit := ParseFloat(r["limit_bytes"]); limit > 0 {
			rm["nodeFsLimit"] = append(rm["nodeFsLimit"], promMetric(labels, limit))
		}
	}
}

// ParseCapacityAllocatable reads alloc_cpu / alloc_mem from CapacityAllocatableSQL.
func ParseCapacityAllocatable(rows []map[string]any) (cpu, mem float64) {
	if len(rows) == 0 {
		return 0, 0
	}
	return ParseFloat(rows[0]["alloc_cpu"]), ParseFloat(rows[0]["alloc_mem"])
}

// CapacityRecRow is one recommendations bucket aggregate.
type CapacityRecRow struct {
	Namespace string
	Pod       string
	Container string
	Cluster   string
	CPUP50    float64
	CPUP95    float64
	CPUMax    float64
	MemP95    float64
	MemMax    float64
	Throttled float64
	ReqCPU    float64
	ReqMem    float64
	LimCPU    float64
	LimMem    float64
}

// ParseCapacityRecRows maps CapacityRecsBucketSQL output.
func ParseCapacityRecRows(rows []map[string]any) []CapacityRecRow {
	out := make([]CapacityRecRow, 0, len(rows))
	for _, r := range rows {
		ctr := ParseString(r["container"])
		ns := ParseString(r["namespace"])
		if ns == "" || ctr == "" || ctr == "POD" {
			continue
		}
		out = append(out, CapacityRecRow{
			Namespace: ns,
			Pod:       ParseString(r["pod"]),
			Container: ctr,
			Cluster:   ParseString(r["k8s_cluster_name"]),
			CPUP50:    ParseFloat(r["cpu_p50"]),
			CPUP95:    ParseFloat(r["cpu_p95"]),
			CPUMax:    ParseFloat(r["cpu_max"]),
			MemP95:    ParseFloat(r["mem_p95"]),
			MemMax:    ParseFloat(r["mem_max"]),
			Throttled: ParseFloat(r["thr_rate"]),
			ReqCPU:    ParseFloat(r["req_cpu"]),
			ReqMem:    ParseFloat(r["req_mem"]),
			LimCPU:    ParseFloat(r["lim_cpu"]),
			LimMem:    ParseFloat(r["lim_mem"]),
		})
	}
	return out
}

// CapacityOwner is a kube_pod_owner row.
type CapacityOwner struct {
	Namespace string
	Pod       string
	Kind      string
	Name      string
	Cluster   string
}

// ParseCapacityOwners maps CapacityPodOwnerSQL.
func ParseCapacityOwners(rows []map[string]any) []CapacityOwner {
	out := make([]CapacityOwner, 0, len(rows))
	for _, r := range rows {
		pod := ParseString(r["pod"])
		if pod == "" {
			continue
		}
		out = append(out, CapacityOwner{
			Namespace: ParseString(r["namespace"]),
			Pod:       pod,
			Kind:      ParseString(r["owner_kind"]),
			Name:      ParseString(r["owner_name"]),
			Cluster:   ParseString(r["k8s_cluster_name"]),
		})
	}
	return out
}

// CapacityHistoryPointRow is one bucket from CapacityHistorySeriesSQL.
type CapacityHistoryPointRow struct {
	BucketMs     int64
	CPUUsage     float64
	CPURequest   float64
	CPULimit     float64
	MemUsage     float64
	MemRequest   float64
	MemLimit     float64
	CPUThrottled float64
	Restarts     float64
}

// ParseCapacityHistoryRows maps history SQL.
func ParseCapacityHistoryRows(rows []map[string]any) []CapacityHistoryPointRow {
	out := make([]CapacityHistoryPointRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, CapacityHistoryPointRow{
			BucketMs:     int64(ParseFloat(r["bucket_ms"])),
			CPUUsage:     ParseFloat(r["cpu_usage"]),
			CPURequest:   ParseFloat(r["cpu_request"]),
			CPULimit:     ParseFloat(r["cpu_limit"]),
			MemUsage:     ParseFloat(r["mem_usage"]),
			MemRequest:   ParseFloat(r["mem_request"]),
			MemLimit:     ParseFloat(r["mem_limit"]),
			CPUThrottled: ParseFloat(r["cpu_throttled"]),
			Restarts:     ParseFloat(r["restarts"]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BucketMs < out[j].BucketMs })
	return out
}

// LinearForecast projects usage forward 7d from points. Returns end prediction and slope/sec.
func LinearForecast(points []struct {
	T int64
	V float64
}, horizonSec float64) (pred, slopePerSec float64, ok bool) {
	n := len(points)
	if n < 2 {
		return 0, 0, false
	}
	var sumT, sumV, sumTT, sumTV float64
	for _, p := range points {
		t := float64(p.T) / 1000 // seconds
		sumT += t
		sumV += p.V
		sumTT += t * t
		sumTV += t * p.V
	}
	nf := float64(n)
	den := nf*sumTT - sumT*sumT
	if den == 0 {
		return 0, 0, false
	}
	slope := (nf*sumTV - sumT*sumV) / den
	intercept := (sumV - slope*sumT) / nf
	tLast := float64(points[n-1].T) / 1000
	pred = intercept + slope*(tLast+horizonSec)
	return pred, slope, true
}

// CapacityProbeForbidsProcess reports whether SQL avoids process_* (forbidden usage substitute).
func CapacityProbeForbidsProcess(sql string) bool {
	return !strings.Contains(sql, "process_")
}
