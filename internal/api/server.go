// Package api exposes the query, observatory, and incident engines over HTTP for the web interface.
// JSON for most reads; server sent events for the one view that needs to show its own computation
// happening rather than its finished result, so a live traversal can draw real search frontiers as
// they are discovered instead of animating an already-finished answer.
package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/incident"
	"github.com/sumanthd032/keyholders/internal/query"
)

// Server holds what every handler needs: the graph connection and an auditor built once, since
// building one is cheap but there is no reason to repeat it per request.
type Server struct {
	db      *graph.Client
	auditor *query.Auditor
	src     incident.GraphSource
}

func New(db *graph.Client) *Server {
	return &Server{
		db:      db,
		auditor: query.NewAuditor(db),
		src:     incident.GraphSource{DB: db, Ecosystem: "npm"},
	}
}

// Mux wires every route. Go 1.22's ServeMux pattern matching is enough for this surface: no path
// carries ambiguous segments, and a router dependency would earn nothing a few method-and-path
// strings do not already give for free.
func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/audit", s.handleAudit)
	mux.HandleFunc("POST /api/audit/stream", s.handleAuditStream)
	mux.HandleFunc("GET /api/who/{handle}", s.handleWho)
	mux.HandleFunc("GET /api/observatory", s.handleObservatory)
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("GET /api/incident/{name}/{version}", s.handleIncident)
	mux.HandleFunc("GET /api/incident/{name}/{version}/stream", s.handleIncidentStream)
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return withCORS(withLogging(mux))
}

// withCORS allows the Next.js dev server, on its own port, to call this API. Permissive by design:
// this is a single user's local instrument, not a multi-tenant service, and the graph it reads is
// the same local HydraDB the CLI already trusts.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Lockfile-Name")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: err.Error()})
}
