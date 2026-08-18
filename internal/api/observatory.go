package api

import (
	"fmt"
	"net/http"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// LeaderboardEntry is one ranked row read back from a node's own kri and kri_at properties, written
// by `keyholders observatory`. The API never recomputes the sketch pipeline itself: that is a batch
// job on the order of a minute against the current ingest, and this is a read of its last result.
type LeaderboardEntry struct {
	Name string `json:"name"`
	KRI  int64  `json:"kri"`
	At   int64  `json:"kri_at"`
}

// ObservatoryView is the two leaderboards the dashboard needs. The orphaned-but-load-bearing view is
// not served here yet: it depends on knowing which packages have no MAINTAINS edge at all, which this
// Cypher subset has no negated-pattern way to ask directly, and Aggregate's own computation of it
// during the observatory run is not persisted back onto a node property today.
type ObservatoryView struct {
	Maintainers []LeaderboardEntry `json:"maintainers"`
	Packages    []LeaderboardEntry `json:"packages"`
}

func (s *Server) handleObservatory(w http.ResponseWriter, r *http.Request) {
	top := 50

	// Maintainer's identifying property is handle, everywhere else in this project; Package's is
	// name. The two labels are not interchangeable here even though both carry kri and kri_at.
	maintainers, err := s.leaderboard(r, "Maintainer", "handle", top)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	packages, err := s.leaderboard(r, "Package", "name", top)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, ObservatoryView{Maintainers: maintainers, Packages: packages})
}

// leaderboard reads the top n nodes of one label by their own kri property. Unanchored, since there
// is no id to anchor a "top N ever computed" question on; label and nameProperty are literals, never
// derived from a request, so there is no injection surface in interpolating them.
func (s *Server) leaderboard(r *http.Request, label, nameProperty string, n int) ([]LeaderboardEntry, error) {
	recs, err := s.db.Query(r.Context(), fmt.Sprintf(
		`MATCH (n:%s) WHERE n.kri > 0
     RETURN n.%s AS name, n.kri AS kri, n.kri_at AS at
     ORDER BY n.kri DESC LIMIT %d`, label, nameProperty, n), nil)
	if err != nil {
		return nil, err
	}

	out := make([]LeaderboardEntry, 0, len(recs))
	for _, rec := range recs {
		out = append(out, entryFrom(rec))
	}
	return out, nil
}

func entryFrom(rec *neo4j.Record) LeaderboardEntry {
	name, _ := rec.Get("name")
	kri, _ := rec.Get("kri")
	at, _ := rec.Get("at")
	n, _ := name.(string)
	k, _ := kri.(int64)
	a, _ := at.(int64)
	return LeaderboardEntry{Name: n, KRI: k, At: a}
}
