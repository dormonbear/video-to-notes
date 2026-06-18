// Package gitsync commits a freshly written note and pushes it, so a vault
// served from a server can sync to the user's local Obsidian (Git plugin pulls).
package gitsync

import (
	"fmt"
	"os/exec"
)

// Push runs git add/commit/push for relPath inside repoDir.
// repoDir must be a git repo with a configured remote and push credentials.
// If push is rejected because the remote moved ahead (e.g. blog edits pushed
// elsewhere), it rebases local commits onto the remote and pushes again. New
// notes use unique filenames, so the rebase never conflicts.
func Push(repoDir, relPath, message string) error {
	for _, args := range [][]string{
		{"add", "--", relPath},
		{"commit", "-m", message},
	} {
		if out, err := run(repoDir, args...); err != nil {
			return fmt.Errorf("git %v failed: %w: %s", args, err, out)
		}
	}

	if _, err := run(repoDir, "push"); err != nil {
		// Remote moved ahead: integrate then retry once.
		if out, err := run(repoDir, "pull", "--rebase"); err != nil {
			return fmt.Errorf("git pull --rebase failed: %w: %s", err, out)
		}
		if out, err := run(repoDir, "push"); err != nil {
			return fmt.Errorf("git push (after rebase) failed: %w: %s", err, out)
		}
	}
	return nil
}

func run(repoDir string, args ...string) ([]byte, error) {
	full := append([]string{"-C", repoDir}, args...)
	return exec.Command("git", full...).CombinedOutput()
}
