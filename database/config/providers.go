package databasecfg

import (
	"github.com/primandproper/primitives-go/v2/database"
)

// NewClientConfig converts Config to database.ClientConfig.
//
//nolint:gocritic // hugeParam: intentionally accepts value for compatibility
func NewClientConfig(cfg Config) database.ClientConfig {
	return &cfg
}
