// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/pluginsapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const (
	maxRefLength    = 500
	defaultPanelTTL = time.Hour
	minPanelTTL     = time.Minute
	maxPanelTTL     = 24 * time.Hour
)

// InstallInput is what an install request asks for.
type InstallInput struct {
	Source  string
	Ref     string
	Version string // empty: latest
	Enable  bool
}

// PanelToken is a freshly issued panel token. Value is the secret and is
// shown once.
type PanelToken struct {
	ID        string
	Value     string
	Label     string
	Subject   string // <kind>:<name>, here plugin:<plugin id>
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// PluginService is what the plugins group needs from the app layer. Errors
// wrap core sentinels or domain sentinels; both reach the client as the
// right problem.
type PluginService interface {
	Sources(ctx context.Context) ([]domain.SourceInfo, error)
	// Catalog returns one page of entries and the cursor of the next page,
	// empty on the last page.
	Catalog(ctx context.Context, q domain.CatalogQuery) ([]domain.CatalogEntry, string, error)
	// List returns plugins after the cursor and the next cursor, empty on the
	// last page.
	List(ctx context.Context, f domain.PluginFilter, after string, limit int) ([]domain.Plugin, string, error)
	Install(ctx context.Context, in InstallInput) (domain.Plugin, error)
	Get(ctx context.Context, id string) (domain.Plugin, error)
	Uninstall(ctx context.Context, id string) error
	Enable(ctx context.Context, id string) (domain.Plugin, error)
	Disable(ctx context.Context, id string) (domain.Plugin, error)
	IssuePanelToken(ctx context.Context, id string, ttl time.Duration) (PanelToken, error)
}

// PluginsHandler serves the plugins group.
type PluginsHandler struct{ svc PluginService }

var _ pluginsapi.StrictServerInterface = (*PluginsHandler)(nil)

// NewPluginsHandler returns the plugins handler over the service.
func NewPluginsHandler(svc PluginService) *PluginsHandler { return &PluginsHandler{svc: svc} }

// ListPluginSources returns one page of the configured catalog sources.
func (h *PluginsHandler) ListPluginSources(ctx context.Context, req pluginsapi.ListPluginSourcesRequestObject) (pluginsapi.ListPluginSourcesResponseObject, error) {
	all, err := h.svc.Sources(ctx)
	if err != nil {
		return nil, coreErr(err)
	}
	cursor, limit := paging(req.Params.Cursor, req.Params.Limit)
	items, next := page(all, func(s domain.SourceInfo) string { return s.ID }, cursor, limit)
	out := pluginsapi.ListPluginSources200JSONResponse{Items: make([]api.PluginSource, 0, len(items)), NextCursor: nextCursor(next)}
	for _, s := range items {
		out.Items = append(out.Items, api.PluginSource{Id: api.PluginSourceId(s.ID), Name: s.Name, Url: s.URL, Enabled: s.Enabled})
	}
	return out, nil
}

// SearchPluginCatalog searches every enabled source.
func (h *PluginsHandler) SearchPluginCatalog(ctx context.Context, req pluginsapi.SearchPluginCatalogRequestObject) (pluginsapi.SearchPluginCatalogResponseObject, error) {
	cursor, limit := paging(req.Params.Cursor, req.Params.Limit)
	q := domain.CatalogQuery{After: cursor, Limit: limit}
	if req.Params.Q != nil {
		q.Text = *req.Params.Q
	}
	if req.Params.Source != nil {
		q.Source = string(*req.Params.Source)
	}
	if req.Params.Kind != nil {
		q.Kind = string(*req.Params.Kind)
	}
	entries, next, err := h.svc.Catalog(ctx, q)
	if err != nil {
		return nil, coreErr(err)
	}
	out := pluginsapi.SearchPluginCatalog200JSONResponse{Items: make([]api.CatalogEntry, 0, len(entries)), NextCursor: nextCursor(next)}
	for _, e := range entries {
		out.Items = append(out.Items, toCatalogEntry(e))
	}
	return out, nil
}

// ListPlugins returns one page of installed plugins.
func (h *PluginsHandler) ListPlugins(ctx context.Context, req pluginsapi.ListPluginsRequestObject) (pluginsapi.ListPluginsResponseObject, error) {
	cursor, limit := paging(req.Params.Cursor, req.Params.Limit)
	f := domain.PluginFilter{Enabled: req.Params.Enabled}
	if req.Params.Kind != nil {
		f.Kind = string(*req.Params.Kind)
	}
	plugins, next, err := h.svc.List(ctx, f, cursor, limit)
	if err != nil {
		return nil, coreErr(err)
	}
	out := pluginsapi.ListPlugins200JSONResponse{Items: make([]api.Plugin, 0, len(plugins)), NextCursor: nextCursor(next)}
	for _, p := range plugins {
		dto, err := toPlugin(p)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, dto)
	}
	return out, nil
}

