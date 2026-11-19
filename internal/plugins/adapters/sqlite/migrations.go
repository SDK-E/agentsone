// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"embed"
	"fmt"

	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "plugins"

// Migrate brings the plugins and themes tables up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load plugins migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate plugins: %w", err)
	}
	return nil
}
