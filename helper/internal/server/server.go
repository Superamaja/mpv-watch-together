package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

const (
	guestStaleAfter      = 20 * time.Second
	guestPruneAfter      = 2 * time.Minute
	stuckBufferingAfter  = 8 * time.Second
	serverClockPollEvery = 30 * time.Second
	coordinatorTickEvery = 2 * time.Second
	hostPresenceGrace    = 20 * time.Second
)

type App struct {
	mu                  sync.RWMutex
	cfg                 config.Config
	firebase            *firebase.Client
	room                protocol.Room
	syncEnabled         bool
	lastLocal           protocol.ParticipantState
	serverTimeOffset    int64
	streamCancel        context.CancelFunc
	appCtx              context.Context
	appCancel           context.CancelFunc
	clockWarningSent    bool
	subscribers         map[chan []byte]struct{}
	guestBufferingSince map[string]int64
	stuckBufferingSent  map[string]bool
}

type apiError struct {
	Error string `json:"error"`
}

func New(cfg config.Config) (*App, error) {
	fb, err := firebase.New(cfg.FirebaseDatabaseURL, cfg.FirebaseAuthToken)
	if err != nil {
		return nil, err
	}
	appCtx, appCancel := context.WithCancel(context.Background())
	app := &App{
		cfg:                 cfg,
		firebase:            fb,
		appCtx:              appCtx,
		appCancel:           appCancel,
		subscribers:         make(map[chan []byte]struct{}),
		guestBufferingSince: make(map[string]int64),
		stuckBufferingSent:  make(map[string]bool),
	}
	go app.monitorServerClock()
	go app.runCoordinator()
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
	if a.appCancel != nil {
		a.appCancel()
	}

	if cfg.Role == protocol.RoleGuest && cfg.RoomID != "" && cfg.UserID != "" {
		a.removeGuestParticipant(context.Background(), cfg)
	}
}

func (a *App) serverNow() int64 {
	a.mu.RLock()
	offset := a.serverTimeOffset
	a.mu.RUnlock()
	return protocol.NowMillis() + offset
}

func (a *App) normalizeParticipant(cfg config.Config, state protocol.ParticipantState, now int64) protocol.ParticipantState {
	state.UserID = cfg.UserID
	state.DisplayName = cfg.DisplayName
	state.SampledAt = now
	state.LastUpdated = now
	state.LastSeen = now
	state.Connected = true
	state.TimeReliable = true
	return state
}

func (a *App) isFreshParticipant(participant *protocol.ParticipantState, now int64, maxAge time.Duration) bool {
	return participant != nil && participant.LastSeen > 0 && now-participant.LastSeen <= maxAge.Milliseconds()
}

func (a *App) publishRoomEvent(ctx context.Context, roomID string, eventType string, message string, userID string, level string) {
	if roomID == "" {
		return
	}
	now := a.serverNow()
	event := protocol.RoomEvent{
		EventID: fmt.Sprintf("%s_%d_%06d", eventType, now, rand.Intn(1000000)),
		Type:    eventType,
		Message: message,
		UserID:  userID,
		Level:   level,
		At:      now,
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.firebase.Patch(reqCtx, roomPath(roomID)+"/events", map[string]any{"latest": event}, nil); err != nil {
		slog.Warn("failed to publish room event", "room", roomID, "type", eventType, "error", err)
		return
	}

	a.mu.Lock()
	a.room.Events.Latest = &event
	a.mu.Unlock()
	a.publishState()
}

func (a *App) monitorServerClock() {
	a.refreshServerClock(a.appCtx)
	ticker := time.NewTicker(serverClockPollEvery)
	defer ticker.Stop()

	for {
		select {
		case <-a.appCtx.Done():
			return
		case <-ticker.C:
			a.refreshServerClock(a.appCtx)
		}
	}
}

func (a *App) refreshServerClock(ctx context.Context) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var offset float64
	if err := a.firebase.Get(reqCtx, ".info/serverTimeOffset", &offset); err != nil {
		a.mu.Lock()
		shouldWarn := !a.clockWarningSent
		a.clockWarningSent = true
		a.mu.Unlock()
		if shouldWarn {
			slog.Warn("failed to refresh firebase server time offset; falling back to local helper time", "error", err)
		}
		return
	}

	a.mu.Lock()
	a.serverTimeOffset = int64(offset)
	a.clockWarningSent = false
	a.mu.Unlock()
}

