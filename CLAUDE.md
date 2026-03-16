# gitops-remediator — Orchestration Guide

## Agent Roles

- **Orchestrator (Claude Code / Sonnet):** Routes tasks, runs `go test ./...`, assembles context, writes phase documentation. Does **not** write production Go code.
- **claude-coder (Sonnet):** Writes all Go implementation + `_test.go` files together per spec. Iterates until tests pass. See `.claude/agents/claude-coder.md`.
- **claude-reviewer (Opus):** Reviews all coder output before the orchestrator accepts it. See `.claude/agents/claude-reviewer.md`.
- **DeepSeek R1:** Runtime only — called by the running remediator service. Not a build-time agent.

---

## Workflow

1. **Orchestrator** breaks the phase task into a spec for claude-coder.
2. **claude-coder** implements Go code + tests. Returns modified files + `go test` output.
3. **claude-reviewer** reviews the output. Returns explicit **PASS** or **FAIL** with findings.
4. If FAIL: feed reviewer output back to claude-coder. Repeat from step 2.
5. If PASS: orchestrator accepts, runs final `go test ./...` in the repo, advances phase.

---

## Hard Rules

- Orchestrator never writes production Go code. Route to claude-coder.
- claude-reviewer must be invoked after every coder output before acceptance.
- `go test ./...` must pass before moving to the next phase.
- The diagnostician prompt (system + user template in `internal/diagnostician/`) requires **explicit orchestrator approval** to modify. Coder cannot change it unilaterally.
- Never modify Phase N+1 code while working in Phase N.
- `tasks/lessons.md` is updated at the end of each phase with model behavior observations, bugs found by reviewer, and environment issues.

---

## Phase Timeout (Termination Clause)

At the start of each phase, the orchestrator writes the current Unix timestamp:

```bash
date +%s > tasks/phase-start.txt
```

Before **every** agent iteration, check elapsed time:

```bash
(( $(date +%s) - $(cat tasks/phase-start.txt) > 3600 ))
```

If this evaluates to true (> 60 minutes elapsed):
1. **Halt immediately.** Do not start another iteration.
2. Log a structured entry to `tasks/lessons.md`:
   - Phase number
   - Elapsed seconds
   - Last completed step
   - Blocker description
3. Surface the blockage to the user for manual intervention.

This applies to orchestrator loops and all coder/reviewer iteration cycles.

---

## Halt on Failure

If a subagent step fails **twice consecutively** on the same task, **stop**. Do not loop blindly. Log the failure to `tasks/lessons.md` and escalate to the user with the exact error output.

---

## Failure Taxonomy (for reference)

| Failure | Remediable | Patch Target |
|---|---|---|
| OOMKilled | Yes | Increase `resources.limits.memory` |
| CrashLoopBackOff (bad config) | Yes | Patch env var or secret ref |
| CrashLoopBackOff (code panic) | No | Log escalation, no PR |
| ImagePullBackOff (wrong tag) | Yes | Fix image tag |
| ImagePullBackOff (auth) | No | Log escalation, no PR |

---

## Current Phase

**Phase 0 — Scaffold** (started 2026-03-16)
