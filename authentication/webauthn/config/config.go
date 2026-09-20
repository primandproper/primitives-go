package webauthncfg

import (
	"context"

	"github.com/primandproper/primitives-go/v2/authentication/webauthn"
	webauthncache "github.com/primandproper/primitives-go/v2/authentication/webauthn/cache"
	cachecfg "github.com/primandproper/primitives-go/v2/cache/config"
	"github.com/primandproper/primitives-go/v2/config/cfgnorm"
	"github.com/primandproper/primitives-go/v2/errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// ProviderCache names the ceremony store this package builds, and is the only
// provider it implements. A deployment holding ceremony state in SQL is
// configured by platform-go's authentication/webauthnsessions/config, which
// embeds this Config and selects between that table and this store.
const ProviderCache = "cache"

// Config assembles a webauthn.RelyingParty and a cache-backed ceremony store
// from environment configuration.
type Config struct {
	_ struct{} `json:"-" yaml:"-"`

	// Provider selects where ceremony state lives. ProviderCache is the only
	// value this package builds, and an unset Provider takes it.
	//
	// It is declared here rather than only on the domain half so that the
	// variable is read by somebody — see NewSessionStore for what that buys
	// and why the refusal is not a validation rule.
	Provider string `env:"PROVIDER" json:"provider,omitempty" yaml:"provider,omitempty"`

	// RelyingParty is the WebAuthn relying party itself — the domain, the
	// display name, the permitted origins, and the ceremony deadline.
	RelyingParty webauthn.Config `env:",init" envPrefix:"RP_" json:"relyingParty,omitzero" yaml:"relyingParty,omitempty"`

	// Cache configures the ceremony store. Use the redis provider: the memory
	// provider is per-process, so a challenge issued by one replica cannot be
	// answered on another.
	Cache cachecfg.Config `env:",init" envPrefix:"CACHE_" json:"cache,omitzero" yaml:"cache,omitempty"`
}

var _ validation.ValidatableWithContext = (*Config)(nil)

// EnsureDefaults fills in zero fields, including the relying party's own.
func (cfg *Config) EnsureDefaults() {
	cfg.RelyingParty.EnsureDefaults()

	if cfg.Provider == "" {
		cfg.Provider = ProviderCache
	}
}

// ValidateWithContext validates a Config struct.
func (cfg *Config) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, cfg,
		validation.Field(&cfg.RelyingParty,
			validation.By(func(any) error { return cfg.RelyingParty.ValidateWithContext(ctx) })),
		validation.Field(&cfg.Cache,
			validation.By(func(any) error { return cfg.Cache.ValidateWithContext(ctx) })),
	)
}

// NewSessionStore builds the cache-backed ceremony store, and refuses any other
// provider.
//
// A deployment holding ceremony state in SQL calls webauthndbcfg.NewSessionStore
// instead, which selects between that table and this store.
//
// The refusal lives here and deliberately not in ValidateWithContext. The
// domain half embeds this Config with no env tag, so one PROVIDER populates
// both Provider fields — and its own validator runs this one first. A rule
// rejecting ProviderDatabase there would fail every SQL deployment during
// validation, before the dispatch that would have built the right store ever
// ran.
func NewSessionStore(
	ctx context.Context,
	cfg *Config,
	opts ...Option,
) (webauthn.SessionStore, error) {
	if cfg == nil {
		return nil, errors.ErrNilInputParameter
	}

	o := newOptions(opts)

	cfg.EnsureDefaults()

	if p := cfgnorm.Provider(cfg.Provider); p != ProviderCache {
		return nil, errors.Wrapf(errors.ErrUnknownProvider,
			"webauthn ceremony store provider %q: this config builds the cache-backed store only, "+
				"and a SQL-backed one is built by platform-go's "+
				"authentication/webauthnsessions/config", cfg.Provider)
	}

	if err := cfg.ValidateWithContext(ctx); err != nil {
		return nil, errors.Wrap(err, "validating webauthn config")
	}

	return newSessionStore(ctx, cfg, o)
}

// newSessionStore builds the cache and the store over it.
func newSessionStore(
	ctx context.Context,
	cfg *Config,
	o *options,
) (webauthn.SessionStore, error) {
	c, err := cachecfg.NewCache[webauthn.SessionData](ctx, &cfg.Cache,
		cachecfg.WithLogger(o.logger),
		cachecfg.WithTracerProvider(o.tracerProvider),
		cachecfg.WithMetricsProvider(o.metricsProvider))
	if err != nil {
		return nil, errors.Wrap(err, "building webauthn ceremony session cache")
	}

	// Built into a variable and returned only once err is known to be nil:
	// webauthncache.NewSessionStore hands back its own concrete type, and
	// returning it straight through would convert a nil pointer into a non-nil
	// webauthn.SessionStore on the error path — a value that passes a caller's
	// nil check and panics on the first Save.
	store, err := webauthncache.NewSessionStore(c, append([]webauthncache.Option{
		webauthncache.WithLogger(o.logger),
		webauthncache.WithTracerProvider(o.tracerProvider),
	}, o.cacheStore...)...)
	if err != nil {
		return nil, err
	}

	return store, nil
}

// NewRelyingParty builds the relying party over store.
//
// The store is a parameter rather than something this function builds, and that
// is the seam the tier split runs along — see the package documentation. A
// caller that wants the store selected for it, from a provider string, calls
// webauthndbcfg.NewRelyingParty.
//
// Only the relying party's own configuration is validated, not the Config's
// Cache block, because the store has already been built by whoever is handing
// it over — possibly not from this Config at all, which is exactly what a
// SQL-backed ceremony store is. Refusing to build a relying party over a working
// store on the strength of a cache nobody asked for would make the parameter
// less useful than the client it replaced.
func NewRelyingParty(
	ctx context.Context,
	cfg *Config,
	store webauthn.SessionStore,
	opts ...Option,
) (*webauthn.RelyingParty, error) {
	if cfg == nil {
		return nil, errors.ErrNilInputParameter
	}

	o := newOptions(opts)

	cfg.EnsureDefaults()

	if err := cfg.RelyingParty.ValidateWithContext(ctx); err != nil {
		return nil, errors.Wrap(err, "validating webauthn config")
	}

	return webauthn.NewRelyingParty(ctx, &cfg.RelyingParty, store, append([]webauthn.Option{
		webauthn.WithLogger(o.logger),
		webauthn.WithTracerProvider(o.tracerProvider),
		webauthn.WithMetricsProvider(o.metricsProvider),
	}, o.relyingParty...)...)
}
