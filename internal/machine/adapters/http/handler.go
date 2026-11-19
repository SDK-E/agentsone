// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/machineapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

const defaultLimit = 50

// HomeOf returns a custom agent's home folder.
type HomeOf func(agent string) string

// Handler serves the machine group. Terminals answer 501 until their slice
// lands.
type Handler struct {
	machineapi.Unimplemented
	m    *app.Machine
	home HomeOf
}

var _ machineapi.StrictServerInterface = (*Handler)(nil)

// New returns a handler over m.
func New(m *app.Machine, home HomeOf) *Handler { return &Handler{m: m, home: home} }

// actor maps the request's caller to a machine actor. Requests without a
// caller are refused.
func (h *Handler) actor(ctx context.Context) (domain.Actor, error) {
	c, ok := core.CallerFrom(ctx)
	if !ok {
		return domain.Actor{}, fmt.Errorf("machine control needs a caller: %w", core.ErrUnauthorized)
	}
	a := domain.Actor{Kind: domain.ActorKind(c.Kind), Name: c.Name, UID: c.UID, IsRoot: c.IsRoot}
	switch c.Kind {
	case core.CallerHost:
		a.Kind = domain.ActorSystem
	case core.CallerAgent:
		if !c.IsRoot {
			a.Home = h.home(c.Name)
		}
	}
	return a, nil
}

// problem maps machine errors the core sentinels do not cover.
func problem(err error) error {
	if errors.Is(err, domain.ErrTooLarge) {
		return core.NewProblem(http.StatusRequestEntityTooLarge, "payload-too-large", err.Error())
	}
	return err
}

// GetMachineMetrics returns a snapshot of the machine.
func (h *Handler) GetMachineMetrics(ctx context.Context, _ machineapi.GetMachineMetricsRequestObject) (machineapi.GetMachineMetricsResponseObject, error) {
	if _, err := h.actor(ctx); err != nil {
		return nil, err
	}
	m, err := h.m.Metrics(ctx)
	if err != nil {
		return nil, err
	}
	return machineapi.GetMachineMetrics200JSONResponse(toMetrics(m)), nil
}

