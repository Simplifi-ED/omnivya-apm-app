import React from 'react';
import { useStyles2 } from '@grafana/ui';
import { GrafanaTheme2 } from '@grafana/data';
import { css } from '@emotion/css';
import { CapacityEfficiencyBand } from '../../api/capacity';
import { typographyFontSize } from '../../utils/themeTypography';

type Props = {
  label: string;
  band?: CapacityEfficiencyBand | null;
  /** When true, show "No data" instead of a bar. */
  noData?: boolean;
};

function pct(v: number): string {
  return `${Math.round(v * 100)}%`;
}

/** Population efficiency bar: under (green) / ok (orange) / over (red). */
export function SegmentedEfficiencyBar({ label, band, noData }: Props) {
  const styles = useStyles2(getStyles);
  if (noData || !band) {
    return (
      <div className={styles.row}>
        <div className={styles.label}>{label}</div>
        <div className={styles.empty}>No data</div>
      </div>
    );
  }
  const under = Math.max(0, band.under);
  const ok = Math.max(0, band.ok);
  const over = Math.max(0, band.over);
  const sum = under + ok + over;
  if (sum <= 0) {
    return (
      <div className={styles.row}>
        <div className={styles.label}>{label}</div>
        <div className={styles.empty}>No data</div>
      </div>
    );
  }
  return (
    <div className={styles.row}>
      <div className={styles.label}>{label}</div>
      <div
        className={styles.bar}
        role="img"
        aria-label={`${label}: under ${pct(under)}, ok ${pct(ok)}, over ${pct(over)}`}
      >
        {under > 0 && (
          <div className={styles.under} style={{ flexGrow: under }} title={`Under ${pct(under)}`}>
            {under >= 0.08 ? pct(under) : ''}
          </div>
        )}
        {ok > 0 && (
          <div className={styles.ok} style={{ flexGrow: ok }} title={`OK ${pct(ok)}`}>
            {ok >= 0.08 ? pct(ok) : ''}
          </div>
        )}
        {over > 0 && (
          <div className={styles.over} style={{ flexGrow: over }} title={`Over ${pct(over)}`}>
            {over >= 0.08 ? pct(over) : ''}
          </div>
        )}
      </div>
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  row: css`
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(0.5)};
    margin-bottom: ${theme.spacing(1)};
  `,
  label: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
  empty: css`
    color: ${theme.colors.text.disabled};
    font-size: ${typographyFontSize(theme, 'sm')};
    padding: ${theme.spacing(0.75)} 0;
  `,
  bar: css`
    display: flex;
    height: 22px;
    border-radius: ${theme.shape.radius.default};
    overflow: hidden;
    background: ${theme.colors.background.canvas};
  `,
  under: css`
    display: flex;
    align-items: center;
    justify-content: center;
    background: ${theme.colors.success.main};
    color: ${theme.colors.success.contrastText};
    font-size: ${typographyFontSize(theme, 'sm')};
    font-variant-numeric: tabular-nums;
    min-width: 0;
  `,
  ok: css`
    display: flex;
    align-items: center;
    justify-content: center;
    background: ${theme.colors.warning.main};
    color: ${theme.colors.warning.contrastText};
    font-size: ${typographyFontSize(theme, 'sm')};
    font-variant-numeric: tabular-nums;
    min-width: 0;
  `,
  over: css`
    display: flex;
    align-items: center;
    justify-content: center;
    background: ${theme.colors.error.main};
    color: ${theme.colors.error.contrastText};
    font-size: ${typographyFontSize(theme, 'sm')};
    font-variant-numeric: tabular-nums;
    min-width: 0;
  `,
});
