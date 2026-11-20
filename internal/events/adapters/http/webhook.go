// SPDX-License-Identifier: MIT

package httpadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/eventsapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// SignatureHeader carries the HMAC-SHA256 of the body, as "sha256=<hex>".
const SignatureHeader = "X-Agentsone-Signature"

const receiveWebhookOperation = "receiveWebhook"

// EventsGroup mounts the events tag. The generated strict handler decodes the
// body before our code runs, which would lose the exact bytes the sender
// signed. So the group serves every operation through the generated handler
// on a private mux, except the webhook intake, which reads the raw body
// itself.
func EventsGroup(h *Events) core.APIGroup {
	generated := eventsapi.Group(h)
	return core.APIGroup{
		Tag: eventsapi.Tag,
		Register: func(mux *http.ServeMux, o core.APIOptions) {
			private := http.NewServeMux()
			generated.Register(private, o)
			for _, route := range api.Routes {
				if route.Tag != eventsapi.Tag {
					continue
				}
				pattern := route.Method + " " + route.Path
				if route.OperationID == receiveWebhookOperation {
					mux.Handle(pattern, webhookHandler(h.svc, o))
					continue
				}
				mux.Handle(pattern, private)
			}
		},
	}
}

// webhookError adapts app errors to what the public route may answer. The
// contract has no 403 for the intake, and a switched-off hook is as good as
// gone to a sender, so a disabled hook answers 404.
func webhookError(err error) error {
	if errors.Is(err, domain.ErrEmitterDisabled) {
		return fmt.Errorf("webhook is off: %w", core.ErrNotFound)
	}
	return mapError(err)
}

// webhookHandler takes in one webhook. It reads at most one byte more than
// the limit, so an oversize body is refused without being buffered.
func webhookHandler(svc Service, o core.APIOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, app.MaxWebhookBody+1))
		if err != nil {
			o.RequestError(w, r, err)
			return
		}
		e, err := svc.ReceiveWebhook(r.Context(), app.Webhook{
			EmitterID:   r.PathValue("emitter_id"),
			Method:      r.Method,
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
			Signature:   r.Header.Get(SignatureHeader),
			Remote:      r.RemoteAddr,
		})
		if err != nil {
			o.ResponseError(w, r, webhookError(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.WebhookAccepted{EventId: e.ID}) //nolint:errcheck // the event is stored and the status sent; a failed body write means the client left
	})
}
