package llmcfg

import (
	"context"

	"github.com/primandproper/primitives-go/llm"
)

// NewLLMProvider provides an LLM provider from config.
func NewLLMProvider(ctx context.Context, c *Config, opts ...Option) (llm.Provider, error) {
	return c.NewLLMProvider(ctx, opts...)
}
