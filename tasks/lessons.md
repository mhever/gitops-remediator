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
