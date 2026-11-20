// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/pluginsapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/plugins/adapters/http"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const manifestFixtures = "../../../../contract/fixtures/schemas/plugin-manifest/valid/"

const pluginID = "plg_01JD7ZK3M00000000000000001"

// fakePlugins is an in-memory PluginService. A non-nil err is returned by
// every method, so one test can drive each documented error status.
type fakePlugins struct {
	plugins    []domain.Plugin
	sources    []domain.SourceInfo
	catalog    []domain.CatalogEntry
	err        error
	installed  httpadapter.InstallInput
	lastFilter domain.PluginFilter
	lastQuery  domain.CatalogQuery
	lastTTL    time.Duration
	removed    string
}

var _ httpadapter.PluginService = (*fakePlugins)(nil)

func (f *fakePlugins) Sources(context.Context) ([]domain.SourceInfo, error) {
	return f.sources, f.err
}

func (f *fakePlugins) Catalog(_ context.Context, q domain.CatalogQuery) ([]domain.CatalogEntry, string, error) {
	f.lastQuery = q
	if f.err != nil {
		return nil, "", f.err
	}
	var out []domain.CatalogEntry
	for _, e := range f.catalog {
		if e.Ref > q.After {
			out = append(out, e)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
		return out, out[len(out)-1].Ref, nil
	}
	return out, "", nil
}

func (f *fakePlugins) List(_ context.Context, flt domain.PluginFilter, after string, limit int) ([]domain.Plugin, string, error) {
	f.lastFilter = flt
	if f.err != nil {
		return nil, "", f.err
	}
	var out []domain.Plugin
	for _, p := range f.plugins {
		if p.ID > after {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
		return out, out[len(out)-1].ID, nil
	}
	return out, "", nil
}

func (f *fakePlugins) Install(_ context.Context, in httpadapter.InstallInput) (domain.Plugin, error) {
	f.installed = in
	if f.err != nil {
		return domain.Plugin{}, f.err
	}
	p := f.plugins[0]
	p.Enabled = in.Enable
	return p, nil
}

func (f *fakePlugins) find(id string) (domain.Plugin, error) {
	if f.err != nil {
		return domain.Plugin{}, f.err
	}
	for _, p := range f.plugins {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.Plugin{}, fmt.Errorf("plugin %s: %w", id, domain.ErrNotFound)
}

func (f *fakePlugins) Get(_ context.Context, id string) (domain.Plugin, error) { return f.find(id) }

func (f *fakePlugins) Uninstall(_ context.Context, id string) error {
	if _, err := f.find(id); err != nil {
		return err
	}
	f.removed = id
	return nil
}

func (f *fakePlugins) Enable(_ context.Context, id string) (domain.Plugin, error) {
	p, err := f.find(id)
	p.Enabled = true
	return p, err
}

func (f *fakePlugins) Disable(_ context.Context, id string) (domain.Plugin, error) {
	p, err := f.find(id)
	p.Enabled = false
	return p, err
}

func (f *fakePlugins) IssuePanelToken(_ context.Context, id string, ttl time.Duration) (httpadapter.PanelToken, error) {
	f.lastTTL = ttl
	p, err := f.find(id)
	if err != nil {
		return httpadapter.PanelToken{}, err
	}
	now := coretest.Epoch
	return httpadapter.PanelToken{
		ID: "tok_01JD7ZK3M00000000000000009", Value: "secret-value", Label: "panel " + p.Name,
		Subject: "plugin:" + p.ID, Scopes: []string{"live:read"}, CreatedAt: now, ExpiresAt: now.Add(ttl),
	}, nil
}

func manifestFixture(t *testing.T, file string) domain.Manifest {
	t.Helper()
	raw, err := os.ReadFile(manifestFixtures + file) // #nosec G304 -- fixture path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	m, err := domain.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func samplePlugin(t *testing.T, id, name string) domain.Plugin {
	t.Helper()
	m := manifestFixture(t, "small-seed.json")
	var kinds []string
	for _, k := range m.Kinds() {
		kinds = append(kinds, string(k))
	}
	return domain.Plugin{
		ID: id, Name: name, Version: m.Version, Kinds: kinds, Source: string(domain.SourceAgentsOne),
		Ref: "https://example.com/seed.git", Enabled: true, Health: domain.HealthOK,
		InstalledBy: domain.Actor{Kind: "human", Name: "ada"}, InstalledAt: coretest.Epoch, Manifest: m,
	}
}

func apiOptions() core.APIOptions {
	return core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	}
}

// serve mounts groups on a mux and makes caller the request's caller; a nil
// caller means nobody signed in.
func serve(caller *core.Caller, mount func(mux *http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	mount(mux)
	if caller == nil {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), *caller)))
	})
}

