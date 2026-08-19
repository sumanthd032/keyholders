package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sumanthd032/keyholders/internal/query"
)

// handlePath answers the same question as the CLI's path command: for one handle, what does the
// uploaded lockfile actually reach through them, and what is the shortest coexistence proof.
//
// A chain costs one algo.MSpaths call per held package, which for a keyholder with a hundred
// packages is a hundred sequential graph calls, tens of seconds against a warm graph. The CLI's
// default of proving only the widest-reach package unless -all is passed exists for the same
// reason and is mirrored here: by default this proves one package, named by the package query
// parameter or, absent that, the widest-reach package, exactly like the CLI's unflagged case.
// Every held package is only computed when all=true is passed explicitly.
//
// The audit runs fresh rather than being cached from a prior /api/audit call, since a proof asked
// for at --at is not necessarily the same audit the roster on screen was built from.
func (s *Server) handlePath(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimPrefix(r.URL.Query().Get("handle"), "@")
	if handle == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("handle is required"))
		return
	}
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

	audit, err := s.auditor.Audit(r.Context(), lf, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	held, ok := query.HeldPackages(audit, handle)
	if !ok {
		writeJSON(w, http.StatusOK, query.NewPathView(handle, nil, nil))
		return
	}

	targets := held
	if !parseBool(r.URL.Query().Get("all")) {
		targets = held[:1]
		if want := r.URL.Query().Get("package"); want != "" {
			if urn, ok := matchHeld(held, want); ok {
				targets = []string{urn}
			}
		}
	}

	chains, err := s.auditor.HolderChains(r.Context(), audit, targets, opts.MaxDepth)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, query.NewPathView(handle, held, chains))
}

// matchHeld finds a held package by exact URN or by bare name, so a caller can pass either the wire
// form ("pkg:npm/aria-query") or what a user would type or click ("aria-query").
func matchHeld(held []string, want string) (string, bool) {
	for _, h := range held {
		if h == want || h == "pkg:npm/"+want {
			return h, true
		}
	}
	return "", false
}

func parseBool(v string) bool {
	return v == "1" || v == "true"
}
