package tierguard_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// forbidden is the module this one does not depend on, in the form that catches
// every major of it: platform-go's import paths carry a /vN suffix from v2 on,
// so the prefix without one is what both v14/database and a future v15/database
// have in common.
const forbidden = "github.com/primandproper/platform-go"

// TestNoFileImportsPlatformGo walks every Go file in the module, test files
// included, and fails on an import of platform-go.
//
// Test files are not an exception and are the likelier way in: a primitive
// needing a domain type to *assert against* is a real pull, and platform-go's
// own audit of the crossings found exactly that — a roster test naming fourteen
// domain packages, in a package whose production code was clean.
func TestNoFileImportsPlatformGo(t *testing.T) {
	t.Parallel()

	moduleDir := moduleRoot(t)
	fset := token.NewFileSet()

	must.NoError(t, filepath.WalkDir(moduleDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if skipDir(moduleDir, path, d.Name()) {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(moduleDir, path)
		if err != nil {
			return err
		}

		for _, imported := range parsed.Imports {
			unquoted, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}

			if unquoted == forbidden || strings.HasPrefix(unquoted, forbidden+"/") {
				t.Errorf("%s imports %s\n\t"+
					"this module imports platform-go from nowhere, ever: the dependency runs one way, "+
					"and a primitive that needs a domain package has found a seam to invert — the domain "+
					"package exports the value and platform-go's service registers it",
					filepath.ToSlash(rel), unquoted)
			}
		}

		return nil
	}))
}

// TestGoModDoesNotRequirePlatformGo is the other half. An import is what a
// compiler sees; a require line is what the module graph sees, and one left
// behind with no import under it still pins this module to a platform-go
// version and still puts platform-go's own graph into this one's.
func TestGoModDoesNotRequirePlatformGo(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join(moduleRoot(t), "go.mod"))
	must.NoError(t, err)

	test.StrNotContains(t, string(contents), forbidden)
}

// moduleRoot is two directories up, which is where this package sits and where
// go.mod has to be for the answer to be this module rather than whatever tree a
// test binary was copied into.
func moduleRoot(t *testing.T) string {
	t.Helper()

	moduleDir, err := filepath.Abs(filepath.Join("..", ".."))
	must.NoError(t, err)
	must.FileExists(t, filepath.Join(moduleDir, "go.mod"))

	return moduleDir
}

// skipDir names the directories that hold no packages of this module's. One of
// them holds other checkouts of it: an agent worktree under .claude would
// otherwise report every package in the module twice. testdata is skipped
// because the go tool skips it — a fixture there is compiled only by the test
// that names it, and creates no module edge.
func skipDir(moduleDir, path, name string) bool {
	return path != moduleDir && (strings.HasPrefix(name, ".") || name == "artifacts" || name == "testdata")
}