func servePlugins(t *testing.T, caller *core.Caller, svc httpadapter.PluginService) http.Handler {
	t.Helper()
	return serve(caller, func(mux *http.ServeMux) {
		pluginsapi.Group(httpadapter.NewPluginsHandler(svc)).Register(mux, apiOptions())
	})
}

func newFake(t *testing.T) *fakePlugins {
	t.Helper()
	return &fakePlugins{
		plugins: []domain.Plugin{
			samplePlugin(t, "plg_01JD7ZK3M00000000000000001", "small-seed"),
			samplePlugin(t, "plg_01JD7ZK3M00000000000000002", "other-seed"),
		},
		sources: []domain.SourceInfo{
			{ID: string(domain.SourceAgentsOne), Name: "Agents One", URL: "https://plugins.example.com/agentsone", Enabled: true},
			{ID: string(domain.SourceMCPRegistry), Name: "MCP registry", URL: "https://registry.modelcontextprotocol.io", Enabled: false},
		},
		catalog: []domain.CatalogEntry{
			{Source: string(domain.SourceAgentsOne), Ref: "a-ref", Name: "alpha", Version: "1.0.0", Summary: "First.", Kinds: []string{"skill"}, Publisher: "acme", Homepage: "https://example.com"},
			{Source: string(domain.SourceMCPRegistry), Ref: "b-ref", Name: "beta", Version: "0.2.0", Summary: "Second.", Kinds: []string{"mcp-server"}, Publisher: "io.example", Installed: true},
		},
	}
}

type plugin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Kinds       []string `json:"kinds"`
	Source      string   `json:"source"`
	Ref         string   `json:"ref"`
	Enabled     bool     `json:"enabled"`
	Health      string   `json:"health"`
	InstalledBy string   `json:"installed_by"`
	InstalledAt string   `json:"installed_at"`
	Manifest    struct {
		Name string `json:"name"`
	} `json:"manifest"`
}

type pluginPage struct {
	Items      []plugin `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

func TestListPlugins_ReturnsPluginsWithManifestAndActor(t *testing.T) {
	t.Parallel()
	h := servePlugins(t, ada(), newFake(t))

	got := decode[pluginPage](t, send(t, h, http.MethodGet, "/v1/plugins", nil), http.StatusOK)

	if len(got.Items) != 2 || got.NextCursor != nil {
		t.Fatalf("page = %+v", got)
	}
	p := got.Items[0]
	if p.ID != pluginID || p.InstalledBy != "human:ada" || p.Manifest.Name != "small-seed" || p.Health != "ok" || p.Source != "agentsone" {
		t.Errorf("plugin = %+v", p)
	}
}

func TestListPlugins_PagesAndPassesFilters(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)

	first := decode[pluginPage](t, send(t, h, http.MethodGet, "/v1/plugins?limit=1&kind=skill&enabled=true", nil), http.StatusOK)
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first = %+v", first)
	}
	if fake.lastFilter.Kind != "skill" || fake.lastFilter.Enabled == nil || !*fake.lastFilter.Enabled {
		t.Errorf("filter = %+v", fake.lastFilter)
	}
	second := decode[pluginPage](t, send(t, h, http.MethodGet, "/v1/plugins?limit=1&cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != nil {
		t.Fatalf("second = %+v", second)
	}
}

func TestInstallPlugin_Returns201AndPassesTheRequest(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)

	rec := send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":"agentsone","ref":"https://example.com/seed.git","version":"1.2.3"}`))

	got := decode[plugin](t, rec, http.StatusCreated)
	want := httpadapter.InstallInput{Source: "agentsone", Ref: "https://example.com/seed.git", Version: "1.2.3", Enable: true}
	if fake.installed != want || got.Name != "small-seed" || !got.Enabled {
		t.Errorf("installed %+v, plugin %+v", fake.installed, got)
	}
}

