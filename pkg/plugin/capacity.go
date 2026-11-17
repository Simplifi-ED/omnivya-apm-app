package plugin

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/chquery"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/otelconfig"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/queries"
)

const (
	capacityCacheTTL       = 2 * time.Minute
	capacityTopN           = 50
	capacityLookback       = "5m"
	capacityEnablementNote = "Capacity efficiency needs cAdvisor and kube-state-metrics " +
		"(container_* and kube_pod_container_resource_*) on a Prometheus metricsDataSource (Path A) " +
		"or the same Prom-style series in ClickHouse otel_metrics_* (Path B). " +
		"Scrape kube-state-metrics and kubelet/cAdvisor into the configured metrics backend. " +
		"No cluster agents (for example Goldilocks or VPA) are required."
	capacityCHMissingUsageNote = "ClickHouse has kube-state-metrics gauges but no cAdvisor usage series " +
		"(container_cpu_usage_seconds_total or container_memory_working_set_bytes). " +
		"Scrape kubelet/cAdvisor with Prom-style MetricNames into otel_metrics_*. process_* is not a valid usage substitute."
	capacityCHMissingKSMNote = "ClickHouse has usage series but no kube_pod_container_resource_requests. " +
		"Scrape kube-state-metrics into otel_metrics_gauge."
)

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// CapacityGapCounts aggregates fleet-wide configuration and utilization gaps.
type CapacityGapCounts struct {
	MissingRequest       int `json:"missingRequest"`
	MissingLimit         int `json:"missingLimit"`
	OverRequestedCPU     int `json:"overRequestedCpu"`
	OverRequestedMem     int `json:"overRequestedMem"`
	UnderProvisioned     int `json:"underProvisioned"`
	Throttled            int `json:"throttled"`
	ReservedWithoutUsage int `json:"reservedWithoutUsage"`
}

// CapacityEfficiency summarizes used/requested/allocatable ratios and rate-based USD estimates.
type CapacityEfficiency struct {
	CPUUsedRatio          float64  `json:"cpuUsedRatio"`
	MemUsedRatio          float64  `json:"memUsedRatio"`
	CPUAllocRatio         *float64 `json:"cpuAllocRatio,omitempty"`
	MemAllocRatio         *float64 `json:"memAllocRatio,omitempty"`
	ReclaimableMonthlyUSD float64  `json:"reclaimableMonthlyUsd"`
	DeficitMonthlyUSD     float64  `json:"deficitMonthlyUsd"`
	CPUHourUSD            float64  `json:"cpuHourUsd"`
	MemoryGiBHourUSD      float64  `json:"memoryGibHourUsd"`
	RatesAreEstimates     bool     `json:"ratesAreEstimates"`
}

// CapacityNamespaceRollup is one namespace row in the fleet table.
type CapacityNamespaceRollup struct {
	Namespace             string  `json:"namespace"`
	Environment           string  `json:"environment,omitempty"`
	WasteCPU              float64 `json:"wasteCpu"`
	WasteMem              float64 `json:"wasteMem"`
	CPUUsage              float64 `json:"cpuUsage,omitempty"`
	CPURequest            float64 `json:"cpuRequest,omitempty"`
	MemUsage              float64 `json:"memUsage,omitempty"`
	MemRequest            float64 `json:"memRequest,omitempty"`
	CPUUsedRatio          float64 `json:"cpuUsedRatio,omitempty"`
	MemUsedRatio          float64 `json:"memUsedRatio,omitempty"`
	ReclaimableMonthlyUSD float64 `json:"reclaimableMonthlyUsd"`
	MissingRequest        int     `json:"missingRequest"`
	MissingLimit          int     `json:"missingLimit"`
	UnderProvisioned      int     `json:"underProvisioned"`
	Throttled             int     `json:"throttled"`
}

// CapacityEfficiencyBand is under/ok/over share of a population (fractions 0..1).
type CapacityEfficiencyBand struct {
	Under float64 `json:"under"`
	OK    float64 `json:"ok"`
	Over  float64 `json:"over"`
}

// CapacityResourcePopulation is CPU/mem/storage band shares for one scope.
type CapacityResourcePopulation struct {
	CPU     CapacityEfficiencyBand  `json:"cpu"`
	Mem     CapacityEfficiencyBand  `json:"mem"`
	Storage *CapacityEfficiencyBand `json:"storage,omitempty"` // nil until fs series present
}

// CapacityPopulationScope is one Efficiency card (cluster/node/namespace).
type CapacityPopulationScope struct {
	Total int                        `json:"total"`
	Bands CapacityResourcePopulation `json:"bands"`
}

// CapacityPopulation powers Overview Efficiency cards.
type CapacityPopulation struct {
	Clusters   CapacityPopulationScope `json:"clusters"`
	Nodes      CapacityPopulationScope `json:"nodes"`
	Namespaces CapacityPopulationScope `json:"namespaces"`
}

// CapacityNodeRollup is one node row for the Nodes tab.
type CapacityNodeRollup struct {
	Node           string   `json:"node"`
	Environment    string   `json:"environment,omitempty"`
	CPUUsage       float64  `json:"cpuUsage"`
	CPUAlloc       float64  `json:"cpuAlloc"`
	CPUUsedRatio   float64  `json:"cpuUsedRatio,omitempty"`
	MemUsage       float64  `json:"memUsage"`
	MemAlloc       float64  `json:"memAlloc"`
	MemUsedRatio   float64  `json:"memUsedRatio,omitempty"`
	StorageUsage   *float64 `json:"storageUsage,omitempty"`
	StorageLimit   *float64 `json:"storageLimit,omitempty"`
	StorageRatio   *float64 `json:"storageRatio,omitempty"`
	PodCount       int      `json:"podCount,omitempty"`
	ConditionReady bool     `json:"conditionReady"`
}

// CapacityContainerRow is a ranked container waste or risk entry.
type CapacityContainerRow struct {
	Namespace   string  `json:"namespace"`
	Pod         string  `json:"pod,omitempty"`
	Container   string  `json:"container"`
	Environment string  `json:"environment,omitempty"`
	WasteCPU    float64 `json:"wasteCpu,omitempty"`
	WasteMem    float64 `json:"wasteMem,omitempty"`
	CPUUsage    float64 `json:"cpuUsage,omitempty"`
	CPURequest  float64 `json:"cpuRequest,omitempty"`
	MemUsage    float64 `json:"memUsage,omitempty"`
	MemRequest  float64 `json:"memRequest,omitempty"`
	Throttled   float64 `json:"throttled,omitempty"`
	Risk        string  `json:"risk,omitempty"`
}

// CapacityOverview is the GET /capacity payload.
type CapacityOverview struct {
	Available         bool                      `json:"available"`
	EnablementNote    string                    `json:"enablementNote,omitempty"`
	Efficiency        CapacityEfficiency        `json:"efficiency"`
	Population        CapacityPopulation        `json:"population"`
	GapCounts         CapacityGapCounts         `json:"gapCounts"`
	Namespaces        []CapacityNamespaceRollup `json:"namespaces"`
	Nodes             []CapacityNodeRollup      `json:"nodes"`
	TopWasteCPU       []CapacityContainerRow    `json:"topWasteCpu"`
	TopWasteMem       []CapacityContainerRow    `json:"topWasteMem"`
	TopRiskContainers []CapacityContainerRow    `json:"topRiskContainers"`
}

// WorkloadResources is a request/limit set for one QoS class.
type WorkloadResources struct {
	CPUReq float64  `json:"cpuReq"`
	CPULim *float64 `json:"cpuLim,omitempty"`
	MemReq float64  `json:"memReq"`
	MemLim float64  `json:"memLim"`
}

// WorkloadObserved is historical usage used for right-sizing.
type WorkloadObserved struct {
	CPUP50        float64 `json:"cpuP50"`
	CPUP90        float64 `json:"cpuP90"`
	CPUP95        float64 `json:"cpuP95"`
	CPUMax        float64 `json:"cpuMax"`
	MemP50        float64 `json:"memP50"`
	MemP90        float64 `json:"memP90"`
	MemP95        float64 `json:"memP95"`
	MemMax        float64 `json:"memMax"`
	ThrottledRate float64 `json:"throttledRate"`
}

// WorkloadRecommendation is one Goldilocks-style right-sizing row.
type WorkloadRecommendation struct {
	Namespace             string            `json:"namespace"`
	Environment           string            `json:"environment,omitempty"`
	WorkloadKind          string            `json:"workloadKind"`
	WorkloadName          string            `json:"workloadName"`
	Container             string            `json:"container"`
	Replicas              int               `json:"replicas"`
	Confidence            string            `json:"confidence"`
	Current               WorkloadResources `json:"current"`
	Observed              WorkloadObserved  `json:"observed"`
	Bounds                WorkloadBounds    `json:"bounds"`
	Burstable             WorkloadResources `json:"burstable"`
	Guaranteed            WorkloadResources `json:"guaranteed"`
	ReclaimableMonthlyUSD float64           `json:"reclaimableMonthlyUsd"`
	DeficitMonthlyUSD     float64           `json:"deficitMonthlyUsd"`
	YAMLPatch             string            `json:"yamlPatch"`
	YAMLPatchGuaranteed   string            `json:"yamlPatchGuaranteed"`
	QoSClass              string            `json:"qosClass"`
	Notes                 []string          `json:"notes,omitempty"`
}

// CapacityRecommendationsResponse is GET /capacity/recommendations.
type CapacityRecommendationsResponse struct {
	Available       bool                     `json:"available"`
	EnablementNote  string                   `json:"enablementNote,omitempty"`
	Recommendations []WorkloadRecommendation `json:"recommendations"`
}

// CapacityHistoryPoint is one step in a history series.
type CapacityHistoryPoint struct {
	TimestampMs int64   `json:"timestampMs"`
	Value       float64 `json:"value"`
}

// CapacityHistorySeries is a named time series.
type CapacityHistorySeries struct {
	Name   string                 `json:"name"`
	Points []CapacityHistoryPoint `json:"points"`
}

// CapacityHistoryResponse is GET /capacity/history.
type CapacityHistoryResponse struct {
	Available      bool                    `json:"available"`
	EnablementNote string                  `json:"enablementNote,omitempty"`
	Series         []CapacityHistorySeries `json:"series"`
	Note           string                  `json:"note,omitempty"`
}

// CapacityForecastResponse is GET /capacity/forecast.
type CapacityForecastResponse struct {
	Available      bool                    `json:"available"`
	EnablementNote string                  `json:"enablementNote,omitempty"`
	Method         string                  `json:"method"` // "ml" | "linear"
	MethodLabel    string                  `json:"methodLabel"`
	Resource       string                  `json:"resource"` // "cpu" | "memory"
	Series         []CapacityHistorySeries `json:"series"`
	DaysToRequest  *float64                `json:"daysToRequest,omitempty"`
	DaysToLimit    *float64                `json:"daysToLimit,omitempty"`
}

// ---------------------------------------------------------------------------
// Cache
// ---------------------------------------------------------------------------

