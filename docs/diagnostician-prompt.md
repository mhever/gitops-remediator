# Diagnostician Prompt Design

## Overview

The diagnostician sends the assembled `DiagnosticBundle` to DeepSeek R1 (`deepseek-reasoner`) via the OpenAI-compatible chat completions API. The prompt is split into a system message and a user message.

## System Prompt

The system prompt instructs the model to return **only** a JSON object with no preamble, markdown, or chain-of-thought visible in the response. It specifies the exact required fields and their constraints.

Key rules enforced by the prompt:
- `remediable: false` is required when a panic stack trace is present in logs, when the failure is an auth error, or when the root cause cannot be fixed by changing a manifest field
- `patch_type` and `patch_value` are required when `remediable: true`
- `patch_value` for `memory_limit` must be a valid Kubernetes quantity (e.g. `256Mi`)
- `patch_value` for `image_tag` must be only the tag portion (e.g. `v1.2.3`)
- `patch_value` for `env_var` must be in `KEY=VALUE` format

The prompt is defined in `internal/diagnostician/prompt.go` and is **locked** — modifications require explicit orchestrator approval to prevent unreviewed changes to the model's decision-making logic.

## Failure Taxonomy

| Failure | Remediable | Patch Type | Example patch_value |
|---|---|---|---|
| OOMKilled | Yes | `memory_limit` | `256Mi` |
| CrashLoopBackOff (bad config) | Yes | `env_var` | `DATABASE_URL=postgres://host:5432/db` |
| CrashLoopBackOff (code panic) | No | — | escalation_reason required |
| ImagePullBackOff (wrong tag) | Yes | `image_tag` | `v1.2.3` |
| ImagePullBackOff (auth) | No | — | escalation_reason required |

## Escalation Logic

When `remediable: false`, the diagnostician:
1. Logs a structured entry via `slog.Warn` with fields `failure_type`, `root_cause`, `escalation_reason`, `action: "escalated"`
2. Returns the `Diagnosis` with no error — escalation is a valid outcome, not a failure
3. The pipeline increments `remediator_escalations_total` with the normalised reason label

No GitHub PR is created for non-remediable failures.

## Logging

Every API call writes two entries to the diagnostician log file (default `/var/log/remediator/diagnostician.log`):

```
=== 2026-03-16T18:00:00Z PROMPT ===
<full system + user prompt>

=== 2026-03-16T18:00:00Z RESPONSE ===
<raw JSON from DeepSeek>
tokens: prompt=N completion=N total=N
```

Log writes are non-fatal — a write failure is logged to stderr but does not interrupt the pipeline.

## Code-fence Stripping

DeepSeek R1 occasionally wraps its JSON response in markdown code fences (` ```json ... ``` `) despite explicit instructions not to. The diagnostician strips these with `strings.TrimPrefix` / `strings.TrimSuffix` before unmarshalling.

## R1 Reasoning Trace

The `reasoning_summary` field in the response captures R1's condensed reasoning chain. It is included in the GitHub PR body under "Agent Reasoning", providing a human-readable explanation of the model's decision alongside the automated patch.
