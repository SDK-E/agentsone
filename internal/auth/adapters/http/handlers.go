// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/app"
	"github.com/SDK-E/agentsone/internal/auth/domain"
	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/humansapi"
	"github.com/SDK-E/agentsone/internal/contract/api/sessionapi"
	"github.com/SDK-E/agentsone/internal/contract/api/setupapi"
	"github.com/SDK-E/agentsone/internal/core"
)

// AgentReader reads one agent for getMe; core.AgentDirectory satisfies it.
type AgentReader interface {
	GetAgent(ctx context.Context, name string) (core.Agent, error)
}

// Groups returns the three groups served by svc.
func Groups(svc *app.Service, agents AgentReader) []core.APIGroup {
	h := &handlers{svc: svc, agents: agents}
	return []core.APIGroup{
		sessionapi.Group(sessionHandlers{h}, captureRequest[sessionapi.StrictHandlerFunc]),
		humansapi.Group(humansHandlers{h}, captureRequest[humansapi.StrictHandlerFunc]),
		setupapi.Group(setupHandlers{h}),
	}
}

type handlers struct {
	svc    *app.Service
	agents AgentReader
}

type sessionHandlers struct{ *handlers }

type humansHandlers struct{ *handlers }

type setupHandlers struct{ *handlers }

var (
	_ sessionapi.StrictServerInterface = sessionHandlers{}
	_ humansapi.StrictServerInterface  = humansHandlers{}
	_ setupapi.StrictServerInterface   = setupHandlers{}
)

func caller(ctx context.Context) core.Caller {
	c, _ := core.CallerFrom(ctx)
	return c
}

