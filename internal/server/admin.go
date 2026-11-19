package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

// Admin API: user/key/audit management over HTTP, restricted to Bearer keys
// whose user holds the admin role. These endpoints back the MCP admin tools
// against remote instances; the CLI keeps its local-store paths.
//
// Every mutation is recorded in the audit log with the acting admin as actor.

// adminClaims is the JSON body of POST /api/admin/users.
type adminCreateUserBody struct {
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

type adminSetRoleBody struct {
	Role string `json:"role"`
}

type adminPruneBody struct {
	Days int `json:"days"`
}

// requireAdminBearer authenticates the request's Bearer key and requires the
// admin role. Returns the acting admin's userID on success.
func (s *Server) requireAdminBearer(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid, ok, err := s.verifyBearer(r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return "", false
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sth"`)
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized: a valid API key is required.")
		return "", false
	}
	admin, err := s.store.IsAdmin(uid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return "", false
	}
	if !admin {
		writeJSONError(w, http.StatusForbidden, "admin role required")
		return "", false
	}
	return uid, true
}

// adminActor resolves the acting admin's username for audit records.
func (s *Server) adminActor(uid string) string {
	user, err := s.store.FindUserByID(uid)
	if err != nil {
		return uid
	}
	return user.Username
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdminBearer(w, r); !ok {
		return
	}

	users, err := s.store.ListUsers()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type adminUser struct {
		UserID     string `json:"userId"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		ActiveKeys int    `json:"activeKeys"`
		Created    string `json:"created"`
	}
	out := make([]adminUser, 0, len(users))
	for _, u := range users {
		keyCount, err := s.store.CountAPIKeysByUser(u.ID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, adminUser{
			UserID:     u.ID,
			Username:   u.Username,
			Role:       u.Role,
			ActiveKeys: keyCount,
			Created:    u.CreatedAt.Format("2006-01-02"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	actorUID, ok := s.requireAdminBearer(w, r)
	if !ok {
		return
	}

	var body adminCreateUserBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if strings.TrimSpace(body.Username) == "" {
		writeJSONError(w, http.StatusBadRequest, "username must not be empty")
		return
	}

	user, err := s.store.CreateUser(body.Username)
	if err != nil {
		if errors.Is(err, session.ErrUsernameTaken) {
			writeJSONError(w, http.StatusConflict, "username already taken")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	role := ""
	if body.Admin {
		if err := s.store.SetUserRole(user.Username, session.RoleAdmin); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		role = session.RoleAdmin
	}

	actor := s.adminActor(actorUID)
	_ = s.store.RecordAudit(session.AuditEvent{
		Actor:  actor,
		Action: session.AuditUserAdd,
		Target: user.Username,
		Detail: "role=" + role,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"userId":   user.ID,
		"username": user.Username,
		"role":     role,
	})
}

// adminUsernameFromPath extracts the username from /api/admin/users/{name}/…
func adminUsernameFromPath(r *http.Request, suffix string) string {
	name := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	name = strings.TrimSuffix(name, suffix)
	return name
}

func (s *Server) handleAdminSetRole(w http.ResponseWriter, r *http.Request) {
	actorUID, ok := s.requireAdminBearer(w, r)
	if !ok {
		return
	}

	username := adminUsernameFromPath(r, "/role")
	var body adminSetRoleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}

	if err := s.store.SetUserRole(username, body.Role); err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	actor := s.adminActor(actorUID)
	_ = s.store.RecordAudit(session.AuditEvent{
		Actor:  actor,
		Action: session.AuditUserRole,
		Target: username,
		Detail: "role=" + body.Role,
	})

	writeJSON(w, http.StatusOK, map[string]any{"username": username, "role": body.Role})
}

func (s *Server) handleAdminIssueKey(w http.ResponseWriter, r *http.Request) {
	actorUID, ok := s.requireAdminBearer(w, r)
	if !ok {
		return
	}

	username := adminUsernameFromPath(r, "/keys")
	user, err := s.store.FindUserByUsername(username)
	if err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	plaintext, record, err := s.store.IssueAPIKey(user.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	actor := s.adminActor(actorUID)
	_ = s.store.RecordAudit(session.AuditEvent{
		Actor:  actor,
		Action: session.AuditKeyIssue,
		Target: record.ID,
		Detail: "user=" + user.Username,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"keyId":  record.ID,
		"apiKey": plaintext,
	})
}

func (s *Server) handleAdminRevokeKey(w http.ResponseWriter, r *http.Request) {
	actorUID, ok := s.requireAdminBearer(w, r)
	if !ok {
		return
	}

	idOrPrefix := strings.TrimPrefix(r.URL.Path, "/api/admin/keys/")
	if err := s.store.RevokeAPIKey(idOrPrefix); err != nil {
		switch {
		case errors.Is(err, session.ErrAPIKeyAmbiguous):
			writeJSONError(w, http.StatusConflict, "multiple keys match; provide a longer prefix or the full key id")
		case errors.Is(err, session.ErrAPIKeyNotFound):
			writeJSONError(w, http.StatusNotFound, "no active key matches")
		default:
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	actor := s.adminActor(actorUID)
	_ = s.store.RecordAudit(session.AuditEvent{
		Actor:  actor,
		Action: session.AuditKeyRevoke,
		Target: idOrPrefix,
	})

	writeJSON(w, http.StatusOK, map[string]any{"revoked": idOrPrefix})
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdminBearer(w, r); !ok {
		return
	}

	q := r.URL.Query()
	filter := session.AuditFilter{
		Action:  q.Get("action"),
		Actor:   q.Get("actor"),
		Outcome: q.Get("outcome"),
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		filter.Limit = n
	}
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeJSONError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		filter.Offset = n
	}
	if raw := q.Get("since"); raw != "" {
		ts, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "since must be YYYY-MM-DD")
			return
		}
		filter.Since = ts.UTC()
	}

	events, err := s.store.ListAuditEvents(filter)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type wireEvent struct {
		ID        int64  `json:"id"`
		TSUnix    int64  `json:"tsUnix"`
		Time      string `json:"time"`
		Actor     string `json:"actor"`
		ActorID   string `json:"actorId,omitempty"`
		Action    string `json:"action"`
		Target    string `json:"target,omitempty"`
		Outcome   string `json:"outcome"`
		IP        string `json:"ip,omitempty"`
		UserAgent string `json:"userAgent,omitempty"`
		Detail    string `json:"detail,omitempty"`
	}
	out := make([]wireEvent, 0, len(events))
	for _, e := range events {
		out = append(out, wireEvent{
			ID:        e.ID,
			TSUnix:    e.TSUnix,
			Time:      time.Unix(e.TSUnix, 0).Format(time.RFC3339),
			Actor:     e.Actor,
			ActorID:   e.ActorID,
			Action:    e.Action,
			Target:    e.Target,
			Outcome:   e.Outcome,
			IP:        e.IP,
			UserAgent: e.UserAgent,
			Detail:    e.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (s *Server) handleAdminPruneAudit(w http.ResponseWriter, r *http.Request) {
	actorUID, ok := s.requireAdminBearer(w, r)
	if !ok {
		return
	}

	var body adminPruneBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if body.Days < 1 {
		writeJSONError(w, http.StatusBadRequest, "days must be a positive integer")
		return
	}

	cutoff := time.Now().UTC().Add(-time.Duration(body.Days) * 24 * time.Hour)
	removed, err := s.store.PruneAuditEvents(cutoff)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	actor := s.adminActor(actorUID)
	_ = s.store.RecordAudit(session.AuditEvent{
		Actor:  actor,
		Action: session.AuditAuditPrune,
		Detail: "days=" + strconv.Itoa(body.Days),
	})

	writeJSON(w, http.StatusOK, map[string]any{"pruned": removed})
}
