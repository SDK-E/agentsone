// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/themesapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// ThemesHandler serves the themes group. Domain and core errors come back
// from the app layer already wrapped with their core sentinel, so the API
// layer turns them into problem responses.
type ThemesHandler struct{ themes *app.Themes }

var _ themesapi.StrictServerInterface = (*ThemesHandler)(nil)

// NewThemesHandler returns the themes handler over the use cases.
func NewThemesHandler(themes *app.Themes) *ThemesHandler { return &ThemesHandler{themes: themes} }

// ListThemes returns one page of installed themes.
func (h *ThemesHandler) ListThemes(ctx context.Context, req themesapi.ListThemesRequestObject) (themesapi.ListThemesResponseObject, error) {
	cursor, limit := "", 0
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	page, err := h.themes.List(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}
	items := make([]api.InstalledTheme, 0, len(page.Items))
	for _, t := range page.Items {
		dto, err := toInstalledTheme(t)
		if err != nil {
			return nil, err
		}
		items = append(items, dto)
	}
	out := themesapi.ListThemes200JSONResponse{Items: items}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

// ImportTheme installs the theme in the request body.
func (h *ThemesHandler) ImportTheme(ctx context.Context, req themesapi.ImportThemeRequestObject) (themesapi.ImportThemeResponseObject, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("import theme: a theme is required: %w", core.ErrInvalid)
	}
	raw, err := json.Marshal(req.Body.Theme)
	if err != nil {
		return nil, fmt.Errorf("import theme: %w: %w", core.ErrInvalid, err)
	}
	t, err := h.themes.Import(ctx, raw)
	if err != nil {
		return nil, err
	}
	dto, err := toInstalledTheme(t)
	if err != nil {
		return nil, err
	}
	return themesapi.ImportTheme201JSONResponse(dto), nil
}

// GetTheme returns one installed theme.
func (h *ThemesHandler) GetTheme(ctx context.Context, req themesapi.GetThemeRequestObject) (themesapi.GetThemeResponseObject, error) {
	t, err := h.themes.Get(ctx, req.ThemeId)
	if err != nil {
		return nil, err
	}
	dto, err := toInstalledTheme(t)
	if err != nil {
		return nil, err
	}
	return themesapi.GetTheme200JSONResponse(dto), nil
}

// DeleteTheme removes an imported theme.
func (h *ThemesHandler) DeleteTheme(ctx context.Context, req themesapi.DeleteThemeRequestObject) (themesapi.DeleteThemeResponseObject, error) {
	if err := h.themes.Delete(ctx, req.ThemeId); err != nil {
		return nil, err
	}
	return themesapi.DeleteTheme204Response{}, nil
}

// GetThemeSelection returns the calling human's theme and mode.
func (h *ThemesHandler) GetThemeSelection(ctx context.Context, _ themesapi.GetThemeSelectionRequestObject) (themesapi.GetThemeSelectionResponseObject, error) {
	s, err := h.themes.Selection(ctx)
	if err != nil {
		return nil, err
	}
	return themesapi.GetThemeSelection200JSONResponse(toSelection(s)), nil
}

// SetThemeSelection changes the calling human's theme, mode or both.
func (h *ThemesHandler) SetThemeSelection(ctx context.Context, req themesapi.SetThemeSelectionRequestObject) (themesapi.SetThemeSelectionResponseObject, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("set theme selection: a body is required: %w", core.ErrInvalid)
	}
	var mode *domain.Mode
	if req.Body.Mode != nil {
		m := domain.Mode(*req.Body.Mode)
		mode = &m
	}
	s, err := h.themes.SetSelection(ctx, req.Body.ThemeId, mode)
	if err != nil {
		return nil, err
	}
	return themesapi.SetThemeSelection200JSONResponse(toSelection(s)), nil
}

func toSelection(s domain.Selection) api.ThemeSelection {
	return api.ThemeSelection{ThemeId: s.ThemeID, Mode: api.ThemeMode(s.Mode)}
}

// toInstalledTheme maps a stored theme to the generated DTO. The document was
// validated on the way in, so a decode failure means the store is corrupt.
func toInstalledTheme(t domain.InstalledTheme) (api.InstalledTheme, error) {
	var doc api.Theme
	if err := json.Unmarshal(t.Document, &doc); err != nil {
		return api.InstalledTheme{}, fmt.Errorf("theme %s: stored document: %w", t.ID, err)
	}
	dto := api.InstalledTheme{
		Id: t.ID, Name: t.Name, Builtin: t.Builtin, InstalledAt: t.InstalledAt, Theme: doc,
	}
	if t.PluginID != "" {
		dto.PluginId = &t.PluginID
	}
	return dto, nil
}