// InstallPlugin installs a plugin from a catalog source.
func (h *PluginsHandler) InstallPlugin(ctx context.Context, req pluginsapi.InstallPluginRequestObject) (pluginsapi.InstallPluginResponseObject, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("install plugin: a body is required: %w", core.ErrInvalid)
	}
	if !domain.SourceID(req.Body.Source).Valid() {
		return nil, fmt.Errorf("install plugin: unknown source %q: %w", req.Body.Source, core.ErrInvalid)
	}
	if req.Body.Ref == "" || len(req.Body.Ref) > maxRefLength {
		return nil, fmt.Errorf("install plugin: ref must be 1 to %d characters: %w", maxRefLength, core.ErrInvalid)
	}
	in := InstallInput{Source: string(req.Body.Source), Ref: req.Body.Ref, Enable: true}
	if req.Body.Version != nil {
		in.Version = *req.Body.Version
	}
	if req.Body.Enable != nil {
		in.Enable = *req.Body.Enable
	}
	p, err := h.svc.Install(ctx, in)
	if err != nil {
		return nil, coreErr(err)
	}
	dto, err := toPlugin(p)
	if err != nil {
		return nil, err
	}
	return pluginsapi.InstallPlugin201JSONResponse(dto), nil
}

// GetPlugin returns one installed plugin.
func (h *PluginsHandler) GetPlugin(ctx context.Context, req pluginsapi.GetPluginRequestObject) (pluginsapi.GetPluginResponseObject, error) {
	p, err := h.svc.Get(ctx, req.PluginId)
	if err != nil {
		return nil, coreErr(err)
	}
	dto, err := toPlugin(p)
	if err != nil {
		return nil, err
	}
	return pluginsapi.GetPlugin200JSONResponse(dto), nil
}

// UninstallPlugin removes a plugin and what it registered.
func (h *PluginsHandler) UninstallPlugin(ctx context.Context, req pluginsapi.UninstallPluginRequestObject) (pluginsapi.UninstallPluginResponseObject, error) {
	if err := h.svc.Uninstall(ctx, req.PluginId); err != nil {
		return nil, coreErr(err)
	}
	return pluginsapi.UninstallPlugin204Response{}, nil
}

// EnablePlugin turns a plugin on.
func (h *PluginsHandler) EnablePlugin(ctx context.Context, req pluginsapi.EnablePluginRequestObject) (pluginsapi.EnablePluginResponseObject, error) {
	p, err := h.svc.Enable(ctx, req.PluginId)
	if err != nil {
		return nil, coreErr(err)
	}
	dto, err := toPlugin(p)
	if err != nil {
		return nil, err
	}
	return pluginsapi.EnablePlugin200JSONResponse(dto), nil
}

// DisablePlugin turns a plugin off without removing it.
func (h *PluginsHandler) DisablePlugin(ctx context.Context, req pluginsapi.DisablePluginRequestObject) (pluginsapi.DisablePluginResponseObject, error) {
	p, err := h.svc.Disable(ctx, req.PluginId)
	if err != nil {
		return nil, coreErr(err)
	}
	dto, err := toPlugin(p)
	if err != nil {
		return nil, err
	}
	return pluginsapi.DisablePlugin200JSONResponse(dto), nil
}

// IssuePanelToken issues a short-lived token for a panel frame. Only a signed
// in human may ask.
func (h *PluginsHandler) IssuePanelToken(ctx context.Context, req pluginsapi.IssuePanelTokenRequestObject) (pluginsapi.IssuePanelTokenResponseObject, error) {
	caller, ok := core.CallerFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("issue panel token: %w", core.ErrUnauthorized)
	}
	if caller.Kind != core.CallerHuman {
		return nil, fmt.Errorf("issue panel token: only a signed-in human may ask: %w", core.ErrForbidden)
	}
	ttl := defaultPanelTTL
	if req.Body != nil && req.Body.TtlSeconds != nil {
		ttl = time.Duration(*req.Body.TtlSeconds) * time.Second
	}
	if ttl < minPanelTTL || ttl > maxPanelTTL {
		return nil, fmt.Errorf("issue panel token: ttl_seconds must be between 60 and 86400: %w", core.ErrInvalid)
	}
	tok, err := h.svc.IssuePanelToken(ctx, req.PluginId, ttl)
	if err != nil {
		return nil, coreErr(err)
	}
	scopes := tok.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return pluginsapi.IssuePanelToken201JSONResponse{
		Id: tok.ID, Token: tok.Value, Label: tok.Label, Subject: tok.Subject, Scopes: scopes,
		CreatedAt: tok.CreatedAt, ExpiresAt: tok.ExpiresAt,
	}, nil
}

