package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("NAME", "fromenv")

	cfg, err := LoadFromEnvironment[sampleConfig]()
	must.NoError(t, err)
	must.NotNil(t, cfg)
	test.EqOp(t, "fromenv", cfg.Name)
	test.EqOp(t, 8080, cfg.Port)
}

// TestLoadFromEnvironment_ApplyError exercises the error path where the
// environment holds a value the target field cannot parse.
func TestLoadFromEnvironment_ApplyError(t *testing.T) {
	t.Setenv("PORT", "not-an-int") // Port is an int; parsing fails.

	cfg, err := LoadFromEnvironment[sampleConfig]()
	test.Error(t, err)
	test.Nil(t, cfg)
}

// TestLoadFromJSONFile_OverlaysEnvironment uses t.Setenv and therefore runs
// serially, separate from the parallel subtests below.
func TestLoadFromJSONFile_OverlaysEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	must.NoError(t, os.WriteFile(path, []byte(`{"Name":"fromfile","Verbose":true}`), 0o600))

	// NAME is set in the environment and overrides the file value; Verbose has
	// no env var (and no default) so the file value survives; Port has no file
	// value and takes its envDefault.
	t.Setenv("NAME", "fromenv")

	cfg, err := LoadFromJSONFile[sampleConfig](t.Context(), path)
	must.NoError(t, err)
	test.EqOp(t, "fromenv", cfg.Name)
	test.EqOp(t, true, cfg.Verbose)
	test.EqOp(t, 8080, cfg.Port)
}

func TestLoadFromJSONFile_FileBeatsEnvDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	must.NoError(t, os.WriteFile(path, []byte(`{"Port":5000}`), 0o600))

	t.Run("a file value survives an unset variable", func(t *testing.T) { //nolint:paralleltest // shares process env with sibling subtests.
		cfg, err := LoadFromJSONFile[sampleConfig](t.Context(), path)
		must.NoError(t, err)
		test.EqOp(t, 5000, cfg.Port)
	})

	t.Run("a set variable overrides the file value", func(t *testing.T) {
		t.Setenv("PORT", "9090")

		cfg, err := LoadFromJSONFile[sampleConfig](t.Context(), path)
		must.NoError(t, err)
		test.EqOp(t, 9090, cfg.Port)
	})

	t.Run("a file's explicit zero survives too", func(t *testing.T) { //nolint:paralleltest // shares process env with sibling subtests.
		zeroPath := filepath.Join(dir, "zero.json")
		must.NoError(t, os.WriteFile(zeroPath, []byte(`{"Port":0}`), 0o600))

		cfg, err := LoadFromJSONFile[sampleConfig](t.Context(), zeroPath)
		must.NoError(t, err)
		test.EqOp(t, 0, cfg.Port)
	})

	t.Run("required and notEmpty are satisfied by a default", func(t *testing.T) {
		type checkedConfig struct {
			Region string `env:"REGION,notEmpty" envDefault:"us"`
			Zone   string `env:"ZONE,required"   envDefault:"a"`
			Token  string `env:"TOKEN,required"`
		}

		checkedPath := filepath.Join(dir, "checked.json")
		must.NoError(t, os.WriteFile(checkedPath, []byte(`{"Region":"eu"}`), 0o600))

		t.Setenv("TOKEN", "sekret")

		cfg, err := LoadFromJSONFile[checkedConfig](t.Context(), checkedPath)
		must.NoError(t, err)
		test.EqOp(t, "eu", cfg.Region)
		test.EqOp(t, "a", cfg.Zone)
		test.EqOp(t, "sekret", cfg.Token)
	})

	t.Run("a required variable with no default is still required", func(t *testing.T) { //nolint:paralleltest // shares process env with sibling subtests.
		type requiredConfig struct {
			Token string `env:"TOKEN,required"`
		}

		requiredPath := filepath.Join(dir, "required.json")
		must.NoError(t, os.WriteFile(requiredPath, []byte(`{"Token":"fromfile"}`), 0o600))

		_, err := LoadFromJSONFile[requiredConfig](t.Context(), requiredPath)
		test.Error(t, err)
	})

	t.Run("OnSet reports a set variable once", func(t *testing.T) {
		t.Setenv("PORT", "9090")

		var reported []string
		_, err := LoadFromJSONFile[sampleConfig](t.Context(), path, WithOnSet(func(tag string, _ any, _ bool) {
			reported = append(reported, tag)
		}))
		must.NoError(t, err)

		ports := 0
		for _, tag := range reported {
			if tag == "PORT" {
				ports++
			}
		}
		test.EqOp(t, 1, ports)
	})
}

func TestLoadFromJSONFile(t *testing.T) {
	t.Parallel()

	t.Run("errors on missing file", func(t *testing.T) {
		t.Parallel()

		_, err := LoadFromJSONFile[sampleConfig](t.Context(), filepath.Join(t.TempDir(), "nope.json"))
		test.Error(t, err)
	})

	t.Run("errors on invalid JSON", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "bad.json")
		must.NoError(t, os.WriteFile(path, []byte(`{not json`), 0o600))

		_, err := LoadFromJSONFile[sampleConfig](t.Context(), path)
		test.Error(t, err)
	})
}

