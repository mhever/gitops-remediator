# gitops-remediator

A Go service that watches a Kubernetes namespace for failure events, uses DeepSeek R1 for LLM-assisted root cause analysis, and opens GitHub PRs against a GitOps repository with proposed configuration fixes. FluxCD reconciles the merged PR back to the cluster, closing the loop.

Built as a portfolio piece demonstrating Go development, Kubernetes operations, LLM integration, GitOps principles, and agentic system design.

---

## Architecture

```
k3s cluster
  └── remediator-test namespace
        └── failing pod
              │
              ▼
        gitops-remediator (this service)
              │
              ├─ 1. Watcher (client-go SharedInformer)
              │      detects: OOMKilled, CrashLoopBackOff, ImagePullBackOff
              │
              ├─ 2. Collector
              │      assembles: pod spec (sanitised), logs, events, resource limits
              │
              ├─ 3. Diagnostician → DeepSeek R1
              │      returns: failure_type, root_cause, remediable, patch_type, patch_value
              │
              └─ 4. Patcher + GitOps
                     clone GitOps repo → patch manifest → commit → push → GitHub PR
                     FluxCD reconciles merged PR ──────────────────────────────────┐
                                                                                   ▼
                                                                          cluster healed
```

### Design decisions

**Why PRs over direct `kubectl apply`**: Direct patching creates drift between Git and the cluster — the opposite of GitOps. The PR is the artifact. It produces a visible, reviewable trail of automated decision-making and honours the source-of-truth principle.

**Why DeepSeek R1**: Called from Go via raw `net/http` against DeepSeek's OpenAI-compatible API. R1's reasoning trace is logged alongside the structured output and included in the PR description. Full prompt + response written to file for token usage analysis and debugging.

**Why plain-text diagnostic bundles**: LLMs parse structured plain text better than JSON blobs for reasoning tasks. Lesson carried over from [homelab-mcp](https://github.com/mhever/homelab-mcp).

---

## Failure taxonomy

| Failure | Detection | Remediable | Patch |
|---|---|---|---|
| OOMKilled | ContainerStatus.Terminated.Reason | Yes | Increase `resources.limits.memory` |
| CrashLoopBackOff (bad config) | ContainerStatus.Waiting.Reason, no panic in logs | Yes | Patch env var value |
| CrashLoopBackOff (code panic) | ContainerStatus.Waiting.Reason + panic stack trace | No | Log escalation, no PR |
| ImagePullBackOff (wrong tag) | ContainerStatus.Waiting.Reason = ImagePullBackOff | Yes | Fix image tag |
| ImagePullBackOff (auth) | ContainerStatus.Waiting.Reason + auth message | No | Log escalation, no PR |

Non-remediable failures are logged with structured fields and counted in `remediator_escalations_total`. No PR is created.

---

## Prometheus metrics

Served at `:9090/metrics`.

| Metric | Type | Labels |
|---|---|---|
| `remediator_failures_detected_total` | Counter | `type` |
| `remediator_prs_opened_total` | Counter | — |
| `remediator_escalations_total` | Counter | `reason` |
| `remediator_diagnostician_latency_seconds` | Histogram | — |
| `remediator_diagnostician_errors_total` | Counter | — |

---

## Setup

### Prerequisites

- k3s cluster with a `remediator-test` namespace
- FluxCD watching a GitOps repository
- DeepSeek API key (`api.deepseek.com`)
- GitHub personal access token with repo write access

### Test environment

The remediator watches only the `remediator-test` namespace. A sample app with postgres is deployed there via a dedicated FluxCD Kustomization. To trigger failures during development:

```bash
# OOMKilled
kubectl patch deployment sample-app -n remediator-test \
  --patch '{"spec":{"template":{"spec":{"containers":[{"name":"app","resources":{"limits":{"memory":"1Mi"}}}]}}}}'

# ImagePullBackOff
kubectl set image deployment/sample-app app=nginx:does-not-exist -n remediator-test

# CrashLoopBackOff (bad config)
kubectl set env deployment/sample-app DATABASE_URL=invalid -n remediator-test
```

### Running locally

```bash
go build ./cmd/remediator/

KUBECONFIG=~/.kube/config \
DEEPSEEK_API_KEY=sk-... \
GITHUB_TOKEN=ghp_... \
GITOPS_REPO=mhever/your-gitops-repo \
./remediator
```

Without a valid kubeconfig the service starts with `NoopWatcher` and logs a warning — useful for testing config loading and metrics without a cluster.

### Deploying to k3s

```bash
# Create the namespace
kubectl create namespace remediator-system

# Create credentials (do not commit real values)
kubectl create secret generic gitops-remediator-secrets \
  --namespace=remediator-system \
  --from-literal=deepseek-api-key=sk-... \
  --from-literal=github-token=ghp_...

# Edit k8s/configmap.yaml with your repo name, then apply everything
kubectl apply -f k8s/
```

---

## Repository structure

```
gitops-remediator/
├── cmd/remediator/main.go        # Entry point, wiring, signal handling
├── internal/
│   ├── watcher/                  # client-go SharedInformer, failure detection, dedup
│   ├── collector/                # diagnostic bundle assembly, PII sanitisation
│   ├── diagnostician/            # DeepSeek R1 API call, response parsing
│   ├── patcher/                  # YAML manifest patching
│   ├── gitops/                   # git clone/commit/push, GitHub PR via REST API
│   └── metrics/                  # Prometheus metrics
├── config/                       # env var loading
├── k8s/                          # Kubernetes deployment manifests
├── testdata/                     # fixture manifests for patcher tests
├── docs/
│   ├── diagnostician-prompt.md   # prompt design, failure taxonomy, escalation logic
│   ├── pii-decision.md           # env var redaction rationale
│   └── token-usage.md            # DeepSeek token consumption (populated after live runs)
└── tasks/lessons.md              # per-phase model behaviour observations
```

---

## CI

GitHub Actions runs on every push:
- `go vet ./...`
- `go test -race ./...`
- `go mod tidy` check

---

## Related

- [homelab-mcp](https://github.com/mhever/homelab-mcp) — MCP server providing live k8s visibility via Claude Desktop. Used as a real-time monitor during remediator testing.
