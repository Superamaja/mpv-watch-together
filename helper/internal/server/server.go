package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"mpv-watch-together/helper/internal/config"
	"mpv-watch-together/helper/internal/firebase"
	"mpv-watch-together/helper/internal/protocol"
	"mpv-watch-together/helper/web"
)

type App struct {
	mu           sync.RWMutex
	cfg          config.Config
	firebase     *firebase.Client
	room         protocol.Room
	syncEnabled  bool
	lastLocal    protocol.ParticipantState
	streamCancel context.CancelFunc
	subscribers  map[chan []byte]struct{}
}

type apiError struct {
	Error string `json:"error"`
}

func New(cfg config.Config) (*App, error) {
	fb, err := firebase.New(cfg.FirebaseDatabaseURL, cfg.FirebaseAuthToken)
	if err != nil {
		return nil, err
	}
	app := &App{
		cfg:         cfg,
		firebase:    fb,
		subscribers: make(map[chan []byte]struct{}),
	}
	if cfg.RoomID != "" {
		app.startRoomStream()
	}
	return app, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", a.handleGetConfig)
	mux.HandleFunc("POST /api/config", a.handlePostConfig)
	mux.HandleFunc("POST /api/mpv/state", a.handlePostMPVState)
	mux.HandleFunc("GET /api/mpv/commands", a.handleGetMPVCommands)
	mux.HandleFunc("POST /api/sync", a.handlePostSync)
	mux.HandleFunc("POST /api/host/force-sync", a.handlePostForceSync)
	mux.HandleFunc("DELETE /api/host/guests/{userId}", a.handleDeleteGuest)
	mux.HandleFunc("GET /api/events", a.handleEvents)

	staticFS, _ := fs.Sub(web.Static, "static")
	staticHandler := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		role := a.cfg.Role
		a.mu.RUnlock()
		if role != protocol.RoleHost {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("Guest helper is running. The browser dashboard is only available for host role.\nUse mpv Ctrl+w for guest controls.\n"))
				return
			}
			http.NotFound(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})
	return withCORS(mux)
}

func (a *App) Close() {
	a.mu.Lock()
	cfg := a.cfg
	if a.streamCancel != nil {
		a.streamCancel()
		a.streamCancel = nil
	}
	a.mu.Unlock()

	if cfg.Role == protocol.RoleGuest && cfg.RoomID != "" && cfg.UserID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.firebase.Delete(ctx, roomPath(cfg.RoomID)+"/guests/"+cfg.UserID)
	}
}

func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"addr":        a.cfg.Addr,
		"role":        a.cfg.Role,
		"roomId":      a.cfg.RoomID,
		"displayName": a.cfg.DisplayName,
		"userId":      a.cfg.UserID,
		"syncEnabled": a.syncEnabled,
		"room":        a.room,
	})
}

func (a *App) handlePostConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role        string `json:"role"`
		RoomID      string `json:"roomId"`
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if req.Role != protocol.RoleHost && req.Role != protocol.RoleGuest {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "role must be host or guest"})
		return
	}
	if strings.TrimSpace(req.RoomID) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "roomId is required"})
		return
	}

	a.mu.Lock()
	a.cfg.Role = req.Role
	a.cfg.RoomID = strings.TrimSpace(req.RoomID)
	if strings.TrimSpace(req.DisplayName) != "" {
		a.cfg.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	a.mu.Unlock()

	a.startRoomStream()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	a.publishState()
}

func (a *App) handlePostMPVState(w http.ResponseWriter, r *http.Request) {
	var state protocol.ParticipantState
	if !decodeJSON(w, r, &state) {
		return
	}

	a.mu.RLock()
	cfg := a.cfg
	enabled := a.syncEnabled
	a.mu.RUnlock()
	if !enabled || cfg.RoomID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": "sync disabled or room missing"})
		return
	}

	now := protocol.NowMillis()
	state.UserID = cfg.UserID
	state.DisplayName = cfg.DisplayName
	state.SampledAt = now
	state.LastUpdated = now
	state.LastSeen = now
	state.Connected = true
	state.TimeReliable = true

	a.mu.Lock()
	a.lastLocal = state
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if cfg.Role == protocol.RoleHost {
		patch := map[string]any{
			"roomId":    cfg.RoomID,
			"host":      state,
			"status":    "active",
			"updatedAt": now,
			"permissions": protocol.Permissions{
				ControllerID: cfg.UserID,
			},
		}
		if err := a.firebase.Patch(ctx, roomPath(cfg.RoomID), patch, nil); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
	} else {
		if err := a.firebase.Patch(ctx, roomPath(cfg.RoomID)+"/guests/"+cfg.UserID, state, nil); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
		ServerNow:   protocol.NowMillis(),
	})
}

