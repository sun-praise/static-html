package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

// Annotations are per-reader notes on a preview page. Reads and writes are
// granted to anyone who can view the session (requireViewer); the owner column
// keeps each reader's set separate, so a viewer can only ever delete their own
// annotations. With auth disabled currentUser is empty — the shared pool.
type annotationRequest struct {
	Kind    string `json:"kind"`
	Quote   string `json:"quote"`
	Prefix  string `json:"prefix"`
	Suffix  string `json:"suffix"`
	Comment string `json:"comment"`
	Tag     string `json:"tag"`
}

func (s *Server) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/annotations")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if !s.requireViewer(w, r, sessionID) {
		return
	}

	ownerID, _ := currentUser(r)
	list, err := s.store.ListAnnotations(sessionID, ownerID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []session.Annotation{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"annotations": list})
}

func (s *Server) handleCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/annotations")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if !s.requireViewer(w, r, sessionID) {
		return
	}

	var req annotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}

	ownerID, _ := currentUser(r)
	created, err := s.store.AddAnnotation(sessionID, ownerID, session.Annotation{
		Kind:    req.Kind,
		Quote:   req.Quote,
		Prefix:  req.Prefix,
		Suffix:  req.Suffix,
		Comment: req.Comment,
		Tag:     req.Tag,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	// Path shape: /api/sessions/{id}/annotations/{aid}
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "annotations" || parts[2] == "" {
		writeJSONError(w, http.StatusBadRequest, "Invalid annotation path.")
		return
	}
	sessionID, annotationID := parts[0], parts[2]

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if !s.requireViewer(w, r, sessionID) {
		return
	}

	ownerID, _ := currentUser(r)
	if err := s.store.DeleteAnnotation(sessionID, ownerID, annotationID); err != nil {
		if errors.Is(err, session.ErrAnnotationNotFound) {
			writeJSONError(w, http.StatusNotFound, "Annotation not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// isAnnotationTargetPath reports whether the path targets a single annotation:
// /api/sessions/{id}/annotations/{aid}
func isAnnotationTargetPath(urlPath string) bool {
	rest := strings.TrimPrefix(urlPath, "/api/sessions/")
	parts := strings.Split(rest, "/")
	return len(parts) == 3 && parts[0] != "" && parts[1] == "annotations" && parts[2] != ""
}
