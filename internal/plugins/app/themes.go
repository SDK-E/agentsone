// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const (
	themeIDPrefix     = "thm"
	themeDefaultLimit = 50
	themeMaxLimit     = 200
	themeSystemActor  = "system:plugins"
)

// Themes are the use cases of the themes tag: importing, listing and
// deleting themes, and each human's theme and mode.
type Themes struct {
	repo  domain.ThemeRepository
	audit core.AuditLog
	clock core.Clock
	ids   core.IDs
}

// NewThemes returns the theme use cases over repo. Every change is recorded in
// audit.
func NewThemes(repo domain.ThemeRepository, audit core.AuditLog, clock core.Clock, ids core.IDs) *Themes {
	return &Themes{repo: repo, audit: audit, clock: clock, ids: ids}
}

// ThemePage is one page of themes. NextCursor is empty on the last page.
type ThemePage struct {
	Items      []domain.InstalledTheme
	NextCursor string
}

// List returns up to limit themes after cursor (a theme id from a previous page).
func (t *Themes) List(ctx context.Context, cursor string, limit int) (ThemePage, error) {
	if cursor != "" && !strings.HasPrefix(cursor, themeIDPrefix+"_") {
		return ThemePage{}, fmt.Errorf("cursor %q: %w", cursor, core.ErrInvalid)
	}
	switch {
	case limit <= 0:
		limit = themeDefaultLimit
	case limit > themeMaxLimit:
		limit = themeMaxLimit
	}
	items, err := t.repo.List(ctx, cursor, limit+1)
	if err != nil {
		return ThemePage{}, themeError("list themes", err)
	}
	page := ThemePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = page.Items[limit-1].ID
	}
	if page.Items == nil {
		page.Items = []domain.InstalledTheme{}
	}
	return page, nil
}

// Get returns one theme.
func (t *Themes) Get(ctx context.Context, id string) (domain.InstalledTheme, error) {
	th, err := t.repo.Get(ctx, id)
	if err != nil {
		return domain.InstalledTheme{}, themeError("get theme", err)
	}
	return th, nil
}

// Import validates raw and installs it as a theme that was imported by hand.
func (t *Themes) Import(ctx context.Context, raw []byte) (domain.InstalledTheme, error) {
	return t.install(ctx, raw, false, "")
}

// EnsureBuiltin installs raw as the built-in default theme. It is safe to
// call on every start: an existing built-in theme is kept (and moved to the
// new version when raw is newer) without a second audit record.
func (t *Themes) EnsureBuiltin(ctx context.Context, raw []byte) (domain.InstalledTheme, error) {
	doc, err := domain.ParseTheme(raw)
	if err != nil {
		return domain.InstalledTheme{}, themeError("built-in theme", err)
	}
	existing, ok, err := t.repo.FindByName(ctx, doc.Name)
	if err != nil {
		return domain.InstalledTheme{}, themeError("built-in theme", err)
	}
	if !ok {
		return t.install(ctx, raw, true, "")
	}
	if !existing.Builtin {
		return domain.InstalledTheme{}, themeError("built-in theme",
			fmt.Errorf("a theme named %q is already installed: %w", doc.Name, domain.ErrAlreadyInstalled))
	}
	return t.refresh(ctx, existing, doc)
}

// ImportFromPlugin installs the theme a plugin carries. Calling it again for
// the same plugin updates the theme to a new version and otherwise does
// nothing. A theme of the same name that another owner installed is a conflict.
func (t *Themes) ImportFromPlugin(ctx context.Context, pluginID string, raw []byte) (domain.InstalledTheme, error) {
	doc, err := domain.ParseTheme(raw)
	if err != nil {
		return domain.InstalledTheme{}, themeError("plugin theme", err)
	}
	existing, ok, err := t.repo.FindByName(ctx, doc.Name)
	if err != nil {
		return domain.InstalledTheme{}, themeError("plugin theme", err)
	}
	if !ok {
		return t.install(ctx, raw, false, pluginID)
	}
	if existing.PluginID != pluginID || existing.Builtin {
		return domain.InstalledTheme{}, themeError("plugin theme",
			fmt.Errorf("a theme named %q is already installed: %w", doc.Name, domain.ErrAlreadyInstalled))
	}
	return t.refresh(ctx, existing, doc)
}

