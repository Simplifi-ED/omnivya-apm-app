import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Capacity from './Capacity';

jest.mock('@grafana/runtime', () => ({
  PluginPage: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  getBackendSrv: () => ({ fetch: jest.fn() }),
  locationService: { getLocation: () => ({ search: '' }) },
}));

jest.mock('@grafana/scenes', () => {
  const panel = () => ({ setTitle: () => panel(), setUnit: () => panel(), build: () => ({}) });
  return {
    EmbeddedScene: jest.fn().mockImplementation((cfg: unknown) => ({ ...(cfg as object), Component: () => null })),
    SceneDataNode: jest.fn().mockImplementation(() => ({ setState: jest.fn() })),
    SceneFlexLayout: jest.fn(),
    SceneFlexItem: jest.fn(),
    PanelBuilders: { timeseries: panel },
  };
});

jest.mock('../components/HeaderTimeControls', () => ({
  HeaderTimeControls: () => <div data-testid="time-controls" />,
}));

jest.mock('../utils/timeRange', () => ({
  useTimeRange: () => ({ from: 'now-1h', to: 'now', fromMs: 1, toMs: 2, refresh: jest.fn() }),
}));

jest.mock('../api/capacity', () => ({
  getCapacity: jest.fn().mockResolvedValue({
    available: false,
    enablementNote: 'Need kube metrics',
    efficiency: {
      cpuUsedRatio: 0,
      memUsedRatio: 0,
      reclaimableMonthlyUsd: 0,
      deficitMonthlyUsd: 0,
      cpuHourUsd: 0.0316,
      memoryGibHourUsd: 0.0042,
      ratesAreEstimates: true,
    },
    gapCounts: {
      missingRequest: 0,
      missingLimit: 0,
      overRequestedCpu: 0,
      overRequestedMem: 0,
      underProvisioned: 0,
      throttled: 0,
      reservedWithoutUsage: 0,
    },
    namespaces: [],
    nodes: [],
    topWasteCpu: [],
    topWasteMem: [],
    topRiskContainers: [],
  }),
  getCapacityRecommendations: jest.fn().mockResolvedValue({ available: false, recommendations: [] }),
  getCapacityHistory: jest.fn().mockResolvedValue({ available: false, series: [] }),
  getCapacityForecast: jest.fn().mockResolvedValue({
    available: false,
    method: 'linear',
    methodLabel: 'linear projection',
    resource: 'cpu',
    series: [],
  }),
}));

describe('Capacity page', () => {
  it('shows Efficiency IA tabs and enablement empty state', async () => {
    render(
      <MemoryRouter initialEntries={['/a/omnivya-apm-app/capacity']}>
        <Capacity />
      </MemoryRouter>
    );
    expect(screen.getByTestId('time-controls')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Namespaces' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Nodes' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Right-sizing' })).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'History' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Forecast' })).not.toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('Capacity metrics unavailable')).toBeInTheDocument();
    });
    expect(screen.getByText('Need kube metrics')).toBeInTheDocument();
  });
});
