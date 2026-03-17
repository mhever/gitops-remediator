# Testing the gitops-remediator

This document serves two purposes:
1. A reusable reference for triggering each failure type and what to expect
2. A log of real test runs with observations

---

## Prerequisites

- remediator binary running locally with valid `OPENROUTER_API_KEY`, `GITHUB_TOKEN`, and `GITOPS_REPO=mhever/sample-app`
- `sample-app` deployment running in `remediator-test` namespace
- Startup logs show OpenRouter and GitHub connectivity checks passing

Before triggering any failure, record the current image so you can restore it:

```bash
kubectl get deployment sample-app -n remediator-test \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
# ghcr.io/mhever/sample-app:sha-8644a17
```

---

## Failure triggers

### ImagePullBackOff (wrong tag)

**Trigger:**
```bash
kubectl set image deployment/sample-app \
  sample-app=ghcr.io/mhever/sample-app:does-not-exist \
  -n remediator-test
```

**Restore:**
```bash
kubectl set image deployment/sample-app \
  sample-app=ghcr.io/mhever/sample-app:sha-8644a17 \
  -n remediator-test
```

**Expected behaviour:** Pod enters `ImagePullBackOff` within seconds. Remediator detects the event, sends the diagnostic bundle to OpenRouter (DeepSeek R1), and opens a GitHub PR with an image tag patch.

---

### OOMKilled

**Trigger:**
```bash
kubectl patch deployment sample-app -n remediator-test \
  --patch '{"spec":{"template":{"spec":{"containers":[{"name":"sample-app","resources":{"limits":{"memory":"1Mi"}}}]}}}}'
```

**Restore:**
```bash
kubectl patch deployment sample-app -n remediator-test \
  --patch '{"spec":{"template":{"spec":{"containers":[{"name":"sample-app","resources":{"limits":{"memory":"256Mi"}}}]}}}}'
```

**Expected behaviour:** Container is killed with OOMKilled reason. Remediator proposes a memory limit increase in the GitOps manifest.

---

### CrashLoopBackOff (bad config)

**Trigger:**
```bash
kubectl set env deployment/sample-app DATABASE_URL=invalid -n remediator-test
```

**Restore:**
```bash
kubectl set env deployment/sample-app DATABASE_URL- -n remediator-test
```
(The trailing `-` removes the env var override.)

**Expected behaviour:** App crashes on startup due to bad config. Remediator proposes an env var patch. If the logs contain a panic stack trace, the failure is classified as non-remediable and escalated instead.

---

## Log reference

A successful end-to-end run produces these log lines in order:

```
failure event detected     type, namespace, pod, container, reason
failed to fetch logs       WARN — normal when container hasn't started yet
diagLog: ...               prompt + response written to DIAGNOSTICIAN_LOG_PATH
remediation PR opened      url, pod, type
```

Key fields on the `failure event detected` line:

| Field | Meaning |
|---|---|
| `type` | `ImagePullBackOff`, `OOMKilled`, or `CrashLoopBackOff` |
| `container` | Empty string on first event (ContainerCreating state), populated on retry |
| `reason` | Raw Kubernetes reason string (`ErrImagePull`, `Failed`, etc.) |

---

## Known behaviours and limitations

**Duplicate events:** A single pod failure typically fires the pipeline twice — once when the container is in `ContainerCreating` state (reason: `Failed`) and again when the image pull is actively retried (reason: `ErrImagePull`). The second event is deduplicated: `OpenPR` checks whether the remediation branch already exists on GitHub before doing any git work, and skips with a `skipping duplicate event` info log if it does. No error is logged and no LLM call is wasted on the second pass.

**diagLog path:** When running locally, `/var/log/remediator/diagnostician.log` may not exist. The remediator attempts to create the directory at startup (`os.MkdirAll`). If that fails (e.g. on a read-only system path), it logs a single WARN at startup and disables prompt/response logging for the session — the pipeline completes successfully regardless. To capture logs locally, set `DIAGNOSTICIAN_LOG_PATH=/tmp/remediator-diagnostician.log`.

**Image tag for `ImagePullBackOff`:** The diagnostic bundle now includes a `PREVIOUS IMAGE` section showing the last known-good image tag from ReplicaSet history (sorted by `deployment.kubernetes.io/revision`). The LLM is instructed to prefer this tag. The PR body includes a `[!WARNING]` callout noting that the tag should be verified before merging. If no previous RS exists, the LLM falls back to inference.

