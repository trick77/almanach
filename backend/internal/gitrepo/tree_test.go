package gitrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bigSize is past the reader's 64 KiB buffer, so a refusal and a read both
// cross refills of the stream.
const bigSize = 200_000

// history is a bare repository with two commits on main: the first holds a
// file with an umlaut in its path, one with a newline in its name, an
// executable, a symlink, a submodule pointer and a file of bigSize bytes;
// the second edits one file, deletes another and adds a third. Returns the
// bare path and both commits.
func history(t *testing.T) (dir, first, second string) {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	gitRun(t, base, "init", "-q", "-b", "main", work)
	write := func(path, body string, mode os.FileMode) {
		full := filepath.Join(work, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/für.md", "Umlaut\n", 0o600)
	write("odd\nname.txt", "newline\n", 0o600)
	write("run.sh", "#!/bin/sh\n", 0o700)
	write("big.txt", strings.Repeat("x", bigSize), 0o600)
	write("gone.txt", "bye\n", 0o600)
	if err := os.Symlink("run.sh", filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", ".")
	// A submodule pointer without a submodule: the tree entry is all a
	// reader ever sees of one.
	gitRun(t, work, "update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,vendor/sub")
	commit := func(msg string) string {
		gitRun(t, work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", msg)
		return gitRun(t, work, "rev-parse", "HEAD")
	}
	first = commit("first")
	write("docs/für.md", "Umlaut, edited\n", 0o600)
	write("new.go", "package x\n", 0o600)
	gitRun(t, work, "rm", "-q", "gone.txt")
	// By name: "add ." would drop the pointer, which has no directory.
	gitRun(t, work, "add", "docs/für.md", "new.go")
	second = commit("second")
	dir = filepath.Join(base, "remote.git")
	gitRun(t, base, "clone", "-q", "--bare", work, dir)
	return dir, first, second
}

func TestListEntries_ModesSizesUmlauts(t *testing.T) {
	c := client(t)
	dir, first, _ := history(t)
	entries, err := c.ListEntries(context.Background(), dir, first)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Path] = e
	}
	if e := got["docs/für.md"]; e.Mode != "100644" || e.Size != 7 || !Indexable(e.Mode) {
		t.Errorf("umlaut file = %+v, want 100644 of 7 bytes under its own name", e)
	}
	if e := got["odd\nname.txt"]; e.Size != 8 {
		t.Errorf("newline file = %+v, want listed under its own name", e)
	}
	if e := got["run.sh"]; e.Mode != "100755" || !Indexable(e.Mode) {
		t.Errorf("executable = %+v", e)
	}
	if e := got["big.txt"]; e.Size != bigSize {
		t.Errorf("big.txt size = %d", e.Size)
	}
	if e := got["link"]; e.Mode != "120000" || Indexable(e.Mode) {
		t.Errorf("symlink = %+v, want 120000, not indexable", e)
	}
	if e := got["vendor/sub"]; e.Mode != "160000" || e.Size != 0 || Indexable(e.Mode) {
		t.Errorf("submodule = %+v, want 160000 of size 0, not indexable", e)
	}
	if _, err := c.ListEntries(context.Background(), dir, strings.Repeat("0", 40)); err == nil {
		t.Error("listing a commit the clone does not have succeeded")
	}
}

func TestChangedEntries_EditDeleteAdd(t *testing.T) {
	c := client(t)
	dir, first, second := history(t)
	changes, err := c.ChangedEntries(context.Background(), dir, first, second)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range changes {
		got[e.Path] = e
	}
	if len(got) != 3 {
		t.Fatalf("changes = %+v, want three", changes)
	}
	if e := got["docs/für.md"]; e.Deleted || e.Mode != "100644" || e.Size != 15 {
		t.Errorf("edited = %+v", e)
	}
	if e := got["gone.txt"]; !e.Deleted || e.Mode != "" || e.Size != 0 {
		t.Errorf("deleted = %+v", e)
	}
	if e := got["new.go"]; e.Deleted || e.Size != 10 {
		t.Errorf("added = %+v", e)
	}
	if none, err := c.ChangedEntries(context.Background(), dir, second, second); err != nil || len(none) != 0 {
		t.Errorf("no change = %+v %v, want an empty list", none, err)
	}
}

func TestAncestryCommitsAndTime(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	dir, first, second := history(t)
	if ok, err := c.IsAncestor(ctx, dir, first, second); err != nil || !ok {
		t.Errorf("first before second: %v %v", ok, err)
	}
	if ok, err := c.IsAncestor(ctx, dir, second, first); err != nil || ok {
		t.Errorf("second before first: %v %v", ok, err)
	}
	missing := strings.Repeat("0", 40)
	if _, err := c.IsAncestor(ctx, dir, missing, second); err == nil {
		t.Error("a missing commit read as not an ancestor; want an error")
	}
	if ok, err := c.HasCommit(ctx, dir, first); err != nil || !ok {
		t.Errorf("HasCommit(first) = %v %v", ok, err)
	}
	if ok, err := c.HasCommit(ctx, dir, missing); err != nil || ok {
		t.Errorf("HasCommit(missing) = %v %v", ok, err)
	}
	at, err := c.CommitTime(ctx, dir, second)
	if err != nil || at.Location() != time.UTC || time.Since(at) > time.Hour || time.Since(at) < -time.Minute {
		t.Errorf("CommitTime = %v %v, want about now in UTC", at, err)
	}
	if _, err := c.CommitTime(ctx, dir, missing); err == nil {
		t.Error("CommitTime of a missing commit succeeded")
	}
}

// A folder that is no repository fails every read, even inside another
// repository: git must never walk up to a parent's.
func TestReads_PinnedToTheClone(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	dir, first, second := history(t)
	notRepo := filepath.Join(dir, "objects")
	if _, err := c.HasCommit(ctx, notRepo, first); err == nil {
		t.Error("HasCommit in a folder that is no repository succeeded")
	}
	if _, err := c.ListEntries(ctx, notRepo, first); err == nil {
		t.Error("ListEntries in a folder that is no repository succeeded")
	}
	for name, err := range map[string]error{
		"ListEntries":    func() error { _, err := c.ListEntries(ctx, dir, "--output=x"); return err }(),
		"ChangedEntries": func() error { _, err := c.ChangedEntries(ctx, dir, first[:7], second); return err }(),
		"IsAncestor":     func() error { _, err := c.IsAncestor(ctx, dir, first, "HEAD"); return err }(),
		"HasCommit":      func() error { _, err := c.HasCommit(ctx, dir, "-v"); return err }(),
		"CommitTime":     func() error { _, err := c.CommitTime(ctx, dir, ""); return err }(),
	} {
		if !errors.Is(err, ErrBadSHA) {
			t.Errorf("%s with no full commit id: err = %v, want ErrBadSHA", name, err)
		}
	}
}

func TestReader_ReadsRefusesAndStaysInStep(t *testing.T) {
	c := client(t)
	dir, first, second := history(t)
	r, err := c.NewReader(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := r.ReadFile(second, "docs/für.md", 0); err != nil || string(body) != "Umlaut, edited\n" {
		t.Fatalf("read = %q %v", body, err)
	}
	var tooLarge *TooLargeError
	if _, err := r.ReadFile(first, "big.txt", 1000); !errors.As(err, &tooLarge) || tooLarge.Size != bigSize {
		t.Fatalf("over the limit: err = %v, want TooLargeError of %d", err, bigSize)
	}
	if body, err := r.ReadFile(first, "big.txt", 0); err != nil || len(body) != bigSize {
		t.Fatalf("big read = %d bytes %v", len(body), err)
	}
	if _, err := r.ReadFile(first, "docs", 0); err == nil || !strings.Contains(err.Error(), "tree") {
		t.Fatalf("a directory: err = %v, want refused as a tree", err)
	}
	if _, err := r.ReadFile(second, "gone.txt", 0); !errors.Is(err, errNoObject) {
		t.Fatalf("a deleted path: err = %v, want no such object", err)
	}
	if _, err := r.ReadFile("HEAD", "run.sh", 0); !errors.Is(err, ErrBadSHA) {
		t.Fatalf("a ref for a commit: err = %v, want ErrBadSHA", err)
	}
	// Every refusal consumed its answer: the next read is still this file's.
	if body, err := r.ReadFile(first, "run.sh", 100); err != nil || string(body) != "#!/bin/sh\n" {
		t.Fatalf("after the refusals = %q %v", body, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := r.ReadFile(first, "run.sh", 0); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("read after close: err = %v", err)
	}
}

// A newline in a path is asked by argument, with the batch's refusals.
func TestReader_NewlinePathByArgument(t *testing.T) {
	c := client(t)
	dir, first, second := history(t)
	r, err := c.NewReader(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if body, err := r.ReadFile(first, "odd\nname.txt", 0); err != nil || string(body) != "newline\n" {
		t.Fatalf("newline path = %q %v", body, err)
	}
	var tooLarge *TooLargeError
	if _, err := r.ReadFile(first, "odd\nname.txt", 3); !errors.As(err, &tooLarge) || tooLarge.Size != 8 {
		t.Fatalf("newline path over the limit: err = %v", err)
	}
	if _, err := r.ReadFile(second, "no\nsuch", 0); !errors.Is(err, errNoObject) {
		t.Fatalf("missing newline path: err = %v, want no such object", err)
	}
	if body, err := r.ReadFile(first, "run.sh", 0); err != nil || string(body) != "#!/bin/sh\n" {
		t.Fatalf("batch after the slow way = %q %v", body, err)
	}
	if _, err := c.readByArgument(context.Background(), dir, first+":docs", 0); err == nil || !strings.Contains(err.Error(), "tree") {
		t.Fatalf("a directory by argument: err = %v, want refused as a tree", err)
	}
}

func TestReader_CancelledStepBreaksIt(t *testing.T) {
	c := client(t)
	dir, first, _ := history(t)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := c.NewReader(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.ReadFile(first, "run.sh", 0); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := r.ReadFile(first, "run.sh", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("read on a cancelled step: err = %v", err)
	}
	// A read that reaches the dead process marks the reader broken, once and
	// for all.
	if _, err := r.read(first+":run.sh", 0); !errors.Is(err, ErrReaderBroken) {
		t.Fatalf("read past the kill: err = %v, want ErrReaderBroken", err)
	}
	r.ctx = context.Background()
	if _, err := r.ReadFile(first, "run.sh", 0); !errors.Is(err, ErrReaderBroken) {
		t.Fatalf("after breaking: err = %v, want ErrReaderBroken", err)
	}
}
