import React, { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { PluginPage } from '@grafana/runtime';
import {
  Alert,
  Badge,
  Button,
  Combobox,
  Drawer,
  RadioButtonGroup,
  Tab,
  TabsBar,
  TextArea,
  useStyles2,
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
import { typographyFontSize } from '../utils/themeTypography';
import { extractEnvironmentOptions, extractNamespaceOptions } from '../utils/options';
import { EfficiencyCards } from './capacity/EfficiencyCards';

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

function usageBar(ratio: number | undefined, styles: ReturnType<typeof getStyles>): React.ReactNode {
  if (ratio == null || !Number.isFinite(ratio) || ratio <= 0) {
    return <span className={styles.muted}>—</span>;
  }
  const clamped = Math.min(ratio, 1.2);
  const width = `${Math.min(clamped, 1) * 100}%`;
  const colorClass = ratio > 0.8 ? styles.barOver : ratio < 0.4 ? styles.barUnder : styles.barOk;
  return (
    <div className={styles.usageCell}>
      <div className={styles.usageTrack}>
        <div className={`${styles.usageFill} ${colorClass}`} style={{ width }} />
      </div>
      <span className={styles.usagePct}>{pct(ratio)}</span>
    </div>
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
  const [selectedRec, setSelectedRec] = useState<WorkloadRecommendation | null>(null);
  const [predict, setPredict] = useState<PredictTarget | null>(null);
  const [expandedNode, setExpandedNode] = useState<string | null>(null);

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

  const openPredict = (target: PredictTarget) => {
    setPredict(target);
  };

  const filteredNamespaces: CapacityNamespaceRollup[] = useMemo(() => {
    const rows = overview?.namespaces ?? [];
    if (!nsFilter) {
      return rows;
    }
    return rows.filter((r) => r.namespace === nsFilter);
  }, [overview?.namespaces, nsFilter]);

  const nodes: CapacityNodeRollup[] = overview?.nodes ?? [];

  return (
    <PluginPage>
      <div className={styles.container}>
        <div className={styles.toolbar}>
          <div className={styles.scopeRow}>
            <div className={styles.filterGroup}>
              <div className={styles.filterItem}>
                <Combobox
                  options={nsOptions}
                  value={nsFilter}
                  onChange={(v) => updateParams({ namespace: v?.value || null })}
                  placeholder="Filter namespaces"
                  width={32}
                />
              </div>
              <div className={styles.filterItem}>
                <Combobox
                  options={envOptions}
                  value={env}
                  onChange={(v) => updateParams({ environment: v?.value || null })}
                  placeholder="Filter environments"
                  width={32}
                />
              </div>
            </div>
            <HeaderTimeControls onRefresh={handleRefresh} />
          </div>
        </div>

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

        <div className={styles.tabContent}>
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
                <>
                  {tab === 'overview' && (
                    <>
                      <EfficiencyCards population={overview.population} />
                      <div className={styles.effBar} role="group" aria-label="Fleet ratios">
                        <div className={styles.effStat}>
                          <div className={styles.effLabel}>CPU used / requested</div>
                          <div className={styles.effValue}>{pct(eff?.cpuUsedRatio ?? 0)}</div>
                        </div>
                        <div className={styles.effStat}>
                          <div className={styles.effLabel}>Mem used / requested</div>
                          <div className={styles.effValue}>{pct(eff?.memUsedRatio ?? 0)}</div>
                        </div>
                        <div className={styles.effStat}>
                          <div className={styles.effLabel}>CPU packing</div>
                          <div className={styles.effValue}>
                            {eff?.cpuAllocRatio != null ? pct(eff.cpuAllocRatio) : '—'}
                          </div>
                        </div>
                        <div className={styles.effStat}>
                          <div className={styles.effLabel}>Reclaimable / mo</div>
                          <div className={styles.effValueGood}>{formatUsd(eff?.reclaimableMonthlyUsd ?? 0)}</div>
                        </div>
                        <div className={styles.effStat}>
                          <div className={styles.effLabel}>Deficit / mo</div>
                          <div className={styles.effValueWarn}>{formatUsd(eff?.deficitMonthlyUsd ?? 0)}</div>
                        </div>
                      </div>
                      {eff?.ratesAreEstimates && (
                        <p className={styles.rateNote}>
                          USD figures are rate-based estimates (${eff.cpuHourUsd}/vCPU-hr, ${eff.memoryGibHourUsd}
                          /GiB-hr), not cloud billing. Storage bands stay empty until filesystem series are scraped.
                        </p>
                      )}
                      <div className={styles.tiles} role="group" aria-label="Gap counts">
                        {gapTiles.map((t) => (
                          <div key={t.label} className={styles.tile}>
                            <div className={styles.tileValue}>{t.value}</div>
                            <div className={styles.tileLabel}>{t.label}</div>
                          </div>
                        ))}
                      </div>
                    </>
                  )}

                  {(tab === 'overview' || tab === 'namespaces') && (
                    <>
                      <h3 className={styles.sectionTitle}>
                        {tab === 'overview' ? 'Waste by namespace' : 'Namespaces'}
                      </h3>
                      <table className={styles.table} aria-label="Namespaces capacity">
                        <thead>
                          <tr>
                            <th scope="col">Namespace</th>
                            <th scope="col">CPU usage</th>
                            <th scope="col">Mem usage</th>
                            <th scope="col">Waste CPU</th>
                            <th scope="col">Waste mem</th>
                            <th scope="col">Gaps</th>
                            <th scope="col">Predict</th>
                          </tr>
                        </thead>
                        <tbody>
                          {filteredNamespaces.map((ns) => (
                            <tr key={`${ns.environment ?? ''}:${ns.namespace}`} className={styles.row}>
                              <td
                                className={styles.nameCell}
                                onClick={() => {
                                  const extra: Record<string, string> = {};
                                  if (env) {
                                    extra.environment = env;
                                  }
                                  appNavigate(`namespaces/${encodeURIComponent(ns.namespace)}`, extra);
                                }}
                              >
                                {ns.namespace}
                                {ns.environment ? <div className={styles.muted}>{ns.environment}</div> : null}
                              </td>
                              <td>{usageBar(ns.cpuUsedRatio, styles)}</td>
                              <td>{usageBar(ns.memUsedRatio, styles)}</td>
                              <td>{formatCores(ns.wasteCpu)}</td>
                              <td>{formatBytes(ns.wasteMem)}</td>
                              <td className={styles.muted}>
                                {ns.missingRequest + ns.missingLimit + ns.underProvisioned + ns.throttled || '—'}
                              </td>
                              <td>
                                <div className={styles.predictBtns}>
                                  <Button
                                    size="sm"
                                    variant="secondary"
                                    onClick={() => openPredict({ namespace: ns.namespace, resource: 'cpu' })}
                                  >
                                    Predict CPU
                                  </Button>
                                  <Button
                                    size="sm"
                                    variant="secondary"
                                    onClick={() => openPredict({ namespace: ns.namespace, resource: 'memory' })}
                                  >
                                    Predict Mem
                                  </Button>
                                </div>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </>
                  )}

                  {tab === 'overview' && (
                    <>
                      <h3 className={styles.sectionTitle}>Top waste CPU</h3>
                      <table className={styles.table} aria-label="Top waste CPU">
                        <thead>
                          <tr>
                            <th scope="col">Namespace</th>
                            <th scope="col">Pod</th>
                            <th scope="col">Container</th>
                            <th scope="col">Waste</th>
                          </tr>
                        </thead>
                        <tbody>
                          {(overview.topWasteCpu ?? []).map((r, i) => (
                            <tr key={`${r.namespace}-${r.pod}-${r.container}-${i}`}>
                              <td>{r.namespace}</td>
                              <td>{r.pod}</td>
                              <td>{r.container}</td>
                              <td>{formatCores(r.wasteCpu ?? 0)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </>
                  )}

                  {tab === 'nodes' && (
                    <>
                      <h3 className={styles.sectionTitle}>Nodes</h3>
                      {nodes.length === 0 ? (
                        <Alert severity="info" title="No node series">
                          Node rows need cAdvisor usage joined to kube_node_status_allocatable. Storage stays No data
                          until filesystem series are scraped.
                        </Alert>
                      ) : (
                        <table className={styles.table} aria-label="Nodes capacity">
                          <thead>
                            <tr>
                              <th scope="col" />
                              <th scope="col">Node</th>
                              <th scope="col">CPU</th>
                              <th scope="col">Memory</th>
                              <th scope="col">Storage</th>
                              <th scope="col">Ready</th>
                            </tr>
                          </thead>
                          <tbody>
                            {nodes.map((n) => {
                              const open = expandedNode === n.node;
                              return (
                                <React.Fragment key={n.node}>
                                  <tr className={styles.row}>
                                    <td>
                                      <Button
                                        size="sm"
                                        variant="secondary"
                                        icon={open ? 'angle-down' : 'angle-right'}
                                        aria-label={open ? 'Collapse' : 'Expand'}
                                        onClick={() => setExpandedNode(open ? null : n.node)}
                                      />
                                    </td>
                                    <td className={styles.nameCell}>
                                      {n.node}
                                      {n.environment ? <div className={styles.muted}>{n.environment}</div> : null}
                                    </td>
                                    <td>{usageBar(n.cpuUsedRatio, styles)}</td>
                                    <td>{usageBar(n.memUsedRatio, styles)}</td>
                                    <td>
                                      {n.storageRatio != null ? (
                                        usageBar(n.storageRatio, styles)
                                      ) : (
                                        <span className={styles.muted}>No data</span>
                                      )}
                                    </td>
                                    <td>
                                      <Badge
                                        text={n.conditionReady ? 'Ready' : 'NotReady'}
                                        color={n.conditionReady ? 'green' : 'red'}
                                      />
                                    </td>
                                  </tr>
                                  {open && (
                                    <tr>
                                      <td colSpan={6}>
                                        <div className={styles.expandRow}>
                                          <span className={styles.muted}>
                                            CPU {formatCores(n.cpuUsage)} / {formatCores(n.cpuAlloc)} · Mem{' '}
                                            {formatBytes(n.memUsage)} / {formatBytes(n.memAlloc)}
                                            {n.podCount != null ? ` · ${n.podCount} pods` : ''}
                                          </span>
                                          <div className={styles.predictBtns}>
                                            <Button
                                              size="sm"
                                              variant="secondary"
                                              onClick={() =>
                                                openPredict({
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
                                                openPredict({
                                                  namespace: nsFilter || filteredNamespaces[0]?.namespace || 'default',
                                                  resource: 'memory',
                                                  node: n.node,
                                                })
                                              }
                                            >
                                              Predict Mem Usage
                                            </Button>
                                          </div>
                                        </div>
                                      </td>
                                    </tr>
                                  )}
                                </React.Fragment>
                              );
                            })}
                          </tbody>
                        </table>
                      )}
                    </>
                  )}
                </>
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
                <table className={styles.table} aria-label="Right-sizing recommendations">
                  <thead>
                    <tr>
                      <th scope="col">Workload</th>
                      <th scope="col">Container</th>
                      <th scope="col">QoS</th>
                      <th scope="col">Confidence</th>
                      <th scope="col">Reclaimable</th>
                    </tr>
                  </thead>
                  <tbody>
                    {recs.recommendations.map((r) => (
                      <tr
                        key={`${r.namespace}-${r.workloadName}-${r.container}`}
                        className={styles.row}
                        onClick={() => setSelectedRec(r)}
                      >
                        <td className={styles.nameCell}>
                          {r.workloadKind}/{r.workloadName}
                          <div className={styles.muted}>{r.namespace}</div>
                        </td>
                        <td>{r.container}</td>
                        <td>{r.qosClass}</td>
                        <td>
                          <Badge text={r.confidence} color={r.confidence === 'high' ? 'green' : 'orange'} />
                        </td>
                        <td>{formatUsd(r.reclaimableMonthlyUsd)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {selectedRec && (
                <Drawer
                  title={`${selectedRec.workloadName} / ${selectedRec.container}`}
                  onClose={() => setSelectedRec(null)}
                >
                  <TextArea value={selectedRec.yamlPatch} readOnly rows={16} />
                  <div className={styles.drawerActions}>
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
                  </div>
                </Drawer>
              )}
            </DataState>
          )}
        </div>

        {predict && (
          <Drawer
            title={`Predict ${predict.resource.toUpperCase()} · ${predict.namespace}${predict.node ? ` · ${predict.node}` : ''}`}
            onClose={() => setPredict(null)}
            size="lg"
          >
            <div className={styles.predictFilters}>
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
              <RadioButtonGroup
                options={FORECAST_RESOURCE_OPTIONS}
                value={predict.resource}
                onChange={(v) => setPredict({ ...predict, resource: v })}
              />
            </div>
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
                {forecast?.available && (
                  <>
                    <p className={styles.rateNote}>
                      Method: {forecast.methodLabel} ({forecast.method}). Not a billing forecast.
                    </p>
                    <div className={styles.predictBtns}>
                      {forecast.daysToRequest != null && (
                        <Badge text={`~${forecast.daysToRequest.toFixed(1)}d to request`} color="orange" />
                      )}
                      {forecast.daysToLimit != null && (
                        <Badge text={`~${forecast.daysToLimit.toFixed(1)}d to limit`} color="red" />
                      )}
                    </div>
                    <table className={styles.table} aria-label="Capacity forecast">
                      <thead>
                        <tr>
                          <th scope="col">Series</th>
                          <th scope="col">Points</th>
                          <th scope="col">Latest</th>
                        </tr>
                      </thead>
                      <tbody>
                        {forecast.series.map((s) => (
                          <tr key={s.name}>
                            <td>{s.name}</td>
                            <td>{s.points.length}</td>
                            <td>
                              {s.points.length
                                ? forecast.resource === 'memory'
                                  ? formatBytes(s.points[s.points.length - 1].value)
                                  : formatCores(s.points[s.points.length - 1].value)
                                : '—'}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </>
                )}
                {history?.available && (
                  <>
                    <h3 className={styles.sectionTitle}>History</h3>
                    {history.note && <p className={styles.rateNote}>{history.note}</p>}
                    <table className={styles.table} aria-label="Capacity history">
                      <thead>
                        <tr>
                          <th scope="col">Series</th>
                          <th scope="col">Points</th>
                          <th scope="col">Latest</th>
                        </tr>
                      </thead>
                      <tbody>
                        {history.series.map((s) => (
                          <tr key={s.name}>
                            <td>{s.name}</td>
                            <td>{s.points.length}</td>
                            <td>{s.points.length ? s.points[s.points.length - 1].value.toFixed(4) : '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </>
                )}
              </DataState>
            )}
          </Drawer>
        )}
      </div>
    </PluginPage>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  container: css`
    display: flex;
    flex-direction: column;
    flex: 1;
    padding: 0;
    overflow: hidden;
  `,
  toolbar: css`
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(1)};
    margin-bottom: ${theme.spacing(1)};
    z-index: 2;
  `,
  scopeRow: css`
    display: flex;
    gap: ${theme.spacing(1)};
    align-items: center;
  `,
  filterGroup: css`
    display: flex;
    flex: 1;
    gap: ${theme.spacing(1)};
    align-items: center;
    min-width: 0;
  `,
  filterItem: css`
    flex: 1;
    min-width: 160px;
    max-width: 320px;
  `,
  tabContent: css`
    margin-top: ${theme.spacing(2)};
  `,
  effBar: css`
    display: flex;
    flex-wrap: wrap;
    gap: ${theme.spacing(1)};
    margin-bottom: ${theme.spacing(2)};
  `,
  effStat: css`
    background: ${theme.colors.background.secondary};
    border: 1px solid ${theme.colors.border.weak};
    border-radius: ${theme.shape.radius.default};
    padding: ${theme.spacing(1.5)} ${theme.spacing(2)};
    min-width: 120px;
  `,
  effLabel: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
  effValue: css`
    font-size: ${typographyFontSize(theme, 'md')};
    font-weight: ${theme.typography.fontWeightMedium};
    font-variant-numeric: tabular-nums;
  `,
  effValueGood: css`
    font-size: ${typographyFontSize(theme, 'md')};
    font-weight: ${theme.typography.fontWeightMedium};
    font-variant-numeric: tabular-nums;
    color: ${theme.colors.success.text};
  `,
  effValueWarn: css`
    font-size: ${typographyFontSize(theme, 'md')};
    font-weight: ${theme.typography.fontWeightMedium};
    font-variant-numeric: tabular-nums;
    color: ${theme.colors.warning.text};
  `,
  rateNote: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
    margin: 0 0 ${theme.spacing(2)};
  `,
  tiles: css`
    display: flex;
    flex-wrap: wrap;
    gap: ${theme.spacing(1)};
    margin-bottom: ${theme.spacing(3)};
  `,
  tile: css`
    background: ${theme.colors.background.secondary};
    border: 1px solid ${theme.colors.border.weak};
    border-radius: ${theme.shape.radius.default};
    padding: ${theme.spacing(1.5)};
    min-width: 110px;
    text-align: center;
  `,
  tileValue: css`
    font-size: ${typographyFontSize(theme, 'md')};
    font-weight: ${theme.typography.fontWeightMedium};
    font-variant-numeric: tabular-nums;
  `,
  tileLabel: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
  sectionTitle: css`
    margin: ${theme.spacing(3)} 0 ${theme.spacing(1)};
    font-size: ${typographyFontSize(theme, 'base')};
    font-weight: ${theme.typography.fontWeightMedium};
  `,
  table: css`
    width: 100%;
    border-collapse: separate;
    border-spacing: 0;
    th {
      text-align: left;
      padding: ${theme.spacing(1)} ${theme.spacing(1.5)};
      color: ${theme.colors.text.secondary};
      font-size: ${typographyFontSize(theme, 'sm')};
      font-weight: ${theme.typography.fontWeightMedium};
      border-bottom: 1px solid ${theme.colors.border.medium};
      white-space: nowrap;
    }
    td {
      padding: ${theme.spacing(1)} ${theme.spacing(1.5)};
      border-bottom: 1px solid ${theme.colors.border.weak};
      vertical-align: middle;
    }
  `,
  row: css`
    cursor: pointer;
    &:hover {
      background: ${theme.colors.background.secondary};
    }
  `,
  nameCell: css`
    font-weight: ${theme.typography.fontWeightMedium};
    color: ${theme.colors.text.primary};
  `,
  muted: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
  drawerActions: css`
    margin-top: ${theme.spacing(2)};
    display: flex;
    gap: ${theme.spacing(1)};
    flex-wrap: wrap;
  `,
  predictBtns: css`
    display: flex;
    gap: ${theme.spacing(0.5)};
    flex-wrap: wrap;
  `,
  predictFilters: css`
    display: flex;
    gap: ${theme.spacing(1)};
    align-items: center;
    margin-bottom: ${theme.spacing(2)};
    flex-wrap: wrap;
  `,
  expandRow: css`
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: ${theme.spacing(2)};
    padding: ${theme.spacing(1)} 0;
    flex-wrap: wrap;
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
  usagePct: css`
    font-variant-numeric: tabular-nums;
    font-size: ${typographyFontSize(theme, 'sm')};
    min-width: 2.5rem;
  `,
});

export default Capacity;