// TestLoadFromJSONFile_ApplyEnvError covers loadFile's env-overlay error branch:
// the file decodes cleanly but a bad env value fails ApplyEnvironmentVariables.
func TestLoadFromJSONFile_ApplyEnvError(t *testing.T) {
	t.Setenv("PORT", "not-an-int") // Port is an int; parsing fails after decode.

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	must.NoError(t, os.WriteFile(path, []byte(`{"Name":"decoded"}`), 0o600))

	cfg, err := LoadFromJSONFile[sampleConfig](t.Context(), path)
	test.Error(t, err)
	test.Nil(t, cfg)
}

func TestLoadFromTOMLFile(t *testing.T) {
	t.Parallel()

	t.Run("decodes and applies defaults", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		must.NoError(t, os.WriteFile(path, []byte("name = \"fromfile\"\nverbose = true\n"), 0o600))

		cfg, err := LoadFromTOMLFile[sampleConfig](t.Context(), path)
		must.NoError(t, err)
		test.EqOp(t, "fromfile", cfg.Name)
		test.EqOp(t, true, cfg.Verbose)
		test.EqOp(t, 8080, cfg.Port) // from envDefault
	})

	t.Run("errors on invalid TOML", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "bad.toml")
		must.NoError(t, os.WriteFile(path, []byte("name = = broken"), 0o600))

		_, err := LoadFromTOMLFile[sampleConfig](t.Context(), path)
		test.Error(t, err)
	})
}

func TestLoadFromYAMLFile(t *testing.T) {
	t.Parallel()

	t.Run("decodes and applies defaults", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		must.NoError(t, os.WriteFile(path, []byte("name: fromfile\nverbose: true\n"), 0o600))

		cfg, err := LoadFromYAMLFile[sampleConfig](t.Context(), path)
		must.NoError(t, err)
		test.EqOp(t, "fromfile", cfg.Name)
		test.EqOp(t, true, cfg.Verbose)
		test.EqOp(t, 8080, cfg.Port) // from envDefault
	})

	t.Run("errors on invalid YAML", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "bad.yaml")
		must.NoError(t, os.WriteFile(path, []byte("name: : :\n\t- broken"), 0o600))

		_, err := LoadFromYAMLFile[sampleConfig](t.Context(), path)
		test.Error(t, err)
	})
}

// dotEnvConfig uses env keys unique to the dotenv test. godotenv.Load mutates
// the real process environment (not a test-scoped copy), so distinct keys keep
// it from leaking into the other tests when they run in a shuffled order.
type dotEnvConfig struct {
	Name string `env:"DOTENV_NAME"`
	Port int    `env:"DOTENV_PORT" envDefault:"8080"`
}

//nolint:paralleltest // godotenv.Load mutates the process environment; must run serially.
func TestLoadFromDotEnvFile(t *testing.T) {
	t.Cleanup(func() {
		os.Unsetenv("DOTENV_NAME")
		os.Unsetenv("DOTENV_PORT")
	})

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	must.NoError(t, os.WriteFile(path, []byte("DOTENV_NAME=fromdotenv\nDOTENV_PORT=1234\n"), 0o600))

	cfg, err := LoadFromDotEnvFile[dotEnvConfig](path)
	must.NoError(t, err)
	test.EqOp(t, "fromdotenv", cfg.Name)
	test.EqOp(t, 1234, cfg.Port)
}

// TestLoadFromDotEnvFile_MissingFile covers the godotenv.Load error branch. A
// missing file fails before any environment mutation, so this test is
// parallel-safe.
func TestLoadFromDotEnvFile_MissingFile(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFromDotEnvFile[dotEnvConfig](filepath.Join(t.TempDir(), "nonexistent.env"))
	test.Error(t, err)
	test.Nil(t, cfg)
}

func TestResolveDotEnvPath(t *testing.T) {
	t.Parallel()

	t.Run("returns path when file exists", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		must.NoError(t, os.WriteFile(path, []byte("NAME=x\n"), 0o600))

		resolved, err := ResolveDotEnvPath(dir, ".env")
		must.NoError(t, err)
		test.EqOp(t, path, resolved)
	})

	t.Run("returns empty string when file is absent", func(t *testing.T) {
		t.Parallel()

		resolved, err := ResolveDotEnvPath(t.TempDir(), ".env")
		must.NoError(t, err)
		test.EqOp(t, "", resolved)
	})

	t.Run("returns error for a stat failure that is not ErrNotExist", func(t *testing.T) {
		t.Parallel()

		// Use a regular file as the base dir: joining a filename beneath it makes
		// os.Stat fail with ENOTDIR, which is not os.ErrNotExist.
		dir := t.TempDir()
		notADir := filepath.Join(dir, "regular-file")
		must.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

		resolved, err := ResolveDotEnvPath(notADir, ".env")
		test.Error(t, err)
		test.EqOp(t, "", resolved)
	})
}

type validatableConfig struct {
	Name string `env:"NAME"`
}

func (c *validatableConfig) ValidateWithContext(_ context.Context) error {
	if c.Name == "" {
		return validation.NewError("name_required", "name is required")
	}

	return nil
}

func TestValidate(t *testing.T) {
	t.Parallel()

	t.Run("passes for a valid config", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, Validate(context.Background(), &validatableConfig{Name: "ok"}))
	})

	t.Run("fails for an invalid config", func(t *testing.T) {
		t.Parallel()

		test.Error(t, Validate(context.Background(), &validatableConfig{}))
	})

	t.Run("is a no-op for a non-validatable config", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, Validate(context.Background(), &sampleConfig{}))
	})
}
