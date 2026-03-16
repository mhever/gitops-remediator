# gitops-remediator — Project Plan

**Repo:** https://github.com/mhever/gitops-remediator  
**Status:** Planning complete, starting Phase 0

---

## What It Is

A Go service running on the ThinkCentre (k3s host) that watches a Kubernetes namespace for specific failure events, assembles a diagnostic context bundle, sends it to DeepSeek R1 for root cause analysis and patch generation, then opens a GitHub PR against the GitOps repo with the proposed fix. You merge. FluxCD reconciles. Loop closed.

This is a portfolio piece demonstrating Go development, Kubernetes operations, LLM integration, GitOps principles, and agentic system design.

---

## Design Decisions (Locked)

**Why PR over direct patch:**
Direct `kubectl apply` creates drift between Git and the cluster — the opposite of GitOps. The PR is the artifact. It honors the source-of-truth principle and produces a visible, recruiter-legible trail of automated decision-making.

**Why DeepSeek R1 for the Diagnostician:**
Called from Go code directly via raw `net/http` against DeepSeek's OpenAI-compatible API. R1's reasoning trace is logged alongside the structured output and included in the PR description. Full prompt + response logged to file for token usage analysis and debugging. This is a deliberate learning opportunity — calling an external LLM from a long-running Go service.

**PII / sensitive data handling:**
Env var *values* are redacted in the diagnostic bundle before sending to DeepSeek (keys kept, values replaced with `[REDACTED]`). Log content filtering was consciously skipped — pattern matching + entropy analysis exist as enterprise-grade solutions but are out of scope for this project. This is a documented choice, not an oversight.

**Test environment:**
Option B — dedicated `remediator-test` namespace. Remediator watches only this namespace. Sample app deployed there via a separate `test/` path in the GitOps repo, managed by a dedicated FluxCD Kustomization. Live services in the main namespace are never touched. Failures triggered by committing intentionally broken manifests to `test/`. Full GitOps loop runs in isolation.

**homelab-mcp as live monitor:**
During testing, use the existing homelab-mcp connection in Claude Desktop to watch `k8s_events`, `k8s_pods`, and `k8s_pod_logs` in real time. The two projects complement each other and this is worth noting in the writeup.

---

## Failure Taxonomy (Final, Not Expanding)

| Failure | Detection | Remediable? | Patch Target |
|---|---|---|---|
| OOMKilled | Pod status reason = OOMKilled | Yes | Increase `resources.limits.memory` in deployment manifest |
| CrashLoopBackOff (bad config) | Pod status + log pattern (no panic trace) | Yes | Patch env var or secret ref in manifest |
| CrashLoopBackOff (code panic) | Pod status + log pattern contains panic stack trace | No | Log escalation, no PR |
| ImagePullBackOff (wrong tag) | Pod event reason = Failed + message contains tag | Yes | Fix image tag in manifest |
| ImagePullBackOff (auth) | Pod event message contains auth/unauthorized | No | Log escalation, no PR |

**Escalation path:** When `remediable: false`, the agent logs a structured entry with `cause`, `action: escalated`, and `reason`. No PR is created. This boundary is as important as the fix path.

---

## Agent Setup

**Orchestrator:** Claude Code (Sonnet) — routes tasks, runs `go test`, assembles context for subagents, writes documentation at phase end. Does not write production code.

**claude-coder (Sonnet):** Agentic subagent with file access. Writes Go implementation + tests together per spec. Iterates until tests pass. Never modifies the Diagnostician prompt without orchestrator approval.

**claude-reviewer (Opus):** Critical persona. Reviews every coder output before orchestrator accepts it. Checks for correctness, Go idioms, error handling, test coverage, and security issues (especially around the diagnostic bundle assembly and external API call). Runs less frequently than coder so Opus cost is acceptable.

**DeepSeek R1:** Runtime only. Not used during build. Called by the running remediator service to analyze failures and generate patches. Not a build-time subagent.

**Documentation:** Written by the orchestrator at the end of each phase using the phase summary and design decisions. No separate documentation agent — the Gemini-as-librarian pattern from homelab-mcp added friction without proportional value.

---

## Repo Structure