func (a *App) capacityResponseCache() *responseCache {
	a.capacityOnce.Do(func() {
		c := newResponseCache()
		c.ttl = capacityCacheTTL
		a.capacityCache = c
	})
	return a.capacityCache
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleCapacity returns fleet/namespace resource efficiency.
// GET /capacity?environment=&namespace=&from=&to=
func (a *App) handleCapacity(w http.ResponseWriter, req *http.Request) {
	if !requireGET(w, req) {
		return
	}
	ctx := a.requestContext(req)
	_, to := parseTimeRange(req)
	namespace := queries.MustSanitizeLabel(req.URL.Query().Get("namespace"))
	env := parseEnvironment(req)

	orgID := req.Header.Get("X-Grafana-Org-Id")
	ck := cacheKey("capacity", orgID, a.capacityCallerKey(ctx, req), roundedUnix(to), namespace, env)
	cache := a.capacityResponseCache()
	if cached, ok := cache.get(ck); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		_, _ = w.Write(cached)
		return
	}
	data, err := cache.getOrCompute(ck, func() (any, error) {
		return a.resolveCapacity(ctx, req, namespace, env, to), nil
	})
	if err != nil {
		http.Error(w, "querying capacity failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	_, _ = w.Write(data)
}

// handleNamespaceCapacity returns Capacity scoped to one namespace.
// GET /namespaces/{namespace}/capacity?environment=&from=&to=
func (a *App) handleNamespaceCapacity(w http.ResponseWriter, req *http.Request) {
	if !requireGET(w, req) {
		return
	}
	ctx := a.requestContext(req)
	namespace := queries.MustSanitizeLabel(req.PathValue("namespace"))
	if namespace == "" {
		http.Error(w, "namespace is required", http.StatusBadRequest)
		return
	}
	_, to := parseTimeRange(req)
	env := parseEnvironment(req)

	orgID := req.Header.Get("X-Grafana-Org-Id")
	ck := cacheKey("capacity-ns", orgID, a.capacityCallerKey(ctx, req), roundedUnix(to), namespace, env)
	a.writeCached(w, ck, "querying namespace capacity failed", func() (any, error) {
		return a.resolveCapacity(ctx, req, namespace, env, to), nil
	})
}

// handleCapacityRecommendations returns Goldilocks-style right-sizing.
// GET /capacity/recommendations?namespace=&environment=&from=&to=
func (a *App) handleCapacityRecommendations(w http.ResponseWriter, req *http.Request) {
	if !requireGET(w, req) {
		return
	}
	ctx := a.requestContext(req)
	from, to := parseTimeRange(req)
	namespace := queries.MustSanitizeLabel(req.URL.Query().Get("namespace"))
	env := parseEnvironment(req)

	orgID := req.Header.Get("X-Grafana-Org-Id")
	ck := cacheKey("capacity-recs", orgID, a.capacityCallerKey(ctx, req), roundedUnix(from), roundedUnix(to), namespace, env)
	cache := a.capacityResponseCache()
	if cached, ok := cache.get(ck); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		_, _ = w.Write(cached)
		return
	}
	data, err := cache.getOrCompute(ck, func() (any, error) {
		return a.resolveRecommendations(ctx, req, namespace, env, from, to), nil
	})
	if err != nil {
		http.Error(w, "querying capacity recommendations failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	_, _ = w.Write(data)
}

// handleCapacityHistory returns usage/request/limit/throttle series.
// GET /capacity/history?namespace=&workload=&container=&environment=&from=&to=
func (a *App) handleCapacityHistory(w http.ResponseWriter, req *http.Request) {
	if !requireGET(w, req) {
		return
	}
	ctx := a.requestContext(req)
	from, to := parseTimeRange(req)
	namespace := queries.MustSanitizeLabel(req.URL.Query().Get("namespace"))
	workload := queries.MustSanitizeLabel(req.URL.Query().Get("workload"))
	container := queries.MustSanitizeLabel(req.URL.Query().Get("container"))
	env := parseEnvironment(req)

	orgID := req.Header.Get("X-Grafana-Org-Id")
	ck := cacheKey("capacity-hist", orgID, a.capacityCallerKey(ctx, req), roundedUnix(from), roundedUnix(to), namespace, workload, container, env)
	a.writeCached(w, ck, "querying capacity history failed", func() (any, error) {
		return a.resolveHistory(ctx, req, namespace, workload, container, env, from, to), nil
	})
}

// handleCapacityForecast returns ML or linear projections.
// GET /capacity/forecast?resource=cpu|memory&namespace=&workload=&container=&environment=&from=&to=
func (a *App) handleCapacityForecast(w http.ResponseWriter, req *http.Request) {
	if !requireGET(w, req) {
		return
	}
	ctx := a.requestContext(req)
	from, to := parseTimeRange(req)
	resource := strings.ToLower(queries.MustSanitizeLabel(req.URL.Query().Get("resource")))
	if resource != "cpu" && resource != "memory" {
		resource = "cpu"
	}
	namespace := queries.MustSanitizeLabel(req.URL.Query().Get("namespace"))
	workload := queries.MustSanitizeLabel(req.URL.Query().Get("workload"))
	container := queries.MustSanitizeLabel(req.URL.Query().Get("container"))
	env := parseEnvironment(req)

	if to.Sub(from) < 2*time.Hour {
		writeJSON(w, CapacityForecastResponse{
			Available:      false,
			EnablementNote: "Predict needs a time range of at least 2 hours.",
			Method:         "linear",
			MethodLabel:    "linear projection",
			Resource:       resource,
			Series:         []CapacityHistorySeries{},
		})
		return
	}

	orgID := req.Header.Get("X-Grafana-Org-Id")
	ck := cacheKey("capacity-fc", orgID, a.capacityCallerKey(ctx, req), roundedUnix(from), roundedUnix(to), resource, namespace, workload, container, env)
	a.writeCached(w, ck, "querying capacity forecast failed", func() (any, error) {
		return a.resolveForecast(ctx, req, resource, namespace, workload, container, env, from, to), nil
	})
}

// ---------------------------------------------------------------------------
// Resolve / query
// ---------------------------------------------------------------------------

func (a *App) resolveCapacity(ctx context.Context, req *http.Request, namespace, env string, at time.Time) CapacityOverview {
	empty := CapacityOverview{
		Available:         false,
		EnablementNote:    capacityEnablementNote,
		Namespaces:        []CapacityNamespaceRollup{},
		TopWasteCPU:       []CapacityContainerRow{},
		TopWasteMem:       []CapacityContainerRow{},
		TopRiskContainers: []CapacityContainerRow{},
		Efficiency:        CapacityEfficiency{RatesAreEstimates: true},
	}

	if a.prom(ctx) != nil {
		resp := a.queryCapacityProm(ctx, namespace, env, at)
		if resp.Available {
			return resp
		}
		if ch := a.chForRequest(req); ch != nil {
			if chResp := a.queryCapacityClickHouse(ctx, ch, namespace, env, at); chResp.Available {
				return chResp
			}
		}
		if resp.EnablementNote == "" {
			resp.EnablementNote = capacityEnablementNote
		}
		return resp
	}

	if ch := a.chForRequest(req); ch != nil {
		return a.queryCapacityClickHouse(ctx, ch, namespace, env, at)
	}
	return empty
}

func (a *App) queryCapacityProm(ctx context.Context, namespace, env string, at time.Time) CapacityOverview {
	logger := log.DefaultLogger.With("handler", "capacity")
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)

	cpuUsage := capacityCPUUsageExpr(ctr, scope)
	memUsage := capacityMemUsageExpr(ctr, scope)
	cpuReq := capacityResourceExpr(ctr.ResourceReqs, "cpu", ctr, scope)
	memReq := capacityResourceExpr(ctr.ResourceReqs, "memory", ctr, scope)
	cpuLim := capacityResourceExpr(ctr.ResourceLimits, "cpu", ctr, scope)
	memLim := capacityResourceExpr(ctr.ResourceLimits, "memory", ctr, scope)

	jobs := []QueryJob{
		{"wasteCpuNs", capacityWasteByNamespace(cpuReq, cpuUsage)},
		{"wasteMemNs", capacityWasteByNamespace(memReq, memUsage)},
		{"wasteCpuTop", capacityWasteTopk(cpuReq, cpuUsage, capacityTopN)},
		{"wasteMemTop", capacityWasteTopk(memReq, memUsage, capacityTopN)},
		{"sumCpuUsage", fmt.Sprintf(`sum(%s)`, cpuUsage)},
		{"sumMemUsage", fmt.Sprintf(`sum(%s)`, memUsage)},
		{"sumCpuReq", fmt.Sprintf(`sum(%s)`, cpuReq)},
		{"sumMemReq", fmt.Sprintf(`sum(%s)`, memReq)},
		{"gapMissingReqCpu", capacityMissingRequestContainers(cpuUsage, cpuReq)},
		{"gapMissingReqMem", capacityMissingRequestContainers(memUsage, memReq)},
		{"gapMissingLimCpu", capacityMissingRequestContainers(cpuUsage, cpuLim)},
		{"gapMissingLimMem", capacityMissingRequestContainers(memUsage, memLim)},
		{"gapReservedCpu", capacityReservedWithoutUsageContainers(cpuReq, cpuUsage)},
		{"gapReservedMem", capacityReservedWithoutUsageContainers(memReq, memUsage)},
		// Container-level under/throttle series: gap counts + top risk from one round-trip each.
		{"underCpu", capacityUnderProvisionedContainers(cpuUsage, cpuReq)},
		{"underMem", capacityUnderProvisionedContainers(memUsage, memReq)},
		{"throttled", capacityThrottledContainers(ctr, scope)},
		{"overReqCpu", capacityOverRequestedCount(cpuReq, cpuUsage)},
		{"overReqMem", capacityOverRequestedCount(memReq, memUsage)},
		{"allocCpu", capacityNodeAllocSum(ctr.NodeAllocatable, ctr.ResourceLabel, "cpu", a.otelCfg.Labels.DeploymentEnv, env)},
		{"allocMem", capacityNodeAllocSum(ctr.NodeAllocatable, ctr.ResourceLabel, "memory", a.otelCfg.Labels.DeploymentEnv, env)},
		{"nsCpuUsage", fmt.Sprintf(`sum by (namespace, k8s_cluster_name) (%s)`, cpuUsage)},
		{"nsMemUsage", fmt.Sprintf(`sum by (namespace, k8s_cluster_name) (%s)`, memUsage)},
		{"nsCpuReq", fmt.Sprintf(`sum by (namespace, k8s_cluster_name) (%s)`, cpuReq)},
		{"nsMemReq", fmt.Sprintf(`sum by (namespace, k8s_cluster_name) (%s)`, memReq)},
		// Raw per-node usage: capacityCPUUsageExpr aggregates away node labels.
		{"nodeCpuUsage", capacityNodeCPUUsageExpr(ctr, scope)},
		{"nodeMemUsage", capacityNodeMemUsageExpr(ctr, scope)},
		{"nodeCpuAlloc", capacityNodeAllocByNode(ctr.NodeAllocatable, ctr.ResourceLabel, "cpu", a.otelCfg.Labels.DeploymentEnv, env)},
		{"nodeMemAlloc", capacityNodeAllocByNode(ctr.NodeAllocatable, ctr.ResourceLabel, "memory", a.otelCfg.Labels.DeploymentEnv, env)},
		// Root-cgroup fs (container=""); soft-null in UI when series absent — never invent from KSM capacity alone.
		{"nodeFsUsage", capacityNodeFsExpr("container_fs_usage_bytes", a.otelCfg.Labels.DeploymentEnv, env)},
		{"nodeFsLimit", capacityNodeFsExpr("container_fs_limit_bytes", a.otelCfg.Labels.DeploymentEnv, env)},
	}

	resultMap := a.runInstantQueries(ctx, at, jobs, logger)
	return a.aggregateCapacity(resultMap)
}

func (a *App) aggregateCapacity(resultMap map[string][]queries.PromResult) CapacityOverview {
	rates := queriesCapacityRates{
		CPUHourUSD:    a.settings.CapacityCostRates.CPUHourUSD,
		MemoryGiBHour: a.settings.CapacityCostRates.MemoryGiBHour,
	}
	cpuHour, memHour := capacityCostRates(rates)

	sumCPUUsage := firstValue(resultMap["sumCpuUsage"])
	sumMemUsage := firstValue(resultMap["sumMemUsage"])
	sumCPUReq := firstValue(resultMap["sumCpuReq"])
	sumMemReq := firstValue(resultMap["sumMemReq"])
	allocCPU := firstValue(resultMap["allocCpu"])
	allocMem := firstValue(resultMap["allocMem"])

	hasData := sumCPUUsage > 0 || sumMemUsage > 0 || sumCPUReq > 0 || sumMemReq > 0 ||
		len(resultMap["wasteCpuNs"]) > 0 || len(resultMap["wasteMemNs"]) > 0
	if !hasData {
		return CapacityOverview{
			Available:         false,
			EnablementNote:    capacityEnablementNote,
			Namespaces:        []CapacityNamespaceRollup{},
			Nodes:             []CapacityNodeRollup{},
			TopWasteCPU:       []CapacityContainerRow{},
			TopWasteMem:       []CapacityContainerRow{},
			TopRiskContainers: []CapacityContainerRow{},
			Efficiency:        CapacityEfficiency{CPUHourUSD: cpuHour, MemoryGiBHourUSD: memHour, RatesAreEstimates: true},
		}
	}

	eff := CapacityEfficiency{
		CPUHourUSD:        cpuHour,
		MemoryGiBHourUSD:  memHour,
		RatesAreEstimates: true,
	}
	if sumCPUReq > 0 {
		eff.CPUUsedRatio = roundTo(sumCPUUsage/sumCPUReq, 4)
	}
	if sumMemReq > 0 {
		eff.MemUsedRatio = roundTo(sumMemUsage/sumMemReq, 4)
	}
	if allocCPU > 0 {
		v := roundTo(sumCPUReq/allocCPU, 4)
		eff.CPUAllocRatio = &v
	}
	if allocMem > 0 {
		v := roundTo(sumMemReq/allocMem, 4)
		eff.MemAllocRatio = &v
	}

	nsMap := map[string]*CapacityNamespaceRollup{}
	nsKey := func(ns, env string) string {
		if env == "" {
			return ns
		}
		return env + "\x00" + ns
	}
	ensureNs := func(ns, env string) *CapacityNamespaceRollup {
		k := nsKey(ns, env)
		row := nsMap[k]
		if row == nil {
			row = &CapacityNamespaceRollup{Namespace: ns, Environment: env}
			nsMap[k] = row
		} else if row.Environment == "" && env != "" {
			row.Environment = env
		}
		return row
	}
	for _, r := range resultMap["wasteCpuNs"] {
		ns := r.Metric["namespace"]
		if ns == "" {
			continue
		}
		ensureNs(ns, r.Metric["k8s_cluster_name"]).WasteCPU = roundTo(safeFloat(r.Value.Float()), 4)
	}
	for _, r := range resultMap["wasteMemNs"] {
		ns := r.Metric["namespace"]
		if ns == "" {
			continue
		}
		ensureNs(ns, r.Metric["k8s_cluster_name"]).WasteMem = roundTo(safeFloat(r.Value.Float()), 0)
	}
	applyNsMetric := func(key string, set func(*CapacityNamespaceRollup, float64)) {
		for _, r := range resultMap[key] {
			ns := r.Metric["namespace"]
			if ns == "" {
				continue
			}
			set(ensureNs(ns, r.Metric["k8s_cluster_name"]), safeFloat(r.Value.Float()))
		}
	}
	applyNsMetric("nsCpuUsage", func(row *CapacityNamespaceRollup, v float64) { row.CPUUsage = roundTo(v, 4) })
	applyNsMetric("nsMemUsage", func(row *CapacityNamespaceRollup, v float64) { row.MemUsage = roundTo(v, 0) })
	applyNsMetric("nsCpuReq", func(row *CapacityNamespaceRollup, v float64) { row.CPURequest = roundTo(v, 4) })
	applyNsMetric("nsMemReq", func(row *CapacityNamespaceRollup, v float64) { row.MemRequest = roundTo(v, 0) })

	gaps := CapacityGapCounts{}
	countContainerGapsUnion(
		[][]queries.PromResult{resultMap["gapMissingReqCpu"], resultMap["gapMissingReqMem"]},
		nsMap, func(r *CapacityNamespaceRollup) { r.MissingRequest++ }, &gaps.MissingRequest,
	)
	countContainerGapsUnion(
		[][]queries.PromResult{resultMap["gapMissingLimCpu"], resultMap["gapMissingLimMem"]},
		nsMap, func(r *CapacityNamespaceRollup) { r.MissingLimit++ }, &gaps.MissingLimit,
	)
	countContainerGapsUnion(
		[][]queries.PromResult{resultMap["gapReservedCpu"], resultMap["gapReservedMem"]},
		nsMap, func(*CapacityNamespaceRollup) {}, &gaps.ReservedWithoutUsage,
	)
	countContainerGapsUnion(
		[][]queries.PromResult{resultMap["underCpu"], resultMap["underMem"]},
		nsMap, func(r *CapacityNamespaceRollup) { r.UnderProvisioned++ }, &gaps.UnderProvisioned,
	)
	countContainerGaps(resultMap["throttled"], nsMap, func(r *CapacityNamespaceRollup) { r.Throttled++ }, &gaps.Throttled)

	var totalWasteCPU, totalWasteMem float64
	namespaces := make([]CapacityNamespaceRollup, 0, len(nsMap))
	for _, row := range nsMap {
		row.ReclaimableMonthlyUSD = roundTo(monthlyUSD(row.WasteCPU, row.WasteMem, cpuHour, memHour), 2)
		if row.CPURequest > 0 {
			row.CPUUsedRatio = roundTo(row.CPUUsage/row.CPURequest, 4)
		}
		if row.MemRequest > 0 {
			row.MemUsedRatio = roundTo(row.MemUsage/row.MemRequest, 4)
		}
		totalWasteCPU += row.WasteCPU
		totalWasteMem += row.WasteMem
		namespaces = append(namespaces, *row)
	}
	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].WasteCPU > namespaces[j].WasteCPU
	})

	eff.ReclaimableMonthlyUSD = roundTo(monthlyUSD(totalWasteCPU, totalWasteMem, cpuHour, memHour), 2)
	deficitCPU := clampMin0(sumCPUUsage - sumCPUReq)
	deficitMem := clampMin0(sumMemUsage - sumMemReq)
	eff.DeficitMonthlyUSD = roundTo(monthlyUSD(deficitCPU, deficitMem, cpuHour, memHour), 2)

	// Top lists stay capped; fleet over-requested counts use full-population series when present.
	topCPU := containerRowsFromResults(resultMap["wasteCpuTop"], "cpu")
	topMem := containerRowsFromResults(resultMap["wasteMemTop"], "mem")
	if rs := resultMap["overReqCpu"]; len(rs) > 0 {
		gaps.OverRequestedCPU = int(firstValue(rs))
	} else {
		gaps.OverRequestedCPU = len(topCPU)
	}
	if rs := resultMap["overReqMem"]; len(rs) > 0 {
		gaps.OverRequestedMem = int(firstValue(rs))
	} else {
		gaps.OverRequestedMem = len(topMem)
	}

	risk := riskRowsFromResults(resultMap["underCpu"], resultMap["underMem"], resultMap["throttled"])
	nodes := buildCapacityNodes(resultMap)
	population := buildCapacityPopulation(namespaces, nodes)

	return CapacityOverview{
		Available:         true,
		Efficiency:        eff,
		Population:        population,
		GapCounts:         gaps,
		Namespaces:        namespaces,
		Nodes:             nodes,
		TopWasteCPU:       topCPU,
		TopWasteMem:       topMem,
		TopRiskContainers: risk,
	}
}

