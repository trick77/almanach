package clone

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
)

// fakeWrites records the commit and push a brief makes of a clone.
type fakeWrites struct {
	unchanged bool
	// remote is where the branch stands on the remote, "" for nowhere.
	remote string
	dir    string
	base   string
	pushed string
	access gitrepo.Access
}

func (w *fakeWrites) RemoteBranch(context.Context, string, string, string, gitrepo.Access) (string, error) {
	return w.remote, nil
}

// AuthorEmail knows the commits whose name says they are Almanach's.
func (w *fakeWrites) AuthorEmail(_ context.Context, _, sha string) (string, error) {
	if strings.HasPrefix(sha, "almanach-") {
		return pushAuthor.Email, nil
	}
	return "", nil
}

func (w *fakeWrites) HeadSHA(_ context.Context, dir, branch string) (string, error) {
	w.dir = dir
	return "head-of-" + branch, nil
}

func (w *fakeWrites) CommitFile(_ context.Context, _, base, path string, content []byte, _ string, a gitrepo.Author) (string, bool, error) {
	w.base = base
	if w.unchanged {
		return base, false, nil
	}
	return "commit(" + path + "=" + string(content) + " by " + a.Name + ")", true, nil
}

func (w *fakeWrites) Push(_ context.Context, _, cloneURL, sha, branch, expect string, a gitrepo.Access) error {
	w.pushed, w.access = cloneURL+" "+sha+" -> "+branch+" leased "+expect, a
	return nil
}

// sourceOf answers the push's reads of one source and its repositories.
type sourceOf struct {
	src   store.ProjectSource
	repos []store.SourceRepo
}

func (s sourceOf) SourceByKey(_ context.Context, key string) (store.ProjectSource, bool, error) {
	return s.src, key == s.src.ProjectKey, nil
}

func (s sourceOf) SourceRepoByName(_ context.Context, _, slug string) (store.SourceRepo, bool, error) {
	for _, r := range s.repos {
		if r.Slug == slug {
			return r, true, nil
		}
	}
	return store.SourceRepo{}, false, nil
}

// A brief is committed on the clone's default branch head and pushed with
// the source's token to its origin, under the clone's lock; one the
// default branch already holds is not pushed.
func TestPushFile(t *testing.T) {
	s, g, k := newFakeStore(), newFakeGit(), testKey(t)
	sealed, _ := k.Seal("tok")
	src := store.ProjectSource{Kind: store.SourceBitbucket, Host: "https://forge.example.invalid", TokenEnc: sealed, TokenAuth: store.TokenBearer}
	j := job("tools", "svc", "https://forge.example.invalid/scm/tools/svc.git", "main", "h1", src)
	s.jobs = []store.SyncJob{j}
	w := &fakeWrites{}
	code := NewCode(poller(s, g, &fakeLister{}, k), nil).WithWrites(w, sourceOf{src: j.Source, repos: []store.SourceRepo{j.Repo}})

	commit, changed, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "", ".agents/PROJECT.md", []byte("# x"), "m")
	if err != nil || !changed {
		t.Fatalf("PushFile = %q, %v, %v", commit, changed, err)
	}
	if w.dir != dirOf("tools", "svc") || w.base != "head-of-main" {
		t.Errorf("committed in %q on %q", w.dir, w.base)
	}
	if want := "https://forge.example.invalid/scm/tools/svc.git " + commit + " -> feature/x leased "; w.pushed != want {
		t.Errorf("pushed %q, want %q", w.pushed, want)
	}
	if !strings.Contains(commit, "by Almanach") || w.access.Token != "tok" || w.access.Origin != "https://forge.example.invalid" {
		t.Errorf("commit %q access %+v", commit, w.access)
	}

	// The branch where Almanach left it: replaced, leased to it.
	w.remote, w.pushed = "c1", ""
	if _, _, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "c1", ".agents/PROJECT.md", []byte("# x"), "m"); err != nil ||
		!strings.HasSuffix(w.pushed, "leased c1") {
		t.Errorf("own branch: err %v pushed %q", err, w.pushed)
	}
	// Someone pushed there since: nothing pushed.
	w.remote, w.pushed = "theirs", ""
	if _, _, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "c1", ".agents/PROJECT.md", []byte("# x"), "m"); !errors.Is(err, ErrBranchTaken) || w.pushed != "" {
		t.Errorf("taken branch: err %v pushed %q", err, w.pushed)
	}
	// Almanach's own push whose record was lost: still its own.
	w.remote, w.pushed = "almanach-lost", ""
	if _, _, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "c1", ".agents/PROJECT.md", []byte("# x"), "m"); err != nil ||
		!strings.HasSuffix(w.pushed, "leased almanach-lost") {
		t.Errorf("own unrecorded push: err %v pushed %q", err, w.pushed)
	}

	// The default branch holds the file: nothing on a branch of someone
	// else's or none; a branch of its own goes back to the default branch.
	w.unchanged = true
	for _, remote := range []string{"", "theirs"} {
		w.remote, w.pushed = remote, ""
		if got, changed, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "c1", ".agents/PROJECT.md", nil, "m"); err != nil || changed || got != "" || w.pushed != "" {
			t.Errorf("unchanged, branch %q: got %q changed %v err %v pushed %q", remote, got, changed, err, w.pushed)
		}
	}
	w.remote, w.pushed = "c1", ""
	got, changed, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "c1", ".agents/PROJECT.md", nil, "m")
	if err != nil || changed || got != "head-of-main" || !strings.Contains(w.pushed, "head-of-main -> feature/x leased c1") {
		t.Errorf("unchanged, own branch: got %q changed %v err %v pushed %q", got, changed, err, w.pushed)
	}
}

// No token, a token bound to another host, a clone not synced yet: the
// push fails before git runs, saying why.
func TestPushFile_RefusedBeforeGit(t *testing.T) {
	k := testKey(t)
	sealed, _ := k.Seal("tok")
	withToken := store.ProjectSource{Kind: store.SourceBitbucket, Host: "https://forge.example.invalid", TokenEnc: sealed, TokenAuth: store.TokenBearer}
	for name, c := range map[string]struct {
		src     store.ProjectSource
		url     string
		head    string
		wantErr string
	}{
		"no token":     {store.ProjectSource{Kind: store.SourceBitbucket, Host: "https://forge.example.invalid"}, "https://forge.example.invalid/x.git", "h1", "no token"},
		"other host":   {withToken, "https://elsewhere.example.invalid/x.git", "h1", "token withheld"},
		"never synced": {withToken, "https://forge.example.invalid/x.git", "", "no head yet"},
	} {
		t.Run(name, func(t *testing.T) {
			s, g := newFakeStore(), newFakeGit()
			j := job("tools", "svc", c.url, "main", c.head, c.src)
			s.jobs = []store.SyncJob{j}
			w := &fakeWrites{}
			code := NewCode(poller(s, g, &fakeLister{}, k), nil).WithWrites(w, sourceOf{src: j.Source, repos: []store.SourceRepo{j.Repo}})
			_, _, err := code.PushFile(context.Background(), "tools", "svc", "feature/x", "", ".agents/PROJECT.md", nil, "m")
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || w.pushed != "" || w.dir != "" {
				t.Errorf("err %v, git touched %q %q; want %q", err, w.dir, w.pushed, c.wantErr)
			}
		})
	}
}