func (a *App) handlePostSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "enabled is required"})
		return
	}

	a.mu.Lock()
	a.syncEnabled = *req.Enabled
	cfg := a.cfg
	a.mu.Unlock()

	if !*req.Enabled && cfg.Role == protocol.RoleGuest && cfg.RoomID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_ = a.firebase.Delete(ctx, roomPath(cfg.RoomID)+"/guests/"+cfg.UserID)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	a.publishState()
}

func (a *App) handlePostForceSync(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	lastLocal := a.lastLocal
	enabled := a.syncEnabled
	a.mu.RUnlock()
	if cfg.Role != protocol.RoleHost {
		writeJSON(w, http.StatusForbidden, apiError{Error: "force sync is only available for host role"})
		return
	}
	if !enabled || cfg.RoomID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "sync must be enabled and room configured"})
		return
	}

	now := protocol.NowMillis()
	force := protocol.ForceSync{
		SyncID:      fmt.Sprintf("%s_%d_%06d", cfg.UserID, now, rand.Intn(1000000)),
		IssuedAt:    now,
		IssuedBy:    cfg.UserID,
		CurrentTime: lastLocal.CurrentTime,
		IsPlaying:   lastLocal.IsPlaying,
		IsBuffering: lastLocal.IsBuffering,
		Duration:    lastLocal.Duration,
		SampledAt:   now,
	}
	host := lastLocal
	host.UserID = cfg.UserID
	host.DisplayName = cfg.DisplayName
	host.SampledAt = now
	host.LastUpdated = now
	host.LastSeen = now
	host.Connected = true

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	patch := map[string]any{
		"host":      host,
		"forceSync": force,
		"status":    "active",
		"updatedAt": now,
	}
	if err := a.firebase.Patch(ctx, roomPath(cfg.RoomID), patch, nil); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, force)
}

func (a *App) handleDeleteGuest(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if cfg.Role != protocol.RoleHost {
		writeJSON(w, http.StatusForbidden, apiError{Error: "guest removal is only available for host role"})
		return
	}
	if cfg.RoomID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "room is required"})
		return
	}

	userID := strings.TrimSpace(r.PathValue("userId"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "userId is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := a.firebase.Delete(ctx, roomPath(cfg.RoomID)+"/guests/"+userID); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

func (a *App) startRoomStream() {
	a.mu.Lock()
	if a.streamCancel != nil {
		a.streamCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.streamCancel = cancel
	roomID := a.cfg.RoomID
	a.mu.Unlock()

	go func() {
		events, errs := a.firebase.Stream(ctx, roomPath(roomID))
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-errs:
				if err != nil {
					if firebase.IsNotFound(err) {
						slog.Warn("firebase database path returned 404; check FIREBASE_DATABASE_URL points to Realtime Database, not authDomain/projectId/storageBucket", "room", roomID, "error", err)
						return
					}
					slog.Warn("firebase stream error", "room", roomID, "error", err)
				}
			case _, ok := <-events:
				if !ok {
					return
				}
				a.refreshRoom(ctx, roomID)
			}
		}
	}()

	go a.refreshRoom(ctx, roomID)
}

func (a *App) refreshRoom(ctx context.Context, roomID string) {
	var room protocol.Room
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.firebase.Get(reqCtx, roomPath(roomID), &room); err != nil {
		if firebase.IsNotFound(err) {
			slog.Warn("firebase database path returned 404; check FIREBASE_DATABASE_URL points to Realtime Database, not authDomain/projectId/storageBucket", "room", roomID, "error", err)
			return
		}
		slog.Warn("failed to refresh room", "room", roomID, "error", err)
		return
	}
	if room.Guests == nil {
		room.Guests = map[string]protocol.ParticipantState{}
	}
	a.mu.Lock()
	a.room = room
	a.mu.Unlock()
	a.publishState()
}

func (a *App) publishState() {
	a.mu.RLock()
	payload, err := json.Marshal(map[string]any{
		"role":        a.cfg.Role,
		"roomId":      a.cfg.RoomID,
		"displayName": a.cfg.DisplayName,
		"userId":      a.cfg.UserID,
		"syncEnabled": a.syncEnabled,
		"room":        a.room,
	})
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
	payload, _ := json.Marshal(map[string]any{
		"role":        a.cfg.Role,
		"roomId":      a.cfg.RoomID,
		"displayName": a.cfg.DisplayName,
		"userId":      a.cfg.UserID,
		"syncEnabled": a.syncEnabled,
		"room":        a.room,
	})
	a.mu.RUnlock()
	fmt.Fprintf(w, "event: state\ndata: %s\n\n", payload)
	flusher.Flush()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:8765")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func roomPath(roomID string) string {
	return "rooms/" + roomID
}
