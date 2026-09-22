package twilio

import (
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestConfig_ValidateWithContext(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{AccountSID: testAccountSID, AuthToken: testAuthToken}
		must.NoError(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("without an account SID", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{AuthToken: testAuthToken}
		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("without an auth token", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{AccountSID: testAccountSID}
		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("the base URL is optional", func(t *testing.T) {
		t.Parallel()

		// It exists to point the adapter at an httptest server; a deployment
		// leaves it unset and reaches Twilio.
		cfg := &Config{AccountSID: testAccountSID, AuthToken: testAuthToken, BaseURL: ""}
		must.NoError(t, cfg.ValidateWithContext(t.Context()))
	})
}
