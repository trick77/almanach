package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/logging"
	"golang.org/x/oauth2"
)

var (
	// ErrNotDiscovered is a sign-in attempted before the provider answered
	// discovery.
	ErrNotDiscovered = errors.New("oidc provider not discovered yet")
	// ErrMisconfigured is a discovery answer that will not change by
	// asking again: the provider disagrees with the configuration.
	ErrMisconfigured = errors.New("oidc provider misconfigured")
)

// DiscoverTimeout bounds one discovery attempt: the boot context has no
// deadline, and an unreachable provider would otherwise hang it.
const DiscoverTimeout = 30 * time.Second

// Where a sign-in lands when there is none to offer: a full-page
// navigation gets a page that halts with a button, not a JSON body.
const (
	// UnavailableURL: the provider has not answered discovery yet.
	UnavailableURL = "/?auth_error=provider_unavailable"
	// MisconfiguredURL: it answered, and disagreed with the configuration.
	MisconfiguredURL = "/?auth_error=provider_misconfigured"
)

// Provider is kit's OIDC flow once discovery has answered. A provider that
// cannot be reached (a transport error, a timeout, a 5xx) is a warning,
// never a fault: the webhook, the ping and the probes must not depend on
// it, and boot never waits for it. Until it answers, the login lands on
// UnavailableURL and the gated /api answers 401 as ever; Run keeps trying
// in the background. An answer that is configuration, not reachability (an
// issuer that does not match, a 4xx, a 200 that is no discovery document),
// is a fault: logged at error level once, never retried, the login landing
// on MisconfiguredURL until a restart with a corrected configuration.
type Provider struct {
	cfg      kitauth.OIDCConfig
	log      *slog.Logger
	flow     atomic.Pointer[kitauth.OIDC]
	broken   atomic.Bool
	discover func(context.Context, kitauth.OIDCConfig) (*kitauth.OIDC, error)
	// First and Max bound Run's doubling backoff.
	First, Max time.Duration
}

// NewProvider is the flow for c, not yet discovered.
func NewProvider(c Config, log *slog.Logger) *Provider {
	return &Provider{cfg: OIDCConfig(c), log: log, discover: kitauth.NewOIDC, First: 5 * time.Second, Max: 5 * time.Minute}
}

// statusRecorder is the discovery's transport, remembering the last HTTP
// status, so a 4xx can be told from a 5xx and a 200 that disagrees from no
// answer at all: the discovery reports every failure as text. It reaches
// the discovery under oauth2's client key, which kit's OIDC client reads.
type statusRecorder struct{ status atomic.Int64 }

func (s *statusRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		s.status.Store(int64(resp.StatusCode))
	}
	return resp, err
}

// Discover makes one attempt under DiscoverTimeout. An error wrapping
// ErrMisconfigured will not go away by trying again; it marks the provider
// broken.
func (p *Provider) Discover(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, DiscoverTimeout)
	defer cancel()
	rec := &statusRecorder{}
	flow, err := p.discover(context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: rec}), p.cfg)
	if err != nil {
		if permanent(int(rec.status.Load())) {
			p.broken.Store(true)
			return fmt.Errorf("%w: %w", ErrMisconfigured, err)
		}
		return err
	}
	p.flow.Store(flow)
	return nil
}

// permanent reports whether a failed discovery is configuration rather
// than reachability: the provider answered 4xx, or it answered 2xx and the
// discovery still failed, which is an issuer that does not match or a body
// that is no discovery document. No status at all (a transport error, a
// timeout) and a 5xx are worth another try.
func permanent(status int) bool {
	return status >= 200 && status < 300 || status >= 400 && status < 500
}

// Run tries discovery at once and again until it answers, it turns out
// misconfigured, or ctx ends; the wait doubles from First up to Max, every
// failure logged. serve starts it in the background: the listener never
// waits for the provider.
func (p *Provider) Run(ctx context.Context) {
	wait := p.First
	for attempt := 1; ; attempt++ {
		err := p.Discover(ctx)
		switch {
		case err == nil:
			p.log.Info("OIDC: OK", "issuer", p.cfg.Issuer, "attempt", attempt)
			return
		case errors.Is(err, ErrMisconfigured):
			p.log.Error("OIDC: provider disagrees with the configuration, sign-in disabled until restart", "issuer", p.cfg.Issuer,
				logging.Err(err))
			return
		}
		p.log.Warn("OIDC: provider discovery failed, sign-in unavailable until it answers", "issuer", p.cfg.Issuer,
			"attempt", attempt, "retry_in", wait.String(), logging.Err(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, p.Max)
	}
}

// Start redirects to the provider, or to the page that says why not.
func (p *Provider) Start(w http.ResponseWriter, r *http.Request) {
	flow := p.flow.Load()
	switch {
	case flow != nil:
		flow.Start(w, r)
	case p.broken.Load():
		http.Redirect(w, r, MisconfiguredURL, http.StatusFound)
	default:
		http.Redirect(w, r, UnavailableURL, http.StatusFound)
	}
}

// Callback checks the provider's answer; ErrMisconfigured or
// ErrNotDiscovered when there is no flow to check it with.
func (p *Provider) Callback(w http.ResponseWriter, r *http.Request) (Claims, error) {
	flow := p.flow.Load()
	switch {
	case flow != nil:
		return flow.Callback(w, r)
	case p.broken.Load():
		return Claims{}, ErrMisconfigured
	default:
		return Claims{}, ErrNotDiscovered
	}
}
