// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// Plugins stores installed plugins.
type Plugins struct{ db *store.DB }

var _ domain.PluginRepository = (*Plugins)(nil)

// NewPlugins returns a repository on a database whose plugins migrations ran.
func NewPlugins(db *store.DB) *Plugins { return &Plugins{db: db} }

const pluginColumns = `id, name, version, kinds, source, ref, enabled, health,
	installed_by_kind, installed_by_name, installed_at, manifest, dir, sha256`

// Insert adds p; a taken id, or name and version, is ErrAlreadyInstalled.
func (r *Plugins) Insert(ctx context.Context, p domain.Plugin) error {
	kinds, err := json.Marshal(nonNil(p.Kinds))
	if err != nil {
		return fmt.Errorf("insert plugin: kinds: %w", err)
	}
	manifest, err := p.Manifest.Encode()
	if err != nil {
		return fmt.Errorf("insert plugin: %w", err)
	}
	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		var taken int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM plugins WHERE id = ? OR (name = ? AND version = ?)`,
			p.ID, p.Name, p.Version).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return fmt.Errorf("plugin %s %s (%s): %w", p.Name, p.Version, p.ID, domain.ErrAlreadyInstalled)
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO plugins (`+pluginColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Name, p.Version, string(kinds), p.Source, p.Ref, boolInt(p.Enabled), string(p.Health),
			p.InstalledBy.Kind, p.InstalledBy.Name, p.InstalledAt.UTC().Format(time.RFC3339Nano),
			string(manifest), p.Dir, p.SHA256)
		return err
	})
	if err != nil {
		return fmt.Errorf("insert plugin: %w", err)
	}
	return nil
}

// Get returns the plugin with id.
func (r *Plugins) Get(ctx context.Context, id string) (domain.Plugin, error) {
	p, err := scanPlugin(r.db.SQL().QueryRowContext(ctx, `SELECT `+pluginColumns+` FROM plugins WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Plugin{}, fmt.Errorf("plugin %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Plugin{}, fmt.Errorf("get plugin: %w", err)
	}
	return p, nil
}

// ByName returns every installed version of name, newest install first.
func (r *Plugins) ByName(ctx context.Context, name string) ([]domain.Plugin, error) {
	return r.query(ctx,
		`SELECT `+pluginColumns+` FROM plugins WHERE name = ? ORDER BY installed_at DESC, id DESC`, name)
}

// List returns up to limit plugins with an id after after that match f.
func (r *Plugins) List(ctx context.Context, f domain.PluginFilter, after string, limit int) ([]domain.Plugin, error) {
	var where []string
	args := []any{after}
	where = append(where, "id > ?")
	if f.Kind != "" {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(plugins.kinds) WHERE value = ?)`)
		args = append(args, f.Kind)
	}
	if f.Enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, boolInt(*f.Enabled))
	}
	query := `SELECT ` + pluginColumns + ` FROM plugins WHERE ` + strings.Join(where, " AND ") + ` ORDER BY id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.query(ctx, query, args...)
}

func (r *Plugins) query(ctx context.Context, query string, args ...any) ([]domain.Plugin, error) {
	rows, err := r.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only query; rows.Err below reports iteration failures
	var out []domain.Plugin
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, fmt.Errorf("list plugins: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	return out, nil
}

// SetEnabled flips the enabled flag and health of id.
func (r *Plugins) SetEnabled(ctx context.Context, id string, enabled bool, health domain.Health) error {
	res, err := r.db.SQL().ExecContext(ctx,
		`UPDATE plugins SET enabled = ?, health = ? WHERE id = ?`, boolInt(enabled), string(health), id)
	if err != nil {
		return fmt.Errorf("set plugin enabled: %w", err)
	}
	return requirePlugin(res, id)
}

// Delete removes the plugin with id.
func (r *Plugins) Delete(ctx context.Context, id string) error {
	res, err := r.db.SQL().ExecContext(ctx, `DELETE FROM plugins WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete plugin: %w", err)
	}
	return requirePlugin(res, id)
}

func requirePlugin(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("plugin %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("plugin %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

func scanPlugin(row rowScanner) (domain.Plugin, error) {
	var p domain.Plugin
	var kinds, health, installed, manifest string
	var enabled int
	if err := row.Scan(&p.ID, &p.Name, &p.Version, &kinds, &p.Source, &p.Ref, &enabled, &health,
		&p.InstalledBy.Kind, &p.InstalledBy.Name, &installed, &manifest, &p.Dir, &p.SHA256); err != nil {
		return domain.Plugin{}, err
	}
	if err := json.Unmarshal([]byte(kinds), &p.Kinds); err != nil {
		return domain.Plugin{}, fmt.Errorf("plugin %s: kinds: %w", p.ID, err)
	}
	at, err := time.Parse(time.RFC3339Nano, installed)
	if err != nil {
		return domain.Plugin{}, fmt.Errorf("plugin %s: installed_at %q: %w", p.ID, installed, err)
	}
	m, err := domain.ParseManifest([]byte(manifest))
	if err != nil {
		return domain.Plugin{}, fmt.Errorf("plugin %s: manifest: %w", p.ID, err)
	}
	p.Enabled, p.Health, p.InstalledAt, p.Manifest = enabled == 1, domain.Health(health), at.UTC(), m
	return p, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
