# PII Handling Decision

## What is redacted

Before the diagnostic bundle is transmitted to DeepSeek R1, the pod spec is sanitised:

- All environment variable **values** are replaced with `[REDACTED]`
- All `valueFrom` references (SecretKeyRef, ConfigMapKeyRef) are nullified
- Environment variable **keys** are preserved — they are not sensitive and aid root-cause analysis

This is implemented in `internal/collector/sanitize.go` and applied before any external call.

## What is NOT redacted

Container **log output** is included verbatim in the diagnostic bundle.

Log content filtering — pattern matching, entropy analysis for secrets, PII scanning — was consciously skipped. These are enterprise-grade concerns with significant implementation complexity and false-positive risk. For this project, the conscious decision is:

- The remediator only watches a dedicated test namespace (`remediator-test`)
- Sample applications in that namespace produce synthetic logs with no real PII
- The trade-off is documented here rather than implemented partially and incorrectly

If applied to a production namespace, log filtering would be a prerequisite. Tools such as [scrubadub](https://github.com/LeapBeyond/scrubadub) (Python) or entropy-based regex filters exist for this purpose.

## Why env var values but not log content

Env vars are a known, bounded attack surface — secret values are frequently placed there by Kubernetes operators via `secretKeyRef`. Redacting them is a targeted, high-value control that is easy to implement correctly via `pod.DeepCopy()`.

Log content has no structure. Filtering it reliably requires either a corpus of known patterns (brittle) or probabilistic methods (operational overhead). The risk-to-effort ratio does not justify implementation for a single-namespace homelab project.

This is a documented choice, not an oversight.
