// Package gitsync commits a freshly written note and pushes it, so a vault
// served from a server can sync to the user's local Obsidian (Git plugin pulls).
package gitsync

import (
	"fmt"
	"os/exec"
)

// Push runs git add/commit/push for relPath inside repoDir.
// repoDir must be a git repo with a configured remote and push credentials.
func Push(repoDir, relPath, message string) error {
	steps := [][]string{
		{"add", "--", relPath},
		{"commit", "-m", message},
		{"push"},
	}
	for _, args := range steps {
		full := append([]string{"-C", repoDir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %v failed: %w: %s", args, err, out)
		}
	}
	return nil
}
