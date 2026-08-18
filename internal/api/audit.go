package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/sumanthd032/keyholders/internal/lockfile"
	"github.com/sumanthd032/keyholders/internal/query"
)

// auditOptions reads the same two parameters the CLI's scan command takes, --at and --depth, off
// the query string, so the two surfaces stay behaviourally identical rather than the API quietly
// supporting a different set of controls.
func auditOptions(r *http.Request) (query.Options, error) {
	opts := query.Options{Within: query.Always, MaxDepth: 12}
	if at := r.URL.Query().Get("at"); at != "" {
		t, err := time.Parse("2006-01-02", at)
		if err != nil {
			return query.Options{}, fmt.Errorf("parse at: %w", err)
		}
		opts.Within = query.At(t.UTC().Unix())
	}
	if depth := r.URL.Query().Get("depth"); depth != "" {
		d, err := strconv.Atoi(depth)
		if err != nil {
			return query.Options{}, fmt.Errorf("parse depth: %w", err)
		}
		opts.MaxDepth = d
	}
	return opts, nil
}

// parseUploadedLockfile reads a lockfile from the request body. The filename comes from a header
// rather than a multipart form, since the whole payload is one file with no other fields: a header
// is enough context for lockfile.Parse's format sniffing and keeps the client side to a plain
// fetch(..., {body: file}) rather than constructing FormData for one field.
func parseUploadedLockfile(r *http.Request) (lockfile.Lockfile, error) {
	name := r.Header.Get("X-Lockfile-Name")
	if name == "" {
		name = "package-lock.json"
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20)) // 64 MiB: generous for any real lockfile
	if err != nil {
		return lockfile.Lockfile{}, fmt.Errorf("read body: %w", err)
	}
	lf, err := lockfile.Parse(name, data)
	if err != nil {
		return lockfile.Lockfile{}, err
	}
	if lf.Project == "" {
		lf.Project = name
	}
	return lf, nil
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
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

	started := time.Now()
	audit, err := s.auditor.Audit(r.Context(), lf, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, query.NewAuditView(audit, time.Since(started)))
}
