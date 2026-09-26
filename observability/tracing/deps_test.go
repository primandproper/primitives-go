package tracing

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// TestDoesNotDependOnDatabase keeps tracing, and with it every instrumented
// package in the module, from depending on database.
//
// It used to: tracing imports filtering for AttachQueryFilterToSpan, and
// filtering imported database for its sql.Null conversions. Those now come from
// database/nullable, which imports only the standard library. Nothing about the
// build would say so if a new import brought database back, directly or
// through any package in between, so this asks the go tool for the whole
// dependency set rather than checking one import line.
func TestDoesNotDependOnDatabase(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("runs the go tool")
	}

	modOut, err := exec.CommandContext(t.Context(), "go", "list", "-m").Output()
	must.NoError(t, err)

	database := strings.TrimSpace(string(modOut)) + "/database"

	depsOut, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	must.NoError(t, err)

	deps := strings.Fields(string(depsOut))

	// A listing that came back without filtering in it is not evidence of
	// anything: the path below would be passing on a parse of the wrong output.
	must.SliceContains(t, deps, strings.TrimSuffix(database, "/database")+"/filtering")

	for _, dep := range deps {
		if dep == database+"/nullable" {
			continue
		}

		test.False(t, dep == database || strings.HasPrefix(dep, database+"/"), test.Sprintf("tracing depends on %s", dep))
	}
}
