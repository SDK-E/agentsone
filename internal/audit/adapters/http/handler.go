// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/contract/api/auditapi"
	"github.com/SDK-E/agentsone/internal/core"
)

const defaultLimit = 50

// Lister is the use case the handler needs.
type Lister interface {
	List(ctx context.Context, afterSeq int64, limit int) ([]domain.Record, error)
}

// Handler serves the audit group.
type Handler struct {
	auditapi.Unimplemented
	log Lister
}

var _ auditapi.StrictServerInterface = (*Handler)(nil)

// New returns a handler over log.
func New(log Lister) *Handler { return &Handler{log: log} }

// ListAuditRecords returns one page of records, oldest first.
func (h *Handler) ListAuditRecords(ctx context.Context, req auditapi.ListAuditRecordsRequestObject) (auditapi.ListAuditRecordsResponseObject, error) {
	p := req.Params
	if p.Actor != nil || p.Action != nil || p.Cursor != nil {
		return nil, fmt.Errorf("audit filters and cursors: %w", core.ErrNotImplemented)
	}
	records, err := h.log.List(ctx, valueOr(p.AfterSeq, 0), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return auditapi.ListAuditRecords200JSONResponse(toAuditList(records)), nil
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