func capacityNodeName(m map[string]string) string {
	for _, k := range []string{"node", "kubernetes_io_hostname", "instance"} {
		if v := m[k]; v != "" {
			// strip :port from instance
			if k == "instance" {
				if i := strings.LastIndex(v, ":"); i > 0 {
					return v[:i]
				}
			}
			return v
		}
	}
	return ""
}

func capacityNodeKey(cluster, node string) string {
	if cluster == "" {
		return node
	}
	return cluster + "\x00" + node
}

func buildCapacityNodes(resultMap map[string][]queries.PromResult) []CapacityNodeRollup {
	type agg struct {
		name, env                                  string
		cpuUsage, memUsage                         float64
		cpuAlloc, memAlloc                         float64
		fsUsage, fsLimit                           float64
		hasUsage, hasAlloc, hasFsUsage, hasFsLimit bool
	}
	byNode := map[string]*agg{}
	ensure := func(name, env string) *agg {
		k := capacityNodeKey(env, name)
		a := byNode[k]
		if a == nil {
			a = &agg{name: name, env: env}
			byNode[k] = a
		} else if a.env == "" && env != "" {
			a.env = env
		}
		return a
	}
	for _, r := range resultMap["nodeCpuUsage"] {
		if n := capacityNodeName(r.Metric); n != "" {
			a := ensure(n, r.Metric["k8s_cluster_name"])
			a.cpuUsage += safeFloat(r.Value.Float())
			a.hasUsage = true
		}
	}
	for _, r := range resultMap["nodeMemUsage"] {
		if n := capacityNodeName(r.Metric); n != "" {
			a := ensure(n, r.Metric["k8s_cluster_name"])
			a.memUsage += safeFloat(r.Value.Float())
			a.hasUsage = true
		}
	}
	for _, r := range resultMap["nodeCpuAlloc"] {
		if n := capacityNodeName(r.Metric); n != "" {
			a := ensure(n, r.Metric["k8s_cluster_name"])
			a.cpuAlloc += safeFloat(r.Value.Float())
			a.hasAlloc = true
		}
	}
	for _, r := range resultMap["nodeMemAlloc"] {
		if n := capacityNodeName(r.Metric); n != "" {
			a := ensure(n, r.Metric["k8s_cluster_name"])
			a.memAlloc += safeFloat(r.Value.Float())
			a.hasAlloc = true
		}
	}
	for _, r := range resultMap["nodeFsUsage"] {
		if n := capacityNodeName(r.Metric); n != "" {
			a := ensure(n, r.Metric["k8s_cluster_name"])
			a.fsUsage += safeFloat(r.Value.Float())
			a.hasFsUsage = true
		}
	}
	for _, r := range resultMap["nodeFsLimit"] {
		if n := capacityNodeName(r.Metric); n != "" {
			a := ensure(n, r.Metric["k8s_cluster_name"])
			a.fsLimit += safeFloat(r.Value.Float())
			a.hasFsLimit = true
		}
	}
	out := make([]CapacityNodeRollup, 0, len(byNode))
	for _, a := range byNode {
		row := CapacityNodeRollup{
			Node: a.name, Environment: a.env,
			CPUUsage: roundTo(a.cpuUsage, 4), MemUsage: roundTo(a.memUsage, 0),
			CPUAlloc: roundTo(a.cpuAlloc, 4), MemAlloc: roundTo(a.memAlloc, 0),
			ConditionReady: true,
		}
		if a.cpuAlloc > 0 {
			row.CPUUsedRatio = roundTo(a.cpuUsage/a.cpuAlloc, 4)
		}
		if a.memAlloc > 0 {
			row.MemUsedRatio = roundTo(a.memUsage/a.memAlloc, 4)
		}
		if a.hasFsUsage {
			u := roundTo(a.fsUsage, 0)
			row.StorageUsage = &u
		}
		if a.hasFsLimit {
			l := roundTo(a.fsLimit, 0)
			row.StorageLimit = &l
		}
		if a.hasFsUsage && a.hasFsLimit && a.fsLimit > 0 {
			r := roundTo(a.fsUsage/a.fsLimit, 4)
			row.StorageRatio = &r
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

func fractionsToBand(f EfficiencyBandFractions) CapacityEfficiencyBand {
	return CapacityEfficiencyBand(f)
}

func buildCapacityPopulation(namespaces []CapacityNamespaceRollup, nodes []CapacityNodeRollup) CapacityPopulation {
	var nsCPUUnder, nsCPUOK, nsCPUOver, nsMemUnder, nsMemOK, nsMemOver int
	clusterCPU := map[string]struct{ used, req float64 }{}
	clusterMem := map[string]struct{ used, req float64 }{}
	for _, ns := range namespaces {
		accumulateBandClass(classifyUsedRatio(ns.CPUUsage, ns.CPURequest), &nsCPUUnder, &nsCPUOK, &nsCPUOver)
		accumulateBandClass(classifyUsedRatio(ns.MemUsage, ns.MemRequest), &nsMemUnder, &nsMemOK, &nsMemOver)
		env := ns.Environment
		if env == "" {
			env = "_default"
		}
		c := clusterCPU[env]
		c.used += ns.CPUUsage
		c.req += ns.CPURequest
		clusterCPU[env] = c
		m := clusterMem[env]
		m.used += ns.MemUsage
		m.req += ns.MemRequest
		clusterMem[env] = m
	}
	var clCPUUnder, clCPUOK, clCPUOver, clMemUnder, clMemOK, clMemOver int
	for env, c := range clusterCPU {
		accumulateBandClass(classifyUsedRatio(c.used, c.req), &clCPUUnder, &clCPUOK, &clCPUOver)
		m := clusterMem[env]
		accumulateBandClass(classifyUsedRatio(m.used, m.req), &clMemUnder, &clMemOK, &clMemOver)
	}
	var nCPUUnder, nCPUOK, nCPUOver, nMemUnder, nMemOK, nMemOver int
	var nStorUnder, nStorOK, nStorOver int
	var hasNodeStorage bool
	clusterStor := map[string]struct{ used, lim float64 }{}
	for _, n := range nodes {
		accumulateBandClass(classifyUsedRatio(n.CPUUsage, n.CPUAlloc), &nCPUUnder, &nCPUOK, &nCPUOver)
		accumulateBandClass(classifyUsedRatio(n.MemUsage, n.MemAlloc), &nMemUnder, &nMemOK, &nMemOver)
		if n.StorageUsage != nil && n.StorageLimit != nil && *n.StorageLimit > 0 {
			hasNodeStorage = true
			accumulateBandClass(classifyUsedRatio(*n.StorageUsage, *n.StorageLimit), &nStorUnder, &nStorOK, &nStorOver)
			env := n.Environment
			if env == "" {
				env = "_default"
			}
			s := clusterStor[env]
			s.used += *n.StorageUsage
			s.lim += *n.StorageLimit
			clusterStor[env] = s
		}
	}
	var clStorUnder, clStorOK, clStorOver int
	var hasClusterStorage bool
	for _, s := range clusterStor {
		if s.lim <= 0 {
			continue
		}
		hasClusterStorage = true
		accumulateBandClass(classifyUsedRatio(s.used, s.lim), &clStorUnder, &clStorOK, &clStorOver)
	}
	clusterTotal := len(clusterCPU)
	if clusterTotal == 0 && len(namespaces) > 0 {
		clusterTotal = 1
	}
	var nodeStorage *CapacityEfficiencyBand
	if hasNodeStorage {
		b := fractionsToBand(bandFractionsFromCounts(nStorUnder, nStorOK, nStorOver))
		nodeStorage = &b
	}
	var clusterStorage *CapacityEfficiencyBand
	if hasClusterStorage {
		b := fractionsToBand(bandFractionsFromCounts(clStorUnder, clStorOK, clStorOver))
		clusterStorage = &b
	}
	return CapacityPopulation{
		Clusters: CapacityPopulationScope{
			Total: clusterTotal,
			Bands: CapacityResourcePopulation{
				CPU:     fractionsToBand(bandFractionsFromCounts(clCPUUnder, clCPUOK, clCPUOver)),
				Mem:     fractionsToBand(bandFractionsFromCounts(clMemUnder, clMemOK, clMemOver)),
				Storage: clusterStorage,
			},
		},
		Nodes: CapacityPopulationScope{
			Total: len(nodes),
			Bands: CapacityResourcePopulation{
				CPU:     fractionsToBand(bandFractionsFromCounts(nCPUUnder, nCPUOK, nCPUOver)),
				Mem:     fractionsToBand(bandFractionsFromCounts(nMemUnder, nMemOK, nMemOver)),
				Storage: nodeStorage,
			},
		},
		Namespaces: CapacityPopulationScope{
			Total: len(namespaces),
			Bands: CapacityResourcePopulation{
				CPU: fractionsToBand(bandFractionsFromCounts(nsCPUUnder, nsCPUOK, nsCPUOver)),
				Mem: fractionsToBand(bandFractionsFromCounts(nsMemUnder, nsMemOK, nsMemOver)),
				// Namespace storage stays null until PVC/fs-by-ns series exist.
			},
		},
	}
}

func (a *App) capacityEmptyOverview(note string) CapacityOverview {
	rates := queriesCapacityRates{
		CPUHourUSD:    a.settings.CapacityCostRates.CPUHourUSD,
		MemoryGiBHour: a.settings.CapacityCostRates.MemoryGiBHour,
	}
	cpuHour, memHour := capacityCostRates(rates)
	return CapacityOverview{
		Available:         false,
		EnablementNote:    note,
		Namespaces:        []CapacityNamespaceRollup{},
		Nodes:             []CapacityNodeRollup{},
		TopWasteCPU:       []CapacityContainerRow{},
		TopWasteMem:       []CapacityContainerRow{},
		TopRiskContainers: []CapacityContainerRow{},
		Efficiency:        CapacityEfficiency{CPUHourUSD: cpuHour, MemoryGiBHourUSD: memHour, RatesAreEstimates: true},
	}
}

// queryCapacityClickHouse implements Path B: probe usage+KSM → ledger → aggregateCapacity.
func (a *App) queryCapacityClickHouse(ctx context.Context, ch *queries.ClickHouseClient, namespace, env string, at time.Time) CapacityOverview {
	logger := log.DefaultLogger.With("handler", "capacity-clickhouse")
	if ch == nil {
		return a.capacityEmptyOverview(capacityEnablementNote)
	}

	from := at.Add(-time.Duration(chquery.CapacityUsageLookbackSec) * time.Second)
	opts := queries.ClickHouseQueryOptions{From: from, To: at, Limit: 10000, SkipLimitEnforcement: true}

	usageRes, err := ch.Query(ctx, chquery.CapacityUsageProbeSQL(from, at), opts)
	if err != nil {
		logger.Warn("capacity CH usage probe failed", "error", err)
		return a.capacityEmptyOverview(capacityEnablementNote)
	}
	ksmRes, err := ch.Query(ctx, chquery.CapacityKSMProbeSQL(from, at), opts)
	if err != nil {
		logger.Warn("capacity CH KSM probe failed", "error", err)
		return a.capacityEmptyOverview(capacityEnablementNote)
	}
	hasUsage := chquery.ParseCapacityProbeCount(usageRes.Maps()) > 0
	hasKSM := chquery.ParseCapacityProbeCount(ksmRes.Maps()) > 0
	switch {
	case !hasUsage && !hasKSM:
		return a.capacityEmptyOverview(capacityEnablementNote)
	case !hasUsage:
		return a.capacityEmptyOverview(capacityCHMissingUsageNote)
	case !hasKSM:
		return a.capacityEmptyOverview(capacityCHMissingKSMNote)
	}

	ledgerRes, err := ch.Query(ctx, chquery.CapacityLedgerSQL(namespace, env, from, at), opts)
	if err != nil {
		logger.Warn("capacity CH ledger failed", "error", err)
		return a.capacityEmptyOverview(capacityEnablementNote)
	}
	rows := chquery.ParseCapacityLedgerRows(ledgerRes.Maps())

	var allocCPU, allocMem float64
	if allocRes, aerr := ch.Query(ctx, chquery.CapacityAllocatableSQL(env, from, at), opts); aerr == nil {
		allocCPU, allocMem = chquery.ParseCapacityAllocatable(allocRes.Maps())
	} else {
		logger.Warn("capacity CH allocatable optional query failed", "error", aerr)
	}

	rm := chquery.CapacityLedgerToResultMap(rows, allocCPU, allocMem)
	if nodeAlloc, nerr := ch.Query(ctx, chquery.CapacityNodeAllocSQL(env, from, at), opts); nerr == nil {
		chquery.MergeNodeAllocIntoResultMap(rm, nodeAlloc.Maps())
	} else {
		logger.Warn("capacity CH node allocatable optional query failed", "error", nerr)
	}
	if nodeFs, ferr := ch.Query(ctx, chquery.CapacityNodeFsSQL(env, from, at), opts); ferr == nil {
		chquery.MergeNodeFsIntoResultMap(rm, nodeFs.Maps())
	} else {
		logger.Warn("capacity CH node filesystem optional query failed", "error", ferr)
	}
	return a.aggregateCapacity(rm)
}

// ---------------------------------------------------------------------------
// PromQL builders
// ---------------------------------------------------------------------------

func capacityScope(namespace, env, envLabel string) string {
	var parts []string
	parts = append(parts, `container!=""`, `container!="POD"`)
	if namespace != "" {
		parts = append(parts, fmt.Sprintf(`namespace="%s"`, namespace))
	}
	if m := envMatcher(envLabel, env); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, ",")
}

// capacityCallerKey scopes response cache entries by user when requests run with
// forwarded credentials (same convention as scorecard / namespace scorecard).
func (a *App) capacityCallerKey(ctx context.Context, req *http.Request) string {
	if a.resolveServiceToken(ctx) != "" {
		return ""
	}
	caller := req.Header.Get("X-Grafana-Id")
	if caller == "" {
		caller = req.Header.Get("X-Grafana-User")
	}
	return caller
}

func capacityNodeAllocSum(metric, resourceLabel, resource, envLabel, env string) string {
	parts := []string{fmt.Sprintf(`%s="%s"`, resourceLabel, resource)}
	if m := envMatcher(envLabel, env); m != "" {
		parts = append(parts, m)
	}
	return fmt.Sprintf(`sum(%s{%s})`, metric, strings.Join(parts, ","))
}

func capacityNodeAllocByNode(metric, resourceLabel, resource, envLabel, env string) string {
	parts := []string{fmt.Sprintf(`%s="%s"`, resourceLabel, resource)}
	if m := envMatcher(envLabel, env); m != "" {
		parts = append(parts, m)
	}
	return fmt.Sprintf(`sum by (node, k8s_cluster_name) (%s{%s})`, metric, strings.Join(parts, ","))
}

func capacityNodeCPUUsageExpr(ctr otelconfig.ContainerMetrics, scope string) string {
	return fmt.Sprintf(
		`sum by (node, kubernetes_io_hostname, instance, k8s_cluster_name) (rate(%s{%s}[%s]))`,
		ctr.CPUUsage, scope, capacityLookback,
	)
}

func capacityNodeMemUsageExpr(ctr otelconfig.ContainerMetrics, scope string) string {
	metric := ctr.MemoryWorkingSet
	if metric == "" {
		metric = ctr.MemoryUsage
	}
	return fmt.Sprintf(
		`sum by (node, kubernetes_io_hostname, instance, k8s_cluster_name) (%s{%s})`,
		metric, scope,
	)
}

func capacityNodeFsExpr(metric, envLabel, env string) string {
	parts := []string{`container=""`, `id="/"`}
	if m := envMatcher(envLabel, env); m != "" {
		parts = append(parts, m)
	}
	return fmt.Sprintf(
		`sum by (node, kubernetes_io_hostname, instance, k8s_cluster_name) (%s{%s})`,
		metric, strings.Join(parts, ","),
	)
}

// promqlRegexLiteral encodes a regex as a PromQL double-quoted string literal.
func promqlRegexLiteral(re string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range re {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// capacityAppendWorkloadScope restricts PromQL selectors to pods for a workload.
// Matches exact pod name or controller-managed suffixes (ReplicaSet hash / STS ordinal).
func capacityAppendWorkloadScope(scope, workload string) string {
	if workload == "" {
		return scope
	}
	re := `^` + regexp.QuoteMeta(workload) + `(-.*)?$`
	return scope + `,pod=~` + promqlRegexLiteral(re)
}

func capacityCPUUsageExpr(ctr otelconfig.ContainerMetrics, scope string) string {
	return fmt.Sprintf(
		`sum by (namespace, pod, container, k8s_cluster_name) (rate(%s{%s}[%s]))`,
		ctr.CPUUsage, scope, capacityLookback,
	)
}

func capacityMemUsageExpr(ctr otelconfig.ContainerMetrics, scope string) string {
	metric := ctr.MemoryWorkingSet
	if metric == "" {
		metric = ctr.MemoryUsage
	}
	return fmt.Sprintf(`sum by (namespace, pod, container, k8s_cluster_name) (%s{%s})`, metric, scope)
}

func capacityResourceExpr(metric, resource string, ctr otelconfig.ContainerMetrics, scope string) string {
	return fmt.Sprintf(
		`sum by (namespace, pod, container, k8s_cluster_name) (%s{%s,%s="%s"})`,
		metric, scope, ctr.ResourceLabel, resource,
	)
}

func capacityWasteByNamespace(reqExpr, usageExpr string) string {
	return fmt.Sprintf(
		`sum by (namespace) (clamp_min((%s) - on (namespace, pod, container) group_left() (%s), 0))`,
		reqExpr, usageExpr,
	)
}

func capacityWasteTopk(reqExpr, usageExpr string, k int) string {
	return fmt.Sprintf(
		`topk(%d, clamp_min((%s) - on (namespace, pod, container) group_left() (%s), 0))`,
		k, reqExpr, usageExpr,
	)
}

func capacityMissingRequestContainers(usageExpr, reqExpr string) string {
	return fmt.Sprintf(
		`count by (namespace, pod, container) (%s) unless on (namespace, pod, container) count by (namespace, pod, container) (%s)`,
		usageExpr, reqExpr,
	)
}

func capacityReservedWithoutUsageContainers(reqExpr, usageExpr string) string {
	return fmt.Sprintf(
		`count by (namespace, pod, container) (%s) unless on (namespace, pod, container) count by (namespace, pod, container) (%s)`,
		reqExpr, usageExpr,
	)
}

func capacityOverRequestedCount(reqExpr, usageExpr string) string {
	return fmt.Sprintf(
		`count(clamp_min((%s) - on (namespace, pod, container) group_left() (%s), 0) > 0)`,
		reqExpr, usageExpr,
	)
}

func capacityUnderProvisionedContainers(usageExpr, reqExpr string) string {
	return fmt.Sprintf(
		`clamp_min((%s) - on (namespace, pod, container) group_left() (%s), 0) > 0`,
		usageExpr, reqExpr,
	)
}

func capacityThrottledContainers(ctr otelconfig.ContainerMetrics, scope string) string {
	return fmt.Sprintf(
		`sum by (namespace, pod, container, k8s_cluster_name) (rate(%s{%s}[%s])) > 0`,
		ctr.CPUThrottled, scope, capacityLookback,
	)
}

func countContainerGaps(
	rs []queries.PromResult,
	nsMap map[string]*CapacityNamespaceRollup,
	bumpNs func(*CapacityNamespaceRollup),
	fleet *int,
) {
	countContainerGapsUnion([][]queries.PromResult{rs}, nsMap, bumpNs, fleet)
}

// countContainerGapsUnion counts each container identity at most once across the
// given result sets (e.g. CPU+memory gap categories).
func countContainerGapsUnion(
	sets [][]queries.PromResult,
	nsMap map[string]*CapacityNamespaceRollup,
	bumpNs func(*CapacityNamespaceRollup),
	fleet *int,
) {
	seen := map[string]struct{}{}
	for _, rs := range sets {
		for _, r := range rs {
			ns := r.Metric["namespace"]
			if ns == "" {
				continue
			}
			env := r.Metric["k8s_cluster_name"]
			id := ns + "\x00" + r.Metric["pod"] + "\x00" + r.Metric["container"] + "\x00" + env
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			*fleet++
			k := ns
			if env != "" {
				k = env + "\x00" + ns
			}
			row := nsMap[k]
			if row == nil {
				row = &CapacityNamespaceRollup{Namespace: ns, Environment: env}
				nsMap[k] = row
			}
			bumpNs(row)
		}
	}
}

func firstValue(rs []queries.PromResult) float64 {
	if len(rs) == 0 {
		return 0
	}
	return safeFloat(rs[0].Value.Float())
}

func clampMin0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func containerRowsFromResults(rs []queries.PromResult, kind string) []CapacityContainerRow {
	out := make([]CapacityContainerRow, 0, len(rs))
	for _, r := range rs {
		v := safeFloat(r.Value.Float())
		if v <= 0 {
			continue
		}
		row := CapacityContainerRow{
			Namespace:   r.Metric["namespace"],
			Pod:         r.Metric["pod"],
			Container:   r.Metric["container"],
			Environment: r.Metric["k8s_cluster_name"],
		}
		if kind == "cpu" {
			row.WasteCPU = roundTo(v, 4)
		} else {
			row.WasteMem = roundTo(v, 0)
		}
		out = append(out, row)
	}
	return out
}

func riskRowsFromResults(underCPU, underMem, throttled []queries.PromResult) []CapacityContainerRow {
	type key struct{ ns, pod, ctr string }
	seen := map[key]*CapacityContainerRow{}
	add := func(rs []queries.PromResult, risk string) {
		for _, r := range rs {
			ns := r.Metric["namespace"]
			pod := r.Metric["pod"]
			ctr := r.Metric["container"]
			if ns == "" {
				continue
			}
			k := key{ns: ns, pod: pod, ctr: ctr}
			row := seen[k]
			if row == nil {
				row = &CapacityContainerRow{
					Namespace:   ns,
					Pod:         pod,
					Container:   ctr,
					Environment: r.Metric["k8s_cluster_name"],
					Risk:        risk,
				}
				seen[k] = row
			} else if row.Risk != "" && !strings.Contains(row.Risk, risk) {
				row.Risk = row.Risk + "," + risk
			}
		}
	}
	add(underCPU, "underProvisionedCpu")
	add(underMem, "underProvisionedMem")
	add(throttled, "throttled")
	out := make([]CapacityContainerRow, 0, len(seen))
	for _, row := range seen {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace == out[j].Namespace {
			return out[i].Container < out[j].Container
		}
		return out[i].Namespace < out[j].Namespace
	})
	if len(out) > capacityTopN {
		out = out[:capacityTopN]
	}
	return out
}

// ---------------------------------------------------------------------------
// Recommendations (CAP-3)
// ---------------------------------------------------------------------------

func (a *App) resolveRecommendations(ctx context.Context, req *http.Request, namespace, env string, from, to time.Time) CapacityRecommendationsResponse {
	empty := CapacityRecommendationsResponse{
		Available:       false,
		EnablementNote:  capacityEnablementNote,
		Recommendations: []WorkloadRecommendation{},
	}
	if a.prom(ctx) != nil {
		resp := a.queryRecommendations(ctx, namespace, env, from, to)
		if resp.Available {
			return resp
		}
		if ch := a.chForRequest(req); ch != nil {
			chResp := a.queryRecommendationsClickHouse(ctx, ch, namespace, env, from, to)
			if chResp.Available {
				return chResp
			}
			if chResp.EnablementNote != "" {
				resp.EnablementNote = chResp.EnablementNote
			}
		}
		if resp.EnablementNote == "" {
			resp.EnablementNote = capacityEnablementNote
		}
		return resp
	}
	if ch := a.chForRequest(req); ch != nil {
		return a.queryRecommendationsClickHouse(ctx, ch, namespace, env, from, to)
	}
	return empty
}

func (a *App) queryRecommendations(ctx context.Context, namespace, env string, from, to time.Time) CapacityRecommendationsResponse {
	logger := log.DefaultLogger.With("handler", "capacity-recs")
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)
	window := max(to.Sub(from), time.Hour)
	memMetric := ctr.MemoryWorkingSet
	if memMetric == "" {
		memMetric = ctr.MemoryUsage
	}
	cpuRate := capacityCPUUsageExpr(ctr, scope)
	memUsage := fmt.Sprintf(`sum by (namespace, pod, container) (%s{%s})`, memMetric, scope)
	cpuReq := capacityResourceExpr(ctr.ResourceReqs, "cpu", ctr, scope)
	memReq := capacityResourceExpr(ctr.ResourceReqs, "memory", ctr, scope)
	cpuLim := capacityResourceExpr(ctr.ResourceLimits, "cpu", ctr, scope)
	memLim := capacityResourceExpr(ctr.ResourceLimits, "memory", ctr, scope)
	win := formatPromDuration(window)

	jobs := []QueryJob{
		{"cpuP95", fmt.Sprintf(`quantile_over_time(0.95, (%s)[%s:])`, cpuRate, win)},
		{"cpuP90", fmt.Sprintf(`quantile_over_time(0.90, (%s)[%s:])`, cpuRate, win)},
		{"cpuMax", fmt.Sprintf(`max_over_time((%s)[%s:])`, cpuRate, win)},
		{"cpuP50", fmt.Sprintf(`quantile_over_time(0.50, (%s)[%s:])`, cpuRate, win)},
		{"memP95", fmt.Sprintf(`quantile_over_time(0.95, (%s)[%s:])`, memUsage, win)},
		{"memP90", fmt.Sprintf(`quantile_over_time(0.90, (%s)[%s:])`, memUsage, win)},
		{"memP50", fmt.Sprintf(`quantile_over_time(0.50, (%s)[%s:])`, memUsage, win)},
		{"memMax", fmt.Sprintf(`max_over_time((%s)[%s:])`, memUsage, win)},
		{"cpuReq", cpuReq},
		{"memReq", memReq},
		{"cpuLim", cpuLim},
		{"memLim", memLim},
		{"throttled", fmt.Sprintf(`sum by (namespace, pod, container) (rate(%s{%s}[%s]))`, ctr.CPUThrottled, scope, capacityLookback)},
		{"podOwner", fmt.Sprintf(`%s{%s}`, ctr.PodOwner, namespaceOnlyScope(namespace))},
		{"jobOwner", fmt.Sprintf(`kube_job_owner{%s}`, namespaceOnlyScope(namespace))},
	}
	resultMap := a.runInstantQueries(ctx, to, jobs, logger)
	return a.aggregateRecommendations(resultMap, env, window)
}

func (a *App) queryRecommendationsClickHouse(ctx context.Context, ch *queries.ClickHouseClient, namespace, env string, from, to time.Time) CapacityRecommendationsResponse {
	logger := log.DefaultLogger.With("handler", "capacity-recs-clickhouse")
	empty := CapacityRecommendationsResponse{
		Available: false, EnablementNote: capacityEnablementNote, Recommendations: []WorkloadRecommendation{},
	}
	if ch == nil {
		return empty
	}
	window := max(to.Sub(from), time.Hour)
	opts := queries.ClickHouseQueryOptions{From: from, To: to, Limit: 5000, SkipLimitEnforcement: true}

	usageRes, err := ch.Query(ctx, chquery.CapacityUsageProbeSQL(from, to), opts)
	if err != nil {
		logger.Warn("capacity recs CH usage probe failed", "error", err)
		return empty
	}
	ksmRes, err := ch.Query(ctx, chquery.CapacityKSMProbeSQL(from, to), opts)
	if err != nil {
		logger.Warn("capacity recs CH KSM probe failed", "error", err)
		return empty
	}
	hasUsage := chquery.ParseCapacityProbeCount(usageRes.Maps()) > 0
	hasKSM := chquery.ParseCapacityProbeCount(ksmRes.Maps()) > 0
	switch {
	case !hasUsage && !hasKSM:
		return empty
	case !hasUsage:
		empty.EnablementNote = capacityCHMissingUsageNote
		return empty
	case !hasKSM:
		empty.EnablementNote = capacityCHMissingKSMNote
		return empty
	}

	bucketRes, err := ch.Query(ctx, chquery.CapacityRecsBucketSQL(namespace, env, from, to), opts)
	if err != nil {
		logger.Warn("capacity recs CH bucket query failed", "error", err)
		return empty
	}
	ownerRes, err := ch.Query(ctx, chquery.CapacityPodOwnerSQL(namespace, env, from, to), opts)
	if err != nil {
		logger.Warn("capacity recs CH owner query failed", "error", err)
		ownerRes = nil
	}
	var owners []chquery.CapacityOwner
	if ownerRes != nil {
		owners = chquery.ParseCapacityOwners(ownerRes.Maps())
	}
	rm := capacityRecsCHToResultMap(chquery.ParseCapacityRecRows(bucketRes.Maps()), owners)
	if jobRes, jerr := ch.Query(ctx, chquery.JobsGaugeLatestSQL(chquery.MetricKubeJobOwner, namespace, from, to), opts); jerr == nil {
		rm["jobOwner"] = chquery.ParseJobsGaugeRows(jobRes.Maps())
	} else {
		logger.Warn("capacity recs CH job owner query failed", "error", jerr)
	}
	return a.aggregateRecommendations(rm, env, window)
}

func capacityPromMetric(labels map[string]string, v float64) queries.PromResult {
	m := make(map[string]string, len(labels))
	for k, val := range labels {
		if val != "" {
			m[k] = val
		}
	}
	return queries.PromResult{Metric: m, Value: queries.NewPromValue(0, strconv.FormatFloat(v, 'f', -1, 64))}
}

func capacityRecsCHToResultMap(rows []chquery.CapacityRecRow, owners []chquery.CapacityOwner) map[string][]queries.PromResult {
	rm := map[string][]queries.PromResult{
		"cpuP95": {}, "cpuP90": {}, "cpuP50": {}, "cpuMax": {},
		"memP95": {}, "memP90": {}, "memP50": {}, "memMax": {},
		"cpuReq": {}, "memReq": {}, "cpuLim": {}, "memLim": {},
		"throttled": {}, "podOwner": {},
	}
	for _, r := range rows {
		labels := map[string]string{
			"namespace": r.Namespace, "pod": r.Pod, "container": r.Container, "k8s_cluster_name": r.Cluster,
		}
		rm["cpuP95"] = append(rm["cpuP95"], capacityPromMetric(labels, r.CPUP95))
		rm["cpuP90"] = append(rm["cpuP90"], capacityPromMetric(labels, r.CPUP90))
		rm["cpuP50"] = append(rm["cpuP50"], capacityPromMetric(labels, r.CPUP50))
		rm["cpuMax"] = append(rm["cpuMax"], capacityPromMetric(labels, r.CPUMax))
		rm["memP95"] = append(rm["memP95"], capacityPromMetric(labels, r.MemP95))
		rm["memP90"] = append(rm["memP90"], capacityPromMetric(labels, r.MemP90))
		rm["memP50"] = append(rm["memP50"], capacityPromMetric(labels, r.MemP50))
		rm["memMax"] = append(rm["memMax"], capacityPromMetric(labels, r.MemMax))
		rm["cpuReq"] = append(rm["cpuReq"], capacityPromMetric(labels, r.ReqCPU))
		rm["memReq"] = append(rm["memReq"], capacityPromMetric(labels, r.ReqMem))
		if r.LimCPU > 0 {
			rm["cpuLim"] = append(rm["cpuLim"], capacityPromMetric(labels, r.LimCPU))
		}
		rm["memLim"] = append(rm["memLim"], capacityPromMetric(labels, r.LimMem))
		if r.Throttled > 0 {
			rm["throttled"] = append(rm["throttled"], capacityPromMetric(labels, r.Throttled))
		}
	}
	for _, o := range owners {
		rm["podOwner"] = append(rm["podOwner"], capacityPromMetric(map[string]string{
			"namespace": o.Namespace, "pod": o.Pod,
			"owner_kind": o.Kind, "owner_name": o.Name, "k8s_cluster_name": o.Cluster,
		}, 1))
	}
	return rm
}

type capacityPodID struct{ ns, pod, container, cluster string }

// capacityQuantilesSeen records which of the four window quantiles arrived for
// one resource. A recommendation only claims bounds when all four are present,
// because a missing quantile would silently enter the math as zero.
type capacityQuantilesSeen struct{ p50, p90, p95, max bool }

func (s capacityQuantilesSeen) complete() bool { return s.p50 && s.p90 && s.p95 && s.max }
func (s capacityQuantilesSeen) any() bool      { return s.p50 || s.p90 || s.p95 || s.max }
func (s capacityQuantilesSeen) or(o capacityQuantilesSeen) capacityQuantilesSeen {
	return capacityQuantilesSeen{
		p50: s.p50 || o.p50, p90: s.p90 || o.p90, p95: s.p95 || o.p95, max: s.max || o.max,
	}
}

type capacityObsRec struct {
	rec     *WorkloadRecommendation
	cpuSeen capacityQuantilesSeen
	memSeen capacityQuantilesSeen
}

type capacityWkKey struct{ ns, kind, name, container, cluster string }

type capacityWkAgg struct {
	rec     *WorkloadRecommendation
	cpuSeen capacityQuantilesSeen
	memSeen capacityQuantilesSeen
}

// maxObserved is the elementwise max. One recommendation covers every replica,
// so each bound must satisfy the worst one.
func maxObserved(a, b WorkloadObserved) WorkloadObserved {
	return WorkloadObserved{
		CPUP50:        math.Max(a.CPUP50, b.CPUP50),
		CPUP90:        math.Max(a.CPUP90, b.CPUP90),
		CPUP95:        math.Max(a.CPUP95, b.CPUP95),
		CPUMax:        math.Max(a.CPUMax, b.CPUMax),
		MemP50:        math.Max(a.MemP50, b.MemP50),
		MemP90:        math.Max(a.MemP90, b.MemP90),
		MemP95:        math.Max(a.MemP95, b.MemP95),
		MemMax:        math.Max(a.MemMax, b.MemMax),
		ThrottledRate: math.Max(a.ThrottledRate, b.ThrottledRate),
	}
}

func (a *App) aggregateRecommendations(resultMap map[string][]queries.PromResult, env string, window time.Duration) CapacityRecommendationsResponse {
	obs := collectCapacityObservations(resultMap, env)
	ownerByPod := capacityOwnerByPod(resultMap["podOwner"], resultMap["jobOwner"])
	byWorkload := mergeCapacityByWorkload(obs, ownerByPod)

	rates := queriesCapacityRates{
		CPUHourUSD:    a.settings.CapacityCostRates.CPUHourUSD,
		MemoryGiBHour: a.settings.CapacityCostRates.MemoryGiBHour,
	}
	cpuHour, memHour := capacityCostRates(rates)

	out := make([]WorkloadRecommendation, 0, len(byWorkload))
	for _, agg := range byWorkload {
		if rec, ok := finalizeCapacityRecommendation(agg, window, cpuHour, memHour); ok {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReclaimableMonthlyUSD == out[j].ReclaimableMonthlyUSD {
			return out[i].WorkloadName < out[j].WorkloadName
		}
		return out[i].ReclaimableMonthlyUSD > out[j].ReclaimableMonthlyUSD
	})
	if len(out) > capacityTopN {
		out = out[:capacityTopN]
	}
	if len(out) == 0 {
		return CapacityRecommendationsResponse{
			Available:       false,
			EnablementNote:  capacityEnablementNote,
			Recommendations: []WorkloadRecommendation{},
		}
	}
	return CapacityRecommendationsResponse{Available: true, Recommendations: out}
}

func collectCapacityObservations(resultMap map[string][]queries.PromResult, env string) map[capacityPodID]*capacityObsRec {
	obs := map[capacityPodID]*capacityObsRec{}
	upsert := func(rs []queries.PromResult, apply func(*capacityObsRec, float64)) {
		for _, r := range rs {
			ns := r.Metric["namespace"]
			pod := r.Metric["pod"]
			c := r.Metric["container"]
			if ns == "" || c == "" {
				continue
			}
			cluster := r.Metric["k8s_cluster_name"]
			k := capacityPodID{ns, pod, c, cluster}
			entry := obs[k]
			if entry == nil {
				envName := cluster
				if envName == "" {
					envName = env
				}
				entry = &capacityObsRec{
					rec: &WorkloadRecommendation{
						Namespace:   ns,
						Environment: envName,
						Container:   c,
						Replicas:    1,
					},
				}
				obs[k] = entry
			}
			apply(entry, safeFloat(r.Value.Float()))
		}
	}
	upsert(resultMap["cpuP95"], func(e *capacityObsRec, v float64) { e.rec.Observed.CPUP95 = v; e.cpuSeen.p95 = true })
	upsert(resultMap["cpuP90"], func(e *capacityObsRec, v float64) { e.rec.Observed.CPUP90 = v; e.cpuSeen.p90 = true })
	upsert(resultMap["cpuP50"], func(e *capacityObsRec, v float64) { e.rec.Observed.CPUP50 = v; e.cpuSeen.p50 = true })
	upsert(resultMap["cpuMax"], func(e *capacityObsRec, v float64) { e.rec.Observed.CPUMax = v; e.cpuSeen.max = true })
	upsert(resultMap["memP95"], func(e *capacityObsRec, v float64) { e.rec.Observed.MemP95 = v; e.memSeen.p95 = true })
	upsert(resultMap["memP90"], func(e *capacityObsRec, v float64) { e.rec.Observed.MemP90 = v; e.memSeen.p90 = true })
	upsert(resultMap["memP50"], func(e *capacityObsRec, v float64) { e.rec.Observed.MemP50 = v; e.memSeen.p50 = true })
	upsert(resultMap["memMax"], func(e *capacityObsRec, v float64) { e.rec.Observed.MemMax = v; e.memSeen.max = true })
	upsert(resultMap["cpuReq"], func(e *capacityObsRec, v float64) { e.rec.Current.CPUReq = v })
	upsert(resultMap["memReq"], func(e *capacityObsRec, v float64) { e.rec.Current.MemReq = v })
	upsert(resultMap["cpuLim"], func(e *capacityObsRec, v float64) {
		if v > 0 {
			e.rec.Current.CPULim = &v
		}
	})
	upsert(resultMap["memLim"], func(e *capacityObsRec, v float64) { e.rec.Current.MemLim = v })
	upsert(resultMap["throttled"], func(e *capacityObsRec, v float64) { e.rec.Observed.ThrottledRate = v })
	return obs
}

func capacityOwnerByPod(podOwner, jobOwner []queries.PromResult) map[string]struct{ kind, name string } {
	cronByJob := map[string]struct{ kind, name string }{}
	for _, r := range jobOwner {
		if r.Metric["owner_kind"] != "CronJob" {
			continue
		}
		job := r.Metric["job_name"]
		if job == "" {
			continue
		}
		ns := r.Metric["namespace"]
		cluster := r.Metric["k8s_cluster_name"]
		cronByJob[ns+"\x00"+cluster+"\x00"+job] = struct{ kind, name string }{"CronJob", r.Metric["owner_name"]}
	}
	ownerByPod := map[string]struct{ kind, name string }{}
	for _, r := range podOwner {
		pod := r.Metric["pod"]
		if pod == "" {
			continue
		}
		ns := r.Metric["namespace"]
		cluster := r.Metric["k8s_cluster_name"]
		kind := r.Metric["owner_kind"]
		name := r.Metric["owner_name"]
		if kind == "ReplicaSet" {
			kind = "Deployment"
			name = stripReplicaSetHash(name)
		}
		if kind == "Job" {
			if parent, ok := cronByJob[ns+"\x00"+cluster+"\x00"+name]; ok && parent.name != "" {
				kind = parent.kind
				name = parent.name
			}
		}
		ownerByPod[ns+"\x00"+cluster+"\x00"+pod] = struct{ kind, name string }{kind, name}
	}
	return ownerByPod
}

func mergeCapacityByWorkload(
	obs map[capacityPodID]*capacityObsRec,
	ownerByPod map[string]struct{ kind, name string },
) map[capacityWkKey]*capacityWkAgg {
	byWorkload := map[capacityWkKey]*capacityWkAgg{}
	for k, entry := range obs {
		rec := entry.rec
		owner, ok := ownerByPod[k.ns+"\x00"+k.cluster+"\x00"+k.pod]
		if !ok || owner.name == "" {
			owner = struct{ kind, name string }{"Pod", k.pod}
		}
		wkey := capacityWkKey{k.ns, owner.kind, owner.name, k.container, k.cluster}
		agg := byWorkload[wkey]
		if agg == nil {
			rec.WorkloadKind = owner.kind
			rec.WorkloadName = owner.name
			byWorkload[wkey] = &capacityWkAgg{rec: rec, cpuSeen: entry.cpuSeen, memSeen: entry.memSeen}
			continue
		}
		agg.rec.Replicas++
		agg.cpuSeen = agg.cpuSeen.or(entry.cpuSeen)
		agg.memSeen = agg.memSeen.or(entry.memSeen)
		agg.rec.Observed = maxObserved(agg.rec.Observed, rec.Observed)
		// Current requests/limits stay per-replica (do not sum across pods).
		if agg.rec.Current.CPUReq == 0 && rec.Current.CPUReq > 0 {
			agg.rec.Current.CPUReq = rec.Current.CPUReq
		}
		if agg.rec.Current.MemReq == 0 && rec.Current.MemReq > 0 {
			agg.rec.Current.MemReq = rec.Current.MemReq
		}
		if agg.rec.Current.MemLim == 0 && rec.Current.MemLim > 0 {
			agg.rec.Current.MemLim = rec.Current.MemLim
		}
		// Keep the highest replica CPU limit so applyThrottleGuard is order-stable
		// and a throttled pod's limit is never dropped for a lower sibling.
		if rec.Current.CPULim != nil {
			if agg.rec.Current.CPULim == nil || *rec.Current.CPULim > *agg.rec.Current.CPULim {
				lim := *rec.Current.CPULim
				agg.rec.Current.CPULim = &lim
			}
		}
	}
	return byWorkload
}

// finalizeCapacityRecommendation turns one workload's merged observations into a
// single bounds triple, and projects every downstream field from it. Nothing is
// computed twice, so the JSON numbers and the pasted YAML cannot drift.
func finalizeCapacityRecommendation(agg *capacityWkAgg, window time.Duration, cpuHour, memHour float64) (WorkloadRecommendation, bool) {
	rec := agg.rec
	if !agg.cpuSeen.any() && !agg.memSeen.any() {
		return WorkloadRecommendation{}, false
	}

	days := capacityConfidenceDays(window)
	lowerMult, upperMult := capacityConfidenceMultipliers(days)
	rec.Confidence = capacityConfidence(days)

	if agg.cpuSeen.complete() {
		q := monotoneQuantiles(rec.Observed.CPUP50, rec.Observed.CPUP90, rec.Observed.CPUP95, rec.Observed.CPUMax)
		b := capacityResourceBounds(q, lowerMult, upperMult, cpuTargetIgnoresPeak).quantized(ceilCPUCores)
		guarded := applyThrottleGuard(b, rec.Observed.ThrottledRate, rec.Current.CPULim)
		if guarded != b {
			rec.Notes = append(rec.Notes, "CPU limit raised to the current limit: this container is throttling, so shrinking the limit would keep it throttled.")
		}
		rec.Bounds.CPU = &guarded
	} else if agg.cpuSeen.any() {
		rec.Notes = append(rec.Notes, "CPU bounds omitted: the window is missing one of p50/p90/p95/max.")
	}

	if agg.memSeen.complete() {
		q := monotoneQuantiles(rec.Observed.MemP50, rec.Observed.MemP90, rec.Observed.MemP95, rec.Observed.MemMax)
		b := capacityResourceBounds(q, lowerMult, upperMult, memTargetFloorsAtPeak).quantized(ceilMemoryBytes)
		rec.Bounds.Mem = &b
	} else if agg.memSeen.any() {
		rec.Notes = append(rec.Notes, "Memory bounds omitted: the window is missing one of p50/p90/p95/max.")
	}

	if rec.Bounds.CPU == nil && rec.Bounds.Mem == nil {
		return WorkloadRecommendation{}, false
	}

	rec.Burstable = burstableFromBounds(rec.Bounds)
	rec.Guaranteed = guaranteedFromBounds(rec.Bounds)
	rec.YAMLPatch = capacityResourcesYAML(rec.Burstable)
	rec.YAMLPatchGuaranteed = capacityResourcesYAML(rec.Guaranteed)

	var curLim float64
	if rec.Current.CPULim != nil {
		curLim = *rec.Current.CPULim
	}
	rec.QoSClass = qosClass(rec.Current.CPUReq, curLim, rec.Current.MemReq, rec.Current.MemLim)

	// Reclaim/deficit stay measured against the observed p95, not the new
	// recommendation. Moving them to the lower bound is its own diff because it
	// reorders the table.
	reps := float64(rec.Replicas)
	if reps < 1 {
		reps = 1
	}
	wasteCPU := clampMin0(rec.Current.CPUReq - rec.Observed.CPUP95)
	wasteMem := clampMin0(rec.Current.MemReq - rec.Observed.MemP95)
	if !agg.cpuSeen.p95 {
		wasteCPU = 0
	}
	if !agg.memSeen.p95 {
		wasteMem = 0
	}
	rec.ReclaimableMonthlyUSD = roundTo(monthlyUSD(wasteCPU*reps, wasteMem*reps, cpuHour, memHour), 2)
	defCPU := clampMin0(rec.Observed.CPUP95 - rec.Current.CPUReq)
	defMem := clampMin0(rec.Observed.MemP95 - rec.Current.MemReq)
	if !agg.cpuSeen.p95 {
		defCPU = 0
	}
	if !agg.memSeen.p95 {
		defMem = 0
	}
	rec.DeficitMonthlyUSD = roundTo(monthlyUSD(defCPU*reps, defMem*reps, cpuHour, memHour), 2)
	return *rec, true
}

func namespaceOnlyScope(namespace string) string {
	if namespace == "" {
		return `pod!=""`
	}
	return fmt.Sprintf(`namespace="%s"`, namespace)
}

func formatPromDuration(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return strconv.Itoa(secs) + "s"
	}
	if secs%3600 == 0 {
		return strconv.Itoa(secs/3600) + "h"
	}
	if secs%60 == 0 {
		return strconv.Itoa(secs/60) + "m"
	}
	return strconv.Itoa(secs) + "s"
}

// ---------------------------------------------------------------------------
// History (CAP-4)
// ---------------------------------------------------------------------------

func (a *App) resolveHistory(ctx context.Context, req *http.Request, namespace, workload, container, env string, from, to time.Time) CapacityHistoryResponse {
	note := "Only series still present in the metrics backend for this range are shown. Deleted workloads are not reconstructed."
	empty := CapacityHistoryResponse{
		Available:      false,
		EnablementNote: capacityEnablementNote,
		Series:         []CapacityHistorySeries{},
		Note:           note,
	}
	if namespace == "" {
		return empty
	}
	if a.prom(ctx) != nil {
		resp := a.queryHistoryProm(ctx, namespace, workload, container, env, from, to)
		if resp.Available {
			return resp
		}
		if ch := a.chForRequest(req); ch != nil {
			chResp := a.queryHistoryClickHouse(ctx, ch, namespace, workload, container, env, from, to)
			if chResp.Available {
				return chResp
			}
			if chResp.EnablementNote != "" {
				resp.EnablementNote = chResp.EnablementNote
			}
		}
		if resp.EnablementNote == "" {
			resp.EnablementNote = capacityEnablementNote
		}
		return resp
	}
	if ch := a.chForRequest(req); ch != nil {
		return a.queryHistoryClickHouse(ctx, ch, namespace, workload, container, env, from, to)
	}
	return empty
}

func (a *App) queryHistoryProm(ctx context.Context, namespace, workload, container, env string, from, to time.Time) CapacityHistoryResponse {
	note := "Only series still present in the metrics backend for this range are shown. Deleted workloads are not reconstructed."
	empty := CapacityHistoryResponse{
		Available: false, EnablementNote: capacityEnablementNote, Series: []CapacityHistorySeries{}, Note: note,
	}
	client := a.prom(ctx)
	if client == nil {
		return empty
	}
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)
	if container != "" {
		scope += fmt.Sprintf(`,container="%s"`, container)
	}
	scope = capacityAppendWorkloadScope(scope, workload)

	step := max(to.Sub(from)/50, 15*time.Second)

	memMetric := ctr.MemoryWorkingSet
	if memMetric == "" {
		memMetric = ctr.MemoryUsage
	}
	queriesSpec := []struct {
		name string
		expr string
	}{
		{"cpuUsage", fmt.Sprintf(`sum(rate(%s{%s}[%s]))`, ctr.CPUUsage, scope, capacityLookback)},
		{"cpuRequest", fmt.Sprintf(`sum(%s{%s,%s="cpu"})`, ctr.ResourceReqs, scope, ctr.ResourceLabel)},
		{"cpuLimit", fmt.Sprintf(`sum(%s{%s,%s="cpu"})`, ctr.ResourceLimits, scope, ctr.ResourceLabel)},
		{"memUsage", fmt.Sprintf(`sum(%s{%s})`, memMetric, scope)},
		{"memRequest", fmt.Sprintf(`sum(%s{%s,%s="memory"})`, ctr.ResourceReqs, scope, ctr.ResourceLabel)},
		{"memLimit", fmt.Sprintf(`sum(%s{%s,%s="memory"})`, ctr.ResourceLimits, scope, ctr.ResourceLabel)},
		{"cpuThrottled", fmt.Sprintf(`sum(rate(%s{%s}[%s]))`, ctr.CPUThrottled, scope, capacityLookback)},
		{"restarts", fmt.Sprintf(`sum(increase(%s{%s}[1h]))`, ctr.Restarts, scope)},
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	series := make([]CapacityHistorySeries, 0, len(queriesSpec))
	for _, q := range queriesSpec {
		wg.Add(1)
		go func(name, expr string) {
			defer wg.Done()
			rs, err := client.RangeQuery(ctx, expr, from, to, step)
			if err != nil || len(rs) == 0 {
				return
			}
			pts := make([]CapacityHistoryPoint, 0)
			for _, r := range rs {
				for _, v := range r.Values {
					pts = append(pts, CapacityHistoryPoint{
						TimestampMs: v.Timestamp() * 1000,
						Value:       roundTo(safeFloat(v.Float()), 4),
					})
				}
			}
			if len(pts) == 0 {
				return
			}
			mu.Lock()
			series = append(series, CapacityHistorySeries{Name: name, Points: pts})
			mu.Unlock()
		}(q.name, q.expr)
	}
	wg.Wait()
	if len(series) == 0 {
		return empty
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Name < series[j].Name })
	return CapacityHistoryResponse{Available: true, Series: series, Note: note}
}

