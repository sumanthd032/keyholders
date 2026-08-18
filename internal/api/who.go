package api

import (
	"net/http"

	"github.com/sumanthd032/keyholders/internal/query"
)

// HeldView is one package an account can publish to, shaped for the wire.
type HeldView struct {
	Package  string `json:"package"`
	Versions int    `json:"versions"`
}

// WhoView is query.Who shaped for the wire: the dual counter this view needs is dependents against
// union dependents, the same coexistence-versus-union argument the audit view makes in the other
// direction.
type WhoView struct {
	Handle          string     `json:"handle"`
	Holds           []HeldView `json:"holds"`
	Dependents      []string   `json:"dependents"`
	DependentCount  int        `json:"dependent_count"`
	UnionDependents int        `json:"union_dependent_count"`
}

func newWhoView(w query.Who) WhoView {
	view := WhoView{
		Handle:          w.Handle,
		Dependents:      w.Dependents,
		DependentCount:  len(w.Dependents),
		UnionDependents: w.UnionDependents,
	}
	for _, h := range w.Holds {
		view.Holds = append(view.Holds, HeldView{Package: h.Package, Versions: h.Versions})
	}
	return view
}

func (s *Server) handleWho(w http.ResponseWriter, r *http.Request) {
	handle := r.PathValue("handle")
	opts, err := auditOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	who, err := s.auditor.WhoIs(r.Context(), handle, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, newWhoView(who))
}