```
gitops-remediator/
├── cmd/
│   └── remediator/
│       └── main.go
├── internal/
│   ├── watcher/            # k8s SharedInformer, event filtering, FailureEvent type
│   ├── collector/          # context bundle assembly (logs, events, spec/status)
│   ├── diagnostician/      # DeepSeek R1 API call, response parsing, structured output
│   ├── patcher/            # YAML patch generation and validation
│   ├── gitops/             # git clone, branch, commit, PR via GitHub API
│   └── metrics/            # Prometheus endpoint
├── config/                 # config structs, env loading
├── k8s/                    # manifests for deploying the remediator itself to k3s
├── testdata/               # fixture manifests, fake event JSON for unit tests
├── docs/
│   ├── token-usage.md
│   ├── diagnostician-prompt.md
│   └── pii-decision.md
├── .claude/
│   └── agents/
│       ├── claude-coder.md
│       └── claude-reviewer.md
├── CLAUDE.md
├── tasks/
│   └── lessons.md
└── README.md
```

---

## Phases

### Phase 0 — Scaffold

**Goal:** Runnable skeleton with no business logic. CI passing. Agent files in place.

Tasks:
- `go mod init github.com/mhever/gitops-remediator`
- `cmd/remediator/main.go`: config load, logger init, signal handling, graceful shutdown
- Stub interfaces for all internal packages (empty implementations that satisfy the interface)
- GitHub Actions CI: vet, race-enabled tests, module tidy check (copy from homelab-mcp)
- CLAUDE.md with orchestration rules
- `.claude/agents/claude-coder.md` and `.claude/agents/claude-reviewer.md`
- `tasks/lessons.md` initialized

Deliverable: `go build ./...` and `go test ./...` pass. CI green.

---

### Phase 1 — Watcher

**Goal:** Detect the three failure modes from live k3s and emit typed events.

Tasks:
- client-go SharedInformer watching Pod events in configured namespace
- Filter logic:
  - Pod phase transitions to Failed
  - ContainerStatus reason = OOMKilled, CrashLoopBackOff, ImagePullBackOff
  - Warning events with reasons: BackOff, OOMKilling, Failed
- `FailureEvent` struct: namespace, pod name, container name, failure type (enum), raw reason string, timestamp
- Deduplification: don't fire the same event more than once per pod per 10-minute window
- Tests: mock informer with fixture events, verify filtering and dedup logic

No LLM, no git, no network beyond k8s API.

Deliverable: Watcher running against `remediator-test` namespace, emitting events to stdout. Verified with homelab-mcp watching the same namespace.

---

### Phase 2 — Collector

**Goal:** Given a `FailureEvent`, assemble a complete `DiagnosticBundle`.

Bundle contents:
- Last 100 lines of container logs
- Pod spec (from k8s API)
- Pod status (current, full)
- Last 5 minutes of namespace events
- Current resource limits (memory, CPU)

PII mitigation:
- `sanitize()` function strips env var values before including spec in bundle
- Env var keys kept, values replaced with `[REDACTED]`
- Applied before any external transmission

Output format: structured plain text block (not JSON — LLMs read it better, same lesson from homelab-mcp).

Tests: mock k8s client, verify bundle completeness, verify sanitize() correctly redacts values while preserving keys.

Deliverable: Collector producing a complete bundle from a live failing pod in `remediator-test`.

---

### Phase 3 — Diagnostician

**Goal:** Send bundle to DeepSeek R1, parse structured response, decide on action.

Implementation:
- Raw `net/http` call to DeepSeek API (`api.deepseek.com`, model `deepseek-reasoner`)
- OpenAI-compatible chat completions endpoint
- System prompt: forces JSON-only output, no markdown, no preamble
- User prompt: the diagnostic bundle

Required JSON response fields:
```json
{
  "failure_type": "OOMKilled|CrashLoopBackOff|ImagePullBackOff",
  "root_cause": "string",
  "remediable": true|false,
  "escalation_reason": "string (if remediable: false)",
  "patch_type": "memory_limit|env_var|image_tag",
  "patch_value": "string (the new value to apply)",
  "reasoning_summary": "string (condensed from R1 chain of thought)"
}
```

Logging:
- Full prompt written to log file before sending
- Full response written to log file after receiving
- Includes token counts from response metadata
- Log file: `/var/log/remediator/diagnostician.log` or configurable path

If `remediable: false`: log structured escalation entry, return early. No further stages run.

Tests: mock HTTP client returning known-good JSON, malformed JSON, and non-remediable responses. Verify parsing and early-exit behavior.

Deliverable: Diagnostician correctly classifying a live OOMKilled event and returning a structured patch recommendation.

---

