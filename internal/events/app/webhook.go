// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"

	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// MaxWebhookBody is the largest webhook body taken in, in bytes.
const MaxWebhookBody = 1 << 20

const (
	webhookEventType   = "agentsone.webhooks.received"
	webhookEmitterPath = "/emitters/webhooks"
	maxContentType     = 128
)

// Webhook is one request that arrived at a webhook endpoint.
type Webhook struct {
	EmitterID   string
	Method      string
	ContentType string
	Body        []byte
	// Signature is the X-Agentsone-Signature header.
	Signature string
	// Remote is the sender's address, kept in the audit log.
	Remote string
}

// ReceiveWebhook checks a webhook and publishes agentsone.webhooks.received
// for it. The route is public, so every refusal is audited with the sender's
// address, and the sender is rate limited before its signature is checked.
func (s *Service) ReceiveWebhook(ctx context.Context, w Webhook) (core.Event, error) {
	e, err := s.d.Emitters.Get(ctx, w.EmitterID)
	if err != nil || e.Kind != domain.KindWebhook {
		s.reject(ctx, w, "unknown hook")
		return core.Event{}, fmt.Errorf("webhook %s: %w", w.EmitterID, core.ErrNotFound)
	}
	if !s.limiter.Allow("webhook:"+e.ID+":"+w.Remote, webhookRate) {
		s.reject(ctx, w, "rate limited")
		return core.Event{}, core.NewProblem(429, "rate-limited", "too many webhook requests; slow down")
	}
	if len(w.Body) > MaxWebhookBody {
		s.reject(ctx, w, "body too large")
		return core.Event{}, fmt.Errorf("webhook body over %d bytes: %w", MaxWebhookBody, domain.ErrTooLarge)
	}
	if !domain.VerifySignature(e.Secret, w.Body, w.Signature) {
		s.reject(ctx, w, "bad signature")
		return core.Event{}, fmt.Errorf("%w: webhook signature is missing or wrong", core.ErrUnauthorized)
	}
	if !e.Enabled {
		s.reject(ctx, w, "disabled")
		return core.Event{}, domain.ErrEmitterDisabled
	}
	data, err := webhookData(e, w)
	if err != nil {
		return core.Event{}, err
	}
	stored, err := s.publish(core.WithCaller(ctx, systemCaller), systemCaller, domain.Draft{
		Type: webhookEventType, Source: webhookEmitterPath, Subject: e.Name, Data: data,
	}, "", "")
	if err != nil {
		return core.Event{}, err
	}
	if err := s.d.Emitters.Touch(ctx, e.Name, *stored.Time); err != nil {
		s.d.Log.WarnContext(ctx, "emitter touch failed", "emitter", e.Name, "error", err)
	}
	s.audit(ctx, "system:daemon", "webhook.receive", "emitter:"+e.ID,
		map[string]any{"remote": w.Remote, "event": stored.ID, "bytes": len(w.Body)})
	return stored, nil
}

func (s *Service) reject(ctx context.Context, w Webhook, reason string) {
	s.audit(ctx, "system:daemon", "webhook.reject", "emitter:"+w.EmitterID,
		map[string]any{"remote": w.Remote, "reason": reason, "bytes": len(w.Body)})
}

func webhookData(e domain.Emitter, w Webhook) (json.RawMessage, error) {
	sum := sha256.Sum256(w.Body)
	p := eventdata.WebhooksReceived{
		Hook: e.Name, Method: eventdata.WebhooksReceivedMethod(w.Method), ContentType: truncate(w.ContentType, maxContentType),
		BodySha256: hex.EncodeToString(sum[:]), BodyBytes: len(w.Body),
	}
	if isJSON(w.ContentType) && len(w.Body) <= domain.MaxDataBytes/2 {
		if obj, ok := decodeObject(w.Body); ok {
			p.Body = obj
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encode webhook data: %w", err)
	}
	return raw, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func isJSON(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && (media == "application/json" || (len(media) > 5 && media[len(media)-5:] == "+json"))
}

// decodeObject reads body as one JSON object. Numbers stay exact, so a large
// integer in a hook body is not rounded on its way into the event.
func decodeObject(body []byte) (eventdata.WebhooksReceivedBody, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
		return nil, false
	}
	return obj, true
}
