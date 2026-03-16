package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
)

// credentialRe matches https://<token>@ patterns in git output.
var credentialRe = regexp.MustCompile(`https://[^@\s]+@`)

// sanitizeArgs replaces credential-bearing URLs in args with a redacted form,
// so tokens don't appear in error messages.
func sanitizeArgs(args []string) []string {
	result := make([]string, len(args))
	for i, a := range args {
		result[i] = string(credentialRe.ReplaceAll([]byte(a), []byte("https://[REDACTED]@")))
	}
	return result
}

// sanitizeOutput redacts credential-bearing URLs from git command output.
func sanitizeOutput(out []byte) []byte {
	return credentialRe.ReplaceAll(out, []byte("https://[REDACTED]@"))
}

// runGit runs a git command in dir (empty string for no working directory).
// env is an optional list of extra environment variables to set (in addition
// to the current process environment). Pass nil or empty slice for no extras.
// Returns combined stdout+stderr, and a wrapped error on failure.
func runGit(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v: %w\noutput: %s", sanitizeArgs(args), err, sanitizeOutput(out))
	}
	return out, nil
}
