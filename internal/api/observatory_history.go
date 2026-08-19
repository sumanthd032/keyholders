package api

import (
	"fmt"
	"net/http"
	"strconv"
)

// HistoryPoint is one epoch's kri for one node, read off a KRI_AT self loop rather than recomputed:
// the sketch pipeline is a batch job on the order of minutes against the current ingest, and this is
// a read of what a prior `keyholders observatory` run already persisted.
type HistoryPoint struct {
	Epoch int64 `json:"epoch"`
	KRI   int64 `json:"kri"`
}

// handleObservatoryHistory answers the KRI-over-time curve for one named node: every epoch its
// KRI_AT self loop carries, oldest first.
func (s *Server) handleObservatoryHistory(w http.ResponseWriter, r *http.Request) {
	label, nameProperty, err := targetLabel(r.URL.Query().Get("target"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}

	recs, err := s.db.Query(r.Context(), fmt.Sprintf(
		`MATCH (n:%s {%s: $name})-[r:KRI_AT]->(n)
     RETURN r.epoch AS epoch, r.kri AS kri
     ORDER BY r.epoch`, label, nameProperty), map[string]any{"name": name})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	points := make([]HistoryPoint, 0, len(recs))
	for _, rec := range recs {
		epoch, _ := rec.Get("epoch")
		kri, _ := rec.Get("kri")
		e, _ := epoch.(int64)
		k, _ := kri.(int64)
		points = append(points, HistoryPoint{Epoch: e, KRI: k})
	}
	writeJSON(w, http.StatusOK, points)
}

// handleObservatoryOrphaned answers the orphaned-but-load-bearing view: packages with no maintainer
// at all, ranked by how much they are still depended on. Restricted to the most recent epoch written,
// found first since KRI_AT holds every epoch this graph has ever seen and the API has no other record
// of which one is current.
func (s *Server) handleObservatoryOrphaned(w http.ResponseWriter, r *http.Request) {
	n := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("parse limit: %w", err))
			return
		}
		n = parsed
	}

	epochRecs, err := s.db.Query(r.Context(),
		`MATCH (n:Package)-[r:KRI_AT]->(n)
     RETURN r.epoch AS epoch
     ORDER BY r.epoch DESC LIMIT 1`, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(epochRecs) == 0 {
		writeJSON(w, http.StatusOK, ObservatoryOrphanedView{Entries: []LeaderboardEntry{}})
		return
	}
	epoch, _ := epochRecs[0].Get("epoch")

	recs, err := s.db.Query(r.Context(),
		`MATCH (n:Package)-[r:KRI_AT {epoch: $epoch}]->(n)
     WHERE r.orphaned = true
     RETURN n.name AS name, r.kri AS kri, r.epoch AS at
     ORDER BY r.kri DESC LIMIT $n`,
		map[string]any{"epoch": epoch, "n": int64(n)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	entries := make([]LeaderboardEntry, 0, len(recs))
	for _, rec := range recs {
		entries = append(entries, entryFrom(rec))
	}
	writeJSON(w, http.StatusOK, ObservatoryOrphanedView{Epoch: epoch.(int64), Entries: entries})
}

// ObservatoryOrphanedView carries the epoch the ranking was read at alongside the ranking itself,
// since an orphaned list with no dated context reads as a present tense fact rather than the snapshot
// it actually is (the same reasoning kri_at exists for on Package and Maintainer nodes, D44).
type ObservatoryOrphanedView struct {
	Epoch   int64              `json:"epoch"`
	Entries []LeaderboardEntry `json:"entries"`
}

func targetLabel(target string) (label, nameProperty string, err error) {
	switch target {
	case "package":
		return "Package", "name", nil
	case "maintainer":
		return "Maintainer", "handle", nil
	default:
		return "", "", fmt.Errorf("target must be package or maintainer, got %q", target)
	}
}
