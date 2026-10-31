package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
)

// ErrNotDiscovered is a sign-in attempted before the provider answered
// discovery.
var ErrNotDiscovered = errors.New("oidc provider not discovered yet")

// DiscoverTimeout bounds one discovery attempt: the boot context has no
// deadline, and an unreachable provider would otherwise hang it.
const DiscoverTimeout = 30 * time.Second

// Provider is kit's OIDC flow once discovery has answered. A provider that
// is down at startup is a warning, never a fault: the webhook, the ping and
// the probes must not depend on it. Until it answers, Ready is false, the
// login routes say so and the gated /api answers 401 as ever; Run retries
// in the background.
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

// ReadyProvider is a provider around a flow already built; tests pass one
// made with kit's NewOIDCWithBackend.
func ReadyProvider(flow *kitauth.OIDC) *Provider {
	p := &Provider{}
	p.flow.Store(flow)
	return p
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

// Run retries discovery until it answers or ctx ends, the wait doubling
// from First up to Max, every failure logged.
func (p *Provider) Run(ctx context.Context) {
	wait := p.First
	for attempt := 2; !p.Ready(); attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := p.Discover(ctx); err != nil {
			wait = min(wait*2, p.Max)
			p.log.Warn("OIDC: provider discovery failed, sign-in unavailable", "issuer", p.cfg.Issuer, "attempt", attempt,
				"retry_in", wait.String(), logging.Err(err))
			continue
		}
		p.log.Info("OIDC: OK", "issuer", p.cfg.Issuer, "attempt", attempt)
	}
}

// Start redirects to the provider, or answers 503 before Ready.
func (p *Provider) Start(w http.ResponseWriter, r *http.Request) {
	flow := p.flow.Load()
	if flow == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, ErrNotDiscovered.Error())
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
