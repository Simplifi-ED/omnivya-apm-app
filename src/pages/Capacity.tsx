import React, { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { PluginPage } from '@grafana/runtime';
import {
  Alert,
  Badge,
  Box,
  Button,
  Combobox,
  Drawer,
  InlineField,
  InteractiveTable,
  RadioButtonGroup,
  Stack,
  Tab,
  TabsBar,
  Text,
  TextArea,
  Tooltip,
  useStyles2,
  type Column,
} from '@grafana/ui';
import { GrafanaTheme2 } from '@grafana/data';
import { css } from '@emotion/css';
import {
  getCapacity,
  getCapacityForecast,
  getCapacityHistory,
  getCapacityRecommendations,
  CapacityOverview,
  CapacityNamespaceRollup,
  CapacityNodeRollup,
  CapacityContainerRow,
  WorkloadRecommendation,
  CapacityHistoryResponse,
  CapacityForecastResponse,
} from '../api/capacity';
import { DataState } from '../components/DataState';
import { TableSkeleton } from '../components/Skeleton';
import { useTimeRange } from '../utils/timeRange';
import { useAppNavigate, sanitizeParam } from '../utils/navigation';
import { useUrlParams } from '../utils/useUrlState';
import { useFetch } from '../utils/useFetch';
import { HeaderTimeControls } from '../components/HeaderTimeControls';
import { extractEnvironmentOptions, extractNamespaceOptions } from '../utils/options';
import { EfficiencyCards } from './capacity/EfficiencyCards';
import { PredictChartPanel } from './capacity/PredictChartPanel';

type CapTab = 'overview' | 'namespaces' | 'nodes' | 'rightsizing';

type PredictTarget = {
  namespace: string;
  resource: 'cpu' | 'memory';
  node?: string;
  container?: string;
};

const FORECAST_RESOURCE_OPTIONS = [
  { label: 'CPU', value: 'cpu' as const },
  { label: 'Memory', value: 'memory' as const },
];

function pct(ratio: number): string {
  if (!Number.isFinite(ratio) || ratio <= 0) {
    return '—';
  }
  return `${(ratio * 100).toFixed(0)}%`;
}

function formatCores(v: number): string {
  if (!v) {
    return '—';
  }
  if (v < 1) {
    return `${Math.round(v * 1000)}m`;
  }
  return `${v.toFixed(2)}`;
}

function formatBytes(v: number): string {
  if (!v) {
    return '—';
  }
  const mi = v / (1024 * 1024);
  if (mi >= 1024) {
    return `${(mi / 1024).toFixed(1)} GiB`;
  }
  return `${Math.round(mi)} MiB`;
}

function formatUsd(v: number): string {
  if (!Number.isFinite(v) || v === 0) {
    return '$0';
  }
  return `$${v.toFixed(0)}`;
}

function UsageBarCell({ ratio }: { ratio?: number }) {
  const styles = useStyles2(getStyles);
  if (ratio == null || !Number.isFinite(ratio) || ratio <= 0) {
    return (
      <Text variant="bodySmall" color="disabled">
        —
      </Text>
    );
  }
  const width = `${Math.min(ratio, 1) * 100}%`;
  const colorClass = ratio > 0.8 ? styles.barOver : ratio < 0.4 ? styles.barUnder : styles.barOk;
  return (
    <Tooltip content={`${(ratio * 100).toFixed(1)}% of capacity/request`}>
      <div className={styles.usageCell}>
        <div className={styles.usageTrack}>
          <div className={`${styles.usageFill} ${colorClass}`} style={{ width }} />
        </div>
        <Text variant="bodySmall" tabular>
          {pct(ratio)}
        </Text>
      </div>
    </Tooltip>
  );
}

function Capacity() {
  const styles = useStyles2(getStyles);
  const appNavigate = useAppNavigate();
  const updateParams = useUrlParams();
  const { fromMs, toMs, refresh } = useTimeRange();
  const [searchParams] = useSearchParams();
  const env = sanitizeParam(searchParams.get('environment') ?? '');
  const nsFilter = sanitizeParam(searchParams.get('namespace') ?? '');
  const tabParam = (searchParams.get('capTab') as CapTab) || 'overview';
  const tab: CapTab = ['overview', 'namespaces', 'nodes', 'rightsizing'].includes(tabParam) ? tabParam : 'overview';
  const [selectedRecKey, setSelectedRecKey] = useState<string | null>(null);
  const [predict, setPredict] = useState<PredictTarget | null>(null);

  const {
    data: overview,
    loading: overviewLoading,
    error: overviewError,
    refetch: refetchOverview,
  } = useFetch<CapacityOverview>(
    () => getCapacity(fromMs, toMs, { namespace: nsFilter || undefined, environment: env || undefined }),
    [fromMs, toMs, nsFilter, env],
    { skip: tab === 'rightsizing' }
  );

  const {
    data: recs,
    loading: recsLoading,
    error: recsError,
    refetch: refetchRecs,
  } = useFetch(
    () =>
      getCapacityRecommendations(fromMs, toMs, {
        namespace: nsFilter || undefined,
        environment: env || undefined,
      }),
    [fromMs, toMs, nsFilter, env],
    { skip: tab !== 'rightsizing' }
  );

  const predictNs = predict?.namespace ?? '';
  const {
    data: history,
    loading: historyLoading,
    error: historyError,
  } = useFetch<CapacityHistoryResponse>(
    () =>
      getCapacityHistory(fromMs, toMs, {
        namespace: predictNs,
        container: predict?.container,
        environment: env || undefined,
      }),
    [fromMs, toMs, predictNs, predict?.container, env],
    { skip: !predict || !predictNs }
  );

  const {
    data: forecast,
    loading: forecastLoading,
    error: forecastError,
  } = useFetch<CapacityForecastResponse>(
    () =>
      getCapacityForecast(fromMs, toMs, {
        resource: predict?.resource ?? 'cpu',
        namespace: predictNs,
        container: predict?.container,
        environment: env || undefined,
      }),
    [fromMs, toMs, predictNs, predict?.resource, predict?.container, env],
    { skip: !predict || !predictNs || toMs - fromMs < 2 * 60 * 60 * 1000 }
  );

  const nsOptions = useMemo(
    () => [{ label: 'All namespaces', value: '' }, ...extractNamespaceOptions(overview?.namespaces ?? [])],
    [overview?.namespaces]
  );
  const envOptions = useMemo(
    () => [{ label: 'All environments', value: '' }, ...extractEnvironmentOptions(overview?.namespaces ?? [])],
    [overview?.namespaces]
  );

  const gaps = overview?.gapCounts;
  const eff = overview?.efficiency;
  const gapTiles = useMemo(
    () => [
      { label: 'No requests', value: gaps?.missingRequest ?? 0 },
      { label: 'No limits', value: gaps?.missingLimit ?? 0 },
      { label: 'Over-requested CPU', value: gaps?.overRequestedCpu ?? 0 },
      { label: 'Over-requested mem', value: gaps?.overRequestedMem ?? 0 },
      { label: 'Under-provisioned', value: gaps?.underProvisioned ?? 0 },
      { label: 'Throttled', value: gaps?.throttled ?? 0 },
      { label: 'Reserved w/o usage', value: gaps?.reservedWithoutUsage ?? 0 },
    ],
    [gaps]
  );

  const handleRefresh = () => {
    refresh();
    refetchOverview();
    refetchRecs();
  };

  const filteredNamespaces: CapacityNamespaceRollup[] = useMemo(() => {
    const rows = overview?.namespaces ?? [];
    if (!nsFilter) {
      return rows;
    }
    return rows.filter((r) => r.namespace === nsFilter);
  }, [overview?.namespaces, nsFilter]);

  const nodes: CapacityNodeRollup[] = useMemo(() => overview?.nodes ?? [], [overview?.nodes]);
  const topWasteCpu: CapacityContainerRow[] = useMemo(() => overview?.topWasteCpu ?? [], [overview?.topWasteCpu]);
  const recommendations: WorkloadRecommendation[] = useMemo(() => recs?.recommendations ?? [], [recs?.recommendations]);

  const recIdentity = (r: WorkloadRecommendation) =>
    `${r.namespace}\0${r.workloadKind}\0${r.workloadName}\0${r.container}\0${r.environment ?? ''}`;

  // Clear selection when recommendation request scope changes; resolve open
  // drawer against the latest response so yamlPatch is never stale.
  useEffect(() => {
    setSelectedRecKey(null);
  }, [fromMs, toMs, nsFilter, env]);

  const selectedRec = useMemo(() => {
    if (!selectedRecKey) {
      return null;
    }
    return recommendations.find((r) => recIdentity(r) === selectedRecKey) ?? null;
  }, [recommendations, selectedRecKey]);

  const nsColumns = useMemo<Array<Column<CapacityNamespaceRollup>>>(
    () => [
      {
        id: 'namespace',
        header: 'Namespace',
        sortType: 'alphanumeric',
        cell: ({ row }) => {
          const ns = row.original;
          return (
            <Stack direction="column" gap={0}>
              <Button
                variant="secondary"
                fill="text"
                size="sm"
                onClick={() => {
                  const extra: Record<string, string> = {};
                  if (env) {
                    extra.environment = env;
                  }
                  appNavigate(`namespaces/${encodeURIComponent(ns.namespace)}`, extra);
                }}
              >
                {ns.namespace}
              </Button>
              {ns.environment ? (
                <Text variant="bodySmall" color="secondary">
                  {ns.environment}
                </Text>
              ) : null}
            </Stack>
          );
        },
      },
      {
        id: 'cpuUsedRatio',
        header: 'CPU usage',
        sortType: 'basic',
        cell: ({ row }) => <UsageBarCell ratio={row.original.cpuUsedRatio} />,
      },
      {
        id: 'memUsedRatio',
        header: 'Mem usage',
        sortType: 'basic',
        cell: ({ row }) => <UsageBarCell ratio={row.original.memUsedRatio} />,
      },
      {
        id: 'wasteCpu',
        header: 'Waste CPU',
        sortType: 'basic',
        cell: ({ row }) => (
          <Text variant="bodySmall" tabular>
            {formatCores(row.original.wasteCpu)}
          </Text>
        ),
      },
      {
        id: 'wasteMem',
        header: 'Waste mem',
        sortType: 'basic',
        cell: ({ row }) => (
          <Text variant="bodySmall" tabular>
            {formatBytes(row.original.wasteMem)}
          </Text>
        ),
      },
      {
        id: 'gaps',
        header: 'Gaps',
        cell: ({ row }) => {
          const n =
            row.original.missingRequest +
            row.original.missingLimit +
            row.original.underProvisioned +
            row.original.throttled;
          return (
            <Text variant="bodySmall" color="secondary" tabular>
              {n || '—'}
            </Text>
          );
        },
      },
      {
        id: 'predict',
        header: 'Predict',
        cell: ({ row }) => (
          <Stack direction="row" gap={1}>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => setPredict({ namespace: row.original.namespace, resource: 'cpu' })}
            >
              Predict CPU
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => setPredict({ namespace: row.original.namespace, resource: 'memory' })}
            >
              Predict Mem
            </Button>
          </Stack>
        ),
      },
    ],
    [appNavigate, env]
  );

  const wasteColumns = useMemo<Array<Column<CapacityContainerRow>>>(
    () => [
      { id: 'namespace', header: 'Namespace', sortType: 'alphanumeric' },
      { id: 'pod', header: 'Pod', sortType: 'alphanumeric' },
      { id: 'container', header: 'Container', sortType: 'alphanumeric' },
      {
        id: 'wasteCpu',
        header: 'Waste CPU',
        sortType: 'basic',
        cell: ({ row }) => (
          <Text variant="bodySmall" tabular>
            {formatCores(row.original.wasteCpu ?? 0)}
          </Text>
        ),
      },
    ],
    []
  );

  const nodeColumns = useMemo<Array<Column<CapacityNodeRollup>>>(
    () => [
      {
        id: 'node',
        header: 'Node',
        sortType: 'alphanumeric',
        cell: ({ row }) => (
          <Stack direction="column" gap={0}>
            <Text weight="medium">{row.original.node}</Text>
            {row.original.environment ? (
              <Text variant="bodySmall" color="secondary">
                {row.original.environment}
              </Text>
            ) : null}
          </Stack>
        ),
      },
      {
        id: 'cpuUsedRatio',
        header: 'CPU',
        sortType: 'basic',
        cell: ({ row }) => <UsageBarCell ratio={row.original.cpuUsedRatio} />,
      },
      {
        id: 'memUsedRatio',
        header: 'Memory',
        sortType: 'basic',
        cell: ({ row }) => <UsageBarCell ratio={row.original.memUsedRatio} />,
      },
      {
        id: 'storageRatio',
        header: 'Storage',
        cell: ({ row }) =>
          row.original.storageRatio != null ? (
            <UsageBarCell ratio={row.original.storageRatio} />
          ) : (
            <Text variant="bodySmall" color="disabled">
              No data
            </Text>
          ),
      },
      {
        id: 'conditionReady',
        header: 'Status',
        cell: ({ row }) => (
          <Badge
            text={row.original.conditionReady ? 'Ready' : 'NotReady'}
            color={row.original.conditionReady ? 'green' : 'red'}
          />
        ),
      },
    ],
    []
  );

  const recColumns = useMemo<Array<Column<WorkloadRecommendation>>>(
    () => [
      {
        id: 'workloadName',
        header: 'Workload',
        sortType: 'alphanumeric',
        cell: ({ row }) => (
          <Stack direction="column" gap={0}>
            <Button
              variant="secondary"
              fill="text"
              size="sm"
              onClick={() => setSelectedRecKey(recIdentity(row.original))}
            >
              {row.original.workloadKind}/{row.original.workloadName}
            </Button>
            <Text variant="bodySmall" color="secondary">
              {row.original.namespace}
            </Text>
          </Stack>
        ),
      },
      { id: 'container', header: 'Container', sortType: 'alphanumeric' },
      { id: 'qosClass', header: 'QoS', sortType: 'alphanumeric' },
      {
        id: 'confidence',
        header: 'Confidence',
        cell: ({ row }) => (
          <Badge text={row.original.confidence} color={row.original.confidence === 'high' ? 'green' : 'orange'} />
        ),
      },
      {
        id: 'reclaimableMonthlyUsd',
        header: 'Reclaimable / mo',
        sortType: 'basic',
        cell: ({ row }) => (
          <Text variant="bodySmall" tabular>
            {formatUsd(row.original.reclaimableMonthlyUsd)}
          </Text>
        ),
      },
    ],
    []
  );

  const historyForResource = useMemo(() => {
    const all = history?.series ?? [];
    const want =
      predict?.resource === 'memory' ? ['memUsage', 'memRequest', 'memLimit'] : ['cpuUsage', 'cpuRequest', 'cpuLimit'];
    const filtered = all.filter((s) => want.includes(s.name));
    return filtered.length ? filtered : all;
  }, [history?.series, predict?.resource]);

  return (
    <PluginPage>
      <Stack direction="column" gap={2}>
        <Stack direction="row" justifyContent="space-between" alignItems="center" wrap="wrap" gap={2}>
          <Stack direction="row" gap={1.5} alignItems="center" wrap="wrap">
            <InlineField label="Namespace" noMargin>
              <Combobox
                options={nsOptions}
                value={nsFilter}
                onChange={(v) => updateParams({ namespace: v?.value || null })}
                placeholder="All namespaces"
                width={26}
              />
            </InlineField>
            <InlineField label="Environment" noMargin>
              <Combobox
                options={envOptions}
                value={env}
                onChange={(v) => updateParams({ environment: v?.value || null })}
                placeholder="All environments"
                width={26}
              />
            </InlineField>
          </Stack>
          <HeaderTimeControls onRefresh={handleRefresh} />
        </Stack>

        <TabsBar>
          {(
            [
              ['overview', 'Overview'],
              ['namespaces', 'Namespaces'],
              ['nodes', 'Nodes'],
              ['rightsizing', 'Right-sizing'],
            ] as const
          ).map(([id, label]) => (
            <Tab
              key={id}
              label={label}
              active={tab === id}
              onChangeTab={() => updateParams({ capTab: id === 'overview' ? null : id })}
            />
          ))}
        </TabsBar>

        {(tab === 'overview' || tab === 'namespaces' || tab === 'nodes') && (
          <DataState
            loading={overviewLoading}
            error={overviewError}
            empty={overview != null && !overview.available}
            emptyTitle="Capacity metrics unavailable"
            emptyMessage={
              overview?.enablementNote ||
              'Deploy kube-state-metrics and cAdvisor scrapes to Prometheus (Path A) or ClickHouse otel_metrics_* (Path B).'
            }
            skeleton={<TableSkeleton rows={8} />}
          >
            {overview && overview.available && (
              <Stack direction="column" gap={3}>
                {tab === 'overview' && (
                  <>
                    <EfficiencyCards population={overview.population} />

                    <Stack direction="column" gap={1}>
                      <div className={styles.statGrid} role="group" aria-label="Fleet ratios">
                        {[
                          { label: 'CPU used / requested', value: pct(eff?.cpuUsedRatio ?? 0) },
                          { label: 'Mem used / requested', value: pct(eff?.memUsedRatio ?? 0) },
                          {
                            label: 'CPU packing',
                            value: eff?.cpuAllocRatio != null ? pct(eff.cpuAllocRatio) : '—',
                          },
                          {
                            label: 'Reclaimable / mo',
                            value: formatUsd(eff?.reclaimableMonthlyUsd ?? 0),
                            color: 'success' as const,
                          },
                          {
                            label: 'Deficit / mo',
                            value: formatUsd(eff?.deficitMonthlyUsd ?? 0),
                            color: 'warning' as const,
                          },
                        ].map((s) => (
                          <Box
                            key={s.label}
                            padding={1.5}
                            backgroundColor="secondary"
                            borderColor="weak"
                            borderStyle="solid"
                            borderRadius="default"
                          >
                            <Stack direction="column" gap={0.5}>
                              <Text variant="bodySmall" color="secondary">
                                {s.label}
                              </Text>
                              <Text variant="h4" color={s.color} tabular>
                                {s.value}
                              </Text>
                            </Stack>
                          </Box>
                        ))}
                      </div>
                      {eff?.ratesAreEstimates && (
                        <Text variant="bodySmall" color="secondary">
                          USD figures are rate-based estimates (${eff.cpuHourUsd}/vCPU-hr, ${eff.memoryGibHourUsd}
                          /GiB-hr), not cloud billing.
                        </Text>
                      )}
                    </Stack>

                    <div className={styles.tileGrid} role="group" aria-label="Gap counts">
                      {gapTiles.map((t) => (
                        <Box
                          key={t.label}
                          padding={1.5}
                          backgroundColor="secondary"
                          borderColor="weak"
                          borderStyle="solid"
                          borderRadius="default"
                        >
                          <Stack direction="column" gap={0.25} alignItems="center">
                            <Text variant="h4" tabular>
                              {t.value}
                            </Text>
                            <Text variant="bodySmall" color="secondary">
                              {t.label}
                            </Text>
                          </Stack>
                        </Box>
                      ))}
                    </div>
                  </>
                )}

                {(tab === 'overview' || tab === 'namespaces') && (
                  <Box
                    padding={2}
                    backgroundColor="secondary"
                    borderColor="weak"
                    borderStyle="solid"
                    borderRadius="default"
                  >
                    <Stack direction="column" gap={1.5}>
                      <Text element="h3" variant="h5">
                        {tab === 'overview' ? 'Waste by namespace' : 'Namespaces'}
                      </Text>
                      <InteractiveTable
                        columns={nsColumns}
                        data={filteredNamespaces}
                        getRowId={(r) => `${r.environment ?? ''}:${r.namespace}`}
                        pageSize={tab === 'overview' ? 10 : 15}
                      />
                    </Stack>
                  </Box>
                )}

                {tab === 'overview' && (
                  <Box
                    padding={2}
                    backgroundColor="secondary"
                    borderColor="weak"
                    borderStyle="solid"
                    borderRadius="default"
                  >
                    <Stack direction="column" gap={1.5}>
                      <Text element="h3" variant="h5">
                        Top waste CPU
                      </Text>
                      <InteractiveTable
                        columns={wasteColumns}
                        data={topWasteCpu}
                        getRowId={(r, i) => `${r.namespace}-${r.pod ?? ''}-${r.container}-${i}`}
                        pageSize={10}
                      />
                    </Stack>
                  </Box>
                )}

                {tab === 'nodes' && (
                  <Box
                    padding={2}
                    backgroundColor="secondary"
                    borderColor="weak"
                    borderStyle="solid"
                    borderRadius="default"
                  >
                    <Stack direction="column" gap={1.5}>
                      <Text element="h3" variant="h5">
                        Nodes
                      </Text>
                      {nodes.length === 0 ? (
                        <Alert severity="info" title="No node series">
                          Node rows need cAdvisor usage joined to kube_node_status_allocatable.
                        </Alert>
                      ) : (
                        <InteractiveTable
                          columns={nodeColumns}
                          data={nodes}
                          getRowId={(r) => r.node}
                          renderExpandedRow={(n) => (
                            <Box padding={1.5} backgroundColor="canvas" borderRadius="default">
                              <Stack
                                direction="row"
                                justifyContent="space-between"
                                alignItems="center"
                                wrap="wrap"
                                gap={2}
                              >
                                <Stack direction="row" gap={3} wrap="wrap">
                                  <Text variant="bodySmall" color="secondary" tabular>
                                    CPU: {formatCores(n.cpuUsage)} / {formatCores(n.cpuAlloc)}
                                  </Text>
                                  <Text variant="bodySmall" color="secondary" tabular>
                                    Memory: {formatBytes(n.memUsage)} / {formatBytes(n.memAlloc)}
                                  </Text>
                                  {n.storageUsage != null && n.storageLimit != null && (
                                    <Text variant="bodySmall" color="secondary" tabular>
                                      Storage: {formatBytes(n.storageUsage)} / {formatBytes(n.storageLimit)}
                                    </Text>
                                  )}
                                  {n.podCount != null && n.podCount > 0 && (
                                    <Text variant="bodySmall" color="secondary" tabular>
                                      Pods: {n.podCount}
                                    </Text>
                                  )}
                                </Stack>
                                <Stack direction="row" gap={1}>
                                  <Button
                                    size="sm"
                                    variant="secondary"
                                    onClick={() =>
                                      setPredict({
                                        namespace: nsFilter || filteredNamespaces[0]?.namespace || 'default',
                                        resource: 'cpu',
                                        node: n.node,
                                      })
                                    }
                                  >
                                    Predict CPU Usage
                                  </Button>
                                  <Button
                                    size="sm"
                                    variant="secondary"
                                    onClick={() =>
                                      setPredict({
                                        namespace: nsFilter || filteredNamespaces[0]?.namespace || 'default',
                                        resource: 'memory',
                                        node: n.node,
                                      })
                                    }
                                  >
                                    Predict Mem Usage
                                  </Button>
                                </Stack>
                              </Stack>
                            </Box>
                          )}
                        />
                      )}
                    </Stack>
                  </Box>
                )}
              </Stack>
            )}
          </DataState>
        )}

        {tab === 'rightsizing' && (
          <DataState
            loading={recsLoading}
            error={recsError}
            empty={recs != null && !recs.available}
            emptyTitle="No recommendations"
            emptyMessage={recs?.enablementNote || 'Need container metrics for this filter.'}
            skeleton={<TableSkeleton rows={8} />}
          >
            {recs && recs.available && (
              <Box
                padding={2}
                backgroundColor="secondary"
                borderColor="weak"
                borderStyle="solid"
                borderRadius="default"
              >
                <InteractiveTable
                  columns={recColumns}
                  data={recommendations}
                  getRowId={(r) => `${r.namespace}-${r.workloadName}-${r.container}`}
                  pageSize={15}
                />
              </Box>
            )}
            {selectedRec && (
              <Drawer
                title={`${selectedRec.workloadName} / ${selectedRec.container}`}
                onClose={() => setSelectedRecKey(null)}
              >
                <Stack direction="column" gap={2}>
                  <TextArea value={selectedRec.yamlPatch} readOnly rows={16} />
                  <Stack direction="row" gap={1}>
                    <Button
                      onClick={() => {
                        void navigator.clipboard.writeText(selectedRec.yamlPatch);
                      }}
                    >
                      Copy YAML
                    </Button>
                    <Button
                      variant="secondary"
                      onClick={() =>
                        appNavigate(
                          `services/${encodeURIComponent(selectedRec.namespace)}/${encodeURIComponent(selectedRec.workloadName)}`
                        )
                      }
                    >
                      Open service
                    </Button>
                  </Stack>
                </Stack>
              </Drawer>
            )}
          </DataState>
        )}

        {predict && (
          <Drawer
            title={`Predict ${predict.resource.toUpperCase()} · ${predict.namespace}${predict.node ? ` · ${predict.node}` : ''}`}
            onClose={() => setPredict(null)}
            size="lg"
          >
            <Stack direction="column" gap={2}>
              <Stack direction="row" gap={2} alignItems="center" wrap="wrap">
                <InlineField label="Namespace" noMargin>
                  <Combobox
                    options={nsOptions.filter((o) => o.value)}
                    value={predict.namespace}
                    onChange={(v) => {
                      if (v?.value) {
                        setPredict({ ...predict, namespace: v.value });
                      }
                    }}
                    placeholder="Namespace"
                    width={28}
                  />
                </InlineField>
                <RadioButtonGroup
                  options={FORECAST_RESOURCE_OPTIONS}
                  value={predict.resource}
                  onChange={(v) => setPredict({ ...predict, resource: v })}
                />
              </Stack>

              {toMs - fromMs < 2 * 60 * 60 * 1000 ? (
                <Alert severity="info" title="Widen the time range">
                  Predict needs at least 2 hours of history.
                </Alert>
              ) : (
                <DataState
                  loading={historyLoading || forecastLoading}
                  error={historyError || forecastError}
                  empty={
                    (history != null && !history.available && forecast != null && !forecast.available) ||
                    (forecast != null && !forecast.available && !forecastLoading)
                  }
                  emptyTitle="No predict data"
                  emptyMessage={forecast?.enablementNote || history?.enablementNote}
                  skeleton={<TableSkeleton rows={4} />}
                >
                  <Stack direction="column" gap={2}>
                    {forecast?.available && (
                      <>
                        <Stack direction="row" gap={1} alignItems="center" wrap="wrap">
                          <Text variant="bodySmall" color="secondary">
                            Method: {forecast.methodLabel} ({forecast.method}). Not a billing forecast.
                          </Text>
                          {forecast.daysToRequest != null && (
                            <Badge text={`~${forecast.daysToRequest.toFixed(1)}d to request`} color="orange" />
                          )}
                          {forecast.daysToLimit != null && (
                            <Badge text={`~${forecast.daysToLimit.toFixed(1)}d to limit`} color="red" />
                          )}
                        </Stack>
                        <Box
                          padding={1.5}
                          backgroundColor="secondary"
                          borderColor="weak"
                          borderStyle="solid"
                          borderRadius="default"
                        >
                          <PredictChartPanel
                            title={`${predict.resource.toUpperCase()} usage & linear projection`}
                            series={forecast.series}
                            unit={predict.resource === 'memory' ? 'bytes' : 'short'}
                            fromMs={fromMs}
                            toMs={toMs}
                          />
                        </Box>
                      </>
                    )}
                    {history?.available && (
                      <Box
                        padding={1.5}
                        backgroundColor="secondary"
                        borderColor="weak"
                        borderStyle="solid"
                        borderRadius="default"
                      >
                        <Stack direction="column" gap={1}>
                          <PredictChartPanel
                            title="Historical usage vs request / limit"
                            series={historyForResource}
                            unit={predict.resource === 'memory' ? 'bytes' : 'short'}
                            fromMs={fromMs}
                            toMs={toMs}
                          />
                          {history.note && (
                            <Text variant="bodySmall" color="secondary">
                              {history.note}
                            </Text>
                          )}
                        </Stack>
                      </Box>
                    )}
                  </Stack>
                </DataState>
              )}
            </Stack>
          </Drawer>
        )}
      </Stack>
    </PluginPage>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  statGrid: css`
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: ${theme.spacing(1.5)};
  `,
  tileGrid: css`
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
    gap: ${theme.spacing(1.5)};
  `,
  usageCell: css`
    display: flex;
    align-items: center;
    gap: ${theme.spacing(1)};
    min-width: 120px;
  `,
  usageTrack: css`
    flex: 1;
    height: 8px;
    background: ${theme.colors.background.canvas};
    border-radius: ${theme.shape.radius.default};
    overflow: hidden;
  `,
  usageFill: css`
    height: 100%;
  `,
  barUnder: css`
    background: ${theme.colors.success.main};
  `,
  barOk: css`
    background: ${theme.colors.warning.main};
  `,
  barOver: css`
    background: ${theme.colors.error.main};
  `,
});

export default Capacity;
