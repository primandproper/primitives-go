package llmcfg

import (
	"context"
	"testing"

	"github.com/primandproper/primitives-go/llm"
	loggingnoop "github.com/primandproper/primitives-go/observability/logging/noop"
	"github.com/primandproper/primitives-go/observability/metrics"
	tracingnoop "github.com/primandproper/primitives-go/observability/tracing/noop"

	"github.com/samber/do/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestRegisterLLMProvider(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		i := do.New()
		do.ProvideValue[context.Context](i, t.Context())
		do.ProvideValue(i, loggingnoop.NewLogger())
		do.ProvideValue(i, tracingnoop.NewTracerProvider())
		do.ProvideValue[metrics.Provider](i, nil)
		do.ProvideValue(i, &Config{Provider: ProviderNoop})

		RegisterLLMProvider(i)

		provider, err := do.Invoke[llm.Provider](i)
		must.NoError(t, err)
		test.NotNil(t, provider)
	})
}