func (a *App) runCoordinator() {
	ticker := time.NewTicker(coordinatorTickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-a.appCtx.Done():
			return
		case <-ticker.C:
			a.runCoordinatorTick()
		}
	}
}

func (a *App) runCoordinatorTick() {
	now := a.serverNow()

	a.mu.Lock()
	cfg := a.cfg
	room := a.room
	if cfg.Role != protocol.RoleHost || cfg.RoomID == "" {
		a.mu.Unlock()
		return
	}

	pruneIDs := make([]string, 0)
	events := make([]protocol.RoomEvent, 0)
	for guestID, guest := range room.Guests {
		ageMs := now - guest.LastSeen
		if guest.LastSeen > 0 && ageMs > guestPruneAfter.Milliseconds() {
			pruneIDs = append(pruneIDs, guestID)
			delete(a.guestBufferingSince, guestID)
			delete(a.stuckBufferingSent, guestID)
			continue
		}

		stale := guest.LastSeen <= 0 || ageMs > guestStaleAfter.Milliseconds()
		if stale || !guest.IsBuffering {
			delete(a.guestBufferingSince, guestID)
			delete(a.stuckBufferingSent, guestID)
			continue
		}

		if _, ok := a.guestBufferingSince[guestID]; !ok {
			a.guestBufferingSince[guestID] = now
			events = append(events, protocol.RoomEvent{
				Type:    "guest_buffering",
				Message: guest.DisplayName + " is buffering",
				UserID:  guestID,
				Level:   "warning",
			})
		}
		if !a.stuckBufferingSent[guestID] && now-a.guestBufferingSince[guestID] >= stuckBufferingAfter.Milliseconds() {
			a.stuckBufferingSent[guestID] = true
			events = append(events, protocol.RoomEvent{
				Type:    "guest_stuck_buffering",
				Message: guest.DisplayName + " is still buffering",
				UserID:  guestID,
				Level:   "warning",
			})
		}
	}
	a.mu.Unlock()

	for _, guestID := range pruneIDs {
		reqCtx, cancel := context.WithTimeout(a.appCtx, 5*time.Second)
		err := a.firebase.Delete(reqCtx, roomPath(cfg.RoomID)+"/guests/"+guestID)
		cancel()
		if err != nil {
			slog.Warn("failed to prune stale guest", "room", cfg.RoomID, "user", guestID, "error", err)
			continue
		}
		a.publishRoomEvent(a.appCtx, cfg.RoomID, "guest_pruned", "Removed offline guest", guestID, "info")
	}

	for _, event := range events {
		a.publishRoomEvent(a.appCtx, cfg.RoomID, event.Type, event.Message, event.UserID, event.Level)
	}
}