// RemoveFromPlugin deletes the themes a plugin carries. Humans who selected
// one move to the built-in theme and keep their mode.
func (t *Themes) RemoveFromPlugin(ctx context.Context, pluginID string) error {
	themes, err := t.repo.ByPlugin(ctx, pluginID)
	if err != nil {
		return themeError("remove plugin themes", err)
	}
	builtin, hasBuiltin, err := t.repo.Builtin(ctx)
	if err != nil {
		return themeError("remove plugin themes", err)
	}
	for _, th := range themes {
		if hasBuiltin {
			if err := t.repo.ReassignSelections(ctx, th.ID, builtin.ID); err != nil {
				return themeError("remove plugin themes", err)
			}
		}
		if err := t.repo.Delete(ctx, th.ID); err != nil {
			return themeError("remove plugin themes", err)
		}
		if err := t.record(ctx, "theme.delete", th.ID, th.Name, th.Version); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes an imported theme. The built-in theme and a theme some human
// selected cannot be deleted.
func (t *Themes) Delete(ctx context.Context, id string) error {
	th, err := t.repo.Get(ctx, id)
	if err != nil {
		return themeError("delete theme", err)
	}
	if err := th.CheckDeletable(); err != nil {
		return themeError("delete theme", err)
	}
	if err := t.repo.Delete(ctx, id); err != nil {
		return themeError("delete theme", err)
	}
	return t.record(ctx, "theme.delete", th.ID, th.Name, th.Version)
}

// Selection returns the calling human's theme and mode: the built-in theme in
// system mode until they choose.
func (t *Themes) Selection(ctx context.Context) (domain.Selection, error) {
	who, err := signedInHuman(ctx)
	if err != nil {
		return domain.Selection{}, err
	}
	return t.selectionOf(ctx, who)
}

// SetSelection changes the calling human's theme, mode or both; nil leaves a
// part as it is.
func (t *Themes) SetSelection(ctx context.Context, themeID *string, mode *domain.Mode) (domain.Selection, error) {
	who, err := signedInHuman(ctx)
	if err != nil {
		return domain.Selection{}, err
	}
	if themeID == nil && mode == nil {
		return domain.Selection{}, fmt.Errorf("set theme selection: give a theme_id, a mode or both: %w", core.ErrInvalid)
	}
	if mode != nil {
		if _, err := domain.ParseMode(string(*mode)); err != nil {
			return domain.Selection{}, themeError("set theme selection", err)
		}
	}
	next, err := t.selectionOf(ctx, who)
	if err != nil {
		return domain.Selection{}, err
	}
	if themeID != nil {
		next.ThemeID = *themeID
	}
	if mode != nil {
		next.Mode = *mode
	}
	if err := t.repo.SetSelection(ctx, who, next); err != nil {
		return domain.Selection{}, themeError("set theme selection", err)
	}
	if err := t.record(ctx, "theme.select", next.ThemeID, "", string(next.Mode)); err != nil {
		return domain.Selection{}, err
	}
	return next, nil
}

func (t *Themes) selectionOf(ctx context.Context, human string) (domain.Selection, error) {
	s, ok, err := t.repo.Selection(ctx, human)
	if err != nil {
		return domain.Selection{}, themeError("get theme selection", err)
	}
	if ok {
		return s, nil
	}
	builtin, ok, err := t.repo.Builtin(ctx)
	if err != nil {
		return domain.Selection{}, themeError("get theme selection", err)
	}
	if !ok {
		return domain.Selection{}, fmt.Errorf("get theme selection: the built-in theme is not installed: %w", core.ErrNotFound)
	}
	return domain.DefaultSelection(builtin.ID), nil
}

func (t *Themes) install(ctx context.Context, raw []byte, builtin bool, pluginID string) (domain.InstalledTheme, error) {
	doc, err := domain.ParseTheme(raw)
	if err != nil {
		return domain.InstalledTheme{}, themeError("import theme", err)
	}
	th := domain.InstalledTheme{
		ID: t.ids.New(themeIDPrefix), Name: doc.Name, Version: doc.Version,
		Builtin: builtin, PluginID: pluginID,
		InstalledAt: t.clock.Now().UTC(), Document: doc.Document,
	}
	if err := t.repo.Insert(ctx, th); err != nil {
		return domain.InstalledTheme{}, themeError("import theme", err)
	}
	if err := t.record(ctx, "theme.import", th.ID, th.Name, th.Version); err != nil {
		return domain.InstalledTheme{}, err
	}
	return th, nil
}

// refresh moves an installed theme to the version of doc when they differ.
func (t *Themes) refresh(ctx context.Context, existing domain.InstalledTheme, doc domain.Theme) (domain.InstalledTheme, error) {
	if existing.Version == doc.Version {
		return existing, nil
	}
	existing.Version, existing.Document = doc.Version, doc.Document
	if err := t.repo.Update(ctx, existing); err != nil {
		return domain.InstalledTheme{}, themeError("update theme", err)
	}
	if err := t.record(ctx, "theme.update", existing.ID, existing.Name, existing.Version); err != nil {
		return domain.InstalledTheme{}, err
	}
	return existing, nil
}

func (t *Themes) record(ctx context.Context, action, id, name, version string) error {
	detail, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	entry := core.AuditEntry{Actor: themeActor(ctx), Action: action, Target: "theme:" + id, Detail: detail}
	if err := t.audit.Append(ctx, entry); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

func themeActor(ctx context.Context) string {
	c, ok := core.CallerFrom(ctx)
	if !ok || c.Name == "" {
		return themeSystemActor
	}
	return string(c.Kind) + ":" + c.Name
}

// signedInHuman returns the name of the human calling. The selection belongs
// to signed-in humans, and the contract answers anybody else with 401.
func signedInHuman(ctx context.Context) (string, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok || c.Name == "" {
		return "", fmt.Errorf("theme selection: %w", core.ErrUnauthorized)
	}
	if c.Kind != core.CallerHuman {
		return "", fmt.Errorf("theme selection belongs to a signed-in human, not %s %q: %w", c.Kind, c.Name, core.ErrUnauthorized)
	}
	return c.Name, nil
}

// themeError wraps a domain error with the core sentinel that gives it its
// HTTP status. Errors from the repository that are not domain errors pass
// through, and become a 500.
func themeError(op string, err error) error {
	switch {
	case errors.Is(err, domain.ErrTooLarge):
		return fmt.Errorf("%s: %w: %w", op, core.NewProblem(http.StatusRequestEntityTooLarge, "payload-too-large", err.Error()), err)
	case errors.Is(err, domain.ErrInvalidTheme):
		return fmt.Errorf("%s: %w: %w", op, core.ErrInvalid, err)
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%s: %w: %w", op, core.ErrNotFound, err)
	case errors.Is(err, domain.ErrAlreadyInstalled), errors.Is(err, domain.ErrInUse), errors.Is(err, domain.ErrBuiltin):
		return fmt.Errorf("%s: %w: %w", op, core.ErrConflict, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
