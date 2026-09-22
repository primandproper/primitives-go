package twilio

import (
	"context"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type (
	// Config configures Twilio to send text messages.
	Config struct {
		// AccountSID is the account the messages are billed to. It is also part of
		// the request path, which is why it is required even though the auth token
		// alone would authenticate.
		AccountSID string `env:"ACCOUNT_SID" json:"accountSID,omitempty" yaml:"accountSID,omitempty"`
		// AuthToken is the account's auth token, sent as the HTTP basic password.
		AuthToken string `env:"AUTH_TOKEN" json:"authToken,omitempty" yaml:"authToken,omitempty"`
		// BaseURL overrides the API base URL (e.g. for testing with httptest).
		BaseURL string `env:"BASE_URL" json:"baseURL,omitempty" yaml:"baseURL,omitempty"`
	}
)

var _ validation.ValidatableWithContext = (*Config)(nil)

// ValidateWithContext validates a Config struct.
func (cfg *Config) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, cfg,
		validation.Field(&cfg.AccountSID, validation.Required),
		validation.Field(&cfg.AuthToken, validation.Required),
	)
}
