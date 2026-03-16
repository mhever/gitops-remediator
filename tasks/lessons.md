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

## Phase 2 — Collector

| # | Category | Observation |
|---|---|---|
| 1 | Reviewer MINOR | `sanitize()` does not redact `EphemeralContainers` env vars — rare in practice but worth completing in a future pass |
| 2 | Reviewer MINOR | `EnvFrom` (bulk env from ConfigMap/Secret) not redacted — the field contains names only, not values, so no secret data leaks; acceptable |
| 3 | Design | Bundle rendered as structured plain text (not JSON) — LLMs read it better; same lesson from homelab-mcp confirmed here |
| 4 | Coder deviation | `k8sClient` hoisted to outer scope in main.go to make it accessible for collector wiring — correct approach, not a real deviation |
| 5 | Process | PASS on first reviewer cycle — no MAJOR/CRITICAL issues |

---

## Phase 3 — Diagnostician

| # | Category | Observation |
|---|---|---|
| 1 | Reviewer catch | Test suite did not verify the `Authorization` header was sent on the outgoing HTTP request. A regression dropping credentials would have been invisible. Pattern: httptest handlers should always assert auth headers on security-sensitive calls |
| 2 | Reviewer catch | Test suite did not verify request body `model` field. Model name regressions should be caught at test time |
| 3 | Design | `baseURL` field on struct (defaulting to `"https://api.deepseek.com"`) is the right pattern for making raw HTTP clients testable without mocking the whole `http.Client` |
| 4 | Design | Non-remediable diagnosis returns `(*Diagnosis, nil)` — not an error. The escalation is a valid outcome, not a failure. Important distinction for caller logic |
| 5 | Design | Code-fence stripping needed because DeepSeek sometimes wraps JSON in ` ```json ``` ` despite explicit instructions. Applied via `TrimSpace` + `TrimPrefix`/`TrimSuffix` |
| 6 | Coder iteration | One reviewer cycle (FAIL → fix → PASS). MAJOR issue was a missing security assertion in tests |

---

## Phase 4 — Patcher + GitOps

| # | Category | Observation |
|---|---|---|
| 1 | Reviewer catch (CRITICAL) | GitHub token embedded in clone URL `https://<token>@github.com/...` was being printed verbatim in `runGit` error messages. Fix: `sanitizeArgs` + `sanitizeOutput` regex redaction applied to error paths only. Pattern: any URL with credentials must be sanitised before appearing in logs or errors |
| 2 | Reviewer catch (MAJOR) | `os.RemoveAll(tmpDir)` called manually at 6 return sites instead of `defer`. One missed path = leaked temp dir. Always `defer` cleanup immediately after resource acquisition |
| 3 | Reviewer catch (MAJOR) | `findManifest` matched `"name: <x>"` anywhere in file content including container names and labels. Fixed with `containsDeploymentWithName` — line-by-line, scoped to after `kind: Deployment`, exact 2-space indent match |
| 4 | Reviewer catch (MAJOR) | `applyImageTag` patched the first `image:` line in the file regardless of container. Fixed with container-name scoping: scan for `- name: <container>`, patch the next `image:` before the next `- name:` |
| 5 | Reviewer catch (MAJOR) | `applyMemoryLimit` exited the `limits:` block on any unrecognised sub-field (e.g. `ephemeral-storage:`). Fixed with indentation-level tracking — stay in block while indent is deeper than `limits:` line |
| 6 | Design | `diff -u` via `os/exec` for unified diff generation — exit code 1 means files differ (expected), not an error. Must use `errors.As` + `ExitCode()` check |
| 7 | Design | `go-git` not in module cache; `os/exec` with system `git` is simpler, no new dependency, and reliable on the ThinkCentre host |
| 8 | Coder iteration | Two reviewer cycles (FAIL → fix → PASS). All 5 blocking issues from first pass were valid and fixed cleanly |

---

## Phase 5 — Metrics + Polish

| # | Category | Observation |
|---|---|---|
| 1 | Environment | `github.com/prometheus/client_golang v1.23.2` available via proxy; not pre-cached but downloaded successfully |
| 2 | Design | Metrics initialized in `init()`, registered in `Register()` — allows tests to use per-test `prometheus.NewRegistry()` without double-register panics |
| 3 | Design | `escalationReason()` normalises free-form DeepSeek escalation strings to the three Prometheus label values: "application_panic", "auth_failure", "unknown" |
| 4 | Reviewer MINOR | Test counter assertions use `>=` because package-level prometheus vars accumulate across tests. A before/after baseline pattern is cleaner |
| 5 | Coder fix | Dead-code block creating `DeepSeekDiagnostician` on the noop path was correctly removed |
| 6 | Process | PASS on first reviewer cycle — no MAJOR/CRITICAL issues |

---
