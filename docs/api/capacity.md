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
| GET | `/capacity/forecast` | Predict CPU or memory (ML or labeled `predict_linear`) |
| GET | `/namespaces/{namespace}/capacity` | Namespace-scoped overview |

Shared query params: `environment`, `from`, `to`, optional `namespace`.

USD figures are rate-based estimates from configurable
`capacityCostRates` in plugin jsonData (defaults `$0.0316/vCPU-hr`,
`$0.0042/GiB-hr`). They are not cloud-billing sync.

Memory efficiency prefers `container_memory_working_set_bytes` over
page-cache `container_memory_usage_bytes`.