func password(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func problem(p *core.Problem) contract.Problem {
	return contract.Problem{Type: p.Type, Title: p.Title, Status: p.Status, Detail: &p.Detail}
}

func toHuman(h domain.Human) contract.Human {
	out := contract.Human{Name: h.Name, DisplayName: h.DisplayName, Uid: h.UID, CreatedAt: contract.Timestamp(h.CreatedAt)}
	if !h.LastLoginAt.IsZero() {
		t := contract.Timestamp(h.LastLoginAt)
		out.LastLoginAt = &t
	}
	return out
}

// rateLimited turns the throttle's error into the contract's 429 problem
// and the Retry-After seconds.
func rateLimited(err error, detail string) (contract.Problem, int, bool) {
	var tooMany *app.TooManyAttempts
	if !errors.As(err, &tooMany) {
		return contract.Problem{}, 0, false
	}
	retry := int((tooMany.RetryAfter + time.Second - 1) / time.Second)
	return problem(core.NewProblem(http.StatusTooManyRequests, "rate-limited", detail)), retry, true
}

// CreateSession signs a human in and sets the session cookie.
func (h sessionHandlers) CreateSession(ctx context.Context, req sessionapi.CreateSessionRequestObject) (sessionapi.CreateSessionResponseObject, error) {
	info := infoFrom(ctx)
	sess, secret, human, err := h.svc.SignIn(ctx, req.Body.Name, password(req.Body.Password), info.remote)
	if p, retry, ok := rateLimited(err, "Too many sign-in attempts. Wait a minute and try again."); ok {
		return sessionapi.CreateSession429ApplicationProblemPlusJSONResponse{
			Body: p, Headers: sessionapi.TooManyRequestsResponseHeaders{RetryAfter: &retry},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	cookie := sessionCookie(secret, int(domain.SessionTTL/time.Second), info.secure)
	return sessionapi.CreateSession201JSONResponse{
		Body:    contract.Session{Human: toHuman(human), ExpiresAt: contract.Timestamp(sess.ExpiresAt)},
		Headers: sessionapi.CreateSession201ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// DeleteSession signs out and clears the cookie.
func (h sessionHandlers) DeleteSession(ctx context.Context, _ sessionapi.DeleteSessionRequestObject) (sessionapi.DeleteSessionResponseObject, error) {
	info := infoFrom(ctx)
	if info.cookie == "" {
		return nil, fmt.Errorf("no session cookie: %w", core.ErrUnauthorized)
	}
	if err := h.svc.SignOut(ctx, info.cookie); err != nil {
		return nil, err
	}
	return signedOut{cookie: sessionCookie("", -1, info.secure)}, nil
}

// signedOut is the 204 with a cookie that clears the session in the browser.
type signedOut struct{ cookie string }

func (s signedOut) VisitDeleteSessionResponse(w http.ResponseWriter) error {
	w.Header().Set("Set-Cookie", s.cookie)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// GetMe describes the caller.
func (h sessionHandlers) GetMe(ctx context.Context, _ sessionapi.GetMeRequestObject) (sessionapi.GetMeResponseObject, error) {
	c := caller(ctx)
	scopes := append([]string{}, c.Scopes...)
	var p contract.Principal
	var err error
	switch c.Kind {
	case core.CallerHuman:
		human, herr := h.svc.GetHuman(ctx, c.Name)
		if herr != nil {
			return nil, fmt.Errorf("me: %w", core.ErrUnauthorized)
		}
		err = p.FromHumanPrincipal(contract.HumanPrincipal{Kind: "human", Human: toHuman(human), Scopes: scopes})
	case core.CallerAgent:
		a, aerr := h.agents.GetAgent(ctx, c.Name)
		if aerr != nil {
			return nil, fmt.Errorf("me: %w", aerr)
		}
		via := contract.AgentPrincipalVia("peer-credentials")
		if c.UID < 0 {
			via = "token"
		}
		err = p.FromAgentPrincipal(contract.AgentPrincipal{Kind: "agent", Agent: toAgent(a), Via: via, Scopes: scopes})
	case core.CallerPlugin:
		err = p.FromPanelPrincipal(contract.PanelPrincipal{Kind: "panel", PluginId: c.Name, Scopes: scopes})
	default:
		return nil, fmt.Errorf("me: %s callers have no principal: %w", c.Kind, core.ErrUnauthorized)
	}
	if err != nil {
		return nil, fmt.Errorf("encode principal: %w", err)
	}
	return sessionapi.GetMe200JSONResponse(p), nil
}

// toAgent fills the parts of the contract Agent the directory knows.
func toAgent(a core.Agent) contract.Agent {
	return contract.Agent{
		Name: a.Name, Kind: contract.AgentKind(a.Kind), IsRoot: a.IsRoot, Runner: a.Runner,
		Uid: a.UID, Home: a.Home, Unit: "agent@" + a.Name + ".service",
	}
}

// RevokeToken ends a token.
func (h sessionHandlers) RevokeToken(ctx context.Context, req sessionapi.RevokeTokenRequestObject) (sessionapi.RevokeTokenResponseObject, error) {
	if err := h.svc.RevokeToken(ctx, caller(ctx), req.TokenId); err != nil {
		return nil, err
	}
	return sessionapi.RevokeToken204Response{}, nil
}

// ListHumans lists human accounts. One page holds them all: a machine has
// a handful of humans.
func (h humansHandlers) ListHumans(ctx context.Context, _ humansapi.ListHumansRequestObject) (humansapi.ListHumansResponseObject, error) {
	hs, err := h.svc.ListHumans(ctx)
	if err != nil {
		return nil, err
	}
	out := contract.HumanList{Items: make([]contract.Human, 0, len(hs))}
	for _, x := range hs {
		out.Items = append(out.Items, toHuman(x))
	}
	return humansapi.ListHumans200JSONResponse(out), nil
}

// GetHuman reads one human.
func (h humansHandlers) GetHuman(ctx context.Context, req humansapi.GetHumanRequestObject) (humansapi.GetHumanResponseObject, error) {
	x, err := h.svc.GetHuman(ctx, req.HumanName)
	if err != nil {
		return nil, err
	}
	return humansapi.GetHuman200JSONResponse(toHuman(x)), nil
}

// CreateHuman adds a human; only humans may.
func (h humansHandlers) CreateHuman(ctx context.Context, req humansapi.CreateHumanRequestObject) (humansapi.CreateHumanResponseObject, error) {
	x, err := h.svc.CreateHuman(ctx, caller(ctx), app.NewHuman{Name: req.Body.Name, DisplayName: req.Body.DisplayName, Password: password(req.Body.Password)})
	if err != nil {
		return nil, err
	}
	return humansapi.CreateHuman201JSONResponse(toHuman(x)), nil
}

// UpdateHuman changes a human; only humans may. After an own password
// change the response carries the caller's new session cookie.
func (h humansHandlers) UpdateHuman(ctx context.Context, req humansapi.UpdateHumanRequestObject) (humansapi.UpdateHumanResponseObject, error) {
	u, err := h.svc.UpdateHuman(ctx, caller(ctx), req.HumanName, app.HumanChange{
		DisplayName: req.Body.DisplayName, Password: req.Body.Password, CurrentPassword: req.Body.CurrentPassword,
	})
	if p, retry, ok := rateLimited(err, "Too many wrong passwords. Wait a minute and try again."); ok {
		return humansapi.UpdateHuman429ApplicationProblemPlusJSONResponse{TooManyRequestsApplicationProblemPlusJSONResponse: humansapi.TooManyRequestsApplicationProblemPlusJSONResponse{
			Body: p, Headers: humansapi.TooManyRequestsResponseHeaders{RetryAfter: &retry},
		}}, nil
	}
	if msg, ok := currentPasswordMessage(err); ok {
		p := problem(core.ProblemFrom(err))
		p.Detail = &msg
		p.Errors = &[]contract.ValidationIssue{{Pointer: "/current_password", Message: msg}}
		return humansapi.UpdateHuman400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse: humansapi.BadRequestApplicationProblemPlusJSONResponse(p)}, nil
	}
	if err != nil {
		return nil, err
	}
	out := humansapi.UpdateHuman200JSONResponse{Body: toHuman(u.Human)}
	if u.Session != "" {
		cookie := sessionCookie(u.Session, int(domain.SessionTTL/time.Second), infoFrom(ctx).secure)
		out.Headers.SetCookie = &cookie
	}
	return out, nil
}

// currentPasswordMessage is the plain message for a refused own password
// change, shown next to the current password field.
func currentPasswordMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, app.ErrCurrentPasswordMissing):
		return "Enter your current password.", true
	case errors.Is(err, app.ErrCurrentPasswordWrong):
		return "That is not your current password.", true
	}
	return "", false
}

// DeleteHuman removes a human; only humans may.
func (h humansHandlers) DeleteHuman(ctx context.Context, req humansapi.DeleteHumanRequestObject) (humansapi.DeleteHumanResponseObject, error) {
	if err := h.svc.DeleteHuman(ctx, caller(ctx), req.HumanName); err != nil {
		return nil, err
	}
	return humansapi.DeleteHuman204Response{}, nil
}

func toFetch(f domain.EngineFetch) contract.EngineFetchStatus {
	out := contract.EngineFetchStatus{
		State: contract.EngineFetchState(f.State), Progress: float32(f.Progress), BytesDone: f.BytesDone, UpdatedAt: contract.Timestamp(f.UpdatedAt),
	}
	if f.BytesTotal > 0 {
		out.BytesTotal = &f.BytesTotal
	}
	if f.Version != "" {
		out.Version = &f.Version
	}
	if f.Error != "" {
		out.Error = &f.Error
	}
	return out
}

// GetSetupStatus reports first boot; anyone may read it.
func (h setupHandlers) GetSetupStatus(ctx context.Context, _ setupapi.GetSetupStatusRequestObject) (setupapi.GetSetupStatusResponseObject, error) {
	st, err := h.svc.SetupStatus(ctx)
	if err != nil {
		return nil, err
	}
	return setupapi.GetSetupStatus200JSONResponse{
		Phase: contract.SetupPhase(st.Phase), FirstHumanCreated: st.FirstHumanCreated, LicenceAccepted: st.LicenceAccepted,
		Engine: toFetch(st.Engine), SeedInstalled: st.SeedInstalled, ContractVersion: contractVersion(),
	}, nil
}

// CreateFirstHuman creates the first account while no human exists.
func (h setupHandlers) CreateFirstHuman(ctx context.Context, req setupapi.CreateFirstHumanRequestObject) (setupapi.CreateFirstHumanResponseObject, error) {
	x, err := h.svc.CreateFirstHuman(ctx, app.NewHuman{Name: req.Body.Name, DisplayName: req.Body.DisplayName, Password: password(req.Body.Password)})
	if err != nil {
		return nil, err
	}
	return setupapi.CreateFirstHuman201JSONResponse(toHuman(x)), nil
}

// GetEngineLicence returns the licence and its acceptance.
func (h setupHandlers) GetEngineLicence(ctx context.Context, _ setupapi.GetEngineLicenceRequestObject) (setupapi.GetEngineLicenceResponseObject, error) {
	v, err := h.svc.EngineLicence(ctx)
	if err != nil {
		return nil, err
	}
	out := contract.EngineLicence{Engine: v.Engine, Version: v.Version, Text: v.Text}
	if v.Accepted != nil {
		out.Accepted = true
		at := contract.Timestamp(v.Accepted.AcceptedAt)
		out.AcceptedAt = &at
		out.AcceptedBy = &v.Accepted.AcceptedBy
	}
	return setupapi.GetEngineLicence200JSONResponse(out), nil
}

// AcceptEngineLicence records a human's acceptance.
func (h setupHandlers) AcceptEngineLicence(ctx context.Context, req setupapi.AcceptEngineLicenceRequestObject) (setupapi.AcceptEngineLicenceResponseObject, error) {
	f, err := h.svc.AcceptEngineLicence(ctx, caller(ctx), req.Body.Version, bool(req.Body.Accept))
	if err != nil {
		return nil, err
	}
	return setupapi.AcceptEngineLicence202JSONResponse(toFetch(f)), nil
}

// GetEngineFetchStatus returns the engine download status.
func (h setupHandlers) GetEngineFetchStatus(ctx context.Context, _ setupapi.GetEngineFetchStatusRequestObject) (setupapi.GetEngineFetchStatusResponseObject, error) {
	f, err := h.svc.EngineFetch(ctx)
	if err != nil {
		return nil, err
	}
	return setupapi.GetEngineFetchStatus200JSONResponse(toFetch(f)), nil
}
