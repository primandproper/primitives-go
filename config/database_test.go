package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/primandproper/primitives-go/v2/config"
	databasecfg "github.com/primandproper/primitives-go/v2/database/config"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type appConfig struct {
	Database databasecfg.Config `envPrefix:"DATABASE_" json:"database"`
}

// TestLoadFromJSONFile_DatabaseProvider pins the case that motivated keeping
// a file's value over an envDefault: a checked-in config naming sqlite loaded
// as postgres, so the file lied about which database the service talked to.
func TestLoadFromJSONFile_DatabaseProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	must.NoError(t, os.WriteFile(path, []byte(`{"database": {"provider": "sqlite", "maxIdleConns": 2}}`), 0o600))

	t.Run("the file's provider survives an unset variable", func(t *testing.T) { //nolint:paralleltest // shares process env with sibling subtests.
		cfg, err := config.LoadFromJSONFile[appConfig](t.Context(), path, config.WithPrefix("APP_"))
		must.NoError(t, err)

		test.EqOp(t, databasecfg.ProviderSQLite, cfg.Database.Provider)
		test.EqOp(t, 2, cfg.Database.MaxIdleConns)
		test.EqOp(t, 7, cfg.Database.MaxOpenConns) // left out of the file, so the envDefault
	})

	t.Run("a set variable still overrides the file's provider", func(t *testing.T) {
		t.Setenv("APP_DATABASE_PROVIDER", databasecfg.ProviderMySQL)

		cfg, err := config.LoadFromJSONFile[appConfig](t.Context(), path, config.WithPrefix("APP_"))
		must.NoError(t, err)

		test.EqOp(t, databasecfg.ProviderMySQL, cfg.Database.Provider)
	})
}
