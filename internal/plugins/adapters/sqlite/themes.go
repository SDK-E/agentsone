// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
	"github.com/SDK-E/agentsone/internal/store"
)

// Themes stores themes and per-human selections.
type Themes struct{ db *store.DB }

var _ domain.ThemeRepository = (*Themes)(nil)

// NewThemes returns a repository on a database whose plugins migrations ran.
func NewThemes(db *store.DB) *Themes { return &Themes{db: db} }

const themeColumns = `id, name, version, builtin, plugin_id, installed_at, document`

// Insert adds t; a theme with the same name is ErrAlreadyInstalled.
func (r *Themes) Insert(ctx context.Context, t domain.InstalledTheme) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM themes WHERE name = ?`, t.Name).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return fmt.Errorf("theme %q: %w", t.Name, domain.ErrAlreadyInstalled)
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO themes (`+themeColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Name, t.Version, boolInt(t.Builtin), t.PluginID,
			t.InstalledAt.UTC().Format(time.RFC3339Nano), string(t.Document))
		return err
	})
	if err != nil {
		return fmt.Errorf("insert theme: %w", err)
	}
	return nil
}

// Update replaces the version and document of the theme with t.ID.
func (r *Themes) Update(ctx context.Context, t domain.InstalledTheme) error {
	res, err := r.db.SQL().ExecContext(ctx,
		`UPDATE themes SET version = ?, document = ? WHERE id = ?`, t.Version, string(t.Document), t.ID)
	if err != nil {
		return fmt.Errorf("update theme: %w", err)
	}
	return requireRow(res, t.ID)
}

// Get returns the theme with id.
func (r *Themes) Get(ctx context.Context, id string) (domain.InstalledTheme, error) {
	t, err := scanTheme(r.db.SQL().QueryRowContext(ctx, `SELECT `+themeColumns+` FROM themes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InstalledTheme{}, fmt.Errorf("theme %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.InstalledTheme{}, fmt.Errorf("get theme: %w", err)
	}
	return t, nil
}

// FindByName returns the theme called name.
func (r *Themes) FindByName(ctx context.Context, name string) (domain.InstalledTheme, bool, error) {
	return r.findOne(ctx, `SELECT `+themeColumns+` FROM themes WHERE name = ?`, name)
}

// Builtin returns the built-in theme.
func (r *Themes) Builtin(ctx context.Context) (domain.InstalledTheme, bool, error) {
	return r.findOne(ctx, `SELECT `+themeColumns+` FROM themes WHERE builtin = 1 ORDER BY id LIMIT 1`)
}

func (r *Themes) findOne(ctx context.Context, query string, args ...any) (domain.InstalledTheme, bool, error) {
	t, err := scanTheme(r.db.SQL().QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InstalledTheme{}, false, nil
	}
	if err != nil {
		return domain.InstalledTheme{}, false, fmt.Errorf("find theme: %w", err)
	}
	return t, true, nil
}

// ByPlugin returns the themes one plugin carries.
func (r *Themes) ByPlugin(ctx context.Context, pluginID string) ([]domain.InstalledTheme, error) {
	return r.query(ctx, `SELECT `+themeColumns+` FROM themes WHERE plugin_id = ? ORDER BY id`, pluginID)
}

// List returns up to limit themes with an id after afterID.
func (r *Themes) List(ctx context.Context, afterID string, limit int) ([]domain.InstalledTheme, error) {
	return r.query(ctx, `SELECT `+themeColumns+` FROM themes WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
}

func (r *Themes) query(ctx context.Context, query string, args ...any) ([]domain.InstalledTheme, error) {
	rows, err := r.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.InstalledTheme
	for rows.Next() {
		t, err := scanTheme(rows)
		if err != nil {
			return nil, fmt.Errorf("list themes: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list themes: %w", err)
	}
	return out, nil
}

// Delete removes the theme unless a human selected it.
func (r *Themes) Delete(ctx context.Context, id string) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var exists, selected int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM themes WHERE id = ?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("theme %s: %w", id, domain.ErrNotFound)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM theme_selection WHERE theme_id = ?`, id).Scan(&selected); err != nil {
			return err
		}
		if selected > 0 {
			return fmt.Errorf("theme %s is selected by %d: %w", id, selected, domain.ErrInUse)
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM themes WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete theme: %w", err)
	}
	return nil
}

// Selection returns the human's choice.
func (r *Themes) Selection(ctx context.Context, human string) (domain.Selection, bool, error) {
	var s domain.Selection
	var mode string
	err := r.db.SQL().QueryRowContext(ctx,
		`SELECT theme_id, mode FROM theme_selection WHERE human = ?`, human).Scan(&s.ThemeID, &mode)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Selection{}, false, nil
	}
	if err != nil {
		return domain.Selection{}, false, fmt.Errorf("get theme selection: %w", err)
	}
	s.Mode = domain.Mode(mode)
	return s, true, nil
}

// SetSelection stores the human's choice; the theme must exist.
func (r *Themes) SetSelection(ctx context.Context, human string, s domain.Selection) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireTheme(ctx, tx, s.ThemeID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO theme_selection (human, theme_id, mode) VALUES (?, ?, ?)
			 ON CONFLICT (human) DO UPDATE SET theme_id = excluded.theme_id, mode = excluded.mode`,
			human, s.ThemeID, string(s.Mode))
		return err
	})
	if err != nil {
		return fmt.Errorf("set theme selection: %w", err)
	}
	return nil
}

// ReassignSelections moves every selection of from to the theme to.
func (r *Themes) ReassignSelections(ctx context.Context, from, to string) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireTheme(ctx, tx, to); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE theme_selection SET theme_id = ? WHERE theme_id = ?`, to, from)
		return err
	})
	if err != nil {
		return fmt.Errorf("reassign theme selections: %w", err)
	}
	return nil
}

func requireTheme(ctx context.Context, tx *sql.Tx, id string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM themes WHERE id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("theme %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

func requireRow(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("theme %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("theme %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTheme(row rowScanner) (domain.InstalledTheme, error) {
	var t domain.InstalledTheme
	var builtin int
	var installed, doc string
	if err := row.Scan(&t.ID, &t.Name, &t.Version, &builtin, &t.PluginID, &installed, &doc); err != nil {
		return domain.InstalledTheme{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, installed)
	if err != nil {
		return domain.InstalledTheme{}, fmt.Errorf("theme %s: installed_at %q: %w", t.ID, installed, err)
	}
	t.Builtin, t.InstalledAt, t.Document = builtin == 1, at.UTC(), []byte(doc)
	return t, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
