package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// run executes git (or any command) in dir, failing the test on error.
func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
	return string(out)
}

// newRepoWithUpstream sets up a local repo with an initial commit, pushed
// to a bare "origin" remote and tracked, so ChangedFiles has something to
// diff against.
func newRepoWithUpstream(t *testing.T) string {
	t.Helper()

	remote := t.TempDir()
	run(t, remote, "git", "init", "--bare", "-b", "main")

	local := t.TempDir()
	run(t, local, "git", "init", "-b", "main")
	run(t, local, "git", "config", "user.email", "test@example.com")
	run(t, local, "git", "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(local, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, local, "git", "add", "README.md")
	run(t, local, "git", "commit", "-m", "initial")
	run(t, local, "git", "remote", "add", "origin", remote)
	run(t, local, "git", "push", "-u", "origin", "main")

	return local
}

func TestChangedFilesUncommittedAndUnpushed(t *testing.T) {
	repo := newRepoWithUpstream(t)

	// An untracked file: should show up as uncommitted.
	if err := os.WriteFile(filepath.Join(repo, "new_query.sql"), []byte("SELECT 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A committed-but-unpushed file: should show up as ahead-of-upstream.
	if err := os.WriteFile(filepath.Join(repo, "pushed_later.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "pushed_later.go")
	run(t, repo, "git", "commit", "-m", "add pushed_later.go")

	files, err := ChangedFiles(repo)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	sort.Strings(files)

	want := []string{"new_query.sql", "pushed_later.go"}
	if len(files) != len(want) {
		t.Fatalf("ChangedFiles() = %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("ChangedFiles()[%d] = %q, want %q", i, files[i], want[i])
		}
	}
}

func TestChangedFilesNewUntrackedDirectory(t *testing.T) {
	repo := newRepoWithUpstream(t)

	// A brand-new, entirely untracked directory: `git status --porcelain`
	// collapses this into one "?? internal/jobs/" line instead of listing
	// the file inside — ChangedFiles must still surface the actual file.
	if err := os.MkdirAll(filepath.Join(repo, "internal", "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "jobs", "cleanup.go"), []byte("package jobs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ChangedFiles(repo)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}

	want := filepath.Join("internal", "jobs", "cleanup.go")
	found := false
	for _, f := range files {
		if f == want {
			found = true
		}
	}
	if !found {
		t.Errorf("ChangedFiles() = %v, want it to include %q", files, want)
	}
}

func TestChangedFilesCleanRepo(t *testing.T) {
	repo := newRepoWithUpstream(t)

	files, err := ChangedFiles(repo)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	if len(files) != 0 {
		t.Errorf("ChangedFiles() on a clean, fully-pushed repo = %v, want empty", files)
	}
}

func TestChangedFilesNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := ChangedFiles(dir); err == nil {
		t.Error("expected error for a non-git directory, got nil")
	}
}
