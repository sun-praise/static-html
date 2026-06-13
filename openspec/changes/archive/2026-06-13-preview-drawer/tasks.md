## 1. Storage Layer

- [x] 1.1 Add `PeersResult` and `PeerEntry` structs to `internal/session/store.go`
- [x] 1.2 Implement `GetPeers(sessionID string, limit int) (*PeersResult, error)` in store — two queries (by category, by project), exclude self and soft-deleted, order by created_at desc, limit 20 each
- [x] 1.3 Write unit tests for `GetPeers` in `internal/session/store_test.go` — cover: peers found, no category, no project, deleted excluded, ordering, limit

## 2. Peers API

- [x] 2.1 Add `GET /api/sessions/{id}/peers` route case to `routes()` in `internal/server/server.go`
- [x] 2.2 Implement `handleGetPeers()` — extract session ID, validate session exists, call `store.GetPeers`, return JSON
- [x] 2.3 Write server tests for peers endpoint in `internal/server/server_test.go` — cover: success, session not found, empty peers

## 3. Drawer Injection

- [x] 3.1 Add drawer CSS to `internal/live/inject.go` as a constant (`drawerCSS`) — `#sth-drawer-btn`, `#sth-drawer-panel`, slide animation, responsive styles, all with `!important` on critical properties
- [x] 3.2 Add drawer HTML shell to `internal/live/inject.go` as a constant (`drawerHTML`) — button element, drawer panel with close button, category list container, project list container, home link
- [x] 3.3 Add drawer JS to `internal/live/inject.go` as a constant (`drawerJS`) — button click handler, fetch `/api/sessions/{id}/peers`, populate lists, open/close animation, loading state, error state with retry
- [x] 3.4 Modify `injectScript()` to inject drawer CSS + HTML + JS alongside the existing live-reload script (only for HTML responses that already get the reload script)
- [x] 3.5 Ensure drawer injection is skipped for WebSocket upgrade requests and non-HTML assets (already handled by existing middleware logic)
- [x] 3.6 Write tests for `injectScript` — verify drawer elements present in HTML output, verify no injection in non-HTML responses