func (a *App) queryHistoryClickHouse(ctx context.Context, ch *queries.ClickHouseClient, namespace, workload, container, env string, from, to time.Time) CapacityHistoryResponse {
	logger := log.DefaultLogger.With("handler", "capacity-history-clickhouse")
	note := "Only series still present in the metrics backend for this range are shown. Deleted workloads are not reconstructed."
	empty := CapacityHistoryResponse{
		Available: false, EnablementNote: capacityEnablementNote, Series: []CapacityHistorySeries{}, Note: note,
	}
	if ch == nil {
		return empty
	}
	opts := queries.ClickHouseQueryOptions{From: from, To: to, Limit: 5000, SkipLimitEnforcement: true}

	usageRes, err := ch.Query(ctx, chquery.CapacityUsageProbeSQL(from, to), opts)
	if err != nil {
		logger.Warn("capacity history CH usage probe failed", "error", err)
		return empty
	}
	if chquery.ParseCapacityProbeCount(usageRes.Maps()) <= 0 {
		ksmRes, kerr := ch.Query(ctx, chquery.CapacityKSMProbeSQL(from, to), opts)
		if kerr == nil && chquery.ParseCapacityProbeCount(ksmRes.Maps()) > 0 {
			empty.EnablementNote = capacityCHMissingUsageNote
		}
		return empty
	}

	step := max(int((to.Sub(from) / 50).Seconds()), 15)
	histRes, err := ch.Query(ctx, chquery.CapacityHistorySeriesSQL(namespace, container, env, workload, from, to, step), opts)
	if err != nil {
		logger.Warn("capacity history CH series failed", "error", err)
		return empty
	}
	rows := chquery.ParseCapacityHistoryRows(histRes.Maps())
	if len(rows) == 0 {
		return empty
	}

	appendSeries := func(name string, get func(chquery.CapacityHistoryPointRow) float64) CapacityHistorySeries {
		pts := make([]CapacityHistoryPoint, 0, len(rows))
		for _, r := range rows {
			pts = append(pts, CapacityHistoryPoint{TimestampMs: r.BucketMs, Value: roundTo(get(r), 4)})
		}
		return CapacityHistorySeries{Name: name, Points: pts}
	}
	series := []CapacityHistorySeries{
		appendSeries("cpuUsage", func(r chquery.CapacityHistoryPointRow) float64 { return r.CPUUsage }),
		appendSeries("cpuRequest", func(r chquery.CapacityHistoryPointRow) float64 { return r.CPURequest }),
		appendSeries("cpuLimit", func(r chquery.CapacityHistoryPointRow) float64 { return r.CPULimit }),
		appendSeries("memUsage", func(r chquery.CapacityHistoryPointRow) float64 { return r.MemUsage }),
		appendSeries("memRequest", func(r chquery.CapacityHistoryPointRow) float64 { return r.MemRequest }),
		appendSeries("memLimit", func(r chquery.CapacityHistoryPointRow) float64 { return r.MemLimit }),
		appendSeries("cpuThrottled", func(r chquery.CapacityHistoryPointRow) float64 { return r.CPUThrottled }),
		appendSeries("restarts", func(r chquery.CapacityHistoryPointRow) float64 { return r.Restarts }),
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Name < series[j].Name })
	return CapacityHistoryResponse{Available: true, Series: series, Note: note}
}

