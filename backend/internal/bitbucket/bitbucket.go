// Package bitbucket lists a Bitbucket Server / Data Center project's
// repositories over its REST API. net/http only, one endpoint, paged. The
// clones themselves are git's business (gitrepo).
package bitbucket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
)

// pageLimit is Bitbucket's page size; pageTimeout bounds one page.
const (
	pageLimit   = 100
	pageTimeout = 10 * time.Second
	maxPages    = 200
)

// Lister lists a project's repositories.
type Lister struct {
	client *http.Client
}

// New builds a Lister; a nil client is http.DefaultClient's transport with
// no redirects followed (a login page redirect must read as a failure).
func New(client *http.Client) *Lister {
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Lister{client: client}
}

type page struct {
	Values []struct {
		Slug     string `json:"slug"`
		Archived bool   `json:"archived"`
		Links    struct {
			Clone []struct {
				Href string `json:"href"`
				Name string `json:"name"`
			} `json:"clone"`
		} `json:"links"`
	} `json:"values"`
	IsLastPage    bool `json:"isLastPage"`
	NextPageStart int  `json:"nextPageStart"`
}

// ListRepos is GET /rest/api/1.0/projects/{key}/repos, every page. An
// archived repository is skipped; one without an http clone link is
// skipped with its slug in the error-free result's log line left to the
// caller. The access is the source's: bearer as a header, basic as such.
func (l *Lister) ListRepos(ctx context.Context, host, project string, a gitrepo.Access) ([]store.ListedRepo, error) {
	base, err := url.Parse(strings.TrimRight(host, "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, errors.New("host is not an http(s) URL")
	}
	out := []store.ListedRepo{}
	start, live, ended := 0, 0, false
	for i := 0; i < maxPages && !ended; i++ {
		p, err := l.page(ctx, base, project, a, start)
		if err != nil {
			return nil, err
		}
		for _, v := range p.Values {
			if v.Archived {
				continue
			}
			live++
			for _, c := range v.Links.Clone {
				if c.Name == "http" {
					out = append(out, store.ListedRepo{Slug: v.Slug, CloneURL: c.Href})
					break
				}
			}
		}
		ended = p.IsLastPage || p.NextPageStart <= start
		start = p.NextPageStart
	}
	if !ended {
		return nil, fmt.Errorf("listing did not end after %d pages", maxPages)
	}
	// Repositories without an http clone link (the instance has them
	// switched off) would otherwise read as "the project is empty" and mark
	// every row missing without a word: an error names the cause instead.
	if live > 0 && len(out) == 0 {
		return nil, fmt.Errorf("list repositories: %d repositories, none with an http clone link", live)
	}
	return out, nil
}

func (l *Lister) page(ctx context.Context, base *url.URL, project string, a gitrepo.Access, start int) (page, error) {
	ctx, cancel := context.WithTimeout(ctx, pageTimeout)
	defer cancel()
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + "/rest/api/1.0/projects/" + url.PathEscape(project) + "/repos"
	u.RawQuery = "limit=" + strconv.Itoa(pageLimit) + "&start=" + strconv.Itoa(start)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return page{}, err
	}
	req.Header.Set("Accept", "application/json")
	if h := a.Authorization(u.String()); h != "" {
		req.Header.Set("Authorization", h)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return page{}, fmt.Errorf("list repositories: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return page{}, fmt.Errorf("list repositories: read: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return page{}, fmt.Errorf("list repositories: GET %s: %d", u.Path, resp.StatusCode)
	}
	var p page
	if err := json.Unmarshal(body, &p); err != nil {
		return page{}, fmt.Errorf("list repositories: body is not a listing: %w", err)
	}
	return p, nil
}
