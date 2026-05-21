package live

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

func HandleWebSocket(mgr *Manager, getSessionDir func(sessionID string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := extractSessionID(r.URL.Path)
		if sessionID == "" {
			http.NotFound(w, r)
			return
		}

		dir := getSessionDir(sessionID)
		if dir == "" {
			http.NotFound(w, r)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			return
		}

		client := NewWSClient(conn)
		hub := mgr.GetOrCreateHub(sessionID, dir)
		hub.Register(client)

		go func() {
			defer hub.Unregister(client)
			ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
			defer cancel()
			for {
				_, _, err := conn.Read(ctx)
				if err != nil {
					return
				}
			}
		}()
	}
}

func extractSessionID(urlPath string) string {
	if len(urlPath) < 4 {
		return ""
	}
	s := urlPath[3:]
	end := len(s)
	if idx := indexOf(s, '/'); idx >= 0 {
		end = idx
	}
	sessionID := s[:end]
	if sessionID == "" {
		return ""
	}
	return sessionID
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
