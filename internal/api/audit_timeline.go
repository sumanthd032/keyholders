package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sumanthd032/keyholders/internal/query"
)

// TimelineSample is one instant's keyholder counts: the two granularities that show up as thickness
// on the Exposure River, coexistence next to union, without the score decomposition an interactive
// scrub does not need to recompute at every point along the way.
type TimelineSample struct {
	At                  int64 `json:"at"`
	KeyholderCount      int   `json:"keyholder_count"`
	UnionKeyholderCount int   `json:"union_keyholder_count"`
}

// handleAuditTimeline runs the identical lockfile through the audit engine at a series of monthly
// instants, most recent first walked back to oldest, so the Exposure River has a curve to draw
// instead of the single point every other audit endpoint answers. This is what makes exposure a
// shape over the project's history rather than a fact about the present, the distinction the
// interface design's own governing idea is built around.
//
// Each sample is a full, independent audit at that instant: cheap enough to run in a loop at this
// project's measured search cost, and correct in a way a single search reused across instants could
// not be, since coexistence at one instant says nothing about coexistence at another.
func (s *Server) handleAuditTimeline(w http.ResponseWriter, r *http.Request) {
	lf, err := parseUploadedLockfile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	months := 24
	if m := r.URL.Query().Get("months"); m != "" {
		if parsed, err := strconv.Atoi(m); err == nil && parsed > 0 {
			months = parsed
		}
	}

	now := time.Now().UTC()
	samples := make([]TimelineSample, 0, months+1)
	for i := months; i >= 0; i-- {
		at := now.AddDate(0, -i, 0).Unix()
		opts := query.Options{Within: query.At(at), MaxDepth: 12}
		audit, err := s.auditor.Audit(r.Context(), lf, opts)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		samples = append(samples, TimelineSample{
			At:                  at,
			KeyholderCount:      len(audit.Keyholders),
			UnionKeyholderCount: audit.UnionKeyholders,
		})
	}

	writeJSON(w, http.StatusOK, struct {
		Samples []TimelineSample `json:"samples"`
	}{Samples: samples})
}
