# Capacity efficiency plan

Ship past, present, and future Kubernetes resource efficiency inside this cut-off APM fork for team leads and platform engineers who already use Application Observability. The program treats Capacity as a first-class product area beside Services and Jobs, not a My Apps filter and not a full Kubernetes Monitoring clone. Rule. Answer "where is paid compute unused or unsafe?" with ranked waste and config gaps before forecasts. PR order. CAP-1, CAP-2, CAP-3, CAP-4, CAP-5.

## How to read this

One box is one unit of work. Every box names the evidence that checks it. A nested box is a sub-step of the box above it. Check a box only when its evidence exists, a file, a log line, a screenshot, a test run, or a SHA. The body is a how-to. The appendices explain and record.

The program runs `pstack/skills/poteto-mode/playbooks/autopilot-stack.md`. The operator lands every PR. Owners stop at merge-ready. CAP-2, CAP-3, CAP-4, and CAP-5 are review-gated.

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

## Program checklist

### Arm the program

- [ ] State the protocol and this plan to the operator, then stop. Start execution only on the operator's explicit go.
- [ ] Read these from trunk at program start. Re-read them at every tick.
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/autopilot-stack.md`
  - [ ] `git show origin/main:pstack/skills/swarm/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/control-ui/SKILL.md` (fallback path if absent on trunk. `/Users/zine/.cursor/plugins/cache/cursor-public/cursor-team-kit/bf0c7cd80abdbf7fe47b634978a7d1b4071f71a7/skills/control-ui/SKILL.md`)
  - [ ] `git show origin/main:pstack/skills/poteto-mode/playbooks/opening-a-pr.md`
  - [ ] `git show origin/main:pstack/skills/poteto-mode/references/bugbot-triage.md`
  - [ ] `git show origin/main:pstack/skills/technical-writing/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/show-me-your-work/SKILL.md`
  - [ ] `git show origin/main:pstack/skills/interrogate/SKILL.md`
- [ ] On the operator's go, arm the audit tick as `/loop 1h` with the tick prompt below. Never leave the cadence to memory.
- [ ] Use this tick prompt, verbatim. "Re-read the execution playbook from trunk. Audit the operation against it and fix drift in this tick. Probe every active lane and judge progress by side effects only. Stand down a stuck lane and dispatch its replacement now. Then post a short status message to the operator in chat only when the audit found a tracked change that no earlier status message reported, such as a PR opened, a code-ready head, a round launched or closed, a verdict, a merge, a stuck agent and the action taken, a blocker added or cleared, or a decision only the operator can make. Name every such change and nothing else. Do not repeat a table, the merged list, or an unchanged blocker. If the audit found none, end the turn with no reply text. Either way, log this tick's row in your decision trail. The row names the items reported, or none."
- [ ] On the operator's hold or stand-down, send every owner a zero-writes order at once.

### Spawn owners

- [ ] Spawn one owner per PR with the full lifecycle the execution playbook names.
- [ ] Follow this dependency graph. Start dependent work only after its parent merges, or base it on the parent branch when the execution playbook stacks.
  - [ ] CAP-1 branches from `main` first.
  - [ ] CAP-2 after CAP-1.
  - [ ] CAP-3 after CAP-2.
  - [ ] CAP-4 after CAP-3.
  - [ ] CAP-5 after CAP-4.
- [ ] Hold the file boundaries. CAP-1 touches only `pkg/plugin/**`, `docs/api/**`, and matching `*_test.go`. CAP-2 touches only `src/**`, `src/plugin.json`, and Playwright under `tests/pages/`. CAP-3 may touch namespace and Runtime UI plus thin API clients. CAP-4 and CAP-5 may extend the same Capacity modules only.
- [ ] Hold the review gate. CAP-2, CAP-3, CAP-4, and CAP-5 change an interaction. They wait for the operator's review in chat with screenshots and a video before merge.

### PR mechanics, for every PR

- [ ] Resolve the forge once. Default to `gh`; if `command -v origin` succeeds and Origin can resolve the repository, use `origin pr` for every PR operation. Record any fallback to `gh`. Never require `gt`.
- [ ] Open the PR ready, never draft, per **Opening a PR**. Use the run's built-in PR tool when it has one, else `origin pr create --status open --base <base-branch>` or `gh pr create --base <base-branch>` according to the resolved forge. A stack child targets its parent branch.
- [ ] Run the repo's lint and typecheck once before the PR-facing push. Push with hooks on.
- [ ] Run `/deslop` before each commit and `/no-comments` before review.
- [ ] Triage every Bugbot and security-reviewer comment per `../references/bugbot-triage.md`.
- [ ] Rebase onto current trunk before the code-ready report and babysit. Keep that merge base in fix rounds. Rebase again only at merge prep, on a `git merge-tree` conflict with trunk, on a CI failure that comes from a change on trunk, or when a known concurrent change to the same files lands.

### Verdict and merge, for every PR

- [ ] A full swarm runs once, at the code-ready head. Another full swarm runs only for new behavior, a redesign, or a large new diff. A rebase, a merge-conflict resolution, or a minor fix gets a single verifier at the new head, and a wording-only change gets none. Run each full swarm per `pstack/skills/swarm/SKILL.md`. One gates lane. The ten live lanes from the PR's **Verify, live** block. The perf lane from its **Verify, perf** block. Two or more audit lanes, each with its own focus, that read the diff and the receipts and distrust the PR body. The root audits the receipts in the merge-ready report before the verdict.
- [ ] Judge every finding by the execution playbook's blocking rule. The verdict is clean when none blocks. A failure on trunk behavior in a lane marked `Regression lane against trunk.` is recorded against trunk and does not block the PR. A new head gets a fresh verdict, except for results that stay valid under the patch-id rule in `playbooks/shipping.md`.
- [ ] Root marks merge-ready. The operator squash-merges bottom-up through the stack. Owners never force-push and never merge without the operator when this plan runs `autopilot-stack`.

### Boot recipe, for every live lane

Each live lane runs on its own cloud VM at the PR head. Drive through `control-ui` from `cursor-team-kit`. Prefer repo Playwright when a scenario already exists.

- [ ] `git fetch origin <head-branch> && git checkout <head SHA>`.
- [ ] `mise run deploy:up` when containers are down, then `mise run build` so `dist/` includes the Linux backend. Wait until Grafana answers on the provisioned URL and `GET /api/plugins/omnivya-apm-app/health` is healthy.
- [ ] Deliver input only through Playwright or the control-ui CDP commands. Read-only diagnostics are Grafana network logs and `mise run deploy:logs`.
- [ ] Save every screenshot to `/tmp/swarm-<pr-id>/worker-<n>/<slug>.png` and return the paths with the report.

## Build the Capacity domain and present API (CAP-1)

**Depends on.** None.

**Files.**

- [ ] Create `pkg/plugin/capacity.go`.
- [ ] Create `pkg/plugin/capacity_test.go`.
- [ ] Create `pkg/plugin/chquery/capacity.go` for the ClickHouse Path B builders (Jobs dual-path precedent).
- [ ] Edit `pkg/plugin/app.go` to register routes in `App.routes()` so `apidocs_test.go` sees them.
- [ ] Edit `pkg/plugin/queries/models.go` for Capacity types if shared.
- [ ] Edit `pkg/plugin/otelconfig/otelconfig.go` to add `container_memory_working_set_bytes` (or equivalent) beside existing Container metrics. Do not reuse `container_memory_usage_bytes` alone for efficiency waste.
- [ ] Create `docs/api/capacity.md`.
- [ ] Edit `docs/api/README.md` and `docs/api/platform.md` to list the endpoint.
- [ ] Edit `docs/api/openapi.yaml` for every new path (required by `apidocs_test.go`).

**Build.**

- [ ] Add typed domain `CapacityResource`, `CapacityGap`, `CapacityContainer`, `CapacityNamespaceRollup`, `CapacityEfficiencyScore`, `WorkloadRecommendation`, and `CapacityOverview` in `pkg/plugin/capacity.go` (or models) so illegal mixes of units cannot compile.
- [ ] Add `GET /capacity` that returns `{ available, enablementNote?, efficiency, environments?, namespaces[], gapCounts, topWasteCpu[], topWasteMem[], topRiskContainers[] }` at instant `to`, filtered by optional `environment` and `namespace`.
- [ ] Compute CAST AI efficiency ratios (`used / requested`, plus `requested / allocatable` when `kube_node_status_allocatable` exists) and rate-based monthly waste/savings estimates (`$0.0316/vCPU-hr`, `$0.0042/GiB-hr` defaults, overridable in plugin JSON config).
- [ ] Surface under-provisioned (`usage > request`) and CFS-throttled (`container_cpu_cfs_throttled_seconds_total`) containers alongside over-requested waste.
- [ ] Dual-path like Jobs. Path A is Prometheus or Mimir via `a.prom(ctx)`. Path B is ClickHouse via `chquery` when `clickStackMode` is on. Return `available:false` when neither path has series.
- [ ] Compute waste with PromQL `clamp_min(request - usage, 0)` at identity `(namespace, pod, container)`, then `sum by (namespace)` and `topk` for lists (strategy A from Appendix A). Never clamp at coarser grain.
- [ ] Detect gaps with set difference (`usage unless on(identity) requests`), one query per resource (CPU and memory separately). Do not use arithmetic count subtraction. Fan-out that stores missing series as `0.0` is forbidden for gap counts.
- [ ] Aggregate with `sum by (namespace, pod, container)`, never fleet-wide `avg` like today's service Runtime card. Keep `container!=""` (and exclude POD sandbox series) so cgroups do not enter the join.
- [ ] Default for request-only containers (reservation with no usage series). Surface them as a separate gap category `reservedWithoutUsage`, not as waste. Operator may later fold them into waste.
- [ ] Join on kube labels `namespace`, `pod`, `container`, and optional `k8s_cluster_name`. Do not use `container == serviceName` as the fleet join key. Treat APM `service_namespace` and kube `namespace` as distinct until an explicit map exists.
- [ ] Keep CPU in cores on both usage and request sides. Add a unit test that fails on millicore or cores mixups.
- [ ] Cap fan-out with the same class of guards as namespace scorecard or attention (`maxNamespace*` style limits, concurrency bounded). Do not unbounded-loop every service Runtime call.
- [ ] Use a dedicated response cache (scorecard-style longer TTL) keyed by org, rounded `to`, environment, and namespace. Avoid thrashing the default 30s 200-entry cache under wallboard polling.
- [ ] Mirror Jobs enablement. When cAdvisor or kube-state-metrics series are absent, return `available:false` with a concrete note and no fake zeros. Non-nil slices only.

**You see.**

- [ ] `curl` against the plugin resource returns JSON with `available` true on a stack that has kube metrics, or `available` false with `enablementNote` when they are missing. Gap counts and namespace rollups are present when available.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `pkg/plugin/capacity_test.go` covers waste math, missing-request detection when request series is absent, namespace rollup ordering, and `available:false` when the client returns empty. Run `mise run backend:test`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `deepseek-v41-flash-1017-max` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Hit `GET /capacity` at trunk and head. Trunk lacks the route. Record that. On head, gate a 200 JSON body with `available` set. Save `cap1-regression.png`. Pass when head returns JSON and trunk returns 404 or non-Capacity payload.
- [ ] Lane 2. With kube metrics present, overview lists at least one namespace. Save `cap1-available.png`. Pass when `available` is true and `namespaces` length is greater than 0.
- [ ] Lane 3. Soft-unavailable path (misconfigured metrics UID or empty series). Save `cap1-unavailable.png`. Pass when `available` is false and `enablementNote` is non-empty.
- [ ] Lane 4. Environment filter returns only matching cluster label rows. Save `cap1-env-filter.png`. Pass when every row's environment matches the filter.
- [ ] Lane 5. Namespace filter scopes rollup and top lists. Save `cap1-ns-filter.png`. Pass when every container row namespace matches.
- [ ] Lane 6. Gap count for missing requests matches a hand-checked PromQL sample. Save `cap1-gap-missing-req.png`. Pass when counts agree within rounding.
- [ ] Lane 7. Top waste CPU order is descending. Save `cap1-topk-cpu.png`. Pass when each wasteCpu is greater than or equal to the next.
- [ ] Lane 8. Sidecar containers appear when their container name differs from the workload name. Save `cap1-sidecar.png`. Pass when a known sidecar fixture appears.
- [ ] Lane 9. Concurrent identical requests share cache behavior (second latency lower or identical payload). Save `cap1-cache.png`. Pass when bodies match.
- [ ] Lane 10. Invalid query params do not 500. Save `cap1-bad-params.png`. Pass when status is 4xx or a documented empty overview.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. End-to-end `GET /capacity` wall time at head. Trunk lacks the route, so also record PromQL round-trip count inside the handler logs for the diff-added work.
- [ ] Probe. `curl -w '%{time_total}\n' -o /dev/null -s` against head five times interleaved with a no-op trunk health check. Count backend PromQL calls from logs.
- [ ] Baseline. Record trunk plugin health latency first.
- [ ] Rule. Head p95 for `GET /capacity` on a mid-size fixture (about 5 namespaces, about 200 containers) stays under 3000ms. PromQL round trips stay at or below 10 for the overview (prototype A used 8 flat).

**Review gate.** None. CAP-1 is not review-gated.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] Operator squash-merges CAP-1 into `main`.

## Ship the Capacity page (CAP-2)

**Depends on.** CAP-1.

**Files.**

- [ ] Edit `src/plugin.json` nav includes.
- [ ] Edit `src/constants.ts` ROUTES.
- [ ] Edit `src/components/App/App.tsx` routes.
- [ ] Create `src/pages/Capacity.tsx` (JobsInventory patterns for `available`, filters, `DataState`).
- [ ] Create `src/api/capacity.ts` as a module-local fetch helper like `src/api/jobs.ts`, not a drive-by widen of `client.ts`.
- [ ] Create `src/pages/Capacity.test.tsx`.
- [ ] Create `tests/pages/capacity.spec.ts`.
- [ ] Edit `docs/url-contract.md` for the new path and params.
- [ ] Optional. `src/module.tsx` command-palette link. Not required for nav.

**Build.**

- [ ] Add top-level nav item **Capacity** at `/a/omnivya-apm-app/capacity`, peer to Jobs, not under My Apps.
- [ ] Page shows CAST AI efficiency summary bar (used vs requested vs allocatable + estimated monthly reclaimable spend with rate disclosure), gap/risk count tiles (including under-provisioned and throttled), waste-by-namespace table, and top waste/risk container lists from `GET /capacity`.
- [ ] Empty and `available:false` states reuse Jobs-style enablement copy patterns.
- [ ] Time range and environment filters use shared `useTimeRange` and URL params per `docs/url-contract.md`.

**You see.**

- [ ] Grafana nav shows Capacity. Opening it renders gap tiles and tables when the API is available, or the enablement empty state when not.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] `src/pages/Capacity.test.tsx` covers available, unavailable, and loading. Run `mise run frontend:test -- Capacity`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `deepseek-v41-flash-1017-max` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Open Services on trunk and head. Save `cap2-regression-services.png`. Pass when Services still loads on head.
- [ ] Lane 2. Nav shows Capacity. Save `cap2-nav.png`. Pass when the Capacity nav link is visible.
- [ ] Lane 3. Capacity page loads with tiles. Save `cap2-overview.png`. Pass when gap tiles render.
- [ ] Lane 4. Unavailable empty state. Save `cap2-empty.png`. Pass when enablement text is visible and tables are absent.
- [ ] Lane 5. Environment filter updates URL and refetch. Save `cap2-env.png`. Pass when `environment` appears in the URL.
- [ ] Lane 6. Time range change refetches. Save `cap2-time.png`. Pass when network shows a new `/capacity` call.
- [ ] Lane 7. Sort or default order shows highest waste namespace first. Save `cap2-sort.png`. Pass when the first row waste is maximal.
- [ ] Lane 8. Keyboard focus order reaches filters then table. Save `cap2-a11y.png`. Pass when tab order is filters then rows.
- [ ] Lane 9. My Apps still only lists favorites. Save `cap2-myapps.png`. Pass when Capacity is not nested under My Apps.
- [ ] Lane 10. Deep link `/a/omnivya-apm-app/capacity` works on reload. Save `cap2-deeplink.png`. Pass when the page renders after reload.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Time to first Capacity content paint after nav click. Trunk lacks Capacity, so also budget the `/capacity` network call.
- [ ] Probe. Playwright timing from click to tile text, five runs at trunk (Services click as control) and head (Capacity click), interleaved.
- [ ] Baseline. Record trunk Services click-to-table timing first.
- [ ] Rule. Head Capacity click-to-tiles under 4000ms on the local stack. `/capacity` network under 3000ms.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 2 and lane 3 screenshots into `docs/plans/media/CAP-2-review-nav.png` and `docs/plans/media/CAP-2-review-overview.png`.
- [ ] Record a 30 to 60 second video of the change on a lane VM. Save it as `docs/plans/media/CAP-2-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] Operator squash-merges CAP-2 into `main`.

## Wire namespace and service drill-down (CAP-3)

**Depends on.** CAP-2.

**Files.**

- [ ] Edit `src/pages/NamespaceOverview.tsx` and or `src/pages/namespace/*`.
- [ ] Edit `src/pages/tabs/RuntimeTab.tsx` for Capcity deep links only as needed.
- [ ] Edit `src/pages/Capacity.tsx` for row navigation.
- [ ] Extend `pkg/plugin/capacity.go` with `GET /namespaces/{namespace}/capacity` and `GET /capacity/recommendations` (Goldilocks workload right-sizing).
- [ ] Add tests beside each touched file.

**Build.**

- [ ] Resolve Kubernetes workloads (`kube_pod_owner` to Deployment/StatefulSet/DaemonSet with pod-prefix fallback) and compute Burstable and Guaranteed CPU/memory recommendations from historical p95/max usage plus safety headroom, returning current vs recommended deltas and a copyable Kubernetes `resources` YAML patch.
- [ ] Add a Workload Right-Sizing view on Capacity displaying Goldilocks recommendations and YAML copy action.
- [ ] Namespace row on Capacity navigates to Namespace Overview with Capacity context preserved in URL.
- [ ] Namespace Overview gains a Capacity block (gap strip + top waste services) that answers the team question without copying the full fleet page.
- [ ] Service links open Backend Runtime so usage versus request remains the service diagnosis home.

**You see.**

- [ ] From Capacity, a namespace click lands on Namespace Overview showing the Capacity block for that namespace. A service click lands on Backend with Runtime visible or linked.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Namespace and Capacity navigation tests. Run `mise run frontend:test` for the touched suites and `mise run backend:test` if a namespace endpoint was added.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `deepseek-v41-flash-1017-max` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Namespace Overview still shows health stats. Save `cap3-regression-ns.png`. Pass when NamespaceStats still render.
- [ ] Lane 2. Capacity → namespace drill. Save `cap3-drill-ns.png`. Pass when URL contains the namespace and Capacity block is visible.
- [ ] Lane 3. Capacity → service drill into Backend. Save `cap3-drill-svc.png`. Pass when Backend tab content is visible.
- [ ] Lane 4. Namespace Capacity block hidden when `available:false`. Save `cap3-ns-empty.png`. Pass when the block collapses or shows enablement only.
- [ ] Lane 5. Environment preserved across drill. Save `cap3-env-preserve.png`. Pass when `environment` survives navigation.
- [ ] Lane 6. Time range preserved across drill. Save `cap3-time-preserve.png`. Pass when from/to survive.
- [ ] Lane 7. Direct namespace Capacity deep link. Save `cap3-ns-deeplink.png`. Pass when reload keeps the block.
- [ ] Lane 8. Runtime Container card still renders for a known service. Save `cap3-runtime.png`. Pass when Container Resources header exists.
- [ ] Lane 9. Back navigation returns to Capacity without losing filters. Save `cap3-back.png`. Pass when filters match prior state.
- [ ] Lane 10. No duplicate full fleet tables on Namespace Overview. Save `cap3-no-dupe.png`. Pass when only the namespace-scoped block is present.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Namespace Overview load time with Capacity block versus trunk without it.
- [ ] Probe. Playwright load timing interleaved trunk and head, five runs.
- [ ] Baseline. Record trunk Namespace Overview load first.
- [ ] Rule. Head stays within 20% of trunk load time, or under 5000ms absolute if trunk is faster than 1000ms and variance dominates.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 2 and lane 3 screenshots into `docs/plans/media/CAP-3-review-drill-ns.png` and `docs/plans/media/CAP-3-review-drill-svc.png`.
- [ ] Record a 30 to 60 second video of the change on a lane VM. Save it as `docs/plans/media/CAP-3-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] Operator squash-merges CAP-3 into `main`.

## Add historical utilization (CAP-4)

**Depends on.** CAP-3.

**Files.**

- [ ] Extend `pkg/plugin/capacity.go` with range series endpoints or range fields.
- [ ] Extend Capacity and Namespace Capacity UI with usage-versus-request charts.
- [ ] Add unit tests and Playwright coverage.

**Build.**

- [ ] For a selected namespace or container, return CPU and memory usage (avg + p95/max spikes), request, limit, and CPU throttling/restarts over the selected time range via `RangeQuery`.
- [ ] Capacity and Namespace UIs chart history with the shared time picker. Deleted-object inventory is out of scope. Document that only series still queryable in the metrics backend appear.

**You see.**

- [ ] Changing the time range redraws usage versus request charts. A spike six hours ago is visible when metrics retention includes it.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Range aggregation tests in `capacity_test.go` and chart rendering tests. Run `mise run backend:test` and `mise run frontend:test`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `deepseek-v41-flash-1017-max` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Capacity overview tiles still render. Save `cap4-regression.png`. Pass when tiles remain.
- [ ] Lane 2. History chart appears for a namespace drill. Save `cap4-chart.png`. Pass when a chart series is visible.
- [ ] Lane 3. Time range last 6h shows more points than last 15m. Save `cap4-range-dense.png`. Pass when point count increases.
- [ ] Lane 4. Request and usage both plotted. Save `cap4-req-usage.png`. Pass when legend includes both.
- [ ] Lane 5. Environment filter still scopes history. Save `cap4-hist-env.png`. Pass when series labels match env.
- [ ] Lane 6. Missing request series charts usage alone without crashing. Save `cap4-missing-req-chart.png`. Pass when page has no error boundary.
- [ ] Lane 7. Zoom or time drag updates URL once per action. Save `cap4-url-once.png`. Pass when a single param update occurs per gesture.
- [ ] Lane 8. Large range (7d) returns within budget or shows a documented truncation note. Save `cap4-7d.png`. Pass when either chart renders or the note is shown.
- [ ] Lane 9. Service Runtime still instant-cards without requiring history. Save `cap4-runtime-instant.png`. Pass when Container card loads.
- [ ] Lane 10. No claim of deleted cluster inventory in UI copy. Save `cap4-copy.png`. Pass when copy avoids deleted-object fleet language.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Range query wall time for 6h at 1m step for one namespace.
- [ ] Probe. Curl or Playwright-triggered API timing, five interleaved runs. Trunk baseline is CAP-3 instant overview latency.
- [ ] Baseline. Record CAP-3 instant overview p95 first on trunk.
- [ ] Rule. 6h range for one namespace under 5000ms. Fail if head exceeds that absolute budget.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 2 and lane 3 screenshots into `docs/plans/media/CAP-4-review-chart.png` and `docs/plans/media/CAP-4-review-range.png`.
- [ ] Record a 30 to 60 second video of the change on a lane VM. Save it as `docs/plans/media/CAP-4-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] Operator squash-merges CAP-4 into `main`.

## Add forecasts (CAP-5)

**Depends on.** CAP-4.

**Files.**

- [ ] Extend Capacity API with forecast payloads.
- [ ] Extend Capacity UI with Predict CPU and Predict memory actions on namespace or container scope.
- [ ] Add capability detection for forecast backend.
- [ ] Add tests.

**Build.**

- [ ] Prefer Grafana ML forecast APIs when a configured ML datasource or plugin is present. Otherwise use PromQL `predict_linear` over a documented minimum range (at least 2h) and label the result as a linear projection, not ML.
- [ ] Compute estimated days-to-request and days-to-limit saturation horizon when forecast slope is positive.
- [ ] Return point forecast plus lower and upper bands when the backend can supply them. Linear fallback may omit bands and must say so in the payload.
- [ ] Hide Predict actions when the selected range is too short or Capacity metrics are unavailable.

**You see.**

- [ ] Predict CPU on a namespace draws a forward series after a short wait. Short ranges disable or explain why predict is unavailable.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Forecast adapter tests for ML path and linear fallback. Run `mise run backend:test` and frontend Predict button tests.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on `deepseek-v41-flash-1017-max` at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. History charts from CAP-4 still work. Save `cap5-regression.png`. Pass when charts render.
- [ ] Lane 2. Predict CPU with range at least 2h. Save `cap5-predict-cpu.png`. Pass when a forecast series appears.
- [ ] Lane 3. Predict memory similarly. Save `cap5-predict-mem.png`. Pass when a forecast series appears.
- [ ] Lane 4. Range under 2h disables Predict or shows why. Save `cap5-too-short.png`. Pass when no silent empty forecast.
- [ ] Lane 5. Linear fallback labeling when ML is absent. Save `cap5-linear-label.png`. Pass when UI states linear projection.
- [ ] Lane 6. Bands render when provided. Save `cap5-bands.png`. Pass when band series exist or payload documents omission.
- [ ] Lane 7. Predict does not break gap tiles. Save `cap5-tiles-ok.png`. Pass when tiles still show.
- [ ] Lane 8. Error from forecast backend surfaces an Alert. Save `cap5-error.png`. Pass when Alert text is visible.
- [ ] Lane 9. Concurrent Predict clicks are idempotent (one in-flight). Save `cap5-idempotent.png`. Pass when a single forecast request is active.
- [ ] Lane 10. Capability-gated hide when metrics unavailable. Save `cap5-gated.png`. Pass when Predict controls are absent.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Time from Predict click to forecast series painted.
- [ ] Probe. Five Predict runs interleaved with CAP-4 chart refresh as control.
- [ ] Baseline. Record CAP-4 chart refresh latency on trunk first.
- [ ] Rule. Predict path under 10000ms absolute on local linear fallback. Fail above that.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 2 and lane 5 screenshots into `docs/plans/media/CAP-5-review-predict.png` and `docs/plans/media/CAP-5-review-label.png`.
- [ ] Record a 30 to 60 second video of the change on a lane VM. Save it as `docs/plans/media/CAP-5-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at merge-ready. Wait for the operator's click.

**Merge.**

- [ ] Root's clean verdict at the exact head SHA.
- [ ] Bugbot triage done.
- [ ] Rebased onto current trunk after the verdict, patch-id unchanged.
- [ ] Operator squash-merges CAP-5 into `main`.

## Close the program

- [ ] Every box above is checked with its evidence.
- [ ] Reply to the operator with the report the execution playbook names.

## Appendix A. Prototype evidence

**Aggregation strategy (PromQL fixture).** From [Prototype waste PromQL shapes](dc81b6e2-e5da-4e29-89d2-084f3f661156). Scratch `/tmp/capacity-proto/` (`run.mjs`, `probe.mjs`, `probe2.mjs`, `cost.mjs`, `promqlcheck/`). Fixture shape 24 namespaces, 1,518 containers, 11,242 series. Evaluator ms are not Mimir wall time. Series and sample counts are the portable cost signal.

| Variant | Queries (301 refs) | Waste vs clamped truth | Gaps | Decision |
| --- | --- | --- | --- | --- |
| A single-join + `clamp_min` at `(ns,pod,container)` + `unless` gaps | 8 flat | 0.0% | exact | Chosen for fleet and namespace |
| B per-service fan-out (Runtime shape) | 2,408 | −68.5% with `avg`, −3.0% with `sum` | undetectable (missing → 0.0) | Rejected |
| C namespace-first join-free | 8 (+1 drill) | −37.0% | n/a | Rejected |
| C clamped (A with ns prefilter) | same as A | 0.0% | exact | Same as A. No extra mode |

**Shape rules measured.**

1. Clamp only at `(namespace, pod, container)`. Coarser grains were 6%, 9%, or 36% low.
2. Gaps use `unless` set difference per resource. Arithmetic `count(a)-count(b)` went negative and hid the worst namespace when no container set requests (`probe2.mjs`).
3. Keep `container!=""`. Excluded 857 sandbox series in the fixture.

**Join-key fragility.** Full triple matched 98% intact. One rename (`linkerd-proxy` → `linkerd_proxy`) dropped match to 53.2%. Dropping `container` recovered match but merged 631 multi-container pods and cost 6.2% waste accuracy. Keep `container` in the key.

**Cost model (5m window, 15s scrape).** A scanned about 15k series / 324k samples across 8 queries. B scanned about 43k series / 911k samples across 2,408 queries and scaled linearly with fleet size while A stayed flat.

**Earlier lightweight fixture superseded.** `/tmp/capacity-proto/waste-strategies.mjs` preferred C on round-trip count alone. The PromQL accuracy run overturns that. C only ties A when clamped identically.

**IA placement (throwaway HTML).** Scratch `/tmp/capacity-proto/ia-variants.html`.

| Variant | Finding | Decision |
| --- | --- | --- |
| V1 Top-level Capacity | Matches Jobs precedent and bird's-eye blog goal | Chosen for CAP-2 |
| V2 Namespace only | Strong team scope, no fleet compare | Chosen as CAP-3 embed, not the only home |
| V3 Runtime only | Already shipped, no gaps inventory | Rejected as the program deliverable |

**Open product call (default applied).** Request-only containers (28 in the fixture, about 2.3% of requested cores) do not appear as waste under an inner join. Default is a separate `reservedWithoutUsage` category. Tell the plan owner in plain words if those should count as waste instead.

**Unproven on live Mimir.** This session's Grafana MCP exposed only ClickHouse. Live sample cost against production Mimir remains unproven. CAP-1 live lanes must record real series counts before CAP-2 starts.

## Appendix B. Alternatives rejected

- **Clone Grafana Cloud Kubernetes Monitoring.** Node inventory, deleted-object browser, OpenCost, and GPU tabs are a different product. This fork ships service and namespace efficiency inside APM.
- **Hang Capacity under My Apps.** My Apps is favorites of services. Capacity is fleet and team waste. Wrong object.
- **Per-service runtime fan-out for fleet.** PromQL fixture B. 2,408 queries, cannot detect gaps, `avg` understates waste by about 68%.
- **Join-free namespace rollup.** PromQL fixture C without clamp. About 37% low on waste. Only correct when it becomes A.
- **NAIS design-philosophy freeze on USE.** This cut-off fork treats Capacity as intentional USE. Service Runtime stays for diagnosis. Capacity owns bird's-eye waste.
- **ML-only forecasts in CAP-1.** Forecasts need history UX and capability gating. They land last as CAP-5.

## Appendix C. Risks

| Risk | PR | Watch |
| --- | --- | --- |
| PromQL cardinality blowup on fleet join | CAP-1 | Round-trip and timing rule. Cap result sizes like `maxNamespace*` guards. Recording rules only if live lanes fail the budget. |
| `avg` understates capacity (Runtime precedent) | CAP-1 | Require sum-by. Unit test rejects avg-only totals. |
| POD or empty container double-count | CAP-1 | Sidecar lane plus explicit exclusion selectors. |
| Page-cache memory via `container_memory_usage_bytes` | CAP-1 | Prefer working-set metric in otelconfig. Document the choice in `docs/api/capacity.md`. |
| CPU millicore vs cores unit drift | CAP-1 | Dedicated unit test. |
| Default cache thrash under wallboard poll | CAP-1 | Dedicated longer-TTL cache like scorecard. |
| Span-metric service identity vs kube workload | CAP-2 | Fleet page owns identity (Jobs precedent). Do not bolt Capacity onto Service Inventory rows. |
| APM `service_namespace` vs kube `namespace` | CAP-1, CAP-3 | Document map. Do not silently equate them. |
| RangeQuery step clamp (~50 points in services path) | CAP-4 | History lanes assert usable resolution or documented step. |
| `computeRangeStr` 5m to 24h cap vs multi-day predict | CAP-5 | Forecast window must not silently inherit the 24h cap without a documented override. |
| Label mismatch across clusters | CAP-1, CAP-3 | Sidecar lane and env filter lanes. |
| URL param clobber on drill | CAP-3, CAP-4 | Use `useUrlParams` single update per action. |
| Linear forecast misread as ML | CAP-5 | Mandatory labeling lane. |
| OpenAPI drift vs `apidocs_test.go` | CAP-1 | Same commit updates `openapi.yaml`. |
| pstack skills missing on this repo trunk | Program arm | Fallback absolute paths recorded in Arm boxes. |

## Appendix D. Links and reading list

- Blog. https://grafana.com/blog/monitor-the-past-present-and-future-of-your-kubernetes-resource-utilization/
- Efficiency docs. https://grafana.com/docs/grafana-cloud/observe-and-act/monitor-infrastructure/kubernetes-monitoring/optimize-resource-usage.md
- Predict docs. https://grafana.com/docs/grafana-cloud/observe-and-act/monitor-infrastructure/kubernetes-monitoring/optimize-resource-usage/predict-and-analyze-trends.md
- Repo metrics. `docs/metrics-reference.md` Container section
- Jobs enablement precedent. `pkg/plugin/jobs.go`, `pkg/plugin/chquery/jobs.go`, `src/api/jobs.ts`, `src/pages/JobsInventory.tsx`
- Runtime container path. `pkg/plugin/runtime.go` `queryContainerRuntime`, `pkg/plugin/otelconfig/otelconfig.go` `ContainerMetrics`
- Handler registration. `pkg/plugin/app.go` `App.routes()`, `docs/api/openapi.yaml`, `apidocs_test.go`
- Seam map from [Map capacity code seams](712bdee3-2db0-49fd-9be1-4fdef5725129)
- Interrogate CAP-2 and CAP-5 before merge-ready (novel UX and forecast labeling).
- Decision trail via `pstack/skills/show-me-your-work/SKILL.md` for the program run.
