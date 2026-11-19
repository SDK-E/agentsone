// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/SDK-E/agentsone/internal/contract/api/memoryapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

// Memory is the set of use cases the handler needs.
type Memory interface {
	Write(ctx context.Context, in app.WriteInput) (domain.Record, error)
	Get(ctx context.Context, id string) (domain.Record, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, in app.ListInput) (app.RecordPage, error)
	Search(ctx context.Context, in app.SearchInput) (app.HitPage, error)
}

// Handler serves the memory group.
type Handler struct {
	memoryapi.Unimplemented
	memory Memory
}

var _ memoryapi.StrictServerInterface = (*Handler)(nil)

// New returns a handler over the memory use cases.
func New(m Memory) *Handler { return &Handler{memory: m} }

// ListMemory lists the records the caller may read, newest first.
func (h *Handler) ListMemory(ctx context.Context, req memoryapi.ListMemoryRequestObject) (memoryapi.ListMemoryResponseObject, error) {
	p := req.Params
	page, err := h.memory.List(ctx, app.ListInput{
		Scope: string(valueOr(p.Scope, "")), Owner: valueOr(p.Owner, ""), Tag: valueOr(p.Tag, ""),
		Cursor: valueOr(p.Cursor, ""), Limit: valueOr(p.Limit, 0),
	})
	if err != nil {
		return nil, err
	}
	return memoryapi.ListMemory200JSONResponse(toMemoryList(page)), nil
}

// WriteMemory stores a record owned by the caller.
func (h *Handler) WriteMemory(ctx context.Context, req memoryapi.WriteMemoryRequestObject) (memoryapi.WriteMemoryResponseObject, error) {
	if req.Body == nil {
		return nil, fmt.Errorf("memory write needs a body: %w", core.ErrInvalid)
	}
	rec, err := h.memory.Write(ctx, toWriteInput(*req.Body))
	if errors.Is(err, domain.ErrTooLarge) {
		return nil, core.NewProblem(http.StatusRequestEntityTooLarge, "payload-too-large", err.Error())
	}
	if err != nil {
		return nil, err
	}
	return memoryapi.WriteMemory201JSONResponse(toMemoryRecord(rec)), nil
}

// SearchMemory finds records by their words, best match first.
func (h *Handler) SearchMemory(ctx context.Context, req memoryapi.SearchMemoryRequestObject) (memoryapi.SearchMemoryResponseObject, error) {
	p := req.Params
	page, err := h.memory.Search(ctx, app.SearchInput{
		Text: p.Q, Scope: string(valueOr(p.Scope, "")), Cursor: valueOr(p.Cursor, ""), Limit: valueOr(p.Limit, 0),
	})
	if err != nil {
		return nil, err
	}
	return memoryapi.SearchMemory200JSONResponse(toSearchResult(page)), nil
}

// GetMemory returns one record the caller may read.
func (h *Handler) GetMemory(ctx context.Context, req memoryapi.GetMemoryRequestObject) (memoryapi.GetMemoryResponseObject, error) {
	rec, err := h.memory.Get(ctx, req.MemoryId)
	if err != nil {
		return nil, err
	}
	return memoryapi.GetMemory200JSONResponse(toMemoryRecord(rec)), nil
}

// DeleteMemory removes a record its owner or a root agent may delete.
func (h *Handler) DeleteMemory(ctx context.Context, req memoryapi.DeleteMemoryRequestObject) (memoryapi.DeleteMemoryResponseObject, error) {
	if err := h.memory.Delete(ctx, req.MemoryId); err != nil {
		return nil, err
	}
	return memoryapi.DeleteMemory204Response{}, nil
}