// ---------------------------------------------------------------------------
// Forecast (CAP-5)
// ---------------------------------------------------------------------------

func (a *App) resolveForecast(ctx context.Context, req *http.Request, resource, namespace, workload, container, env string, from, to time.Time) CapacityForecastResponse {
	empty := CapacityForecastResponse{
		Available:      false,
		EnablementNote: capacityEnablementNote,
		Method:         "linear",
		MethodLabel:    "linear projection",
		Resource:       resource,
		Series:         []CapacityHistorySeries{},
	}
	if namespace == "" {
		return empty
	}
	if a.prom(ctx) != nil {
		resp := a.queryForecastProm(ctx, resource, namespace, workload, container, env, from, to)
		if resp.Available {
			return resp
		}
		if ch := a.chForRequest(req); ch != nil {
			chResp := a.queryForecastClickHouse(ctx, ch, resource, namespace, workload, container, env, from, to)
			if chResp.Available {
				return chResp
			}
			if chResp.EnablementNote != "" {
				resp.EnablementNote = chResp.EnablementNote
			}
		}
		if resp.EnablementNote == "" {
			resp.EnablementNote = capacityEnablementNote
		}
		return resp
	}
	if ch := a.chForRequest(req); ch != nil {
		return a.queryForecastClickHouse(ctx, ch, resource, namespace, workload, container, env, from, to)
	}
	return empty
}