### Phase 4 — Patcher + GitOps

**Goal:** Apply the patch to the GitOps repo and open a GitHub PR.

Patcher:
- Clone GitOps repo to temp directory
- Create branch: `remediation/<failure-type>-<pod-name>-<unix-timestamp>`
- Locate the correct manifest file (search for the deployment by name)
- Apply patch based on `patch_type`:
  - `memory_limit`: find `resources.limits.memory`, replace value
  - `env_var`: find env var by key name, replace value
  - `image_tag`: find `image:` field, replace tag portion only
- Validate patched YAML parses correctly before committing
- Commit with message: `fix: auto-remediate <failure_type> for <pod-name>`

PR body template:
```
## Automated Remediation

**Failure detected:** <failure_type> on pod <pod-name> at <timestamp>
**Root cause:** <root_cause from diagnostician>
**Proposed fix:** <patch_type> changed to <patch_value>

### Diagnostic Summary
<first 20 lines of bundle>

### Agent Reasoning
<reasoning_summary from diagnostician>

---
*Generated by gitops-remediator. Review before merging.*
```

GitHub PR opened via GitHub REST API (no library, raw `net/http`, same pattern as diagnostician).

Tests: mock git operations and GitHub API. Fixture manifests in `testdata/`. Verify patch correctness for each patch type. Verify YAML validation catches invalid patches.

Deliverable: Full loop — failing pod in `remediator-test` triggers a real PR against the GitOps repo.

---

### Phase 5 — Metrics + Polish

**Goal:** Observability, deployment, documentation, README.

Prometheus metrics endpoint (`/metrics`):
- `remediator_failures_detected_total{type="OOMKilled|CrashLoopBackOff|ImagePullBackOff"}`
- `remediator_prs_opened_total`
- `remediator_escalations_total{reason="application_panic|auth_failure|unknown"}`
- `remediator_diagnostician_latency_seconds` (histogram)
- `remediator_diagnostician_errors_total`

Deployment:
- Kubernetes manifests in `k8s/`: Deployment, ServiceAccount, ClusterRole, ClusterRoleBinding (scoped to `remediator-test` namespace), ConfigMap for config, Secret reference for DeepSeek API key and GitHub token
- Deploy remediator to k3s — dogfood it

Documentation:
- `README.md`: architecture overview, setup instructions, demo walkthrough, example PR screenshot
- `docs/token-usage.md`: DeepSeek token consumption data from real runs
- `docs/diagnostician-prompt.md`: prompt design rationale, failure taxonomy, escalation logic
- `docs/pii-decision.md`: documents the conscious choice to implement env var redaction but skip log content filtering, notes enterprise-grade alternatives exist

Deliverable: Full demo loop working end-to-end, metrics visible, remediator running as a k3s workload monitoring itself.

---

## CLAUDE.md Rules (Key Items)

- Orchestrator does not write production code. Routes to claude-coder subagent.
- claude-reviewer must be invoked after every coder output before orchestrator accepts it.
- `go test ./...` must pass before moving to next phase.
- Diagnostician prompt (system + user template) requires explicit orchestrator approval to modify. Coder cannot change it unilaterally.
- Never modify Phase N+1 code while in Phase N.
- `tasks/lessons.md` updated at end of each phase with model behavior observations, bugs found by reviewer, environment issues encountered.
- If `go test` fails after reviewer approval and one fix attempt, escalate to orchestrator — do not loop indefinitely.

---

## Test Environment Setup (Do First)

Before Phase 0 code work:

1. `kubectl create namespace remediator-test`
2. Copy sample-app manifests to `test/` folder in GitOps repo
3. Add FluxCD Kustomization pointing at `test/` path, targeting `remediator-test` namespace
4. Commit and verify FluxCD deploys sample app to `remediator-test`
5. Verify homelab-mcp can see pods and events in `remediator-test` namespace

To trigger failures during development:
- OOMKilled: set `resources.limits.memory: 1Mi` in test manifest, commit
- CrashLoopBackOff: set a required env var to an invalid value
- ImagePullBackOff: change image tag to `does-not-exist`

---

## Resume Line (Pending Completion)

> **Agentic GitOps Remediator**: Go service that detects Kubernetes failure events, uses DeepSeek R1 for LLM-assisted diagnosis, and opens GitHub PRs against the GitOps source of truth with proposed configuration fixes. FluxCD closes the loop. Prometheus metrics, full diagnostic logging, env var PII redaction.