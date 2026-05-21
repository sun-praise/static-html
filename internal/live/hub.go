package live

import (
	"context"
	"encoding/json"
	"sync"
)

type Message struct {
	Type string `json:"type"`
}

var ReloadMessage = Message{Type: "reload"}

func ReloadJSON() []byte {
	data, _ := json.Marshal(ReloadMessage)
	return data
}

type Client interface {
	Send(data []byte)
	Close()
}

type Hub struct {
	mu       sync.Mutex
	clients  map[Client]struct{}
	onChange func()
	onEmpty  func()
}

func NewHub(onChange, onEmpty func()) *Hub {
	return &Hub{
		clients:  make(map[Client]struct{}),
		onChange: onChange,
		onEmpty:  onEmpty,
	}
}

func (h *Hub) Register(c Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) Unregister(c Client) {
	h.mu.Lock()
	delete(h.clients, c)
	empty := len(h.clients) == 0
	onEmpty := h.onEmpty
	h.mu.Unlock()

	c.Close()

	if empty && onEmpty != nil {
		onEmpty()
	}
}

func (h *Hub) Broadcast(data []byte) {
	h.mu.Lock()
	clients := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		c.Send(data)
	}
}

func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

type Manager struct {
	mu       sync.Mutex
	hubs     map[string]*hubEntry
	watchDir func(sessionID, dir string, notify func()) (context.CancelFunc, error)
}

type hubEntry struct {
	hub         *Hub
	cancelWatch context.CancelFunc
}

func NewManager(watchDir func(sessionID, dir string, notify func()) (context.CancelFunc, error)) *Manager {
	return &Manager{
		hubs:     make(map[string]*hubEntry),
		watchDir: watchDir,
	}
}

func (m *Manager) GetOrCreateHub(sessionID, dir string) *Hub {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.hubs[sessionID]; ok {
		return entry.hub
	}

	hub := NewHub(nil, nil)
	entry := &hubEntry{hub: hub}

	hub.onChange = func() {
		hub.Broadcast(ReloadJSON())
	}
	hub.onEmpty = func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if e, ok := m.hubs[sessionID]; ok {
			if e.cancelWatch != nil {
				e.cancelWatch()
			}
			delete(m.hubs, sessionID)
		}
	}

	if m.watchDir != nil && dir != "" {
		cancel, err := m.watchDir(sessionID, dir, func() {
			hub.Broadcast(ReloadJSON())
		})
		if err == nil {
			entry.cancelWatch = cancel
		}
	}

	m.hubs[sessionID] = entry
	return hub
}

func (m *Manager) BroadcastTo(sessionID string, data []byte) {
	m.mu.Lock()
	entry, ok := m.hubs[sessionID]
	m.mu.Unlock()

	if ok {
		entry.hub.Broadcast(data)
	}
}
