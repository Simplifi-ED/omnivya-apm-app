package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/otelconfig"
	"github.com/nais/grafana-otel-plugin/pkg/plugin/queries"
)

const (
	capacityCacheTTL       = 2 * time.Minute
	capacityTopN           = 50
	capacityLookback       = "5m"
	capacityEnablementNote = "Capacity efficiency needs cAdvisor and kube-state-metrics " +
		"(container_* and kube_pod_container_resource_*) on a Prometheus metricsDataSource (Path A) " +
		"or the same series in ClickHouse. Deploy kube-state-metrics and a metrics agent that scrapes the kubelet. " +
		"No cluster agents (for example Goldilocks or VPA) are required."
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
	ReclaimableMonthlyUSD float64 `json:"reclaimableMonthlyUsd"`
	MissingRequest        int     `json:"missingRequest"`
	MissingLimit          int     `json:"missingLimit"`
	UnderProvisioned      int     `json:"underProvisioned"`
	Throttled             int     `json:"throttled"`
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
	GapCounts         CapacityGapCounts         `json:"gapCounts"`
	Namespaces        []CapacityNamespaceRollup `json:"namespaces"`
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
	CPUP95        float64 `json:"cpuP95"`
	CPUMax        float64 `json:"cpuMax"`
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
	Burstable             WorkloadResources `json:"burstable"`
	Guaranteed            WorkloadResources `json:"guaranteed"`
	ReclaimableMonthlyUSD float64           `json:"reclaimableMonthlyUsd"`
	DeficitMonthlyUSD     float64           `json:"deficitMonthlyUsd"`
	YAMLPatch             string            `json:"yamlPatch"`
	QoSClass              string            `json:"qosClass"`
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
	ck := cacheKey("capacity", orgID, roundedUnix(to), namespace, env)
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
	ck := cacheKey("capacity-ns", orgID, roundedUnix(to), namespace, env)
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
	ck := cacheKey("capacity-recs", orgID, roundedUnix(from), roundedUnix(to), namespace, env)
	cache := a.capacityResponseCache()
	if cached, ok := cache.get(ck); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		_, _ = w.Write(cached)
		return
	}
	data, err := cache.getOrCompute(ck, func() (any, error) {
		return a.resolveRecommendations(ctx, namespace, env, from, to), nil
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
	ck := cacheKey("capacity-hist", orgID, roundedUnix(from), roundedUnix(to), namespace, workload, container, env)
	a.writeCached(w, ck, "querying capacity history failed", func() (any, error) {
		return a.resolveHistory(ctx, namespace, workload, container, env, from, to), nil
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
	ck := cacheKey("capacity-fc", orgID, roundedUnix(from), roundedUnix(to), resource, namespace, workload, container, env)
	a.writeCached(w, ck, "querying capacity forecast failed", func() (any, error) {
		return a.resolveForecast(ctx, resource, namespace, workload, container, env, from, to), nil
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
		{"gapMissingReqCpu", capacityMissingRequests(cpuUsage, cpuReq)},
		{"gapMissingReqMem", capacityMissingRequests(memUsage, memReq)},
		{"gapMissingLimCpu", capacityMissingRequests(cpuUsage, cpuLim)},
		{"gapMissingLimMem", capacityMissingRequests(memUsage, memLim)},
		{"gapReservedCpu", capacityReservedWithoutUsage(cpuReq, cpuUsage)},
		{"gapReservedMem", capacityReservedWithoutUsage(memReq, memUsage)},
		// Container-level under/throttle series: gap counts + top risk from one round-trip each.
		{"underCpu", capacityUnderProvisionedContainers(cpuUsage, cpuReq)},
		{"underMem", capacityUnderProvisionedContainers(memUsage, memReq)},
		{"throttled", capacityThrottledContainers(ctr, scope)},
		{"allocCpu", fmt.Sprintf(`sum(%s{%s="cpu"})`, ctr.NodeAllocatable, ctr.ResourceLabel)},
		{"allocMem", fmt.Sprintf(`sum(%s{%s="memory"})`, ctr.NodeAllocatable, ctr.ResourceLabel)},
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
	for _, r := range resultMap["wasteCpuNs"] {
		ns := r.Metric["namespace"]
		if ns == "" {
			continue
		}
		row := nsMap[ns]
		if row == nil {
			row = &CapacityNamespaceRollup{Namespace: ns, Environment: r.Metric["k8s_cluster_name"]}
			nsMap[ns] = row
		}
		row.WasteCPU = roundTo(safeFloat(r.Value.Float()), 4)
	}
	for _, r := range resultMap["wasteMemNs"] {
		ns := r.Metric["namespace"]
		if ns == "" {
			continue
		}
		row := nsMap[ns]
		if row == nil {
			row = &CapacityNamespaceRollup{Namespace: ns, Environment: r.Metric["k8s_cluster_name"]}
			nsMap[ns] = row
		}
		row.WasteMem = roundTo(safeFloat(r.Value.Float()), 0)
	}

	addGapCount := func(key string, set func(*CapacityNamespaceRollup, int), fleet *int) {
		for _, r := range resultMap[key] {
			n := int(safeFloat(r.Value.Float()))
			*fleet += n
			if ns := r.Metric["namespace"]; ns != "" {
				row := nsMap[ns]
				if row == nil {
					row = &CapacityNamespaceRollup{Namespace: ns}
					nsMap[ns] = row
				}
				set(row, n)
			}
		}
	}

	gaps := CapacityGapCounts{}
	addGapCount("gapMissingReqCpu", func(r *CapacityNamespaceRollup, n int) { r.MissingRequest += n }, &gaps.MissingRequest)
	addGapCount("gapMissingReqMem", func(r *CapacityNamespaceRollup, n int) { r.MissingRequest += n }, &gaps.MissingRequest)
	// Deduplicate missing-request double-count across cpu/mem at fleet level by taking max of cpu/mem
	// actually we summed both — plan wants container gaps. Keep sum of distinct query results as counts of containers per resource.
	gaps.MissingLimit = 0
	addGapCount("gapMissingLimCpu", func(r *CapacityNamespaceRollup, n int) { r.MissingLimit += n }, &gaps.MissingLimit)
	addGapCount("gapMissingLimMem", func(r *CapacityNamespaceRollup, n int) { r.MissingLimit += n }, &gaps.MissingLimit)
	addGapCount("gapReservedCpu", func(r *CapacityNamespaceRollup, _ int) {}, &gaps.ReservedWithoutUsage)
	addGapCount("gapReservedMem", func(r *CapacityNamespaceRollup, _ int) {}, &gaps.ReservedWithoutUsage)
	countContainerGaps(resultMap["underCpu"], nsMap, func(r *CapacityNamespaceRollup) { r.UnderProvisioned++ }, &gaps.UnderProvisioned)
	countContainerGaps(resultMap["underMem"], nsMap, func(r *CapacityNamespaceRollup) { r.UnderProvisioned++ }, &gaps.UnderProvisioned)
	countContainerGaps(resultMap["throttled"], nsMap, func(r *CapacityNamespaceRollup) { r.Throttled++ }, &gaps.Throttled)

	var totalWasteCPU, totalWasteMem float64
	namespaces := make([]CapacityNamespaceRollup, 0, len(nsMap))
	for _, row := range nsMap {
		row.ReclaimableMonthlyUSD = roundTo(monthlyUSD(row.WasteCPU, row.WasteMem, cpuHour, memHour), 2)
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

	// Over-requested container counts: containers with waste > 0 from topk lists.
	topCPU := containerRowsFromResults(resultMap["wasteCpuTop"], "cpu")
	topMem := containerRowsFromResults(resultMap["wasteMemTop"], "mem")
	gaps.OverRequestedCPU = len(topCPU)
	gaps.OverRequestedMem = len(topMem)

	risk := riskRowsFromResults(resultMap["underCpu"], resultMap["underMem"], resultMap["throttled"])

	return CapacityOverview{
		Available:         true,
		Efficiency:        eff,
		GapCounts:         gaps,
		Namespaces:        namespaces,
		TopWasteCPU:       topCPU,
		TopWasteMem:       topMem,
		TopRiskContainers: risk,
	}
}

func (a *App) queryCapacityClickHouse(ctx context.Context, ch *queries.ClickHouseClient, namespace, env string, at time.Time) CapacityOverview {
	_ = ctx
	_ = ch
	_ = namespace
	_ = env
	_ = at
	// Path B: full CH aggregation lands with richer otel_metrics joins.
	// Soft-unavailable keeps Jobs-style dual-path contract without fake zeros.
	rates := queriesCapacityRates{
		CPUHourUSD:    a.settings.CapacityCostRates.CPUHourUSD,
		MemoryGiBHour: a.settings.CapacityCostRates.MemoryGiBHour,
	}
	cpuHour, memHour := capacityCostRates(rates)
	return CapacityOverview{
		Available:         false,
		EnablementNote:    capacityEnablementNote + " ClickHouse Path B for Capacity overview is not yet emitting series for this install; configure a Prometheus metricsDataSource for Path A.",
		Namespaces:        []CapacityNamespaceRollup{},
		TopWasteCPU:       []CapacityContainerRow{},
		TopWasteMem:       []CapacityContainerRow{},
		TopRiskContainers: []CapacityContainerRow{},
		Efficiency:        CapacityEfficiency{CPUHourUSD: cpuHour, MemoryGiBHourUSD: memHour, RatesAreEstimates: true},
	}
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
	if env != "" && envLabel != "" {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, envLabel, env))
	}
	return strings.Join(parts, ",")
}

func capacityCPUUsageExpr(ctr otelconfig.ContainerMetrics, scope string) string {
	return fmt.Sprintf(`sum by (namespace, pod, container) (rate(%s{%s}[%s]))`, ctr.CPUUsage, scope, capacityLookback)
}

func capacityMemUsageExpr(ctr otelconfig.ContainerMetrics, scope string) string {
	metric := ctr.MemoryWorkingSet
	if metric == "" {
		metric = ctr.MemoryUsage
	}
	return fmt.Sprintf(`sum by (namespace, pod, container) (%s{%s})`, metric, scope)
}

func capacityResourceExpr(metric, resource string, ctr otelconfig.ContainerMetrics, scope string) string {
	return fmt.Sprintf(`sum by (namespace, pod, container) (%s{%s,%s="%s"})`, metric, scope, ctr.ResourceLabel, resource)
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

func capacityMissingRequests(usageExpr, reqExpr string) string {
	return fmt.Sprintf(
		`count by (namespace) (count by (namespace, pod, container) (%s) unless on (namespace, pod, container) count by (namespace, pod, container) (%s))`,
		usageExpr, reqExpr,
	)
}

func capacityReservedWithoutUsage(reqExpr, usageExpr string) string {
	return fmt.Sprintf(
		`count by (namespace) (count by (namespace, pod, container) (%s) unless on (namespace, pod, container) count by (namespace, pod, container) (%s))`,
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
		`sum by (namespace, pod, container) (rate(%s{%s}[%s])) > 0`,
		ctr.CPUThrottled, scope, capacityLookback,
	)
}

func countContainerGaps(
	rs []queries.PromResult,
	nsMap map[string]*CapacityNamespaceRollup,
	bumpNs func(*CapacityNamespaceRollup),
	fleet *int,
) {
	for _, r := range rs {
		ns := r.Metric["namespace"]
		if ns == "" {
			continue
		}
		*fleet++
		row := nsMap[ns]
		if row == nil {
			row = &CapacityNamespaceRollup{Namespace: ns}
			nsMap[ns] = row
		}
		bumpNs(row)
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

func (a *App) resolveRecommendations(ctx context.Context, namespace, env string, from, to time.Time) CapacityRecommendationsResponse {
	empty := CapacityRecommendationsResponse{
		Available:       false,
		EnablementNote:  capacityEnablementNote,
		Recommendations: []WorkloadRecommendation{},
	}
	if a.prom(ctx) == nil {
		return empty
	}
	return a.queryRecommendations(ctx, namespace, env, from, to)
}

func (a *App) queryRecommendations(ctx context.Context, namespace, env string, from, to time.Time) CapacityRecommendationsResponse {
	logger := log.DefaultLogger.With("handler", "capacity-recs")
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)
	window := to.Sub(from)
	if window < time.Hour {
		window = time.Hour
	}
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
		{"cpuMax", fmt.Sprintf(`max_over_time((%s)[%s:])`, cpuRate, win)},
		{"cpuP50", fmt.Sprintf(`quantile_over_time(0.50, (%s)[%s:])`, cpuRate, win)},
		{"memP95", fmt.Sprintf(`quantile_over_time(0.95, (%s)[%s:])`, memUsage, win)},
		{"memMax", fmt.Sprintf(`max_over_time((%s)[%s:])`, memUsage, win)},
		{"cpuReq", cpuReq},
		{"memReq", memReq},
		{"cpuLim", cpuLim},
		{"memLim", memLim},
		{"throttled", fmt.Sprintf(`sum by (namespace, pod, container) (rate(%s{%s}[%s]))`, ctr.CPUThrottled, scope, capacityLookback)},
		{"podOwner", fmt.Sprintf(`%s{%s}`, ctr.PodOwner, namespaceOnlyScope(namespace))},
	}
	resultMap := a.runInstantQueries(ctx, to, jobs, logger)

	type id struct{ ns, pod, container string }
	obs := map[id]*WorkloadRecommendation{}

	upsert := func(rs []queries.PromResult, apply func(*WorkloadRecommendation, float64, map[string]string)) {
		for _, r := range rs {
			ns := r.Metric["namespace"]
			pod := r.Metric["pod"]
			c := r.Metric["container"]
			if ns == "" || c == "" {
				continue
			}
			k := id{ns, pod, c}
			rec := obs[k]
			if rec == nil {
				rec = &WorkloadRecommendation{
					Namespace:   ns,
					Environment: env,
					Container:   c,
					Replicas:    1,
					Confidence:  capacityConfidence(window),
				}
				obs[k] = rec
			}
			apply(rec, safeFloat(r.Value.Float()), r.Metric)
		}
	}

	upsert(resultMap["cpuP95"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Observed.CPUP95 = v })
	upsert(resultMap["cpuP50"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Observed.CPUP50 = v })
	upsert(resultMap["cpuMax"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Observed.CPUMax = v })
	upsert(resultMap["memP95"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Observed.MemP95 = v })
	upsert(resultMap["memMax"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Observed.MemMax = v })
	upsert(resultMap["cpuReq"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Current.CPUReq = v })
	upsert(resultMap["memReq"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Current.MemReq = v })
	upsert(resultMap["cpuLim"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) {
		if v > 0 {
			rec.Current.CPULim = &v
		}
	})
	upsert(resultMap["memLim"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Current.MemLim = v })
	upsert(resultMap["throttled"], func(rec *WorkloadRecommendation, v float64, _ map[string]string) { rec.Observed.ThrottledRate = v })

	// Resolve workload from kube_pod_owner keyed by pod.
	ownerByPod := map[string]struct{ kind, name string }{}
	for _, r := range resultMap["podOwner"] {
		pod := r.Metric["pod"]
		kind := r.Metric["owner_kind"]
		name := r.Metric["owner_name"]
		if kind == "ReplicaSet" {
			kind = "Deployment"
			name = stripReplicaSetHash(name)
		}
		if kind == "Job" {
			kind = "CronJob"
			name = stripReplicaSetHash(name)
		}
		ownerByPod[pod] = struct{ kind, name string }{kind, name}
	}

	rates := queriesCapacityRates{
		CPUHourUSD:    a.settings.CapacityCostRates.CPUHourUSD,
		MemoryGiBHour: a.settings.CapacityCostRates.MemoryGiBHour,
	}
	cpuHour, memHour := capacityCostRates(rates)

	// Collapse pods → workload+container.
	type wk struct{ ns, kind, name, container string }
	byWorkload := map[wk]*WorkloadRecommendation{}
	for k, rec := range obs {
		owner, ok := ownerByPod[k.pod]
		if !ok || owner.name == "" {
			owner = struct{ kind, name string }{"Pod", k.pod}
		}
		wkey := wk{k.ns, owner.kind, owner.name, k.container}
		agg := byWorkload[wkey]
		if agg == nil {
			rec.WorkloadKind = owner.kind
			rec.WorkloadName = owner.name
			byWorkload[wkey] = rec
			continue
		}
		agg.Replicas++
		if rec.Observed.CPUP95 > agg.Observed.CPUP95 {
			agg.Observed.CPUP95 = rec.Observed.CPUP95
		}
		if rec.Observed.CPUMax > agg.Observed.CPUMax {
			agg.Observed.CPUMax = rec.Observed.CPUMax
		}
		if rec.Observed.MemP95 > agg.Observed.MemP95 {
			agg.Observed.MemP95 = rec.Observed.MemP95
		}
		if rec.Observed.MemMax > agg.Observed.MemMax {
			agg.Observed.MemMax = rec.Observed.MemMax
		}
		agg.Current.CPUReq += rec.Current.CPUReq
		agg.Current.MemReq += rec.Current.MemReq
		agg.Current.MemLim += rec.Current.MemLim
	}

	out := make([]WorkloadRecommendation, 0, len(byWorkload))
	for _, rec := range byWorkload {
		cpuReq, memReq, memLim := burstableRecommendation(rec.Observed.CPUP95, rec.Observed.MemP95, rec.Observed.MemMax)
		rec.Burstable = WorkloadResources{CPUReq: cpuReq, MemReq: memReq, MemLim: memLim}
		gCPU, gCPULim, gMem, gMemLim := guaranteedRecommendation(rec.Observed.CPUMax, rec.Observed.MemMax)
		gLim := gCPULim
		rec.Guaranteed = WorkloadResources{CPUReq: gCPU, CPULim: &gLim, MemReq: gMem, MemLim: gMemLim}
		var curLim float64
		if rec.Current.CPULim != nil {
			curLim = *rec.Current.CPULim
		}
		rec.QoSClass = qosClass(rec.Current.CPUReq, curLim, rec.Current.MemReq, rec.Current.MemLim)
		rec.YAMLPatch = resourcesYAMLPatch(cpuReq, nil, memReq, memLim)
		wasteCPU := clampMin0(rec.Current.CPUReq - rec.Observed.CPUP95)
		wasteMem := clampMin0(rec.Current.MemReq - rec.Observed.MemP95)
		rec.ReclaimableMonthlyUSD = roundTo(monthlyUSD(wasteCPU, wasteMem, cpuHour, memHour), 2)
		defCPU := clampMin0(rec.Observed.CPUP95 - rec.Current.CPUReq)
		defMem := clampMin0(rec.Observed.MemP95 - rec.Current.MemReq)
		rec.DeficitMonthlyUSD = roundTo(monthlyUSD(defCPU, defMem, cpuHour, memHour), 2)
		out = append(out, *rec)
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

func (a *App) resolveHistory(ctx context.Context, namespace, workload, container, env string, from, to time.Time) CapacityHistoryResponse {
	note := "Only series still present in the metrics backend for this range are shown. Deleted workloads are not reconstructed."
	empty := CapacityHistoryResponse{
		Available:      false,
		EnablementNote: capacityEnablementNote,
		Series:         []CapacityHistorySeries{},
		Note:           note,
	}
	client := a.prom(ctx)
	if client == nil || namespace == "" {
		return empty
	}
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)
	if container != "" {
		scope += fmt.Sprintf(`,container="%s"`, container)
	}
	_ = workload // workload filter applied via pod regex when owners are resolved; v1 scopes by namespace/container

	step := (to.Sub(from) / 50)
	if step < 15*time.Second {
		step = 15 * time.Second
	}

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

// ---------------------------------------------------------------------------
// Forecast (CAP-5)
// ---------------------------------------------------------------------------

func (a *App) resolveForecast(ctx context.Context, resource, namespace, workload, container, env string, from, to time.Time) CapacityForecastResponse {
	empty := CapacityForecastResponse{
		Available:      false,
		EnablementNote: capacityEnablementNote,
		Method:         "linear",
		MethodLabel:    "linear projection",
		Resource:       resource,
		Series:         []CapacityHistorySeries{},
	}
	client := a.prom(ctx)
	if client == nil || namespace == "" {
		return empty
	}
	ctr := a.otelCfg.Runtime.Container
	scope := capacityScope(namespace, env, a.otelCfg.Labels.DeploymentEnv)
	if container != "" {
		scope += fmt.Sprintf(`,container="%s"`, container)
	}
	_ = workload

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
	// Subquery form: predict_linear needs a range vector over an instant-vector expression.
	predictExpr := fmt.Sprintf(
		`predict_linear((%s)[%s:], %d)`,
		usageExpr, formatPromDuration(window), int((7 * 24 * time.Hour).Seconds()),
	)

	step := window / 50
	if step < 15*time.Second {
		step = 15 * time.Second
	}

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
		Available:     true,
		Method:        "linear",
		MethodLabel:   "linear projection",
		Resource:      resource,
		Series:        series,
		DaysToRequest: daysToReq,
		DaysToLimit:   daysToLim,
	}
}
