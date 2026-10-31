package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/logging"
)

// ErrNotDiscovered is a sign-in attempted before the provider answered
// discovery.
var ErrNotDiscovered = errors.New("oidc provider not discovered yet")

// DiscoverTimeout bounds one discovery attempt: the boot context has no
// deadline, and an unreachable provider would otherwise hang it.
const DiscoverTimeout = 30 * time.Second

// UnavailableURL is where a sign-in lands while the provider has not
// answered discovery: a full-page navigation gets a page that halts with a
// button, not a JSON body.
const UnavailableURL = "/?auth_error=provider_unavailable"

// Provider is kit's OIDC flow once discovery has answered. A provider that
// is down is a warning, never a fault: the webhook, the ping and the
// probes must not depend on it, and boot never waits for it. Until it
// answers, the login lands on UnavailableURL and the gated /api answers
// 401 as ever; Run keeps trying in the background.
type Provider struct {
	cfg      kitauth.OIDCConfig
	log      *slog.Logger
	flow     atomic.Pointer[kitauth.OIDC]
	discover func(context.Context, kitauth.OIDCConfig) (*kitauth.OIDC, error)
	// First and Max bound Run's doubling backoff.
	First, Max time.Duration
}

// NewProvider is the flow for c, not yet discovered.
func NewProvider(c Config, log *slog.Logger) *Provider {
	return &Provider{cfg: OIDCConfig(c), log: log, discover: kitauth.NewOIDC, First: 5 * time.Second, Max: 5 * time.Minute}
}

// Ready reports whether discovery has answered.
func (p *Provider) Ready() bool { return p.flow.Load() != nil }

// Discover makes one attempt under DiscoverTimeout.
func (p *Provider) Discover(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, DiscoverTimeout)
	defer cancel()
	flow, err := p.discover(ctx, p.cfg)
	if err != nil {
		return err
	}
	p.flow.Store(flow)
	return nil
}

// Run tries discovery at once and again until it answers or ctx ends, the
// wait doubling from First up to Max, every failure logged. serve starts
// it in the background: the listener never waits for the provider.
func (p *Provider) Run(ctx context.Context) {
	wait := p.First
	for attempt := 1; ; attempt++ {
		err := p.Discover(ctx)
		if err == nil {
			p.log.Info("OIDC: OK", "issuer", p.cfg.Issuer, "attempt", attempt)
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

// Start redirects to the provider, or to UnavailableURL before discovery
// answered.
func (p *Provider) Start(w http.ResponseWriter, r *http.Request) {
	flow := p.flow.Load()
	if flow == nil {
		http.Redirect(w, r, UnavailableURL, http.StatusFound)
		return
	}
	flow.Start(w, r)
}

// Callback checks the provider's answer; ErrNotDiscovered before Ready.
func (p *Provider) Callback(w http.ResponseWriter, r *http.Request) (Claims, error) {
	flow := p.flow.Load()
	if flow == nil {
		return Claims{}, ErrNotDiscovered
	}
	return flow.Callback(w, r)
}