// ListProcesses lists processes, highest CPU first.
func (h *Handler) ListProcesses(ctx context.Context, req machineapi.ListProcessesRequestObject) (machineapi.ListProcessesResponseObject, error) {
	if _, err := h.actor(ctx); err != nil {
		return nil, err
	}
	p := req.Params
	f := app.ProcessFilter{User: valueOr(p.User, ""), Agent: valueOr(p.Agent, "")}
	items, next, err := h.m.ListProcesses(ctx, f, valueOr(p.Cursor, ""), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return machineapi.ListProcesses200JSONResponse{Items: mapAll(items, toProcess), NextCursor: cursor(next)}, nil
}

// SignalProcess sends a signal to a process.
func (h *Handler) SignalProcess(ctx context.Context, req machineapi.SignalProcessRequestObject) (machineapi.SignalProcessResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.m.SignalProcess(ctx, a, req.Pid, string(req.Body.Signal)); err != nil {
		return nil, err
	}
	return machineapi.SignalProcess204Response{}, nil
}

// ListServices lists the daemon and the machine recipe.
func (h *Handler) ListServices(ctx context.Context, req machineapi.ListServicesRequestObject) (machineapi.ListServicesResponseObject, error) {
	if _, err := h.actor(ctx); err != nil {
		return nil, err
	}
	p := req.Params
	state := domain.ServiceState(valueOr(p.State, ""))
	items, next, err := h.m.ListServices(ctx, state, valueOr(p.Cursor, ""), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return machineapi.ListServices200JSONResponse{Items: mapAll(items, toService), NextCursor: cursor(next)}, nil
}

// StartService starts a service (the recipe unit reapplies the recipe).
func (h *Handler) StartService(ctx context.Context, req machineapi.StartServiceRequestObject) (machineapi.StartServiceResponseObject, error) {
	s, err := h.serviceVerb(ctx, req.Unit, h.m.StartService)
	if err != nil {
		return nil, err
	}
	return machineapi.StartService200JSONResponse(s), nil
}

// StopService stops a service.
func (h *Handler) StopService(ctx context.Context, req machineapi.StopServiceRequestObject) (machineapi.StopServiceResponseObject, error) {
	s, err := h.serviceVerb(ctx, req.Unit, h.m.StopService)
	if err != nil {
		return nil, err
	}
	return machineapi.StopService200JSONResponse(s), nil
}

// RestartService restarts a service (the recipe unit reapplies the recipe).
func (h *Handler) RestartService(ctx context.Context, req machineapi.RestartServiceRequestObject) (machineapi.RestartServiceResponseObject, error) {
	s, err := h.serviceVerb(ctx, req.Unit, h.m.RestartService)
	if err != nil {
		return nil, err
	}
	return machineapi.RestartService200JSONResponse(s), nil
}

type serviceFunc func(context.Context, domain.Actor, string) (domain.Service, error)

func (h *Handler) serviceVerb(ctx context.Context, unit string, verb serviceFunc) (api.Service, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return api.Service{}, err
	}
	s, err := verb(ctx, a, unit)
	if err != nil {
		return api.Service{}, err
	}
	return toService(s), nil
}

// ListPackages lists installed packages.
func (h *Handler) ListPackages(ctx context.Context, req machineapi.ListPackagesRequestObject) (machineapi.ListPackagesResponseObject, error) {
	if _, err := h.actor(ctx); err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := h.m.ListPackages(ctx, valueOr(p.Q, ""), valueOr(p.Cursor, ""), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return machineapi.ListPackages200JSONResponse{Items: mapAll(items, toPackage), NextCursor: cursor(next)}, nil
}

// InstallPackage queues an apt install.
func (h *Handler) InstallPackage(ctx context.Context, req machineapi.InstallPackageRequestObject) (machineapi.InstallPackageResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	j, err := h.m.InstallPackage(ctx, a, req.Body.Name, valueOr(req.Body.Version, ""))
	if err != nil {
		return nil, err
	}
	return machineapi.InstallPackage202JSONResponse(toJob(j)), nil
}

// RemovePackage queues an apt remove.
func (h *Handler) RemovePackage(ctx context.Context, req machineapi.RemovePackageRequestObject) (machineapi.RemovePackageResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	j, err := h.m.RemovePackage(ctx, a, req.PackageName)
	if err != nil {
		return nil, err
	}
	return machineapi.RemovePackage202JSONResponse(toJob(j)), nil
}

// ListMachineUsers lists Linux accounts.
func (h *Handler) ListMachineUsers(ctx context.Context, req machineapi.ListMachineUsersRequestObject) (machineapi.ListMachineUsersResponseObject, error) {
	if _, err := h.actor(ctx); err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := h.m.ListUsers(ctx, valueOr(p.Cursor, ""), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return machineapi.ListMachineUsers200JSONResponse{Items: mapAll(items, toUser), NextCursor: cursor(next)}, nil
}

// ListFiles lists a folder.
func (h *Handler) ListFiles(ctx context.Context, req machineapi.ListFilesRequestObject) (machineapi.ListFilesResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := h.m.ListFiles(ctx, a, p.Path, valueOr(p.Cursor, ""), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return machineapi.ListFiles200JSONResponse{Path: p.Path, Items: mapAll(items, toFileEntry), NextCursor: cursor(next)}, nil
}

// ReadFile reads a file as UTF-8 text, or base64 when it is not text.
func (h *Handler) ReadFile(ctx context.Context, req machineapi.ReadFileRequestObject) (machineapi.ReadFileResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	c, err := h.m.ReadFile(ctx, a, req.Params.Path)
	if err != nil {
		return nil, problem(err)
	}
	out := machineapi.ReadFile200JSONResponse{Path: c.Path, SizeBytes: c.Size, Truncated: c.Truncated}
	if utf8.Valid(c.Data) {
		out.Encoding, out.Content = api.FileEncodingUtf8, string(c.Data)
	} else {
		out.Encoding, out.Content = api.FileEncodingBase64, base64.StdEncoding.EncodeToString(c.Data)
	}
	return out, nil
}

// WriteFile writes a whole file as the caller's Linux user.
func (h *Handler) WriteFile(ctx context.Context, req machineapi.WriteFileRequestObject) (machineapi.WriteFileResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	w, err := toWriteRequest(*req.Body)
	if err != nil {
		return nil, err
	}
	e, err := h.m.WriteFile(ctx, a, w)
	if err != nil {
		return nil, problem(err)
	}
	return machineapi.WriteFile200JSONResponse(toFileEntry(e)), nil
}

func toWriteRequest(b api.FileWrite) (app.WriteRequest, error) {
	data := []byte(b.Content)
	if b.Encoding == api.FileEncodingBase64 {
		decoded, err := base64.StdEncoding.DecodeString(b.Content)
		if err != nil {
			return app.WriteRequest{}, fmt.Errorf("content is not base64: %w", core.ErrInvalid)
		}
		data = decoded
	}
	w := app.WriteRequest{Path: b.Path, Data: data, CreateParents: valueOr(b.CreateParents, false)}
	if b.Mode != nil {
		mode, err := domain.ParseMode(*b.Mode)
		if err != nil {
			return app.WriteRequest{}, err
		}
		w.Mode = &mode
	}
	return w, nil
}

// DeleteFile deletes a file or folder.
func (h *Handler) DeleteFile(ctx context.Context, req machineapi.DeleteFileRequestObject) (machineapi.DeleteFileResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.m.DeleteFile(ctx, a, req.Params.Path, valueOr(req.Params.Recursive, false)); err != nil {
		return nil, err
	}
	return machineapi.DeleteFile204Response{}, nil
}

// RebootMachine schedules a reboot.
func (h *Handler) RebootMachine(ctx context.Context, req machineapi.RebootMachineRequestObject) (machineapi.RebootMachineResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := h.m.Reboot(ctx, a, req.Body.Reason, req.Body.DelaySeconds)
	if err != nil {
		return nil, err
	}
	return machineapi.RebootMachine202JSONResponse{
		Action: api.PowerActionActionReboot, Reason: r.Reason, RequestedBy: r.RequestedBy,
		At: api.Timestamp(r.At),
	}, nil
}
