package gitops

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// credentialRe matches https://<token>@ patterns in git output.
var credentialRe = regexp.MustCompile(`https://[^@\s]+@`)

// sanitizeArgs replaces credential-bearing URLs in args with a redacted form,
// so tokens don't appear in error messages.
func sanitizeArgs(args []string) []string {
	result := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "https://") && strings.Contains(a, "@") {
			if idx := strings.Index(a, "@"); idx != -1 {
				result[i] = "https://[REDACTED]" + a[idx:]
			} else {
				result[i] = a
			}
		} else {
			result[i] = a
		}
	}
	return result
}

// sanitizeOutput redacts credential-bearing URLs from git command output.
func sanitizeOutput(out []byte) []byte {
	return credentialRe.ReplaceAll(out, []byte("https://[REDACTED]@"))
}

// runGit runs a git command in dir (empty string for no working directory).
// Returns combined stdout+stderr, and a wrapped error on failure.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v: %w\noutput: %s", sanitizeArgs(args), err, sanitizeOutput(out))
	}
	return out, nil
}
