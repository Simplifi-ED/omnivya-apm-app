#!/usr/bin/env node
/**
 * Throwaway fixture: compare Capacity aggregation strategies.
 * Not production code. Run: node docs/plans/_scratch-capacity-proto.mjs
 */

const namespaces = ['alpha', 'beta', 'gamma', 'delta', 'epsilon'];
const servicesPerNs = 40;
const sidecarRate = 0.15; // containers whose name != service

function synth() {
  const rows = [];
  for (const ns of namespaces) {
    for (let i = 0; i < servicesPerNs; i++) {
      const service = `svc-${ns}-${i}`;
      const containers = [{ name: service, isMain: true }];
      if (Math.random() < sidecarRate) {
        containers.push({ name: `${service}-sidecar`, isMain: false });
      }
      for (const c of containers) {
        const cpuRequest = Math.random() < 0.12 ? 0 : 0.05 + Math.random() * 0.95;
        const memRequest = Math.random() < 0.1 ? 0 : (64 + Math.random() * 960) * 1024 * 1024;
        const cpuUsage = cpuRequest === 0 ? Math.random() * 0.2 : cpuRequest * (0.05 + Math.random() * 0.7);
        const memUsage = memRequest === 0 ? Math.random() * 128 * 1024 * 1024 : memRequest * (0.1 + Math.random() * 0.6);
        rows.push({
          ns,
          service,
          container: c.name,
          joinOk: c.name === service,
          cpuRequest,
          memRequest,
          cpuUsage,
          memUsage,
          cpuLimit: cpuRequest === 0 ? 0 : cpuRequest * 2,
          memLimit: memRequest === 0 ? 0 : memRequest * 1.5,
        });
      }
    }
  }
  return rows;
}

function waste(row) {
  return {
    wasteCpu: Math.max(0, row.cpuRequest - row.cpuUsage),
    wasteMem: Math.max(0, row.memRequest - row.memUsage),
    missingReq: row.cpuRequest === 0 || row.memRequest === 0,
    missingLim: row.cpuLimit === 0 || row.memLimit === 0,
  };
}

function strategyA_fleetTopk(rows, k = 50) {
  const t0 = performance.now();
  const scored = rows.map((r) => ({ ...r, ...waste(r) }));
  scored.sort((a, b) => b.wasteCpu - a.wasteCpu);
  const top = scored.slice(0, k);
  const gaps = {
    missingReq: scored.filter((r) => r.missingReq).length,
    missingLim: scored.filter((r) => r.missingLim).length,
  };
  const ms = performance.now() - t0;
  return { name: 'A_fleet_topk', series: rows.length, top: top.length, gaps, ms, joinMiss: scored.filter((r) => !r.joinOk).length };
}

function strategyB_perServiceFanout(rows) {
  const t0 = performance.now();
  // Simulates ContainerFilter: only rows where container===service
  const filtered = rows.filter((r) => r.joinOk);
  const byService = new Map();
  for (const r of filtered) {
    const key = `${r.ns}/${r.service}`;
    const w = waste(r);
    const prev = byService.get(key) || { wasteCpu: 0, wasteMem: 0, missingReq: false, missingLim: false };
    byService.set(key, {
      wasteCpu: prev.wasteCpu + w.wasteCpu,
      wasteMem: prev.wasteMem + w.wasteMem,
      missingReq: prev.missingReq || w.missingReq,
      missingLim: prev.missingLim || w.missingLim,
    });
  }
  const list = [...byService.entries()].map(([id, v]) => ({ id, ...v }));
  list.sort((a, b) => b.wasteCpu - a.wasteCpu);
  const ms = performance.now() - t0;
  return {
    name: 'B_per_service_fanout',
    series: filtered.length,
    services: list.length,
    droppedSidecars: rows.length - filtered.length,
    topWaste: list.slice(0, 10).map((x) => x.id),
    gaps: {
      missingReq: list.filter((x) => x.missingReq).length,
      missingLim: list.filter((x) => x.missingLim).length,
    },
    ms,
    roundTripsEstimate: list.length, // one runtime-like call per service
  };
}

function strategyC_namespaceFirst(rows) {
  const t0 = performance.now();
  const byNs = new Map();
  for (const r of rows) {
    const w = waste(r);
    const prev = byNs.get(r.ns) || { wasteCpu: 0, wasteMem: 0, containers: 0, missingReq: 0, missingLim: 0 };
    byNs.set(r.ns, {
      wasteCpu: prev.wasteCpu + w.wasteCpu,
      wasteMem: prev.wasteMem + w.wasteMem,
      containers: prev.containers + 1,
      missingReq: prev.missingReq + (w.missingReq ? 1 : 0),
      missingLim: prev.missingLim + (w.missingLim ? 1 : 0),
    });
  }
  const rollup = [...byNs.entries()].map(([ns, v]) => ({ ns, ...v }));
  rollup.sort((a, b) => b.wasteCpu - a.wasteCpu);
  // Drill one namespace (worst) to container topk
  const worst = rollup[0].ns;
  const drilled = rows
    .filter((r) => r.ns === worst)
    .map((r) => ({ ...r, ...waste(r) }))
    .sort((a, b) => b.wasteCpu - a.wasteCpu)
    .slice(0, 50);
  const ms = performance.now() - t0;
  return {
    name: 'C_namespace_first',
    namespaces: rollup.length,
    worstNs: worst,
    drillTop: drilled.length,
    rollup,
    ms,
    roundTripsEstimate: 1 + 1, // fleet ns rollup + one drill
  };
}

const rows = synth();
const results = [strategyA_fleetTopk(rows), strategyB_perServiceFanout(rows), strategyC_namespaceFirst(rows)];
console.log(JSON.stringify({ fixtureContainers: rows.length, results }, null, 2));
