package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"mpv-watch-together/helper/internal/protocol"
)

type appState struct {
	Addr        string        `json:"addr,omitempty"`
	Role        string        `json:"role"`
	RoomID      string        `json:"roomId"`
	DisplayName string        `json:"displayName"`
	UserID      string        `json:"userId"`
	SyncEnabled bool          `json:"syncEnabled"`
	ServerNow   int64         `json:"serverNow"`
	Room        protocol.Room `json:"room"`
}

func (a *App) snapshotLocked(includeAddr bool) appState {
	snapshot := appState{
		Role:        a.cfg.Role,
		RoomID:      a.cfg.RoomID,
		DisplayName: a.cfg.DisplayName,
		UserID:      a.cfg.UserID,
		SyncEnabled: a.syncEnabled,
		ServerNow:   a.serverNowLocked(),
		Room:        a.room,
	}
	if includeAddr {
		snapshot.Addr = a.cfg.Addr
	}
	return snapshot
}

func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	writeJSON(w, http.StatusOK, a.snapshotLocked(true))
}

func (a *App) handleGetMPVCommands(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	writeJSON(w, http.StatusOK, protocol.CommandSnapshot{
		Role:        a.cfg.Role,
		UserID:      a.cfg.UserID,
		RoomID:      a.cfg.RoomID,
		SyncEnabled: a.syncEnabled,
		Host:        a.room.Host,
		ForceSync:   a.room.ForceSync,
		TrackSync:   a.room.TrackSync,
		LatestEvent: a.room.Events.Latest,
		ServerNow:   a.serverNowLocked(),
	})
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan []byte, 8)
	a.mu.Lock()
	a.subscribers[ch] = struct{}{}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.subscribers, ch)
		a.mu.Unlock()
		close(ch)
	}()

	a.writeEvent(w, flusher)
	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			fmt.Fprintf(w, "event: state\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (a *App) publishState() {
	a.mu.RLock()
	payload, err := json.Marshal(a.snapshotLocked(false))
	subscribers := make([]chan []byte, 0, len(a.subscribers))
	for ch := range a.subscribers {
		subscribers = append(subscribers, ch)
	}
	a.mu.RUnlock()
	if err != nil {
		return
	}
	for _, ch := range subscribers {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (a *App) writeEvent(w http.ResponseWriter, flusher http.Flusher) {
	a.mu.RLock()
	payload, _ := json.Marshal(a.snapshotLocked(false))
	a.mu.RUnlock()
	fmt.Fprintf(w, "event: state\ndata: %s\n\n", payload)
	flusher.Flush()
}
