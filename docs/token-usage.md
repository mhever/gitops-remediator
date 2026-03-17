# Token Usage

This document will be populated with real OpenRouter (DeepSeek R1) token consumption data from live runs once the remediator is deployed to the k3s cluster.

## Fields logged per call

Every API call logs the following token counts to `/var/log/remediator/diagnostician.log`:

| Field | Description |
|---|---|
| `prompt_tokens` | Tokens consumed by the system prompt + diagnostic bundle |
| `completion_tokens` | Tokens consumed by the JSON response |
| `total_tokens` | Sum of prompt + completion |

## Expected prompt size

The diagnostic bundle includes:
- Last 100 lines of container logs
- Full pod spec (sanitised) as YAML
- Pod status
- Last 5 namespace events
- Resource limits

For a typical failing pod, this is roughly 500–1500 tokens depending on log verbosity and spec complexity.

## To be filled in

After the first live remediation run, record:

| Failure type | prompt_tokens | completion_tokens | total_tokens | Notes |
|---|---|---|---|---|
| OOMKilled | | | | |
| CrashLoopBackOff | | | | |
| ImagePullBackOff | | | | |
