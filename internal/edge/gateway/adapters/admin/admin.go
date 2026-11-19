// SPDX-License-Identifier: MIT

// Package admin serves the gateway's routes on the edge admin socket: providers, write-only
// provider keys, and the budget. The routes and their types are in package adminapi; the edge
// serves them on the shared admin mux.
package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	budget "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/keyfile"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// Keys is the provider key store.
type Keys interface {
	Get(ctx context.Context, p routing.ProviderID) (string, error)
	Set(ctx context.Context, p routing.ProviderID, key string) error
	Delete(ctx context.Context, p routing.ProviderID) error
}

// Providers reads and switches the providers in gateway.toml.
type Providers interface {
	List() ([]gatewayconfig.Provider, error)
	// SetEnabled returns gatewayconfig.ErrUnknownProvider for an id not in the file.
	SetEnabled(id string, on bool) error
}

// Budget is the budget service.
type Budget interface {
	State() budget.State
	SetTotal(ctx context.Context, total budget.Micros) error
	SetPeriod(ctx context.Context, p budget.Period) error
	SetDefence(ctx context.Context, on bool) error
	SetDefenceShare(ctx context.Context, pct int) error
}

// Options are what the routes work on.
type Options struct {
	Keys      Keys
	Providers Providers
	Budget    Budget
	// Changed is called after a provider is added or removed, so routing applies it at once.
	Changed func()
	Log     *slog.Logger
}

// Register adds the gateway routes to mux.
func Register(mux *http.ServeMux, o Options) {
	if o.Changed == nil {
		o.Changed = func() {}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	h := handlers{o}
	mux.HandleFunc(adminapi.RouteProviders, h.providers)
	mux.HandleFunc(adminapi.RouteAddProvider, h.switchProvider(true))
	mux.HandleFunc(adminapi.RouteRemoveProvider, h.switchProvider(false))
	mux.HandleFunc(adminapi.RouteSetKey, h.setKey)
	mux.HandleFunc(adminapi.RouteClearKey, h.clearKey)
	mux.HandleFunc(adminapi.RouteBudget, h.budget)
	mux.HandleFunc(adminapi.RouteSetBudget, h.setBudget)
	mux.HandleFunc(adminapi.RouteSetDefence, h.setDefence)
}

type handlers struct{ o Options }

func (h handlers) keyState(ctx context.Context, id string) (adminapi.KeyState, error) {
	key, err := h.o.Keys.Get(ctx, routing.ProviderID(id))
	if errors.Is(err, gatewayapp.ErrKeyNotFound) {
		return adminapi.KeyState{}, nil
	}
	if err != nil {
		return adminapi.KeyState{}, err
	}
	return adminapi.KeyState{Set: true, Fingerprint: adminapi.Fingerprint(key)}, nil
}

func (h handlers) provider(ctx context.Context, p gatewayconfig.Provider) (adminapi.Provider, error) {
	ks, err := h.keyState(ctx, p.ID)
	if err != nil {
		return adminapi.Provider{}, err
	}
	out := adminapi.Provider{ID: p.ID, Enabled: p.Enabled, KeyRequired: p.KeyRequired, Key: ks, Models: []adminapi.Model{}}
	for _, m := range p.Models {
		out.Models = append(out.Models, adminapi.Model{ID: m.ID, Tier: m.Tier})
	}
	return out, nil
}

// find returns the provider with id, or a not-found problem.
func (h handlers) find(id string) (gatewayconfig.Provider, error) {
	list, err := h.o.Providers.List()
	if err != nil {
		return gatewayconfig.Provider{}, err
	}
	for _, p := range list {
		if p.ID == id {
			return p, nil
		}
	}
	return gatewayconfig.Provider{}, notFound(id)
}

func notFound(id string) *adminapi.Problem {
	return adminapi.NewProblem(http.StatusNotFound, adminapi.SlugNotFound, "The gateway knows no provider "+id+".")
}

func (h handlers) providers(w http.ResponseWriter, r *http.Request) {
	list, err := h.o.Providers.List()
	if err != nil {
		h.writeError(w, err)
		return
	}
	out := adminapi.ProviderList{Providers: []adminapi.Provider{}}
	for _, p := range list {
		ap, err := h.provider(r.Context(), p)
		if err != nil {
			h.writeError(w, err)
			return
		}
		out.Providers = append(out.Providers, ap)
	}
	adminapi.WriteJSON(w, http.StatusOK, out)
}

func (h handlers) switchProvider(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := h.o.Providers.SetEnabled(id, on); err != nil {
			if errors.Is(err, gatewayconfig.ErrUnknownProvider) {
				err = notFound(id)
			}
			h.writeError(w, err)
			return
		}
		h.o.Changed()
		if !on {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		p, err := h.find(id)
		if err == nil {
			var ap adminapi.Provider
			if ap, err = h.provider(r.Context(), p); err == nil {
				adminapi.WriteJSON(w, http.StatusOK, ap)
				return
			}
		}
		h.writeError(w, err)
	}
}

func (h handlers) setKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.find(id); err != nil {
		h.writeError(w, err)
		return
	}
	var in adminapi.SetKeyRequest
	if p := adminapi.Decode(r, &in); p != nil {
		adminapi.WriteProblem(w, p)
		return
	}
	if err := h.o.Keys.Set(r.Context(), routing.ProviderID(id), in.Key); err != nil {
		h.writeError(w, err)
		return
	}
	adminapi.WriteJSON(w, http.StatusOK, adminapi.KeyState{Set: true, Fingerprint: adminapi.Fingerprint(in.Key)})
}

