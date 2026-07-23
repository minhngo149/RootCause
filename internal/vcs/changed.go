// Package vcs finds which files in a git working tree are worth reviewing
// right now: anything uncommitted, plus anything committed locally that
// hasn't been pushed to the branch's upstream yet.
package vcs

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ChangedFiles returns paths, relative to root, of files that are modified
// (staged or unstaged), newly created and not yet tracked, or committed on
// the current branch but not yet present on its upstream. Deleted files are
// excluded since there is nothing left to read. If the branch has no
// upstream configured, only uncommitted files are returned.
func ChangedFiles(root string) ([]string, error) {
	if _, err := runGit(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("%s is not a git repository: %w", root, err)
	}

	seen := map[string]bool{}
	collect := func(args ...string) {
		out, err := runGit(root, args...)
		if err != nil {
			return
		}
		for _, path := range splitLines(out) {
			if path = strings.TrimSpace(path); path != "" {
				seen[path] = true
			}
		}
	}

	// Unstaged changes to tracked files.
	collect("diff", "--name-only", "--diff-filter=d")
	// Staged changes not yet committed.
	collect("diff", "--name-only", "--diff-filter=d", "--cached")
	// New files not yet tracked. Unlike `git status`, this lists individual
	// files even inside a brand-new, entirely-untracked directory, and it
	// still respects .gitignore.
	collect("ls-files", "--others", "--exclude-standard")

	if upstream, err := runGit(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		upstream = strings.TrimSpace(upstream)
		collect("diff", "--name-only", "--diff-filter=d", upstream+"...HEAD")
	}

	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	return files, nil
}

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return stdout.String(), nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