**Memory limit for `OOMKilled`:** The LLM proposes a new memory limit based on the pod's container status and event logs. It has no knowledge of the application's actual memory footprint, so the proposed value is a heuristic. Review the PR and adjust before merging if needed.

---

## Test run log

### 2026-03-17 — ImagePullBackOff (first live run)

**Trigger command:**
```bash
kubectl set image deployment/sample-app \
  sample-app=ghcr.io/mhever/sample-app:does-not-exist \
  -n remediator-test
```

**Relevant log output:**
```json
{"time":"2026-03-17T19:03:56Z","level":"INFO","msg":"failure event detected","type":"ImagePullBackOff","namespace":"remediator-test","pod":"sample-app-6894bfc947-fmqdx","container":"","reason":"Failed"}
{"time":"2026-03-17T19:03:56Z","level":"WARN","msg":"failed to fetch logs","pod":"sample-app-6894bfc947-fmqdx","container":"sample-app","error":"container is waiting to start: ContainerCreating"}
{"time":"2026-03-17T19:03:56Z","level":"ERROR","msg":"diagLog: failed to open log file","path":"/var/log/remediator/diagnostician.log","error":"open /var/log/remediator/diagnostician.log: no such file or directory"}
{"time":"2026-03-17T19:04:16Z","level":"INFO","msg":"remediation PR opened","url":"https://github.com/mhever/sample-app/pull/1","pod":"sample-app-6894bfc947-fmqdx","type":"ImagePullBackOff"}
```

**Time to PR:** ~20 seconds from event detection to PR open.

**PR:** [mhever/sample-app#1](https://github.com/mhever/sample-app/pull/1)

**PR contents:**
- Title: `fix: auto-remediate ImagePullBackOff for sample-app-6894bfc947-fmqdx`
- Root cause identified: image tag `does-not-exist` does not exist in registry `ghcr.io/mhever/sample-app`
- Patch: `image: sample-app:placeholder` → `image: sample-app:v1.2.3`
- Agent reasoning included in PR body

**Observations:**
- Pipeline worked end-to-end on first attempt
- LLM correctly identified the failure type and root cause
- Proposed image tag (`v1.2.3`) was hallucinated — not a real tag in the registry (see Known behaviours above)
- Duplicate event fired; second event detected while first PR was already open — both issues subsequently fixed (branch existence check, diagLog directory creation)

---

### 2026-03-17 — OOMKilled (first live run)

**Trigger command:**
```bash
kubectl patch deployment sample-app -n remediator-test \
  --patch '{"spec":{"template":{"spec":{"containers":[{"name":"sample-app","resources":{"limits":{"memory":"1Mi"}}}]}}}}'
```

**Relevant log output:**
```json
{"time":"2026-03-17T21:09:29Z","level":"INFO","msg":"failure event detected","type":"CrashLoopBackOff","namespace":"remediator-test","pod":"sample-app-55dd5998f9-8msrz","container":"","reason":"BackOff"}
{"time":"2026-03-17T21:10:39Z","level":"INFO","msg":"remediation PR opened","url":"https://github.com/mhever/sample-app/pull/7","pod":"sample-app-55dd5998f9-8msrz","type":"OOMKilled"}
```

**Time to PR:** ~70 seconds from event to PR (container had to OOMKill and enter CrashLoopBackOff first).

**PR:** [mhever/sample-app#7](https://github.com/mhever/sample-app/pull/7)

**PR contents:**
- Title: `fix: auto-remediate OOMKilled for sample-app-55dd5998f9-8msrz`
- Root cause: memory limit set too low (1Mi), causing OOM kill during container init
- Patch: inserted `resources: limits: memory: 128Mi` into `k8s/base/app/deployment.yaml` (base had no resources block)
- Agent reasoning: event logs show container init OOM-killed due to 1Mi memory limit

**Observations:**
- Watcher classified the event as `CrashLoopBackOff` (pod had restarted before being caught), but the LLM correctly identified the underlying cause as `OOMKilled` from the container status
- Patcher required two bug fixes to reach this point: `resources: {}` expansion and no-resources-block insertion
- Proposed 128Mi limit — reasonable conservative value; LLM has no knowledge of actual application memory footprint
- PR merged by user
