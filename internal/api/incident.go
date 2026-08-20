package api

import (
	"log"
	"net/http"
	"time"

	"github.com/sumanthd032/keyholders/internal/incident"
)

// IncidentView is the full blast radius report, JSON shaped: the union of every track question's
// answer, so the web view renders one payload instead of six round trips.
type IncidentView struct {
	Package       string                       `json:"package"`
	Version       string                       `json:"version"`
	Advisories    []incident.AdvisoryInfo      `json:"advisories"`
	Introductions []incident.Introduction      `json:"introductions"`
	Exposed       []incident.ExposedProject    `json:"exposed"`
	ResolvedLive  []incident.Exposure          `json:"resolved_while_live"`
	Shared        []incident.SharedMaintainer  `json:"shared_maintainers"`
	Typosquats    []incident.TyposquatNeighbor `json:"typosquats"`
}

func (s *Server) handleIncident(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	version := r.PathValue("version")
	ctx := r.Context()

	advisories, err := s.src.AdvisoriesAffecting(ctx, name, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	introductions := make([]incident.Introduction, 0, len(advisories))
	for _, a := range advisories {
		intro, err := incident.Bisect(ctx, s.src, a.ID, name)
		if err != nil {
			// A discovered advisory that fails to bisect is dropped from this list rather than
			// failing the whole report, the same tolerance the CLI's incident command has.
			log.Printf("skip bisecting %s for %s: %v", a.ID, name, err)
			continue
		}
		introductions = append(introductions, intro)
	}

	exposed, err := incident.TransitivelyExposed(ctx, s.src, s.auditor, name, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	live, err := incident.ResolvedWhileLive(ctx, s.src, name, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	shared, err := incident.SharedMaintainers(ctx, s.src, name, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	typosquats, err := s.src.TyposquatsNear(ctx, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, IncidentView{
		Package: name, Version: version,
		Advisories: orEmpty(advisories), Introductions: introductions,
		Exposed: orEmpty(exposed), ResolvedLive: orEmpty(live),
		Shared: orEmpty(shared), Typosquats: orEmpty(typosquats),
	})
}
