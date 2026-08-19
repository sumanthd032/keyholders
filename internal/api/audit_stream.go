package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sumanthd032/keyholders/internal/query"
)

// frontierEvent is one wavefront, shaped for the wire: entries carry only the URN, since the
// interface animates arrival, not the interval arithmetic that produced it.
type frontierEvent struct {
	Depth int      `json:"depth"`
	URNs  []string `json:"urns"`
}

// handleAuditStream runs the identical audit handleAudit does, over server sent events instead of
// one JSON payload: a "frontier" event per wavefront as the interval BFS discovers it, then one
// "done" event carrying the finished AuditView, or "error" if the audit failed.
//
// This works on the request's own goroutine with no buffering in between: query.Options.OnFrontier
// is called synchronously inside Reach's replay pass, and the callback here writes and flushes
// immediately, so the client sees each wavefront at the moment the search actually finds it. The
// animation this drives client side is therefore the real computation happening, not a replay of an
// already-finished one, which is why there is no artificial delay in either direction: unrestrained
// pacing is what keeps it honest.
func (s *Server) handleAuditStream(w http.ResponseWriter, r *http.Request) {
	lf, err := parseUploadedLockfile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opts, err := auditOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported by this connection"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	opts.OnFrontier = func(f query.Frontier) {
		urns := make([]string, len(f.Entries))
		for i, e := range f.Entries {
			urns[i] = e.URN
		}
		writeSSE(w, "frontier", frontierEvent{Depth: f.Depth, URNs: urns})
		flusher.Flush()
	}

	started := time.Now()
	audit, err := s.auditor.Audit(r.Context(), lf, opts)
	if err != nil {
		writeSSE(w, "error", struct {
			Error string `json:"error"`
		}{err.Error()})
		flusher.Flush()
		return
	}

	writeSSE(w, "done", query.NewAuditView(audit, time.Since(started)))
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, event string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
