package api

import "net/http"

// handleProjects lists every project this graph has recorded pins for, from a prior `scan --record`.
// The incident view needs this to offer a project to watch live, since a live traversal starts from
// one project's pins, not from the compromised version itself.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.src.RecordedProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}
