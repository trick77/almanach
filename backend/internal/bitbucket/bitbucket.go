// Package bitbucket lists a Bitbucket Server / Data Center project's
// repositories over its REST API. net/http only, one endpoint, paged. The
// clones themselves are git's business (gitrepo).
package bitbucket

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	// caFile, when set, is the trust of every listing, read for each one.
	caFile string
}

// noRedirect: a login page redirect must read as a failure.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// New builds a Lister; a nil client is http.DefaultClient's transport with
// no redirects followed.
func New(client *http.Client) *Lister {
	if client == nil {
		client = &http.Client{CheckRedirect: noRedirect}
	}
	return &Lister{client: client}
}

// NewWithCA is a Lister trusting caFile as git does with GIT_SSL_CAINFO:
// the file alone, not the system's store, read again for each listing as
// git reads it for each command, so the two never disagree on a forge's
// certificate, a rotated bundle included. "" is New(nil). A file that
// cannot be read or holds no certificate is an error here, so a typo is a
// startup fault, not a failed listing later.
func NewWithCA(caFile string) (*Lister, error) {
	if caFile == "" {
		return New(nil), nil
	}
	if _, err := caClient(caFile); err != nil {
		return nil, err
	}
	return &Lister{caFile: caFile}, nil
}

// caClient is a client trusting caFile's certificates alone.
func caClient(caFile string) (*http.Client, error) {
	pem, err := os.ReadFile(caFile) //nolint:gosec // the operator's configured CA bundle, by design
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s: no PEM certificate in it", caFile)
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: t, CheckRedirect: noRedirect}, nil
}

// httpClient is the client of one listing.
func (l *Lister) httpClient() (*http.Client, error) {
	if l.caFile == "" {
		return l.client, nil
	}
	c, err := caClient(l.caFile)
	if err != nil {
		return nil, fmt.Errorf("BACKEND_GIT_CA_FILE: %w", err)
	}
	return c, nil
}

type page struct {
	Values []struct {
		// ID is Bitbucket's own id of the repository: it survives a
		// rename, which the slug does not.
		ID       int64  `json:"id"`
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
	client, err := l.httpClient()
	if err != nil {
		return nil, err
	}
	out := []store.ListedRepo{}
	start, live, ended := 0, 0, false
	for i := 0; i < maxPages && !ended; i++ {
		p, err := fetchPage(ctx, client, base, project, a, start)
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
					out = append(out, store.ListedRepo{Slug: v.Slug, CloneURL: c.Href, ForgeID: v.ID})
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

func fetchPage(ctx context.Context, client *http.Client, base *url.URL, project string, a gitrepo.Access, start int) (page, error) {
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
	resp, err := client.Do(req)
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
