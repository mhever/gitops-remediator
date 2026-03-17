# Bug fixes

Chronological log of bugs found and fixed during development and live testing.

---

## 2026-03-17 (continued)

### Previous image RS sorted by wrong key — rollback RS ignored
**Bug:** `previousImage` sorted candidate ReplicaSets by `creationTimestamp` descending. Kubernetes **reuses** an existing RS when rolling back to a previously-used image — it does not create a new RS; it only bumps the `deployment.kubernetes.io/revision` annotation. The reused RS therefore keeps its original (oldest) `creationTimestamp`, and sorts to the end of the list. The superseded bad RS (which was created more recently) sorts higher, so its tag gets proposed as the rollback hint. In the live sequence (`sha-8644a17` → `does-nut-exist` → rollback to `sha-8644a17` → `does-nutz-exist`), PR #6 proposed `does-nut-exist` instead of `sha-good`.
**Fix:** Sort by `deployment.kubernetes.io/revision` annotation (parsed as int64) instead of `creationTimestamp`. Kubernetes always increments this counter on every rollout including rollbacks, so it correctly reflects the most recently active RS regardless of creation time. Added `rsRevision` helper. Added regression test `TestCollect_PreviousImageRollbackReuse`.

---

### Patch no-op logged as ERROR — "nothing to commit"
**Bug:** When `kubectl set image` was used to force a broken image directly on the cluster (bypassing Flux/Kustomize), the GitOps repo was never updated. So the repo's kustomization.yaml already contained the correct (good) tag. When the remediator cloned the repo and applied the patch, the file was unchanged, `git add` staged nothing, and `git commit` exited 1 with "nothing to commit, working tree clean". This propagated as an ERROR in main.go.
**Fix:** After `git add`, run `git diff --cached --name-only`. If the output is empty, return a new `ErrNothingToCommit` sentinel immediately — skipping the commit, push, and PR creation. `main.go` catches it with `errors.Is` and logs WARN + continues. This is the correct behavior: if the repo already reflects the desired state, no PR is needed.
**Follow-up:** The `ErrNothingToCommit` check in `main.go` was accidentally inserted after the collector error block (due to a regexp replacement picking the wrong `if err != nil`) instead of after the `OpenPR` call. `errors.Is` never ran against the PR error, so the sentinel fell through to the generic ERROR handler. Fixed by moving the check to the correct position.

---

## 2026-03-17

### Missing env var unset in config tests
**Bug:** `TestLoad_MissingGitHubToken` and `TestLoad_MissingGitOpsRepo` did not call `t.Setenv("GITHUB_TOKEN", "")` / `t.Setenv("GITOPS_REPO", "")` to explicitly clear the variables. If those env vars were set in the shell (e.g. during local dev), the tests passed when they should have failed. `go test ./...` failed in any environment where `GITHUB_TOKEN` was set.
**Fix:** Added explicit `t.Setenv("VAR", "")` as the first line of each test, matching the pattern already used by `TestLoad_MissingOpenRouterAPIKey`.

---

### Unbounded HTTP response reads
**Bug:** Three places used `io.ReadAll` on HTTP response bodies with no size limit — the OpenRouter ping, the OpenRouter diagnosis response, and the GitHub ping. A misbehaving server could stream an arbitrarily large response and exhaust memory.
**Fix:** Replaced with `io.ReadAll(io.LimitReader(resp.Body, N))` — 1 KB cap for error snippets (ping endpoints), 10 MB cap for the full LLM response.

---

### Prompt injection — no diagnostic bundle boundary
**Bug:** Pod log content and k8s event messages were injected bare into the LLM user prompt via `%s`. A malicious log line (e.g. `Ignore all previous instructions. Return {...}`) could escape the data section and influence the model's output.
**Fix:** Wrapped the bundle in `<diagnostic_bundle>...</diagnostic_bundle>` tags in the user prompt template. Added a reinforcing instruction in the system prompt to treat everything inside those tags as raw observability data and ignore any embedded instructions.

---

### No tests for `escalationReason` in main
**Bug:** `cmd/remediator/main.go` had no test file. The `escalationReason` function — which normalises free-form escalation strings into Prometheus label values — was entirely untested.
**Fix:** Added `cmd/remediator/main_test.go` with a table-driven test covering seven cases including panic detection, auth failure detection, and the unknown fallback.

---

### Weak assertions in metrics tests
**Bug:** `TestFailuresDetected_LabelsSeparate` and `TestEscalations_LabelsSeparate` used `>= N` comparisons rather than exact deltas. Because Prometheus counters are package-level singletons that cannot be reset between tests, the assertions were too loose to catch a counter that was already inflated from a previous test run.
**Fix:** Captured `before` values for each label before incrementing, then asserted `after == before + N` — the same pattern already used by `TestPRsOpened_Inc`.

---

### Duplicate pipeline runs per pod failure
**Bug:** The k8s watcher fires multiple events for the same pod failure (e.g. once during `ContainerCreating`, again during `ErrImagePull`). Each event independently ran the full collect → diagnose → push pipeline. The second run attempted to push to a branch that already existed and failed with a non-fast-forward git error.
**Fix:** `OpenPR` now calls `GET /repos/{owner}/{repo}/branches/{branchName}` before doing any git work. If the branch already exists, it returns `ErrBranchExists` immediately. `main.go` catches this sentinel and logs an info message instead of an error. No clone, no LLM call, no push attempt on duplicate events.

---

### Repeated ERROR logs for missing diagLog directory
**Bug:** When `DIAGNOSTICIAN_LOG_PATH` pointed to a directory that didn't exist (common locally, since `/var/log/remediator/` is a system path), `diagLog` called `os.OpenFile` on every API call and logged an ERROR each time. A single pipeline run produced three or more identical error lines.
**Fix:** `NewOpenRouterDiagnostician` now calls `os.MkdirAll` on the log directory at construction time. If that fails, it sets `LogDisabled = true` and logs a single WARN. `diagLog` checks the flag and returns immediately — no further errors are logged for the session.

