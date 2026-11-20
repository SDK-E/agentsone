// SPDX-License-Identifier: MIT

// Package httpadapter serves the ide tag (the editor's state, start and
// stop) and the proxy that carries a human's browser to their own editor.
package httpadapter

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/ideapi"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/ide/domain"
)

// Servers are the humans' editor servers (app.Servers).
type Servers interface {
	Start(ctx context.Context, c core.Caller, human string) (domain.Server, error)
	Stop(ctx context.Context, c core.Caller, human string) error
	Get(ctx context.Context, human string) (domain.Server, error)
	List(ctx context.Context) ([]domain.Server, error)
	Socket(c core.Caller, human string) (string, error)
}

// Build is the baked-in open-source editor build.
type Build struct {
	Name, Version string
}

// Handler implements the ide tag. Downloading the official VS Code build
// is not built yet and answers 501.
type Handler struct {
	ideapi.Unimplemented
	servers Servers
	build   Build
}

var _ ideapi.StrictServerInterface = (*Handler)(nil)

// New returns the ide tag handler over servers.
func New(servers Servers, build Build) *Handler { return &Handler{servers: servers, build: build} }

func caller(ctx context.Context) core.Caller {
	c, _ := core.CallerFrom(ctx)
	return c
}

// notRequested is the official build's state until its download exists.
func notRequested() api.OfficialIdeDownload {
	return api.OfficialIdeDownload{State: api.OfficialIdeStateNotRequested}
}

// GetIdeStatus returns the open build, the official download state and
// every human's server.
func (h *Handler) GetIdeStatus(ctx context.Context, _ ideapi.GetIdeStatusRequestObject) (ideapi.GetIdeStatusResponseObject, error) {
	all, err := h.servers.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("editor servers: %w", err)
	}
	out := make([]api.IdeServer, 0, len(all))
	for _, s := range all {
		out = append(out, toAPI(s))
	}
	return ideapi.GetIdeStatus200JSONResponse{
		Build: h.build.Name, Version: h.build.Version, ActiveBuild: api.IdeStatusActiveBuildOpen,
		Official: notRequested(), Servers: out,
	}, nil
}

// GetOfficialIdeDownload says the official build was not requested.
func (h *Handler) GetOfficialIdeDownload(context.Context, ideapi.GetOfficialIdeDownloadRequestObject) (ideapi.GetOfficialIdeDownloadResponseObject, error) {
	return ideapi.GetOfficialIdeDownload200JSONResponse(notRequested()), nil
}

// GetIdeServer returns one human's server.
func (h *Handler) GetIdeServer(ctx context.Context, req ideapi.GetIdeServerRequestObject) (ideapi.GetIdeServerResponseObject, error) {
	s, err := h.servers.Get(ctx, req.HumanName)
	if err != nil {
		return nil, err //nolint:wrapcheck // the app error already names the human
	}
	return ideapi.GetIdeServer200JSONResponse(toAPI(s)), nil
}

// StartIdeServer starts the caller's own server.
func (h *Handler) StartIdeServer(ctx context.Context, req ideapi.StartIdeServerRequestObject) (ideapi.StartIdeServerResponseObject, error) {
	s, err := h.servers.Start(ctx, caller(ctx), req.HumanName)
	if err != nil {
		return nil, err //nolint:wrapcheck // the app error already names the human
	}
	return ideapi.StartIdeServer202JSONResponse(toAPI(s)), nil
}

// StopIdeServer stops the caller's own server.
func (h *Handler) StopIdeServer(ctx context.Context, req ideapi.StopIdeServerRequestObject) (ideapi.StopIdeServerResponseObject, error) {
	if err := h.servers.Stop(ctx, caller(ctx), req.HumanName); err != nil {
		return nil, err //nolint:wrapcheck // the app error already names the human
	}
	return ideapi.StopIdeServer204Response{}, nil
}

func toAPI(s domain.Server) api.IdeServer {
	out := api.IdeServer{Human: s.Human, State: api.IdeServerState(s.State)}
	if u, ok := s.URL(); ok {
		out.Url = &u
	}
	if !s.StartedAt.IsZero() {
		t := common.Timestamp(s.StartedAt.UTC())
		out.StartedAt = &t
	}
	if s.Error != "" {
		e := s.Error
		out.Error = &e
	}
	return out
}
