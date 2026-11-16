import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Capacity from './Capacity';

jest.mock('@grafana/runtime', () => ({
  PluginPage: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  getBackendSrv: () => ({ fetch: jest.fn() }),
  locationService: { getLocation: () => ({ search: '' }) },
}));

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
  it('shows enablement empty state when metrics are unavailable', async () => {
    render(
      <MemoryRouter initialEntries={['/a/omnivya-apm-app/capacity']}>
        <Capacity />
      </MemoryRouter>
    );
    expect(screen.getByRole('heading', { name: 'Capacity' })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('Capacity metrics unavailable')).toBeInTheDocument();
    });
    expect(screen.getByText('Need kube metrics')).toBeInTheDocument();
  });
});