// coreErr gives a domain error the core sentinel the API layer maps to a
// status, keeping the message. Errors that already carry a core sentinel, and
// unknown errors, pass through; unknown ones become a 500.
func coreErr(err error) error {
	if err == nil {
		return nil
	}
	for _, c := range coreSentinels {
		if errors.Is(err, c) {
			return err
		}
	}
	for _, m := range domainToCore {
		if errors.Is(err, m.domain) {
			return fmt.Errorf("%w: %w", m.core, err)
		}
	}
	return err
}

var coreSentinels = []error{
	core.ErrInvalid, core.ErrUnauthorized, core.ErrForbidden, core.ErrNotFound, core.ErrConflict,
	core.ErrNotImplemented, core.ErrUnavailable, core.ErrBudget, core.ErrFrozen,
}

var domainToCore = []struct{ domain, core error }{
	{domain.ErrNotFound, core.ErrNotFound},
	{domain.ErrAlreadyInstalled, core.ErrConflict},
	{domain.ErrInUse, core.ErrConflict},
	{domain.ErrBuiltin, core.ErrConflict},
	{domain.ErrInvalidManifest, core.ErrInvalid},
	{domain.ErrEmptyPlugin, core.ErrInvalid},
	{domain.ErrUnsafePath, core.ErrInvalid},
	{domain.ErrNotSemantic, core.ErrInvalid},
	{domain.ErrUnsupportedSource, core.ErrInvalid},
	{domain.ErrNothingToInstall, core.ErrInvalid},
	{domain.ErrTooLarge, core.ErrInvalid},
	{domain.ErrIntegrity, core.ErrInvalid},
	{domain.ErrInvalidTheme, core.ErrInvalid},
	{domain.ErrIncompatible, core.ErrInvalid},
}

// paging reads the cursor and limit parameters.
func paging(cursor *string, limit *int) (string, int) {
	c, l := "", 0
	if cursor != nil {
		c = *cursor
	}
	if limit != nil {
		l = *limit
	}
	return c, l
}

// page cuts items (sorted by key, ascending) after the cursor to limit
// entries. next is the key of the last item when more follow, else empty. A
// limit of 0 returns every item after the cursor.
func page[T any](items []T, key func(T) string, cursor string, limit int) ([]T, string) {
	start := 0
	if cursor != "" {
		for start < len(items) && key(items[start]) <= cursor {
			start++
		}
	}
	rest := items[start:]
	if limit <= 0 || len(rest) <= limit {
		return rest, ""
	}
	rest = rest[:limit]
	return rest, key(rest[len(rest)-1])
}

func nextCursor(next string) *string {
	if next == "" {
		return nil
	}
	return &next
}

func toCatalogEntry(e domain.CatalogEntry) api.CatalogEntry {
	kinds := make([]api.PluginKind, 0, len(e.Kinds))
	for _, k := range e.Kinds {
		kinds = append(kinds, api.PluginKind(k))
	}
	dto := api.CatalogEntry{
		Source: api.PluginSourceId(e.Source), Ref: e.Ref, Name: e.Name, Version: e.Version, Summary: e.Summary,
		Kinds: kinds, Publisher: e.Publisher, Installed: e.Installed,
	}
	if e.Homepage != "" {
		dto.Homepage = &e.Homepage
	}
	return dto
}

// toPlugin maps an installed plugin to the generated DTO. The manifest was
// validated on the way in, so a decode failure means the store is corrupt.
func toPlugin(p domain.Plugin) (api.Plugin, error) {
	raw, err := p.Manifest.Encode()
	if err != nil {
		return api.Plugin{}, fmt.Errorf("plugin %s: encode manifest: %w", p.ID, err)
	}
	var manifest api.PluginManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return api.Plugin{}, fmt.Errorf("plugin %s: stored manifest: %w", p.ID, err)
	}
	kinds := make([]api.PluginKind, 0, len(p.Kinds))
	for _, k := range p.Kinds {
		kinds = append(kinds, api.PluginKind(k))
	}
	return api.Plugin{
		Id: p.ID, Name: p.Name, Version: p.Version, Kinds: kinds, Source: api.PluginSourceId(p.Source),
		Ref: p.Ref, Enabled: p.Enabled, Health: api.PluginHealth(p.Health),
		InstalledBy: p.InstalledBy.Kind + ":" + p.InstalledBy.Name, InstalledAt: p.InstalledAt, Manifest: manifest,
	}, nil
}
