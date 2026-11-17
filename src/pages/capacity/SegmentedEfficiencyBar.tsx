import React from 'react';
import { Box, Stack, Text, Tooltip, useStyles2 } from '@grafana/ui';
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
  const under = Math.max(0, band?.under ?? 0);
  const ok = Math.max(0, band?.ok ?? 0);
  const over = Math.max(0, band?.over ?? 0);
  const sum = under + ok + over;

  if (noData || !band || sum <= 0) {
    return (
      <Stack direction="column" gap={0.5}>
        <Stack direction="row" justifyContent="space-between" alignItems="center">
          <Text variant="bodySmall" color="secondary">
            {label}
          </Text>
          <Text variant="bodySmall" color="disabled">
            No data
          </Text>
        </Stack>
        <Box height="20px" backgroundColor="canvas" borderColor="weak" borderStyle="dashed" borderRadius="default" />
      </Stack>
    );
  }

  return (
    <Stack direction="column" gap={0.5}>
      <Stack direction="row" justifyContent="space-between" alignItems="center">
        <Text variant="bodySmall" color="secondary">
          {label}
        </Text>
      </Stack>
      <div
        className={styles.bar}
        role="img"
        aria-label={`${label}: under ${pct(under)}, optimal ${pct(ok)}, over ${pct(over)}`}
      >
        {under > 0 && (
          <Tooltip content={`Under-utilized (<40%): ${pct(under)}`}>
            <div className={styles.under} style={{ flexGrow: under }}>
              {under >= 0.1 ? pct(under) : ''}
            </div>
          </Tooltip>
        )}
        {ok > 0 && (
          <Tooltip content={`Optimal (40–80%): ${pct(ok)}`}>
            <div className={styles.ok} style={{ flexGrow: ok }}>
              {ok >= 0.1 ? pct(ok) : ''}
            </div>
          </Tooltip>
        )}
        {over > 0 && (
          <Tooltip content={`Over-utilized (>80%): ${pct(over)}`}>
            <div className={styles.over} style={{ flexGrow: over }}>
              {over >= 0.1 ? pct(over) : ''}
            </div>
          </Tooltip>
        )}
      </div>
    </Stack>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  bar: css`
    display: flex;
    height: 20px;
    border-radius: ${theme.shape.radius.default};
    overflow: hidden;
    background: ${theme.colors.background.canvas};
    border: 1px solid ${theme.colors.border.weak};
  `,
  under: css`
    display: flex;
    align-items: center;
    justify-content: center;
    background: ${theme.colors.success.main};
    color: ${theme.colors.success.contrastText};
    font-size: ${typographyFontSize(theme, 'sm')};
    font-weight: ${theme.typography.fontWeightMedium};
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
    font-weight: ${theme.typography.fontWeightMedium};
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
    font-weight: ${theme.typography.fontWeightMedium};
    font-variant-numeric: tabular-nums;
    min-width: 0;
  `,
});