func (a *App) queryForecastProm(ctx context.Context, resource, namespace, workload, container, env string, from, to time.Time) CapacityForecastResponse {
	empty := CapacityForecastResponse{
		Available: false, EnablementNote: capacityEnablementNote,
		Method: "linear", MethodLabel: "linear projection", Resource: resource, Series: []CapacityHistorySeries{},
	}
	client := a.prom(ctx)
	if client == nil {
		return empty
	}
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)
	if container != "" {
		scope += fmt.Sprintf(`,container="%s"`, container)
	}
	scope = capacityAppendWorkloadScope(scope, workload)

	var usageExpr, reqExpr, limExpr string
	if resource == "memory" {
		memMetric := ctr.MemoryWorkingSet
		if memMetric == "" {
			memMetric = ctr.MemoryUsage
		}
		usageExpr = fmt.Sprintf(`sum(%s{%s})`, memMetric, scope)
		reqExpr = fmt.Sprintf(`sum(%s{%s,%s="memory"})`, ctr.ResourceReqs, scope, ctr.ResourceLabel)
		limExpr = fmt.Sprintf(`sum(%s{%s,%s="memory"})`, ctr.ResourceLimits, scope, ctr.ResourceLabel)
	} else {
		usageExpr = fmt.Sprintf(`sum(rate(%s{%s}[%s]))`, ctr.CPUUsage, scope, capacityLookback)
		reqExpr = fmt.Sprintf(`sum(%s{%s,%s="cpu"})`, ctr.ResourceReqs, scope, ctr.ResourceLabel)
		limExpr = fmt.Sprintf(`sum(%s{%s,%s="cpu"})`, ctr.ResourceLimits, scope, ctr.ResourceLabel)
	}

	window := to.Sub(from)
	predictExpr := fmt.Sprintf(
		`predict_linear((%s)[%s:], %d)`,
		usageExpr, formatPromDuration(window), int((7 * 24 * time.Hour).Seconds()),
	)

	step := max(window/50, 15*time.Second)

	usageRS, err := client.RangeQuery(ctx, usageExpr, from, to, step)
	if err != nil {
		return empty
	}
	predictRS, _ := client.InstantQuery(ctx, predictExpr, to)
	reqRS, _ := client.InstantQuery(ctx, reqExpr, to)
	limRS, _ := client.InstantQuery(ctx, limExpr, to)

	series := []CapacityHistorySeries{}
	var curUsage float64
	if len(usageRS) > 0 {
		pts := make([]CapacityHistoryPoint, 0)
		for _, r := range usageRS {
			for _, v := range r.Values {
				pts = append(pts, CapacityHistoryPoint{TimestampMs: v.Timestamp() * 1000, Value: roundTo(safeFloat(v.Float()), 4)})
			}
		}
		if len(pts) > 0 {
			curUsage = pts[len(pts)-1].Value
		}
		series = append(series, CapacityHistorySeries{Name: "usage", Points: pts})
	}
	if len(predictRS) > 0 {
		futureMs := to.Add(7 * 24 * time.Hour).UnixMilli()
		predVal := roundTo(safeFloat(predictRS[0].Value.Float()), 4)
		series = append(series, CapacityHistorySeries{
			Name: "forecast",
			Points: []CapacityHistoryPoint{
				{TimestampMs: to.UnixMilli(), Value: curUsage},
				{TimestampMs: futureMs, Value: predVal},
			},
		})
	}

	var daysToReq, daysToLim *float64
	if len(predictRS) > 0 && curUsage > 0 {
		pred := safeFloat(predictRS[0].Value.Float())
		slopePerSec := (pred - curUsage) / (7 * 24 * 3600)
		if slopePerSec > 0 {
			if req := firstValue(reqRS); req > curUsage {
				d := (req - curUsage) / slopePerSec / 86400
				daysToReq = &d
			}
			if lim := firstValue(limRS); lim > curUsage {
				d := (lim - curUsage) / slopePerSec / 86400
				daysToLim = &d
			}
		}
	}

	if len(series) == 0 {
		return empty
	}
	return CapacityForecastResponse{
		Available: true, Method: "linear", MethodLabel: "linear projection",
		Resource: resource, Series: series, DaysToRequest: daysToReq, DaysToLimit: daysToLim,
	}
}

