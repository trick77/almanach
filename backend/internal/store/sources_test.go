package store

import (
	"context"
	"errors"
	"testing"
)

func bitbucketSource(key string) ProjectSource {
	return ProjectSource{ProjectKey: key, Kind: SourceBitbucket, Host: "https://forge.example.invalid", TokenAuth: TokenBearer, TokenEnc: []byte("sealed"), AutoDescribe: true}
}

func staticSource(key string) ProjectSource {
	return ProjectSource{ProjectKey: key, Kind: SourceStatic, TokenAuth: TokenBasic, TokenUser: "x-access-token", AutoDescribe: true}
}

func TestSources_CreateListUpdateDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSource(ctx, staticSource("tools")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSource(ctx, staticSource("tools")); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: err = %v, want ErrExists", err)
	}

	rows, err := s.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ProjectKey != "SHOP" || rows[1].ProjectKey != "tools" {
		t.Fatalf("Sources = %+v, want SHOP, tools by key", rows)
	}
	if !rows[0].HasToken() || rows[1].HasToken() || rows[1].Host != "" {
		t.Fatalf("token/host: SHOP token=%v tools token=%v host=%q", rows[0].HasToken(), rows[1].HasToken(), rows[1].Host)
	}

	// Keep the token when setToken is false, clear it with an empty one.
	ok, err := s.UpdateSourceAccess(ctx, "SHOP", "https://other.example.invalid", TokenBasic, "me", nil, false)
	if err != nil || !ok {
		t.Fatalf("update: %v %v", ok, err)
	}
	src, _, _ := s.SourceByKey(ctx, "SHOP")
	if src.Host != "https://other.example.invalid" || src.TokenAuth != TokenBasic || src.TokenUser != "me" || !src.HasToken() {
		t.Fatalf("after update: %+v", src)
	}
	if _, err := s.UpdateSourceAccess(ctx, "SHOP", src.Host, src.TokenAuth, src.TokenUser, nil, true); err != nil {
		t.Fatal(err)
	}
	src, _, _ = s.SourceByKey(ctx, "SHOP")
	if src.HasToken() {
		t.Fatal("token not cleared")
	}
	if ok, _ := s.UpdateSourceAccess(ctx, "nope", "", TokenBearer, "", nil, false); ok {
		t.Fatal("update of an unknown source reported ok")
	}

	if err := s.AddSourceRepo(ctx, SourceRepo{ProjectKey: "tools", Slug: "cli", CloneURL: "https://example.invalid/cli.git"}); err != nil {
		t.Fatal(err)
	}
	ok, err = s.DeleteSource(ctx, "tools")
	if err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if n := count(t, s, "source_repos", "project_key = 'tools'"); n != 0 {
		t.Fatalf("repos survived the cascade: %d", n)
	}
	if _, found, _ := s.SourceByKey(ctx, "tools"); found {
		t.Fatal("deleted source still found")
	}
}

func TestSourceRepos_ManualAndStates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, staticSource("tools")); err != nil {
		t.Fatal(err)
	}
	r := SourceRepo{ProjectKey: "tools", Slug: "cli", CloneURL: "https://example.invalid/cli.git", Branch: "main"}
	if err := s.AddSourceRepo(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSourceRepo(ctx, r); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate repo: %v", err)
	}
	repos, _ := s.SourceRepos(ctx, "tools")
	if len(repos) != 1 || repos[0].State != RepoPending || repos[0].Origin != OriginManual || repos[0].Branch != "main" {
		t.Fatalf("after add: %+v", repos)
	}

	jobs, err := s.SyncJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Source.ProjectKey != "tools" || jobs[0].Repo.Slug != "cli" || jobs[0].Repo.ProjectKey != "tools" {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}

	if err := s.MarkRepoSyncing(ctx, "tools", "cli"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRepoFailed(ctx, "tools", "cli", "boom"); err != nil {
		t.Fatal(err)
	}
	repos, _ = s.SourceRepos(ctx, "tools")
	if repos[0].State != RepoFailed || repos[0].LastError != "boom" || repos[0].LastSyncedAt != nil {
		t.Fatalf("after fail: %+v", repos[0])
	}
	if err := s.MarkRepoReady(ctx, "tools", "cli", "master", "abc123"); err != nil {
		t.Fatal(err)
	}
	repos, _ = s.SourceRepos(ctx, "tools")
	if repos[0].State != RepoReady || repos[0].LastError != "" || repos[0].HeadCommit != "abc123" || repos[0].Branch != "master" || repos[0].LastSyncedAt == nil {
		t.Fatalf("after ready: %+v", repos[0])
	}

	n, err := s.RequestSync(ctx, "tools", "")
	if err != nil || n != 1 {
		t.Fatalf("RequestSync = %d, %v", n, err)
	}
	rows, _ := s.Sources(ctx)
	if rows[0].Repos != 1 || rows[0].Active != 1 || rows[0].Ready != 0 {
		t.Fatalf("counts after request: %+v", rows[0])
	}

	ok, err := s.DeleteSourceRepo(ctx, "tools", "cli")
	if err != nil || !ok {
		t.Fatalf("delete repo: %v %v", ok, err)
	}
	if ok, _ := s.DeleteSourceRepo(ctx, "tools", "cli"); ok {
		t.Fatal("second delete reported ok")
	}
}

