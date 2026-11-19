// SPDX-License-Identifier: MIT

// Package httpadapter serves the edge audit sink.
//
// On the inner address, for the machine's daemon only:
//
//	POST /v1/audit/records  {"records": [<audit record as written>, ...]} -> {"head": {"seq", "hash"}}
//	GET  /v1/audit/head     the last machine record the edge holds: {"seq", "hash"}
//
// A record that does not continue the chain answers 409 urn:agentsone:problem:conflict with the
// verdict (gap, fork, broken) and the edge's head, so the shipper resumes from there.
//
// On the admin socket, for the control page:
//
//	GET /v1/audit/head
//	GET /v1/audit/inside?after=&limit=   the machine's chain as the edge holds it
//	GET /v1/audit/proof?after=&limit=    the edge's own proof chain
//	GET /v1/audit/alerts?limit=          gaps and forks, newest first
//	GET /v1/audit/verify                 both chains checked end to end
package httpadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/edge/audit/domain"
)

// MaxBodyBytes is the largest batch the machine may send.
const MaxBodyBytes = 4 << 20

// Problem types.
const (
	ProblemValidation = "urn:agentsone:problem:validation"
	ProblemTooLarge   = "urn:agentsone:problem:payload-too-large"
	ProblemConflict   = "urn:agentsone:problem:conflict"
	ProblemInternal   = "urn:agentsone:problem:internal"
)

type headBody struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

func toHead(h domain.Head) headBody { return headBody(h) }

// Inner returns the machine-side handler. It serves nothing else: the machine cannot read the
// edge's copy or the edge's proof.
func Inner(s *app.Sink, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/audit/head", headHandler(s, log))
	mux.HandleFunc("POST /v1/audit/records", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
		if err != nil {
			writeProblem(w, http.StatusRequestEntityTooLarge, ProblemTooLarge, "Batch too large", nil)
			return
		}
		var body struct {
			Records []json.RawMessage `json:"records"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || dec.More() || len(body.Records) == 0 {
			writeProblem(w, http.StatusBadRequest, ProblemValidation, "Body must be {\"records\": [...]} with at least one record", nil)
			return
		}
		if len(body.Records) > app.MaxLimit {
			writeProblem(w, http.StatusRequestEntityTooLarge, ProblemTooLarge, "Too many records in one batch", nil)
			return
		}
		head, err := s.Receive(r.Context(), body.Records)
		var re *app.RejectError
		switch {
		case errors.As(err, &re):
			writeProblem(w, http.StatusConflict, ProblemConflict, "Record does not continue the chain", map[string]any{
				"verdict": re.Verdict.String(), "seq": re.Seq, "detail": re.Detail, "head": toHead(re.Head),
			})
		case err != nil:
			log.Error("audit: receive", "err", err)
			writeProblem(w, http.StatusInternalServerError, ProblemInternal, "Internal error", nil)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"head": toHead(head)})
		}
	})
	return mux
}

func headHandler(s *app.Sink, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, err := s.Head(r.Context())
		if err != nil {
			log.Error("audit: head", "err", err)
			writeProblem(w, http.StatusInternalServerError, ProblemInternal, "Internal error", nil)
			return
		}
		writeJSON(w, http.StatusOK, toHead(h))
	}
}

// RegisterAdmin adds the admin routes to mux.
func RegisterAdmin(mux *http.ServeMux, s *app.Sink, log *slog.Logger) {
	mux.HandleFunc("GET /v1/audit/head", headHandler(s, log))
	mux.HandleFunc("GET /v1/audit/inside", listHandler(s, app.Inside, log))
	mux.HandleFunc("GET /v1/audit/proof", listHandler(s, app.Proof, log))
	mux.HandleFunc("GET /v1/audit/alerts", func(w http.ResponseWriter, r *http.Request) {
		limit, ok := intParam(r, "limit")
		if !ok {
			writeProblem(w, http.StatusBadRequest, ProblemValidation, "limit must be a whole number of 0 or more", nil)
			return
		}
		alerts, err := s.Alerts(r.Context(), int(limit))
		if err != nil {
			log.Error("audit: alerts", "err", err)
			writeProblem(w, http.StatusInternalServerError, ProblemInternal, "Internal error", nil)
			return
		}
		type alertBody struct {
			Kind   string `json:"kind"`
			Seq    int64  `json:"seq"`
			Detail string `json:"detail"`
			Time   string `json:"time"`
		}
		out := make([]alertBody, 0, len(alerts))
		for _, a := range alerts {
			out = append(out, alertBody{Kind: a.Kind, Seq: a.Seq, Detail: a.Detail, Time: a.Time.UTC().Format(time.RFC3339Nano)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
	})
	mux.HandleFunc("GET /v1/audit/verify", func(w http.ResponseWriter, r *http.Request) {
		type result struct {
			OK    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		}
		check := func(c app.Chain) result {
			if err := s.Verify(r.Context(), c); err != nil {
				log.Warn("audit: verify", "chain", c, "err", err)
				return result{Error: "the chain does not verify; see the edge log"}
			}
			return result{OK: true}
		}
		writeJSON(w, http.StatusOK, map[string]result{"inside": check(app.Inside), "proof": check(app.Proof)})
	})
}

func listHandler(s *app.Sink, c app.Chain, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		after, okAfter := intParam(r, "after")
		limit, okLimit := intParam(r, "limit")
		if !okAfter || !okLimit {
			writeProblem(w, http.StatusBadRequest, ProblemValidation, "after and limit must be whole numbers of 0 or more", nil)
			return
		}
		rs, err := s.List(r.Context(), c, after, int(limit))
		if err != nil {
			log.Error("audit: list", "chain", c, "err", err)
			writeProblem(w, http.StatusInternalServerError, ProblemInternal, "Internal error", nil)
			return
		}
		out := make([]json.RawMessage, 0, len(rs))
		for _, rec := range rs {
			out = append(out, rec.Raw)
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": out})
	}
}

// intParam reads a non-negative whole number; absent is 0.
func intParam(r *http.Request, name string) (int64, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= 0
}

func writeProblem(w http.ResponseWriter, code int, typ, title string, ext map[string]any) {
	body := map[string]any{"type": typ, "title": title, "status": code}
	for k, v := range ext {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
