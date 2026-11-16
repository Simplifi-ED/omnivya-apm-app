import React from 'react';
import { useStyles2 } from '@grafana/ui';
import { GrafanaTheme2 } from '@grafana/data';
import { css } from '@emotion/css';
import { CapacityPopulation } from '../../api/capacity';
import { SegmentedEfficiencyBar } from './SegmentedEfficiencyBar';
import { typographyFontSize } from '../../utils/themeTypography';

type Props = {
  population?: CapacityPopulation;
  timeLabel?: string;
};

export function EfficiencyCards({ population, timeLabel = 'Selected range' }: Props) {
  const styles = useStyles2(getStyles);
  const clusters = population?.clusters;
  const nodes = population?.nodes;
  const namespaces = population?.namespaces;

  return (
    <div className={styles.grid} role="group" aria-label="Efficiency overview">
      <article className={styles.card}>
        <header className={styles.cardHead}>
          <h3 className={styles.title}>Cluster efficiency</h3>
          <span className={styles.meta}>{timeLabel}</span>
        </header>
        <p className={styles.sub}>{clusters?.total ?? 0} clusters total</p>
        <SegmentedEfficiencyBar label="CPU" band={clusters?.bands.cpu} />
        <SegmentedEfficiencyBar label="Memory" band={clusters?.bands.mem} />
        <SegmentedEfficiencyBar label="Storage" noData={!clusters?.bands.storage} band={clusters?.bands.storage} />
      </article>
      <article className={styles.card}>
        <header className={styles.cardHead}>
          <h3 className={styles.title}>Node efficiency</h3>
          <span className={styles.meta}>{timeLabel}</span>
        </header>
        <p className={styles.sub}>{nodes?.total ?? 0} nodes total</p>
        <SegmentedEfficiencyBar label="CPU" band={nodes?.bands.cpu} />
        <SegmentedEfficiencyBar label="Memory" band={nodes?.bands.mem} />
        <SegmentedEfficiencyBar label="Storage" noData={!nodes?.bands.storage} band={nodes?.bands.storage} />
      </article>
      <article className={styles.card}>
        <header className={styles.cardHead}>
          <h3 className={styles.title}>Namespace efficiency</h3>
          <span className={styles.meta}>{timeLabel}</span>
        </header>
        <p className={styles.sub}>{namespaces?.total ?? 0} namespaces total</p>
        <SegmentedEfficiencyBar label="CPU" band={namespaces?.bands.cpu} />
        <SegmentedEfficiencyBar label="Memory" band={namespaces?.bands.mem} />
        <SegmentedEfficiencyBar label="Storage" noData={!namespaces?.bands.storage} band={namespaces?.bands.storage} />
      </article>
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css`
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: ${theme.spacing(2)};
    margin-bottom: ${theme.spacing(3)};
    @media (max-width: 1100px) {
      grid-template-columns: 1fr;
    }
  `,
  card: css`
    background: ${theme.colors.background.secondary};
    border: 1px solid ${theme.colors.border.weak};
    border-radius: ${theme.shape.radius.default};
    padding: ${theme.spacing(2)};
  `,
  cardHead: css`
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: ${theme.spacing(1)};
  `,
  title: css`
    margin: 0;
    font-size: ${typographyFontSize(theme, 'base')};
    font-weight: ${theme.typography.fontWeightMedium};
  `,
  meta: css`
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
  sub: css`
    margin: ${theme.spacing(0.5)} 0 ${theme.spacing(1.5)};
    color: ${theme.colors.text.secondary};
    font-size: ${typographyFontSize(theme, 'sm')};
  `,
});
