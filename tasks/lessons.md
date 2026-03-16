# Lessons Learned

Updated at the end of each phase. Records model behavior observations, bugs found by reviewer, and environment issues encountered.

---

## Phase 0 — Scaffold

| # | Category | Observation |
|---|---|---|
| 1 | Environment | `go 1.25.5` is genuinely installed on this machine — go.mod is correct as-is |
| 2 | Environment | `github.com/prometheus/client_golang` is not in the module cache and no network is available. Metrics package uses a stdlib stub for Phase 0; swap in Phase 5 when network access is confirmed |
| 3 | Reviewer catch | `Config` struct needed `slog.LogValuer` (`LogValue()`) to prevent credential leakage via structured logging. Add this pattern to all config structs that contain secrets in future projects |
| 4 | Reviewer catch | Noop stub `Run()` methods should return `ctx.Err()` (not `nil`) after context cancel, so callers can distinguish clean shutdown from errors. Apply this pattern to all blocking noop stubs |
| 5 | Reviewer catch | `CounterVec.With()` in the stdlib metrics stub initially returned `self` (discarding labels). Correct key is `strings.Join(labelValues, ",")`. Test label independence explicitly |
| 6 | Coder iteration | One reviewer cycle required (FAIL → fix → PASS). All CRITICAL/MAJOR issues from first pass were valid and fixed cleanly |

---

## Phase 1 — Watcher

| # | Category | Observation |
|---|---|---|
| 1 | Reviewer catch | `classifyEvent` mapped `"BackOff"` and `"Failed"` k8s event reasons to failure types without inspecting `event.Message`. Both reasons are ambiguous — "BackOff" covers crash-loop and image-pull; "Failed" covers dozens of unrelated failures. Fix: `strings.Contains` on lowercased message to disambiguate. Always inspect message content for ambiguous k8s event reasons |
| 2 | Reviewer catch | `AddEventHandler` return value `(ResourceEventHandlerRegistration, error)` was suppressed with `//nolint:errcheck`. Should be checked and returned — propagation up through `Run()` is correct |
| 3 | Coder iteration | One reviewer cycle required (FAIL → fix → PASS). Both MAJOR and MINOR issues from first pass were valid and fixed cleanly |
| 4 | Design | `classifyPod` on container statuses is the ground truth; `classifyEvent` is a supplementary signal and requires message inspection to be reliable |
| 5 | Environment | k8s.io dependencies resolved to v0.35.2 despite specifying v0.34.3 — `go mod tidy` always resolves to the highest compatible available version |

---
