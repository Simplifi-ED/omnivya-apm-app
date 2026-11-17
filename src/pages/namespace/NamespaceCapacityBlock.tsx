import React from 'react';
import { Alert, useStyles2 } from '@grafana/ui';
import { GrafanaTheme2 } from '@grafana/data';
import { css } from '@emotion/css';
import { getNamespaceCapacity, CapacityOverview } from '../../api/capacity';
import { useFetch } from '../../utils/useFetch';
import { useAppNavigate } from '../../utils/navigation';
import { typographyFontSize } from '../../utils/themeTypography';
import { CardGridSkeleton } from '../../components/Skeleton';

interface NamespaceCapacityBlockProps {
  namespace: string;
  environment?: string;
  fromMs: number;
  toMs: number;
  /** Bumped on absolute-range refresh so capacity refetches without time change. */
  refreshKey?: number;
}

function formatUsd(v: number): string {
  if (!Number.isFinite(v) || v === 0) {
    return '$0';
  }
  return `$${v.toFixed(0)}`;
}

export function NamespaceCapacityBlock({
  namespace,
  environment,
  fromMs,
  toMs,
  refreshKey = 0,
}: NamespaceCapacityBlockProps) {
  const styles = useStyles2(getStyles);
  const appNavigate = useAppNavigate();
  const { data, loading, error } = useFetch<CapacityOverview>(
    () => getNamespaceCapacity(namespace, fromMs, toMs, environment),
    [namespace, environment, fromMs, toMs, refreshKey]
  );

  if (loading) {
    return <CardGridSkeleton cards={3} minCardWidth={140} cardHeight={72} />;
  }
  if (error) {
    return (
      <Alert severity="error" title="Capacity">
        {error}
      </Alert>
    );
  }
  if (!data?.available) {
    return null;
  }

  const gaps = data.gapCounts;

  return (
    <section className={styles.section} aria-label="Capacity">
      <div className={styles.header}>
        <h3 className={styles.title}>Capacity</h3>
        <button
          type="button"
          className={styles.link}
          onClick={() => {
            const params: Record<string, string> = { namespace, histNs: namespace };
            if (environment) {
              params.environment = environment;
            }
            appNavigate('capacity', params);
          }}
        >
          Open Capacity →
        </button>
      </div>
      <div className={styles.tiles}>
        <div className={styles.tile}>
          <div className={styles.value}>{formatUsd(data.efficiency.reclaimableMonthlyUsd)}</div>
          <div className={styles.label}>Reclaimable / mo</div>
        </div>
        <div className={styles.tile}>
          <div className={styles.value}>{gaps.missingRequest}</div>
          <div className={styles.label}>No requests</div>
        </div>
        <div className={styles.tile}>
          <div className={styles.value}>{gaps.underProvisioned}</div>
          <div className={styles.label}>Under-provisioned</div>
        </div>
        <div className={styles.tile}>
          <div className={styles.value}>{gaps.throttled}</div>
          <div className={styles.label}>Throttled</div>
        </div>
      </div>
    </section>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  section: css`
    margin: ${theme.spacing(3)} 0;
  `,
  header: css`
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    margin-bottom: ${theme.spacing(1)};
  `,
  title: css`
    margin: 0;
    font-size: ${typographyFontSize(theme, 'base')};
  `,
  link: css`
    background: none;
    border: none;
    color: ${theme.colors.text.link};
    cursor: pointer;
    font-size: ${typographyFontSize(theme, 'sm')};
    padding: 0;
    &:hover {
      text-decoration: underline;
    }
  `,
  tiles: css`
    display: flex;
    flex-wrap: wrap;
    gap: ${theme.spacing(1)};
  `,
  tile: css`
    background: ${theme.colors.background.secondary};
    border-radius: ${theme.shape.radius.default};
    padding: ${theme.spacing(1.5)};
    min-width: 120px;
  `,
  value: css`
    font-size: ${typographyFontSize(theme, 'base')};
    font-weight: ${theme.typography.fontWeightMedium};
  `,
  label: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
});
