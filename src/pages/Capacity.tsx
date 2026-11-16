import React, { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { PluginPage } from '@grafana/runtime';
import {
  Alert,
  Badge,
  Button,
  Drawer,
  Icon,
  Input,
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

type CapTab = 'overview' | 'rightsizing' | 'history' | 'forecast';

const FORECAST_RESOURCE_OPTIONS = [
  { label: 'CPU', value: 'cpu' as const },
  { label: 'Memory', value: 'memory' as const },
];

function pct(ratio: number): string {
  if (!Number.isFinite(ratio)) {
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

function Capacity() {
  const styles = useStyles2(getStyles);
  const appNavigate = useAppNavigate();
  const updateParams = useUrlParams();
  const { fromMs, toMs, refresh } = useTimeRange();
  const [searchParams] = useSearchParams();
  const env = sanitizeParam(searchParams.get('environment') ?? '');
  const nsFilter = sanitizeParam(searchParams.get('namespace') ?? '');
  const tabParam = (searchParams.get('capTab') as CapTab) || 'overview';
  const tab: CapTab = ['overview', 'rightsizing', 'history', 'forecast'].includes(tabParam) ? tabParam : 'overview';
  const histNs = sanitizeParam(searchParams.get('histNs') ?? nsFilter);
  const [selectedRec, setSelectedRec] = useState<WorkloadRecommendation | null>(null);
  const [forecastResource, setForecastResource] = useState<'cpu' | 'memory'>('cpu');

  const {
    data: overview,
    loading: overviewLoading,
    error: overviewError,
    refetch: refetchOverview,
  } = useFetch<CapacityOverview>(
    () => getCapacity(fromMs, toMs, { namespace: nsFilter || undefined, environment: env || undefined }),
    [fromMs, toMs, nsFilter, env],
    { skip: tab !== 'overview' && tab !== 'history' && tab !== 'forecast' }
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

  const {
    data: history,
    loading: histLoading,
    error: histError,
  } = useFetch<CapacityHistoryResponse>(
    () =>
      getCapacityHistory(fromMs, toMs, {
        namespace: histNs || 'default',
        environment: env || undefined,
      }),
    [fromMs, toMs, histNs, env],
    { skip: tab !== 'history' || !histNs }
  );

  const {
    data: forecast,
    loading: fcLoading,
    error: fcError,
    refetch: refetchFc,
  } = useFetch<CapacityForecastResponse>(
    () =>
      getCapacityForecast(fromMs, toMs, {
        resource: forecastResource,
        namespace: histNs || 'default',
        environment: env || undefined,
      }),
    [fromMs, toMs, histNs, env, forecastResource],
    { skip: tab !== 'forecast' || !histNs }
  );

  const handleRefresh = () => {
    refresh();
    refetchOverview();
    refetchRecs();
    refetchFc();
  };

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

  return (
    <PluginPage>
      <div className={styles.container}>
        <div className={styles.toolbar}>
          <div className={styles.scopeRow}>
            <div className={styles.filterGroup}>
              <div className={styles.filterItem}>
                <Input
                  aria-label="Filter namespace"
                  prefix={<Icon name="search" />}
                  placeholder="Filter namespace..."
                  value={nsFilter}
                  onChange={(e) => updateParams({ namespace: e.currentTarget.value || null })}
                />
              </div>
              <div className={styles.filterItem}>
                <Input
                  aria-label="Environment"
                  prefix={<Icon name="cloud" />}
                  placeholder="Environment (cluster)"
                  value={env}
                  onChange={(e) => updateParams({ environment: e.currentTarget.value || null })}
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
              ['rightsizing', 'Right-sizing'],
              ['history', 'History'],
              ['forecast', 'Forecast'],
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
          {tab === 'overview' && (
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
                  <div className={styles.effBar} role="group" aria-label="Efficiency summary">
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
                      <div className={styles.effValue}>{eff?.cpuAllocRatio != null ? pct(eff.cpuAllocRatio) : '—'}</div>
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
                      /GiB-hr), not cloud billing.
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

                  <h3 className={styles.sectionTitle}>Waste by namespace</h3>
                  <table className={styles.table} aria-label="Waste by namespace">
                    <thead>
                      <tr>
                        <th scope="col">Namespace</th>
                        <th scope="col">Waste CPU</th>
                        <th scope="col">Waste mem</th>
                        <th scope="col">Reclaimable / mo</th>
                        <th scope="col">Gaps</th>
                      </tr>
                    </thead>
                    <tbody>
                      {overview.namespaces.map((ns) => (
                        <tr
                          key={ns.namespace}
                          className={styles.row}
                          onClick={() => {
                            const extra: Record<string, string> = {};
                            if (env) {
                              extra.environment = env;
                            }
                            appNavigate(`namespaces/${encodeURIComponent(ns.namespace)}`, extra);
                          }}
                        >
                          <td className={styles.nameCell}>{ns.namespace}</td>
                          <td>{formatCores(ns.wasteCpu)}</td>
                          <td>{formatBytes(ns.wasteMem)}</td>
                          <td>{formatUsd(ns.reclaimableMonthlyUsd)}</td>
                          <td className={styles.muted}>
                            req {ns.missingRequest} · lim {ns.missingLimit} · under {ns.underProvisioned} · thr{' '}
                            {ns.throttled}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>

                  <h3 className={styles.sectionTitle}>Top waste CPU</h3>
                  <table className={styles.table} aria-label="Top waste CPU">
                    <thead>
                      <tr>
                        <th scope="col">Namespace</th>
                        <th scope="col">Pod</th>
                        <th scope="col">Container</th>
                        <th scope="col">Waste CPU</th>
                      </tr>
                    </thead>
                    <tbody>
                      {overview.topWasteCpu.map((r, i) => (
                        <tr key={`${r.namespace}-${r.pod}-${r.container}-${i}`}>
                          <td>{r.namespace}</td>
                          <td className={styles.muted}>{r.pod}</td>
                          <td>{r.container}</td>
                          <td>{formatCores(r.wasteCpu ?? 0)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>

                  <h3 className={styles.sectionTitle}>Top waste memory</h3>
                  <table className={styles.table} aria-label="Top waste memory">
                    <thead>
                      <tr>
                        <th scope="col">Namespace</th>
                        <th scope="col">Pod</th>
                        <th scope="col">Container</th>
                        <th scope="col">Waste mem</th>
                      </tr>
                    </thead>
                    <tbody>
                      {overview.topWasteMem.map((r, i) => (
                        <tr key={`${r.namespace}-${r.pod}-${r.container}-mem-${i}`}>
                          <td>{r.namespace}</td>
                          <td className={styles.muted}>{r.pod}</td>
                          <td>{r.container}</td>
                          <td>{formatBytes(r.wasteMem ?? 0)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>

                  <h3 className={styles.sectionTitle}>Top risk containers</h3>
                  <table className={styles.table} aria-label="Top risk containers">
                    <thead>
                      <tr>
                        <th scope="col">Namespace</th>
                        <th scope="col">Pod</th>
                        <th scope="col">Container</th>
                        <th scope="col">Risk</th>
                      </tr>
                    </thead>
                    <tbody>
                      {overview.topRiskContainers.map((r, i) => (
                        <tr
                          key={`${r.namespace}-${r.pod}-${r.container}-risk-${i}`}
                          className={styles.row}
                          onClick={() => {
                            updateParams({
                              namespace: r.namespace,
                              histNs: r.namespace,
                              capTab: 'history',
                            });
                          }}
                        >
                          <td>{r.namespace}</td>
                          <td className={styles.muted}>{r.pod}</td>
                          <td>{r.container || '—'}</td>
                          <td className={styles.muted}>{r.risk || '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
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
                <>
                  <table className={styles.table} aria-label="Workload right-sizing">
                    <thead>
                      <tr>
                        <th scope="col">Workload</th>
                        <th scope="col">Container</th>
                        <th scope="col">QoS</th>
                        <th scope="col">Confidence</th>
                        <th scope="col">Burstable CPU</th>
                        <th scope="col">Burstable mem</th>
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
                          <td>
                            <Badge text={r.qosClass} color="blue" />
                          </td>
                          <td>
                            <Badge
                              text={r.confidence}
                              color={r.confidence === 'high' ? 'green' : r.confidence === 'medium' ? 'orange' : 'red'}
                            />
                          </td>
                          <td>{formatCores(r.burstable.cpuReq)}</td>
                          <td>
                            {formatBytes(r.burstable.memReq)} / lim {formatBytes(r.burstable.memLim)}
                          </td>
                          <td>{formatUsd(r.reclaimableMonthlyUsd)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {selectedRec && (
                    <Drawer
                      title={`${selectedRec.workloadName} / ${selectedRec.container}`}
                      onClose={() => setSelectedRec(null)}
                    >
                      <p className={styles.muted}>
                        Burstable omits CPU limit to avoid CFS throttling. Guaranteed sets req = lim from peak usage.
                      </p>
                      <TextArea rows={12} value={selectedRec.yamlPatch} readOnly />
                      <div className={styles.drawerActions}>
                        <Button onClick={() => navigator.clipboard.writeText(selectedRec.yamlPatch)}>
                          Copy YAML patch
                        </Button>
                        <Button
                          variant="secondary"
                          onClick={() => {
                            const extra: Record<string, string> = { tab: 'backend' };
                            if (env) {
                              extra.environment = env;
                            }
                            appNavigate(
                              `services/${encodeURIComponent(selectedRec.namespace)}/${encodeURIComponent(selectedRec.workloadName)}`,
                              extra
                            );
                          }}
                        >
                          Open Backend Runtime
                        </Button>
                      </div>
                    </Drawer>
                  )}
                </>
              )}
            </DataState>
          )}

          {tab === 'history' && (
            <>
              <div className={styles.viewRow}>
                <div className={styles.filterItem}>
                  <Input
                    aria-label="Namespace for history"
                    prefix={<Icon name="layer-group" />}
                    placeholder="Namespace for history..."
                    value={histNs}
                    onChange={(e) => updateParams({ histNs: e.currentTarget.value || null })}
                  />
                </div>
              </div>
              {!histNs && (
                <Alert severity="info" title="Pick a namespace">
                  History requires a namespace.
                </Alert>
              )}
              {histNs && (
                <DataState
                  loading={histLoading}
                  error={histError}
                  empty={history != null && !history.available}
                  emptyTitle="No history"
                  emptyMessage={history?.enablementNote || history?.note}
                  skeleton={<TableSkeleton rows={6} />}
                >
                  {history && history.available && (
                    <>
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
                              <td className={styles.nameCell}>{s.name}</td>
                              <td>{s.points.length}</td>
                              <td>
                                {s.points.length
                                  ? s.name.includes('mem') || s.name.includes('Mem')
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
                </DataState>
              )}
            </>
          )}

          {tab === 'forecast' && (
            <>
              <div className={styles.viewRow}>
                <div className={styles.filterItem}>
                  <Input
                    aria-label="Namespace for forecast"
                    prefix={<Icon name="layer-group" />}
                    placeholder="Namespace for forecast..."
                    value={histNs}
                    onChange={(e) => updateParams({ histNs: e.currentTarget.value || null })}
                  />
                </div>
                <RadioButtonGroup
                  size="sm"
                  options={FORECAST_RESOURCE_OPTIONS}
                  value={forecastResource}
                  onChange={setForecastResource}
                />
              </div>
              {!histNs && (
                <Alert severity="info" title="Pick a namespace">
                  Forecast requires a namespace and at least a 2h time range.
                </Alert>
              )}
              {histNs && (
                <DataState
                  loading={fcLoading}
                  error={fcError}
                  empty={forecast != null && !forecast.available}
                  emptyTitle="Forecast unavailable"
                  emptyMessage={forecast?.enablementNote}
                  skeleton={<TableSkeleton rows={4} />}
                >
                  {forecast && forecast.available && (
                    <>
                      <p className={styles.rateNote}>
                        Method: {forecast.methodLabel} ({forecast.method}). Not a billing forecast.
                      </p>
                      <div className={styles.viewRow}>
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
                            <th scope="col">End value</th>
                          </tr>
                        </thead>
                        <tbody>
                          {forecast.series.map((s) => (
                            <tr key={s.name}>
                              <td className={styles.nameCell}>{s.name}</td>
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
                </DataState>
              )}
            </>
          )}
        </div>
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
  viewRow: css`
    display: flex;
    gap: ${theme.spacing(1)};
    align-items: center;
    flex-wrap: wrap;
    margin-bottom: ${theme.spacing(2)};
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
});

export default Capacity;
