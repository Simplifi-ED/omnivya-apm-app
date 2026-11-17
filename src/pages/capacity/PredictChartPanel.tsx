import React, { useEffect, useMemo } from 'react';
import { EmbeddedScene, PanelBuilders, SceneDataNode, SceneFlexItem, SceneFlexLayout } from '@grafana/scenes';
import {
  createDataFrame,
  dateTime,
  FieldType,
  LoadingState,
  type DataFrame,
  type GrafanaTheme2,
  type PanelData,
  type TimeRange,
} from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { css } from '@emotion/css';
import { type CapacityHistorySeries } from '../../api/capacity';

type Props = {
  title: string;
  series: CapacityHistorySeries[];
  unit: string;
  fromMs: number;
  toMs: number;
};

function toTimeRange(fromMs: number, toMs: number): TimeRange {
  const from = dateTime(fromMs);
  const to = dateTime(toMs);
  return { from, to, raw: { from, to } };
}

function seriesToFrames(series: CapacityHistorySeries[]): DataFrame[] {
  return series
    .filter((s) => s.points.length > 0)
    .map((s) =>
      createDataFrame({
        name: s.name,
        fields: [
          {
            name: 'Time',
            type: FieldType.time,
            values: s.points.map((p) => p.timestampMs),
          },
          {
            name: s.name,
            type: FieldType.number,
            values: s.points.map((p) => p.value),
          },
        ],
      })
    );
}

function buildPanelData(series: CapacityHistorySeries[], fromMs: number, toMs: number): PanelData {
  const allPoints = series.flatMap((s) => s.points.map((p) => p.timestampMs));
  const maxTs = allPoints.length ? Math.max(...allPoints, toMs) : toMs;
  const minTs = allPoints.length ? Math.min(...allPoints, fromMs) : fromMs;
  return {
    state: LoadingState.Done,
    series: seriesToFrames(series),
    timeRange: toTimeRange(minTs, maxTs),
  };
}

/** Native Grafana TimeSeries panel inside an EmbeddedScene for Predict drawer charts. */
export function PredictChartPanel({ title, series, unit, fromMs, toMs }: Props) {
  const styles = useStyles2(getStyles);

  const { scene, dataNode } = useMemo(() => {
    const dataNode = new SceneDataNode({
      data: buildPanelData(series, fromMs, toMs),
    });
    const scene = new EmbeddedScene({
      $data: dataNode,
      body: new SceneFlexLayout({
        direction: 'column',
        children: [
          new SceneFlexItem({
            minHeight: 260,
            body: PanelBuilders.timeseries().setTitle(title).setUnit(unit).build(),
          }),
        ],
      }),
    });
    return { scene, dataNode };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- scene created once; data pushed below
  }, [title, unit]);

  useEffect(() => {
    dataNode.setState({ data: buildPanelData(series, fromMs, toMs) });
  }, [dataNode, series, fromMs, toMs]);

  return (
    <div className={styles.root}>
      <scene.Component model={scene} />
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css`
    width: 100%;
    min-height: 260px;
    border-radius: ${theme.shape.radius.default};
  `,
});
