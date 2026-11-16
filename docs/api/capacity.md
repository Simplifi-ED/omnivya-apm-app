# Capacity

Kubernetes resource efficiency, right-sizing recommendations, history, and
forecasts. All calculations use standard cAdvisor and kube-state-metrics series.
Goldilocks-compatible right-sizing recommendations are computed in-plugin from
the same series; no Goldilocks or VPA agents are required in the cluster.

Experimental.

Base: `{GRAFANA_URL}/api/plugins/omnivya-apm-app/resources`.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/capacity` | Fleet waste, gaps, efficiency, estimated reclaimable/deficit USD |
| GET | `/capacity/recommendations` | Burstable / Guaranteed QoS recommendations + YAML patch |
| GET | `/capacity/history` | Usage / request / limit / throttle / restart range series |
| GET | `/capacity/forecast` | Predict CPU or memory (labeled `linear projection`) |
| GET | `/namespaces/{namespace}/capacity` | Namespace-scoped overview |

Shared query params: `environment`, `from`, `to`, optional `namespace`.

USD figures are rate-based estimates from configurable
`capacityCostRates` in plugin jsonData (defaults `$0.0316/vCPU-hr`,
`$0.0042/GiB-hr`). They are not cloud-billing sync.

Memory efficiency prefers `container_memory_working_set_bytes` over
page-cache `container_memory_usage_bytes`.

## Dual path (Path A / Path B)

Same Jobs-style resolve order:

1. **Path A** — Prometheus `metricsDataSource` with PromQL (preferred when series exist).
2. **Path B** — ClickHouse `otel_metrics_sum` / `otel_metrics_gauge` with **Prom-style MetricNames** (prometheus receiver → CH), when Path A is missing or unavailable.
3. Soft-unavailable (`available: false` + `enablementNote`) when neither path has the required families. No fake zeros.

### Path B enablement

Path B needs **both** families in ClickHouse:

| Family | MetricNames | Table |
| --- | --- | --- |
| Usage (cAdvisor / kubelet) | `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes` (optional throttle/restarts) | sum / gauge |
| KSM | `kube_pod_container_resource_requests`, `_limits`, optional `kube_pod_owner`, `kube_node_status_allocatable` | gauge |

- Cluster label coalesces `Attributes['k8s_cluster_name']`, `ResourceAttributes['k8s.cluster.name']`, and `ResourceAttributes['cluster']`.
- Excludes `container=""` and `container="POD"`.
- Rate window is **5m** (same as Path A `capacityLookback`).
- **`process_*` is never treated as Capacity usage** (wrong grain / join keys).
- KSM-only installs return `available: false` with a **missing-usage** enablement note (not fake efficiency from requests alone).

Scrape kubelet/cAdvisor and kube-state-metrics into ClickHouse the same way existing KSM gauges already land.

### Path B recommendations

Window percentiles approximate Path A `quantile_over_time(rate(...))` via 5m buckets in ClickHouse (`quantileExact` over per-bucket rates). Same Goldilocks burstable/guaranteed math and YAML patch as Path A.

### Path B history / forecast

Bucketed range series with step clamp parity to Path A. Forecast uses an in-plugin **linear projection** over usage points (`methodLabel: "linear projection"`), including `daysToRequest` / `daysToLimit` when slope is positive. Ranges under 2h stay disabled with the existing note.