func (a *App) queryForecastClickHouse(ctx context.Context, ch *queries.ClickHouseClient, resource, namespace, workload, container, env string, from, to time.Time) CapacityForecastResponse {
	logger := log.DefaultLogger.With("handler", "capacity-forecast-clickhouse")
	empty := CapacityForecastResponse{
		Available: false, EnablementNote: capacityEnablementNote,
		Method: "linear", MethodLabel: "linear projection", Resource: resource, Series: []CapacityHistorySeries{},
	}
	if ch == nil {
		return empty
	}
	opts := queries.ClickHouseQueryOptions{From: from, To: to, Limit: 5000, SkipLimitEnforcement: true}

	usageRes, err := ch.Query(ctx, chquery.CapacityUsageProbeSQL(from, to), opts)
	if err != nil {
		logger.Warn("capacity forecast CH usage probe failed", "error", err)
		return empty
	}
	if chquery.ParseCapacityProbeCount(usageRes.Maps()) <= 0 {
		ksmRes, kerr := ch.Query(ctx, chquery.CapacityKSMProbeSQL(from, to), opts)
		if kerr == nil && chquery.ParseCapacityProbeCount(ksmRes.Maps()) > 0 {
			empty.EnablementNote = capacityCHMissingUsageNote
		}
		return empty
	}

	step := max(int((to.Sub(from) / 50).Seconds()), 15)
	histRes, err := ch.Query(ctx, chquery.CapacityHistorySeriesSQL(namespace, container, env, workload, from, to, step), opts)
	if err != nil {
		logger.Warn("capacity forecast CH series failed", "error", err)
		return empty
	}
	rows := chquery.ParseCapacityHistoryRows(histRes.Maps())
	if len(rows) == 0 {
		return empty
	}

	usagePts := make([]CapacityHistoryPoint, 0, len(rows))
	fitPts := make([]struct {
		T int64
		V float64
	}, 0, len(rows))
	var curReq, curLim float64
	for _, r := range rows {
		var v float64
		if resource == "memory" {
			v = r.MemUsage
			curReq = r.MemRequest
			curLim = r.MemLimit
		} else {
			v = r.CPUUsage
			curReq = r.CPURequest
			curLim = r.CPULimit
		}
		usagePts = append(usagePts, CapacityHistoryPoint{TimestampMs: r.BucketMs, Value: roundTo(v, 4)})
		fitPts = append(fitPts, struct {
			T int64
			V float64
		}{r.BucketMs, v})
	}

	series := []CapacityHistorySeries{{Name: "usage", Points: usagePts}}
	horizon := float64((7 * 24 * time.Hour).Seconds())
	pred, slopePerSec, ok := chquery.LinearForecast(fitPts, horizon)
	var curUsage float64
	if len(usagePts) > 0 {
		curUsage = usagePts[len(usagePts)-1].Value
	}
	if ok {
		series = append(series, CapacityHistorySeries{
			Name: "forecast",
			Points: []CapacityHistoryPoint{
				{TimestampMs: to.UnixMilli(), Value: curUsage},
				{TimestampMs: to.Add(7 * 24 * time.Hour).UnixMilli(), Value: roundTo(pred, 4)},
			},
		})
	}

	var daysToReq, daysToLim *float64
	if ok && slopePerSec > 0 && curUsage > 0 {
		if curReq > curUsage {
			d := (curReq - curUsage) / slopePerSec / 86400
			daysToReq = &d
		}
		if curLim > curUsage {
			d := (curLim - curUsage) / slopePerSec / 86400
			daysToLim = &d
		}
	}

	return CapacityForecastResponse{
		Available: true, Method: "linear", MethodLabel: "linear projection",
		Resource: resource, Series: series, DaysToRequest: daysToReq, DaysToLimit: daysToLim,
	}
}
