package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/lockfile"
	"github.com/sumanthd032/keyholders/internal/query"
)

// incidentDone is the final SSE event for an incident's live traversal: the finished audit, plus
// whether the compromised version the incident is about was actually reached, so the client does not
// have to search AuditView's graph for a URN itself to answer the one question this view exists for.
type incidentDone struct {
	Audit   query.AuditView `json:"audit"`
	Target  string          `json:"target"`
	Reached bool            `json:"reached"`
}

// handleIncidentStream streams one recorded project's coexistence search live, exactly the way
// handleAuditStream does for an uploaded lockfile, so the incident view can watch the blast radius
// expand hop by hop rather than show a finished exposure list. It answers a narrower question than
// the JSON /api/incident endpoint's TransitivelyExposed, which checks every recorded project at
// once: this is one project, chosen by the caller, watched live.
func (s *Server) handleIncidentStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	version := r.PathValue("version")
	project := r.URL.Query().Get("project")
	if project == "" {
		writeError(w, http.StatusBadRequest, errors.New("project query parameter is required"))
		return
	}

	pins, err := s.src.Pins(r.Context(), project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(pins) == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("no recorded pins for project %q", project))
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

	opts := query.Options{Within: query.Always, MaxDepth: 12}
	opts.OnFrontier = func(f query.Frontier) {
		urns := make([]string, len(f.Entries))
		for i, e := range f.Entries {
			urns[i] = e.URN
		}
		writeSSE(w, "frontier", frontierEvent{Depth: f.Depth, URNs: urns})
		flusher.Flush()
	}

	lf := lockfile.Lockfile{Project: project, Pins: pins}
	started := time.Now()
	audit, err := s.auditor.Audit(r.Context(), lf, opts)
	if err != nil {
		writeSSE(w, "error", struct {
			Error string `json:"error"`
		}{err.Error()})
		flusher.Flush()
		return
	}

	target := graph.VersionURN("npm", name, version)
	_, reached := audit.Reach.Coexistence[target]
	writeSSE(w, "done", incidentDone{
		Audit:   query.NewAuditView(audit, time.Since(started)),
		Target:  target,
		Reached: reached,
	})
	flusher.Flush()
}