func TestInstallPlugin_EnableFalseIsHonoured(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)

	rec := send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":"agentsone","ref":"x","enable":false}`))

	if got := decode[plugin](t, rec, http.StatusCreated); got.Enabled || fake.installed.Enable {
		t.Errorf("enable = %v / %v, want false", got.Enabled, fake.installed.Enable)
	}
}

func TestInstallPlugin_BadBodyIs400(t *testing.T) {
	t.Parallel()
	h := servePlugins(t, ada(), newFake(t))

	problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":`)), http.StatusBadRequest)
	problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":"nope","ref":"x"}`)), http.StatusBadRequest)
	problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":"agentsone","ref":""}`)), http.StatusBadRequest)
	problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":"agentsone","ref":"`+strings.Repeat("x", 501)+`"}`)), http.StatusBadRequest)
}

// A missing body reaches the handler as nil when the generated decoder is
// bypassed; it must still answer an invalid-request error.
func TestInstallPlugin_NilBodyIsInvalid(t *testing.T) {
	t.Parallel()
	h := httpadapter.NewPluginsHandler(newFake(t))

	_, err := h.InstallPlugin(context.Background(), pluginsapi.InstallPluginRequestObject{})

	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("error = %v, want core.ErrInvalid", err)
	}
}

// Reading panel files lands with its own change; until then it says so with
// 501 instead of pretending.
func TestGetPluginFile_IsNotImplementedYet(t *testing.T) {
	t.Parallel()
	h := httpadapter.NewPluginsHandler(newFake(t))

	_, err := h.GetPluginFile(context.Background(), pluginsapi.GetPluginFileRequestObject{PluginId: pluginID})

	if !errors.Is(err, core.ErrNotImplemented) {
		t.Fatalf("error = %v, want core.ErrNotImplemented", err)
	}
}

// Failures the contract has no status for pass through unchanged so the API
// layer answers its generic server error.
func TestPluginService_UnknownFailureIsNotMapped(t *testing.T) {
	t.Parallel()
	boom := errors.New("disk on fire")
	fake := newFake(t)
	fake.err = boom
	h := httpadapter.NewPluginsHandler(fake)
	ctx := context.Background()

	_, errSources := h.ListPluginSources(ctx, pluginsapi.ListPluginSourcesRequestObject{})
	_, errInstall := h.InstallPlugin(ctx, pluginsapi.InstallPluginRequestObject{Body: &api.PluginInstall{Source: "agentsone", Ref: "x"}})

	if !errors.Is(errSources, boom) || errors.Is(errSources, core.ErrUnavailable) || !errors.Is(errInstall, boom) {
		t.Fatalf("errors = %v / %v", errSources, errInstall)
	}
}

func TestPluginErrors_MapToTheDocumentedStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"already installed", domain.ErrAlreadyInstalled, http.StatusConflict},
		{"in use", domain.ErrInUse, http.StatusConflict},
		{"builtin", domain.ErrBuiltin, http.StatusConflict},
		{"not found", domain.ErrNotFound, http.StatusNotFound},
		{"invalid manifest", domain.ErrInvalidManifest, http.StatusBadRequest},
		{"unsafe path", domain.ErrUnsafePath, http.StatusBadRequest},
		{"too large", domain.ErrTooLarge, http.StatusBadRequest},
		{"integrity", domain.ErrIntegrity, http.StatusBadRequest},
		{"unsupported source", domain.ErrUnsupportedSource, http.StatusBadRequest},
		{"nothing to install", domain.ErrNothingToInstall, http.StatusBadRequest},
		{"not semantic", domain.ErrNotSemantic, http.StatusBadRequest},
		{"incompatible", domain.ErrIncompatible, http.StatusBadRequest},
		{"core forbidden", core.ErrForbidden, http.StatusForbidden},
		{"core unavailable", core.ErrUnavailable, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(t)
			fake.err = fmt.Errorf("install: %w", tt.err)
			h := servePlugins(t, ada(), fake)

			rec := send(t, h, http.MethodPost, "/v1/plugins", []byte(`{"source":"agentsone","ref":"x"}`))

			problemStatus(t, rec, tt.status)
			if !strings.Contains(rec.Body.String(), "install:") {
				t.Errorf("message lost: %s", rec.Body)
			}
		})
	}
}