func TestApplyListing_ReconcilesListedRows(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{"ui", "https://forge.example.invalid/scm/shop/ui.git"}, {"api", "https://forge.example.invalid/scm/shop/api.git"}})
	if err != nil || res.Added != 2 || res.Missing != 0 {
		t.Fatalf("first listing: %+v, %v", res, err)
	}
	if err := s.MarkRepoReady(ctx, "SHOP", "ui", "main", "aaa"); err != nil {
		t.Fatal(err)
	}

	// api vanishes, infra appears, ui's URL changes but its state stays.
	res, err = s.ApplyListing(ctx, "SHOP", []ListedRepo{{"ui", "https://forge.example.invalid/scm/shop/ui-2.git"}, {"infra", "https://forge.example.invalid/scm/shop/infra.git"}})
	if err != nil || res.Added != 1 || res.Missing != 1 {
		t.Fatalf("second listing: %+v, %v", res, err)
	}
	by := map[string]SourceRepo{}
	repos, _ := s.SourceRepos(ctx, "SHOP")
	for _, r := range repos {
		by[r.Slug] = r
	}
	if by["api"].State != RepoMissing || by["infra"].State != RepoPending || by["ui"].State != RepoReady || by["ui"].CloneURL != "https://forge.example.invalid/scm/shop/ui-2.git" {
		t.Fatalf("after second listing: %+v", by)
	}

	// A missing row the listing names again is pending once more, and not
	// counted as added. Missing rows are no sync jobs.
	jobs, _ := s.SyncJobs(ctx)
	if len(jobs) != 2 {
		t.Fatalf("jobs with a missing row: %d, want 2", len(jobs))
	}
	res, err = s.ApplyListing(ctx, "SHOP", []ListedRepo{{"ui", "u"}, {"infra", "i"}, {"api", "a"}})
	if err != nil || res.Added != 0 || res.Missing != 0 {
		t.Fatalf("third listing: %+v, %v", res, err)
	}
	repos, _ = s.SourceRepos(ctx, "SHOP")
	for _, r := range repos {
		if r.Slug == "api" && r.State != RepoPending {
			t.Fatalf("api back from missing: %+v", r)
		}
	}
	// RequestSync leaves a missing row alone.
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{"ui", "u"}}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.RequestSync(ctx, "SHOP", "")
	if n != 1 {
		t.Fatalf("RequestSync moved %d rows, want 1 (ui only)", n)
	}

	if err := s.MarkSourceListed(ctx, "SHOP", "401 from the API"); err != nil {
		t.Fatal(err)
	}
	src, _, _ := s.SourceByKey(ctx, "SHOP")
	if src.LastError != "401 from the API" || src.LastListedAt != nil {
		t.Fatalf("after failed listing: %+v", src)
	}
	if err := s.MarkSourceListed(ctx, "SHOP", ""); err != nil {
		t.Fatal(err)
	}
	src, _, _ = s.SourceByKey(ctx, "SHOP")
	if src.LastError != "" || src.LastListedAt == nil {
		t.Fatalf("after good listing: %+v", src)
	}
}