func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	serverNow := protocol.NowMillis() + a.serverTimeOffset
	writeJSON(w, http.StatusOK, map[string]any{
		"addr":        a.cfg.Addr,
		"role":        a.cfg.Role,
		"roomId":      a.cfg.RoomID,
		"displayName": a.cfg.DisplayName,
		"userId":      a.cfg.UserID,
		"syncEnabled": a.syncEnabled,
		"serverNow":   serverNow,
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
	previousRoomID := a.cfg.RoomID
	a.cfg.Role = req.Role
	a.cfg.RoomID = strings.TrimSpace(req.RoomID)
	if strings.TrimSpace(req.DisplayName) != "" {
		a.cfg.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	cfg := a.cfg
	enabled := a.syncEnabled
	lastLocal := a.lastLocal
	lastLocal.DisplayName = cfg.DisplayName
	a.lastLocal = lastLocal
	a.mu.Unlock()

	if previousRoomID != cfg.RoomID {
		a.startRoomStream()
	}
	if enabled {
		go a.updateParticipantConfig(context.Background(), cfg, lastLocal)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	a.publishState()
}

func (a *App) updateParticipantConfig(ctx context.Context, cfg config.Config, state protocol.ParticipantState) {
	if cfg.RoomID == "" || cfg.UserID == "" {
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	now := a.serverNow()
	state = a.normalizeParticipant(cfg, state, now)

	if cfg.Role == protocol.RoleHost {
		patch := map[string]any{
			"roomId":    cfg.RoomID,
			"host":      state,
			"updatedAt": now,
		}
		if err := a.firebase.Patch(reqCtx, roomPath(cfg.RoomID), patch, nil); err != nil {
			slog.Warn("failed to update host config in firebase", "room", cfg.RoomID, "error", err)
		}
		return
	}

	if err := a.firebase.Patch(reqCtx, roomPath(cfg.RoomID)+"/guests/"+cfg.UserID, state, nil); err != nil {
		slog.Warn("failed to update guest config in firebase", "room", cfg.RoomID, "user", cfg.UserID, "error", err)
	}
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

	now := a.serverNow()
	state = a.normalizeParticipant(cfg, state, now)

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
	serverNow := protocol.NowMillis() + a.serverTimeOffset
	writeJSON(w, http.StatusOK, protocol.CommandSnapshot{
		Role:        a.cfg.Role,
		UserID:      a.cfg.UserID,
		RoomID:      a.cfg.RoomID,
		SyncEnabled: a.syncEnabled,
		Host:        a.room.Host,
		ForceSync:   a.room.ForceSync,
		LatestEvent: a.room.Events.Latest,
		ServerNow:   serverNow,
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

	now := a.serverNow()
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if *req.Enabled && cfg.Role == protocol.RoleGuest && cfg.RoomID != "" {
		a.refreshRoom(r.Context(), cfg.RoomID)
		now = a.serverNow()
	}

	a.mu.Lock()
	cfg = a.cfg
	if *req.Enabled && cfg.Role == protocol.RoleGuest {
		room := a.room
		if !a.isFreshParticipant(room.Host, now, hostPresenceGrace) {
			a.mu.Unlock()
			writeJSON(w, http.StatusConflict, apiError{Error: "no host found in room"})
			return
		}
	}
	a.syncEnabled = *req.Enabled
	a.mu.Unlock()

	if !*req.Enabled && cfg.Role == protocol.RoleGuest && cfg.RoomID != "" {
		a.removeGuestParticipant(r.Context(), cfg)
		a.publishRoomEvent(context.Background(), cfg.RoomID, "guest_left", cfg.DisplayName+" left", cfg.UserID, "info")
	} else if !*req.Enabled && cfg.Role == protocol.RoleHost && cfg.RoomID != "" {
		a.removeHostParticipant(r.Context(), cfg, now)
		a.publishRoomEvent(context.Background(), cfg.RoomID, "host_left", cfg.DisplayName+" stopped hosting", cfg.UserID, "warning")
	} else if *req.Enabled {
		if cfg.Role == protocol.RoleGuest {
			a.publishRoomEvent(context.Background(), cfg.RoomID, "guest_synced", cfg.DisplayName+" synced", cfg.UserID, "success")
		} else if cfg.Role == protocol.RoleHost {
			a.publishRoomEvent(context.Background(), cfg.RoomID, "host_synced", cfg.DisplayName+" started hosting", cfg.UserID, "success")
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	a.publishState()
}

func (a *App) handlePostForceSync(w http.ResponseWriter, r *http.Request) {
	stateOverride, hasStateOverride, ok := decodeOptionalPlaybackState(w, r)
	if !ok {
		return
	}

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

	now := a.serverNow()
	sourceState := lastLocal
	if hasStateOverride {
		sourceState = stateOverride
		sourceState = a.normalizeParticipant(cfg, sourceState, now)

		a.mu.Lock()
		a.lastLocal = sourceState
		a.mu.Unlock()
	}

	force := protocol.ForceSync{
		SyncID:      fmt.Sprintf("%s_%d_%06d", cfg.UserID, now, rand.Intn(1000000)),
		IssuedAt:    now,
		IssuedBy:    cfg.UserID,
		CurrentTime: sourceState.CurrentTime,
		IsPlaying:   sourceState.IsPlaying,
		IsBuffering: sourceState.IsBuffering,
		Duration:    sourceState.Duration,
		SampledAt:   now,
	}
	host := sourceState
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
	guestCount := 0
	nowForGuests := now
	a.mu.RLock()
	for index := range a.room.Guests {
		guest := a.room.Guests[index]
		if a.isFreshParticipant(&guest, nowForGuests, guestStaleAfter) {
			guestCount++
		}
	}
	a.mu.RUnlock()
	a.publishRoomEvent(context.Background(), cfg.RoomID, "force_sync", fmt.Sprintf("Force sync sent to %d guests", guestCount), cfg.UserID, "success")
	writeJSON(w, http.StatusOK, force)
}

func decodeOptionalPlaybackState(w http.ResponseWriter, r *http.Request) (protocol.ParticipantState, bool, bool) {
	defer r.Body.Close()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		if err == io.EOF {
			return protocol.ParticipantState{}, false, true
		}
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return protocol.ParticipantState{}, false, false
	}
	if _, ok := raw["currentTime"]; !ok {
		return protocol.ParticipantState{}, false, true
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return protocol.ParticipantState{}, false, false
	}
	var state protocol.ParticipantState
	if err := json.Unmarshal(payload, &state); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return protocol.ParticipantState{}, false, false
	}
	return state, true, true
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
	a.publishRoomEvent(context.Background(), cfg.RoomID, "guest_pruned", "Removed offline guest", userID, "info")
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
	now := a.serverNow()
	var cleanupGuest config.Config
	a.mu.Lock()
	cfg := a.cfg
	a.room = room
	if cfg.Role == protocol.RoleGuest && cfg.RoomID == roomID && a.syncEnabled && !a.isFreshParticipant(room.Host, now, hostPresenceGrace) {
		a.syncEnabled = false
		cleanupGuest = cfg
	}
	a.mu.Unlock()

	if cleanupGuest.RoomID != "" && cleanupGuest.UserID != "" {
		go a.removeGuestParticipant(context.Background(), cleanupGuest)
	}
	a.publishState()
}

func (a *App) removeGuestParticipant(ctx context.Context, cfg config.Config) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.firebase.Delete(reqCtx, roomPath(cfg.RoomID)+"/guests/"+cfg.UserID); err != nil {
		slog.Warn("failed to remove guest participant", "room", cfg.RoomID, "user", cfg.UserID, "error", err)
	}
}

func (a *App) removeHostParticipant(ctx context.Context, cfg config.Config, now int64) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.firebase.Delete(reqCtx, roomPath(cfg.RoomID)+"/host"); err != nil {
		slog.Warn("failed to remove host participant", "room", cfg.RoomID, "user", cfg.UserID, "error", err)
	}
	if err := a.firebase.Patch(reqCtx, roomPath(cfg.RoomID), map[string]any{
		"status":    "inactive",
		"updatedAt": now,
	}, nil); err != nil {
		slog.Warn("failed to mark room inactive", "room", cfg.RoomID, "error", err)
	}
}

func (a *App) publishState() {
	a.mu.RLock()
	serverNow := protocol.NowMillis() + a.serverTimeOffset
	payload, err := json.Marshal(map[string]any{
		"role":        a.cfg.Role,
		"roomId":      a.cfg.RoomID,
		"displayName": a.cfg.DisplayName,
		"userId":      a.cfg.UserID,
		"syncEnabled": a.syncEnabled,
		"serverNow":   serverNow,
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
	serverNow := protocol.NowMillis() + a.serverTimeOffset
	payload, _ := json.Marshal(map[string]any{
		"role":        a.cfg.Role,
		"roomId":      a.cfg.RoomID,
		"displayName": a.cfg.DisplayName,
		"userId":      a.cfg.UserID,
		"syncEnabled": a.syncEnabled,
		"serverNow":   serverNow,
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
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
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
