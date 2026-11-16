import { getBackendSrv } from '@grafana/runtime';
import { lastValueFrom } from 'rxjs';
import pluginJson from '../plugin.json';

const BASE_URL = `/api/plugins/${pluginJson.id}/resources`;

async function fetchResource<T>(path: string, params?: Record<string, string>): Promise<T> {
  const url = params ? `${BASE_URL}${path}?${new URLSearchParams(params).toString()}` : `${BASE_URL}${path}`;
  const response = await lastValueFrom(getBackendSrv().fetch<T>({ url, method: 'GET' }));
  return response.data;
}

function timeParams(from: number, to: number): Record<string, string> {
  return { from: String(Math.floor(from / 1000)), to: String(Math.floor(to / 1000)) };
}

export interface CapacityGapCounts {
  missingRequest: number;
  missingLimit: number;
  overRequestedCpu: number;
  overRequestedMem: number;
  underProvisioned: number;
  throttled: number;
  reservedWithoutUsage: number;
}

export interface CapacityEfficiency {
  cpuUsedRatio: number;
  memUsedRatio: number;
  cpuAllocRatio?: number;
  memAllocRatio?: number;
  reclaimableMonthlyUsd: number;
  deficitMonthlyUsd: number;
  cpuHourUsd: number;
  memoryGibHourUsd: number;
  ratesAreEstimates: boolean;
}

export interface CapacityNamespaceRollup {
  namespace: string;
  environment?: string;
  wasteCpu: number;
  wasteMem: number;
  cpuUsage?: number;
  cpuRequest?: number;
  memUsage?: number;
  memRequest?: number;
  cpuUsedRatio?: number;
  memUsedRatio?: number;
  reclaimableMonthlyUsd: number;
  missingRequest: number;
  missingLimit: number;
  underProvisioned: number;
  throttled: number;
}

export interface CapacityEfficiencyBand {
  under: number;
  ok: number;
  over: number;
}

export interface CapacityResourcePopulation {
  cpu: CapacityEfficiencyBand;
  mem: CapacityEfficiencyBand;
  storage?: CapacityEfficiencyBand | null;
}

export interface CapacityPopulationScope {
  total: number;
  bands: CapacityResourcePopulation;
}

export interface CapacityPopulation {
  clusters: CapacityPopulationScope;
  nodes: CapacityPopulationScope;
  namespaces: CapacityPopulationScope;
}

export interface CapacityNodeRollup {
  node: string;
  environment?: string;
  cpuUsage: number;
  cpuAlloc: number;
  cpuUsedRatio?: number;
  memUsage: number;
  memAlloc: number;
  memUsedRatio?: number;
  storageUsage?: number;
  storageLimit?: number;
  storageRatio?: number;
  podCount?: number;
  conditionReady: boolean;
}

export interface CapacityContainerRow {
  namespace: string;
  pod?: string;
  container: string;
  environment?: string;
  wasteCpu?: number;
  wasteMem?: number;
  cpuUsage?: number;
  cpuRequest?: number;
  memUsage?: number;
  memRequest?: number;
  throttled?: number;
  risk?: string;
}

export interface CapacityOverview {
  available: boolean;
  enablementNote?: string;
  efficiency: CapacityEfficiency;
  population?: CapacityPopulation;
  gapCounts: CapacityGapCounts;
  namespaces: CapacityNamespaceRollup[];
  nodes?: CapacityNodeRollup[];
  topWasteCpu: CapacityContainerRow[];
  topWasteMem: CapacityContainerRow[];
  topRiskContainers: CapacityContainerRow[];
}

export interface WorkloadResources {
  cpuReq: number;
  cpuLim?: number;
  memReq: number;
  memLim: number;
}

export interface WorkloadRecommendation {
  namespace: string;
  environment?: string;
  workloadKind: string;
  workloadName: string;
  container: string;
  replicas: number;
  confidence: 'low' | 'medium' | 'high';
  current: WorkloadResources;
  observed: {
    cpuP50: number;
    cpuP95: number;
    cpuMax: number;
    memP95: number;
    memMax: number;
    throttledRate: number;
  };
  burstable: WorkloadResources;
  guaranteed: WorkloadResources;
  reclaimableMonthlyUsd: number;
  deficitMonthlyUsd: number;
  yamlPatch: string;
  qosClass: string;
}

export interface CapacityRecommendationsResponse {
  available: boolean;
  enablementNote?: string;
  recommendations: WorkloadRecommendation[];
}

export interface CapacityHistoryPoint {
  timestampMs: number;
  value: number;
}

export interface CapacityHistorySeries {
  name: string;
  points: CapacityHistoryPoint[];
}

export interface CapacityHistoryResponse {
  available: boolean;
  enablementNote?: string;
  series: CapacityHistorySeries[];
  note?: string;
}

export interface CapacityForecastResponse {
  available: boolean;
  enablementNote?: string;
  method: string;
  methodLabel: string;
  resource: string;
  series: CapacityHistorySeries[];
  daysToRequest?: number;
  daysToLimit?: number;
}

export async function getCapacity(
  from: number,
  to: number,
  opts?: { namespace?: string; environment?: string }
): Promise<CapacityOverview> {
  const params: Record<string, string> = { ...timeParams(from, to) };
  if (opts?.namespace) {
    params.namespace = opts.namespace;
  }
  if (opts?.environment) {
    params.environment = opts.environment;
  }
  return fetchResource<CapacityOverview>('/capacity', params);
}

export async function getNamespaceCapacity(
  namespace: string,
  from: number,
  to: number,
  environment?: string
): Promise<CapacityOverview> {
  const params: Record<string, string> = { ...timeParams(from, to) };
  if (environment) {
    params.environment = environment;
  }
  return fetchResource<CapacityOverview>(`/namespaces/${encodeURIComponent(namespace)}/capacity`, params);
}

export async function getCapacityRecommendations(
  from: number,
  to: number,
  opts?: { namespace?: string; environment?: string }
): Promise<CapacityRecommendationsResponse> {
  const params: Record<string, string> = { ...timeParams(from, to) };
  if (opts?.namespace) {
    params.namespace = opts.namespace;
  }
  if (opts?.environment) {
    params.environment = opts.environment;
  }
  return fetchResource<CapacityRecommendationsResponse>('/capacity/recommendations', params);
}

export async function getCapacityHistory(
  from: number,
  to: number,
  opts: { namespace: string; workload?: string; container?: string; environment?: string }
): Promise<CapacityHistoryResponse> {
  const params: Record<string, string> = { ...timeParams(from, to), namespace: opts.namespace };
  if (opts.workload) {
    params.workload = opts.workload;
  }
  if (opts.container) {
    params.container = opts.container;
  }
  if (opts.environment) {
    params.environment = opts.environment;
  }
  return fetchResource<CapacityHistoryResponse>('/capacity/history', params);
}

export async function getCapacityForecast(
  from: number,
  to: number,
  opts: {
    resource: 'cpu' | 'memory';
    namespace: string;
    workload?: string;
    container?: string;
    environment?: string;
  }
): Promise<CapacityForecastResponse> {
  const params: Record<string, string> = {
    ...timeParams(from, to),
    namespace: opts.namespace,
    resource: opts.resource,
  };
  if (opts.workload) {
    params.workload = opts.workload;
  }
  if (opts.container) {
    params.container = opts.container;
  }
  if (opts.environment) {
    params.environment = opts.environment;
  }
  return fetchResource<CapacityForecastResponse>('/capacity/forecast', params);
}