func TestGetPlugin(t *testing.T) {
	t.Parallel()
	h := servePlugins(t, ada(), newFake(t))

	got := decode[plugin](t, send(t, h, http.MethodGet, "/v1/plugins/"+pluginID, nil), http.StatusOK)
	if got.ID != pluginID || got.Manifest.Name != "small-seed" {
		t.Errorf("plugin = %+v", got)
	}
	problemStatus(t, send(t, h, http.MethodGet, "/v1/plugins/plg_01JD7ZK3M00000000000000099", nil), http.StatusNotFound)
}

func TestUninstallPlugin(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)

	rec := send(t, h, http.MethodDelete, "/v1/plugins/"+pluginID, nil)
	if rec.Code != http.StatusNoContent || fake.removed != pluginID {
		t.Fatalf("status %d, removed %q", rec.Code, fake.removed)
	}
	problemStatus(t, send(t, h, http.MethodDelete, "/v1/plugins/plg_01JD7ZK3M00000000000000099", nil), http.StatusNotFound)
	fake.err = domain.ErrInUse
	problemStatus(t, send(t, h, http.MethodDelete, "/v1/plugins/"+pluginID, nil), http.StatusConflict)
}

func TestEnableAndDisablePlugin(t *testing.T) {
	t.Parallel()
	h := servePlugins(t, ada(), newFake(t))

	off := decode[plugin](t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/disable", nil), http.StatusOK)
	on := decode[plugin](t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/enable", nil), http.StatusOK)

	if off.Enabled || !on.Enabled {
		t.Errorf("disabled enabled=%v, enabled enabled=%v", off.Enabled, on.Enabled)
	}
	problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/plg_01JD7ZK3M00000000000000099/enable", nil), http.StatusNotFound)
	problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/plg_01JD7ZK3M00000000000000099/disable", nil), http.StatusNotFound)
}

func TestListPluginSources_ReturnsAPage(t *testing.T) {
	t.Parallel()
	h := servePlugins(t, ada(), newFake(t))
	type src struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		URL     string `json:"url"`
		Enabled bool   `json:"enabled"`
	}
	type page struct {
		Items      []src   `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}

	first := decode[page](t, send(t, h, http.MethodGet, "/v1/plugin-sources?limit=1", nil), http.StatusOK)
	if len(first.Items) != 1 || first.Items[0].ID != "agentsone" || !first.Items[0].Enabled || first.NextCursor == nil {
		t.Fatalf("first = %+v", first)
	}
	second := decode[page](t, send(t, h, http.MethodGet, "/v1/plugin-sources?limit=1&cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].ID != "mcp-registry" || second.Items[0].Enabled || second.NextCursor != nil {
		t.Fatalf("second = %+v", second)
	}
}

func TestSearchPluginCatalog_PassesTheQueryAndPages(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)
	type entry struct {
		Source    string   `json:"source"`
		Ref       string   `json:"ref"`
		Name      string   `json:"name"`
		Summary   string   `json:"summary"`
		Kinds     []string `json:"kinds"`
		Homepage  *string  `json:"homepage"`
		Installed bool     `json:"installed"`
	}
	type page struct {
		Items      []entry `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}

	first := decode[page](t, send(t, h, http.MethodGet, "/v1/plugin-catalog?q=al&source=agentsone&kind=skill&limit=1", nil), http.StatusOK)

	want := domain.CatalogQuery{Text: "al", Source: "agentsone", Kind: "skill", Limit: 1}
	if fake.lastQuery != want {
		t.Errorf("query = %+v, want %+v", fake.lastQuery, want)
	}
	if len(first.Items) != 1 || first.Items[0].Homepage == nil || first.NextCursor == nil {
		t.Fatalf("first = %+v", first)
	}
	second := decode[page](t, send(t, h, http.MethodGet, "/v1/plugin-catalog?cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].Homepage != nil || !second.Items[0].Installed || second.NextCursor != nil {
		t.Fatalf("second = %+v", second)
	}
}

func TestSearchPluginCatalog_UpstreamDownIs503(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	fake.err = fmt.Errorf("registry: %w", core.ErrUnavailable)
	h := servePlugins(t, ada(), fake)

	problemStatus(t, send(t, h, http.MethodGet, "/v1/plugin-catalog", nil), http.StatusServiceUnavailable)
}

type issued struct {
	ID      string   `json:"id"`
	Token   string   `json:"token"`
	Label   string   `json:"label"`
	Subject string   `json:"subject"`
	Scopes  []string `json:"scopes"`
}

func TestIssuePanelToken_HumanGetsATokenWithTheDefaultTTL(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)

	got := decode[issued](t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/panel-token", []byte(`{}`)), http.StatusCreated)

	if got.Token != "secret-value" || got.Subject != "plugin:"+pluginID || len(got.Scopes) != 1 || !strings.HasPrefix(got.ID, "tok_") {
		t.Errorf("token = %+v", got)
	}
	if fake.lastTTL != time.Hour {
		t.Errorf("ttl = %v, want 1h", fake.lastTTL)
	}
}

func TestIssuePanelToken_TTLFromTheBody(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := servePlugins(t, ada(), fake)

	send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/panel-token", []byte(`{"ttl_seconds":120}`))

	if fake.lastTTL != 2*time.Minute {
		t.Errorf("ttl = %v, want 2m", fake.lastTTL)
	}
}

func TestIssuePanelToken_NilBodyUsesTheDefault(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	h := httpadapter.NewPluginsHandler(fake)
	ctx := core.WithCaller(context.Background(), *ada())

	if _, err := h.IssuePanelToken(ctx, pluginsapi.IssuePanelTokenRequestObject{PluginId: pluginID}); err != nil {
		t.Fatal(err)
	}

	if fake.lastTTL != time.Hour {
		t.Errorf("ttl = %v, want 1h", fake.lastTTL)
	}
}

func TestIssuePanelToken_Refusals(t *testing.T) {
	t.Parallel()
	agent := &core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 1001}

	t.Run("agent is forbidden", func(t *testing.T) {
		t.Parallel()
		h := servePlugins(t, agent, newFake(t))
		problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/panel-token", []byte(`{}`)), http.StatusForbidden)
	})
	t.Run("nobody signed in is 401", func(t *testing.T) {
		t.Parallel()
		h := servePlugins(t, nil, newFake(t))
		problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/panel-token", []byte(`{}`)), http.StatusUnauthorized)
	})
	t.Run("unknown plugin", func(t *testing.T) {
		t.Parallel()
		h := servePlugins(t, ada(), newFake(t))
		problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/plg_01JD7ZK3M00000000000000099/panel-token", []byte(`{}`)), http.StatusNotFound)
	})
	t.Run("ttl out of range", func(t *testing.T) {
		t.Parallel()
		h := servePlugins(t, ada(), newFake(t))
		problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/panel-token", []byte(`{"ttl_seconds":5}`)), http.StatusBadRequest)
	})
	t.Run("plugin without a panel is a conflict", func(t *testing.T) {
		t.Parallel()
		fake := newFake(t)
		fake.err = fmt.Errorf("no panel: %w", domain.ErrInUse)
		h := servePlugins(t, ada(), fake)
		problemStatus(t, send(t, h, http.MethodPost, "/v1/plugins/"+pluginID+"/panel-token", []byte(`{}`)), http.StatusConflict)
	})
}