func (h handlers) clearKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.find(id); err != nil {
		h.writeError(w, err)
		return
	}
	if err := h.o.Keys.Delete(r.Context(), routing.ProviderID(id)); err != nil && !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h handlers) budget(w http.ResponseWriter, _ *http.Request) {
	adminapi.WriteJSON(w, http.StatusOK, View(h.o.Budget.State()))
}

func (h handlers) setBudget(w http.ResponseWriter, r *http.Request) {
	var in adminapi.SetBudgetRequest
	if p := adminapi.Decode(r, &in); p != nil {
		adminapi.WriteProblem(w, p)
		return
	}
	// Check every field before changing any, so a refused request changes nothing.
	var period budget.Period
	if in.Period != nil {
		var err error
		if period, err = budget.ParsePeriod(*in.Period); err != nil || *in.Period == "" {
			h.writeError(w, errors.Join(budget.ErrInvalidPeriod, err))
			return
		}
	}
	if in.TotalMicros != nil && *in.TotalMicros < 0 {
		h.writeError(w, budget.ErrInvalidAmount)
		return
	}
	if in.TotalMicros != nil {
		if err := h.o.Budget.SetTotal(r.Context(), budget.Micros(*in.TotalMicros)); err != nil {
			h.writeError(w, err)
			return
		}
	}
	if in.Period != nil {
		if err := h.o.Budget.SetPeriod(r.Context(), period); err != nil {
			h.writeError(w, err)
			return
		}
	}
	h.budget(w, r)
}

func (h handlers) setDefence(w http.ResponseWriter, r *http.Request) {
	var in adminapi.SetDefenceRequest
	if p := adminapi.Decode(r, &in); p != nil {
		adminapi.WriteProblem(w, p)
		return
	}
	if in.SharePct != nil && (*in.SharePct < 0 || *in.SharePct > 100) {
		h.writeError(w, budget.ErrInvalidShare)
		return
	}
	if in.SharePct != nil {
		if err := h.o.Budget.SetDefenceShare(r.Context(), *in.SharePct); err != nil {
			h.writeError(w, err)
			return
		}
	}
	if in.Enabled != nil {
		if err := h.o.Budget.SetDefence(r.Context(), *in.Enabled); err != nil {
			h.writeError(w, err)
			return
		}
	}
	h.budget(w, r)
}

// View is the admin API view of the budget state.
func View(st budget.State) adminapi.Budget {
	return adminapi.Budget{
		Currency:        "USD",
		TotalMicros:     int64(st.Total),
		SpentMicros:     int64(st.Spent),
		ReservedMicros:  int64(st.Reserved),
		RemainingMicros: int64(st.Remaining),
		Exhausted:       st.Exhausted,
		Period:          string(st.Period),
		PeriodStart:     st.PeriodStart,
		PeriodEnd:       st.PeriodEnd,
		Defence: adminapi.Defence{
			Enabled:         st.DefenceOn,
			SharePct:        st.DefenceSharePct,
			ReservedMicros:  int64(st.DefenceReserve),
			RemainingMicros: int64(st.DefenceRemaining),
		},
	}
}

// writeError maps errors to problems. An error that is not a known refusal is internal and its
// text is not sent: it may carry file paths.
func (h handlers) writeError(w http.ResponseWriter, err error) {
	var p *adminapi.Problem
	switch {
	case errors.As(err, &p):
	case errors.Is(err, keyfile.ErrBadKey), errors.Is(err, keyfile.ErrBadProvider),
		errors.Is(err, budget.ErrInvalidAmount), errors.Is(err, budget.ErrInvalidPeriod), errors.Is(err, budget.ErrInvalidShare):
		p = adminapi.NewProblem(http.StatusBadRequest, adminapi.SlugValidation, firstLine(err))
	default:
		h.o.Log.Error("gateway admin request failed", "error", err)
		p = adminapi.NewProblem(http.StatusInternalServerError, adminapi.SlugInternal, "The edge could not do this. Its log has the reason.")
	}
	adminapi.WriteProblem(w, p)
}

// firstLine keeps a joined error readable.
func firstLine(err error) string {
	msg := err.Error()
	for i, c := range msg {
		if c == '\n' {
			return msg[:i]
		}
	}
	return msg
}
