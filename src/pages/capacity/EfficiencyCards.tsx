import React from 'react';
import { Box, Stack, Text, useStyles2 } from '@grafana/ui';
import { GrafanaTheme2 } from '@grafana/data';
import { css } from '@emotion/css';
import { CapacityPopulation } from '../../api/capacity';
import { SegmentedEfficiencyBar } from './SegmentedEfficiencyBar';

type Props = {
  population?: CapacityPopulation;
  timeLabel?: string;
};

export function EfficiencyCards({ population, timeLabel = 'Selected range' }: Props) {
  const styles = useStyles2(getStyles);
  const clusters = population?.clusters;
  const nodes = population?.nodes;
  const namespaces = population?.namespaces;

  const cards = [
    {
      title: 'Cluster efficiency',
      countLabel: `${clusters?.total ?? 0} clusters total`,
      bands: clusters?.bands,
    },
    {
      title: 'Node efficiency',
      countLabel: `${nodes?.total ?? 0} nodes total`,
      bands: nodes?.bands,
    },
    {
      title: 'Namespace efficiency',
      countLabel: `${namespaces?.total ?? 0} namespaces total`,
      bands: namespaces?.bands,
    },
  ];

  return (
    <Stack direction="column" gap={1.5}>
      <div className={styles.grid} role="group" aria-label="Efficiency overview">
        {cards.map((c) => (
          <Box
            key={c.title}
            padding={2}
            backgroundColor="secondary"
            borderColor="weak"
            borderStyle="solid"
            borderRadius="default"
          >
            <Stack direction="column" gap={1.5}>
              <Stack direction="row" justifyContent="space-between" alignItems="baseline">
                <Text element="h3" variant="h5">
                  {c.title}
                </Text>
                <Text variant="bodySmall" color="secondary">
                  {timeLabel}
                </Text>
              </Stack>
              <Text variant="bodySmall" color="secondary">
                {c.countLabel}
              </Text>
              <Stack direction="column" gap={1}>
                <SegmentedEfficiencyBar label="CPU" band={c.bands?.cpu} />
                <SegmentedEfficiencyBar label="Memory" band={c.bands?.mem} />
                <SegmentedEfficiencyBar label="Storage" noData={!c.bands?.storage} band={c.bands?.storage} />
              </Stack>
            </Stack>
          </Box>
        ))}
      </div>
      <Stack direction="row" gap={2} alignItems="center" wrap="wrap">
        <Stack direction="row" gap={0.5} alignItems="center">
          <span className={`${styles.dot} ${styles.dotUnder}`} />
          <Text variant="bodySmall" color="secondary">
            Under (&lt;40%)
          </Text>
        </Stack>
        <Stack direction="row" gap={0.5} alignItems="center">
          <span className={`${styles.dot} ${styles.dotOk}`} />
          <Text variant="bodySmall" color="secondary">
            Optimal (40–80%)
          </Text>
        </Stack>
        <Stack direction="row" gap={0.5} alignItems="center">
          <span className={`${styles.dot} ${styles.dotOver}`} />
          <Text variant="bodySmall" color="secondary">
            Over (&gt;80%)
          </Text>
        </Stack>
      </Stack>
    </Stack>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css`
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: ${theme.spacing(2)};
    @media (max-width: 1100px) {
      grid-template-columns: 1fr;
    }
  `,
  dot: css`
    width: 10px;
    height: 10px;
    border-radius: ${theme.shape.radius.pill};
    display: inline-block;
  `,
  dotUnder: css`
    background: ${theme.colors.success.main};
  `,
  dotOk: css`
    background: ${theme.colors.warning.main};
  `,
  dotOver: css`
    background: ${theme.colors.error.main};
  `,
});
