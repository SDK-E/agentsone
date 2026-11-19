// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/agentsapi"
)

const defaultTokenTTL = 24 * time.Hour

// ListAgentTemplates returns one page of installed agent templates, sorted
// by id. The cursor is the id of the last template of the previous page.
func (h *Handler) ListAgentTemplates(ctx context.Context, req agentsapi.ListAgentTemplatesRequestObject) (agentsapi.ListAgentTemplatesResponseObject, error) {
	all, err := h.s.Templates(ctx)
	if err != nil {
		return nil, err
	}
	limit, after := limitOf(req.Params.Limit), valueOr(req.Params.Cursor, "")
	out := api.AgentTemplateList{Items: []api.AgentTemplate{}}
	lastID := ""
	for _, tpl := range all {
		if after != "" && tpl.ID <= after {
			continue
		}
		if len(out.Items) == limit {
			last := lastID
			out.NextCursor = &last
			break
		}
		var doc api.AgentTemplate
		if err := json.Unmarshal(tpl.Raw, &doc); err != nil {
			return nil, fmt.Errorf("decode template %s: %w", tpl.ID, err)
		}
		out.Items = append(out.Items, doc)
		lastID = tpl.ID
	}
	return agentsapi.ListAgentTemplates200JSONResponse(out), nil
}

// IssueAgentToken issues a bearer token for the agent. The secret is in
// this response only.
func (h *Handler) IssueAgentToken(ctx context.Context, req agentsapi.IssueAgentTokenRequestObject) (agentsapi.IssueAgentTokenResponseObject, error) {
	ttl := defaultTokenTTL
	if req.Body.TtlSeconds != nil {
		ttl = time.Duration(*req.Body.TtlSeconds) * time.Second
	}
	tok, err := h.s.IssueToken(ctx, req.AgentName, app.TokenRequest{Label: req.Body.Label, Scopes: req.Body.Scopes, TTL: ttl})
	if err != nil {
		return nil, err
	}
	return agentsapi.IssueAgentToken201JSONResponse(api.IssuedToken{
		Id: tok.ID, Token: tok.Value, Subject: tok.Subject, Label: tok.Label, Scopes: tok.Scopes,
		ExpiresAt: api.Timestamp(tok.Expires), CreatedAt: api.Timestamp(tok.Created),
	}), nil
}
