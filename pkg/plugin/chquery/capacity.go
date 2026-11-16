package chquery

// Capacity Path B helpers. The overview currently soft-fails to Path A
// (Prometheus) when ClickHouse lacks joined container request/usage series.
// These constants document the MetricName values Path B will query when the
// CH join path is enabled.

// Metric names for Capacity Path B (ClickHouse otel metrics).
const (
	MetricContainerCPUUsage         = "container_cpu_usage_seconds_total"
	MetricContainerMemoryWorkingSet = "container_memory_working_set_bytes"
	MetricKubePodResourceRequests   = "kube_pod_container_resource_requests"
	MetricKubePodResourceLimits     = "kube_pod_container_resource_limits"
	MetricKubeNodeAllocatable       = "kube_node_status_allocatable"
)
