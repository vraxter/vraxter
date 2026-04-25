package utils

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CaptureGitContext creates an autonomous fingerprint of the user's active workspace.
// It returns a summarized diff or state of the CWD if it is a git repository.
func CaptureGitContext(wd string) string {
	if wd == "" {
		cwd, err := os.Getwd()
		if err == nil {
			wd = cwd
		} else {
			return ""
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "-s")
	cmd.Dir = wd
	statusBytes, err := cmd.Output()
	if err != nil || len(statusBytes) == 0 {
		return "" // Not a git repo, no diffs, or timeout
	}
	
	status := strings.TrimSpace(string(statusBytes))
	if status == "" {
		return "[Git Context: Working tree clean. Repository exists.]"
	}

	diffCmd := exec.CommandContext(ctx, "git", "diff", "--unified=1")
	diffCmd.Dir = wd
	diffBytes, _ := diffCmd.Output()
	diffOutput := strings.TrimSpace(string(diffBytes))

	// Truncate massively if it's over 40kb to protect LLM context windows
	if len(diffOutput) > 40000 {
		diffOutput = diffOutput[:40000] + "\n...[TRUNCATED]"
	}

	return "WORKSPACE GIT CONTEXT:\n" + status + "\n\nUNCOMMITTED CHANGES:\n" + diffOutput
}