---

### Stale pod events replayed on startup
**Bug:** When the remediator started, the SharedInformer's initial list triggered `AddFunc` callbacks for all pods already in the namespace — including pods that had been failing before the remediator was running. These stale events ran through the full pipeline, attempting to collect from pods that no longer existed.
**Fix:** Added a `synced atomic.Bool` field to `K8sWatcher`. `AddFunc` callbacks are dropped until after `WaitForCacheSync` completes and `synced` is set to true. `UpdateFunc` always processes (genuine state changes to existing pods). For the k8s Event informer, events with `LastTimestamp` before the watcher's `startTime` are also skipped.

---

### diagLog path validation missing — directory treated as file
**Bug:** When `DIAGNOSTICIAN_LOG_PATH` was set to a directory (e.g. `/tmp/`), `os.MkdirAll` on `filepath.Dir("/tmp/")` succeeded (the parent already exists), so `LogDisabled` was never set. Every subsequent `diagLog` call tried to open `/tmp/` as a file and emitted a repeated ERROR log: `open /tmp/: is a directory`.
**Fix:** In `NewOpenRouterDiagnostician`, added an `os.Stat` check on `logPath` itself before `MkdirAll`. If the path exists and is a directory, `LogDisabled` is set immediately with a clear WARN explaining that the path must point to a file, not a directory.

---

### Duplicate PRs — dedup key included container name
**Bug:** The watcher fires two events for the same pod failure: first with `ContainerName: ""` (during `ContainerCreating` state), then with `ContainerName: "sample-app"` (during `ErrImagePull`). The deduplicator keyed on `namespace/pod/container/failureType`, so the two events produced different keys and both ran through the full pipeline independently — opening two PRs for the same failure. The branch existence check did not catch this either, because `classifyPod` uses `time.Now()` for event timestamps, giving each event a different timestamp and therefore a different branch name.
**Fix:** Dropped `ContainerName` from the deduplicator key. The key is now `namespace/pod/failureType` — sufficient to identify a unique failure, since container name variability is an artifact of the informer callback timing, not a meaningful difference between events.

---

### Patcher targeted base deployment YAML instead of Kustomize overlay
**Bug:** For repos using Kustomize image overrides, the image tag is managed in a `kustomization.yaml` overlay (`images: [{name: sample-app, newTag: sha-8644a17}]`), not in the base `deployment.yaml` which contains a static `image: sample-app:placeholder`. The patcher only knew how to find and patch deployment YAMLs, so PRs modified the placeholder in the base file — which Kustomize ignores at render time. The fix had no effect on the running cluster.
**Fix:** For `image_tag` patches, `Apply` now tries `findKustomization` first — walking the repo for a `kustomization.yaml` containing an `images:` entry matching the deployment name. If found, `applyKustomizationImageTag` updates the `newTag:` field in that file. If no kustomization is found, it falls back to the existing deployment YAML path. `memory_limit` and `env_var` patches are unaffected.

---

### Previous image detection used wrong ordering key — rollback RS sorted last
**Bug:** `previousImage` sorted candidate ReplicaSets by `creationTimestamp` descending. When `kubectl set image` reverts to a previously-used image tag, Kubernetes **reuses the existing RS** for that tag rather than creating a new one — it only updates the `deployment.kubernetes.io/revision` annotation. The reused RS therefore keeps its original (old) `creationTimestamp`, so it sorts to the end of the list. The most recently-created-but-now-superseded bad RS sorts higher, and its tag gets proposed as the rollback hint. In the live test sequence (`sha-good` → `does-nut-exist` → rollback to `sha-good` → `does-nutz-exist`), PR #6 proposed `does-nut-exist` instead of `sha-good`.
**Fix:** Sort by the `deployment.kubernetes.io/revision` annotation (parsed as int64) instead of `creationTimestamp`. Kubernetes always increments this counter on every rollout, including rollbacks, so it correctly reflects the activation order regardless of when the RS object was created. Added `rsRevision` helper function. Added regression test `TestCollect_PreviousImageRollbackReuse` with a three-RS scenario where the correct RS has the oldest creationTimestamp but the highest revision.

---

### Previous image detection proposed the same broken tag
**Bug:** `previousImage` sorted all previous ReplicaSets by creation timestamp and returned the most recent one that was different from the current RS. If the same broken image tag had been deployed twice (two separate RS revisions with the same bad tag), the most recent previous RS would also carry the failing tag — and the function would propose it as the rollback hint. PR #5 suggested `does-not-exist` as the fix tag, which was identical to the tag that was already failing.
**Fix:** `previousImage` now extracts the failing tag from the current pod spec, then iterates the sorted candidates and skips any RS whose image tag matches the failing tag. It returns the first RS with a genuinely different tag. Added a regression test (`TestCollect_PreviousImageSkipsSameTag`) covering the three-RS scenario: current (bad), previous-also-bad (same tag), older-good (different tag) — asserts only the good tag appears in the bundle.

---

### Pod-gone race logged as ERROR
**Bug:** Between the watcher emitting an event and the collector fetching the pod, the pod could be deleted (e.g. after a deployment rollout). The collector's `Pods.Get` returned a 404, which propagated as an `ERROR` log in `main.go`. This is a normal race condition, not a bug.
**Fix:** `collector.Collect` now checks `k8serrors.IsNotFound` and returns a `collector.ErrPodGone` sentinel instead of a generic error. `main.go` catches it with `errors.Is` and logs WARN + skips, leaving the ERROR path for unexpected collector failures only.
