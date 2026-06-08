package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
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
	serverClockPollEvery = 30 * time.Second
	coordinatorTickEvery = 2 * time.Second
	hostPresenceGrace    = 20 * time.Second

	eventAutoForceSync  = "auto_force_sync"
	eventConfigChanged  = "config_changed"
	eventForceSync      = "force_sync"
	eventGuestBuffering = "guest_buffering"
	eventGuestLeft      = "guest_left"
	eventGuestPruned    = "guest_pruned"
	eventGuestSynced    = "guest_synced"
	eventHostLeft       = "host_left"
	eventHostSynced     = "host_synced"
	eventSyncToGuest    = "sync_to_guest"
	eventTracksSynced   = "tracks_synced"

	forceSyncReasonAutoSeek = "auto_seek"
	forceSyncReasonToGuest  = "sync_to_guest"
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
	subscribers         map[chan []byte]struct{}
	guestBufferingSince map[string]int64
}

type apiError struct {
	Error string `json:"error"`
}

func New(cfg config.Config) (*App, error) {
	fb, err := firebase.New(cfg.FirebaseDatabaseURL)
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
	mux.HandleFunc("POST /api/host/sync-to-guest", a.handlePostSyncToGuest)
	mux.HandleFunc("POST /api/host/track-sync", a.handlePostTrackSync)
	mux.HandleFunc("POST /api/host/settings", a.handlePostHostSettings)
	mux.HandleFunc("DELETE /api/host/settings", a.handleDeleteHostSettings)
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
	state.LastSeen = now
	state.Connected = true
	state.TimeReliable = true
	return state
}

func (a *App) isFreshParticipant(participant *protocol.ParticipantState, now int64, maxAge time.Duration) bool {
	return participant != nil && participant.LastSeen > 0 && now-participant.LastSeen <= maxAge.Milliseconds()
}

func (a *App) publishRoomEvent(ctx context.Context, roomID string, eventType string, message string, userID string, level string) *protocol.RoomEvent {
	if roomID == "" {
		return nil
	}
	event := a.newRoomEvent(eventType, message, userID, level)

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.firebase.Patch(reqCtx, roomPath(roomID)+"/events", map[string]any{"latest": event}, nil); err != nil {
		slog.Warn("failed to publish room event", "room", roomID, "type", eventType, "error", err)
		return nil
	}

	a.mu.Lock()
	a.room.Events.Latest = &event
	a.mu.Unlock()
	a.publishState()
	return &event
}

func (a *App) newRoomEvent(eventType string, message string, userID string, level string) protocol.RoomEvent {
	now := a.serverNow()
	return protocol.RoomEvent{
		EventID: fmt.Sprintf("%s_%d_%06d", eventType, now, rand.Intn(1000000)),
		Type:    eventType,
		Message: message,
		UserID:  userID,
		Level:   level,
		At:      now,
	}
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

	a.mu.RLock()
	roomID := a.cfg.RoomID
	a.mu.RUnlock()
	if roomID == "" {
		return
	}

	serverTime, err := a.firebase.ServerTime(reqCtx, "rooms/"+roomID)
	if err != nil {
		slog.Warn("failed to refresh firebase server time; falling back to local helper time", "error", err)
		return
	}

	offset := serverTime.UnixMilli() - protocol.NowMillis()
	a.mu.Lock()
	a.serverTimeOffset = offset
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
			continue
		}

		stale := guest.LastSeen <= 0 || ageMs > guestStaleAfter.Milliseconds()
		if stale || !guest.IsBuffering {
			delete(a.guestBufferingSince, guestID)
			continue
		}

		if _, ok := a.guestBufferingSince[guestID]; !ok {
			a.guestBufferingSince[guestID] = now
			events = append(events, protocol.RoomEvent{
				Type:    eventGuestBuffering,
				Message: guest.DisplayName + " is buffering; host paused",
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
		a.publishRoomEvent(a.appCtx, cfg.RoomID, eventGuestPruned, "Removed offline guest", guestID, "info")
	}

	for _, event := range events {
		a.publishRoomEvent(a.appCtx, cfg.RoomID, event.Type, event.Message, event.UserID, event.Level)
	}
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
	previousCfg := a.cfg
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

	roomChanged := previousCfg.RoomID != cfg.RoomID
	nameChanged := previousCfg.DisplayName != cfg.DisplayName
	if enabled && roomChanged && previousCfg.RoomID != "" {
		if previousCfg.Role == protocol.RoleHost {
			a.removeHostParticipant(context.Background(), previousCfg, a.serverNow())
		} else if previousCfg.Role == protocol.RoleGuest {
			a.removeGuestParticipant(context.Background(), previousCfg)
		}
	}
	if roomChanged {
		a.startRoomStream()
	}
	var event *protocol.RoomEvent
	if enabled {
		go a.updateParticipantConfig(context.Background(), cfg, lastLocal)
		if message := configChangedMessage(roomChanged, nameChanged, cfg); message != "" {
			event = a.publishRoomEvent(context.Background(), cfg.RoomID, eventConfigChanged, message, cfg.UserID, "info")
		}
	}
	response := map[string]any{"ok": true}
	if event != nil {
		response["eventId"] = event.EventID
	}
	writeJSON(w, http.StatusOK, response)
	a.publishState()
}

func configChangedMessage(roomChanged bool, nameChanged bool, cfg config.Config) string {
	changes := make([]string, 0, 2)
	if roomChanged {
		changes = append(changes, "room changed to "+cfg.RoomID)
	}
	if nameChanged {
		changes = append(changes, "name changed to "+cfg.DisplayName)
	}
	if len(changes) == 0 {
		return ""
	}
	return strings.Join(changes, "; ")
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
		a.publishRoomEvent(context.Background(), cfg.RoomID, eventGuestLeft, cfg.DisplayName+" left", cfg.UserID, "info")
	} else if !*req.Enabled && cfg.Role == protocol.RoleHost && cfg.RoomID != "" {
		a.removeHostParticipant(r.Context(), cfg, now)
		a.publishRoomEvent(context.Background(), cfg.RoomID, eventHostLeft, cfg.DisplayName+" stopped hosting", cfg.UserID, "warning")
	} else if *req.Enabled {
		if cfg.Role == protocol.RoleGuest {
			a.publishRoomEvent(context.Background(), cfg.RoomID, eventGuestSynced, cfg.DisplayName+" synced", cfg.UserID, "success")
		} else if cfg.Role == protocol.RoleHost {
			a.publishRoomEvent(context.Background(), cfg.RoomID, eventHostSynced, cfg.DisplayName+" started hosting", cfg.UserID, "success")
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	a.publishState()
}

func (a *App) handlePostForceSync(w http.ResponseWriter, r *http.Request) {
	stateOverride, hasStateOverride, reason, ok := decodeForceSyncRequest(w, r)
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
		Reason:      reason,
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
	eventType := eventForceSync
	message := fmt.Sprintf("Force sync sent to %d guests", guestCount)
	if reason == forceSyncReasonAutoSeek {
		eventType = eventAutoForceSync
		message = fmt.Sprintf("Auto sync sent after seek to %d guests", guestCount)
	}
	event := a.publishRoomEvent(context.Background(), cfg.RoomID, eventType, message, cfg.UserID, "success")
	response := map[string]any{
		"forceSync":      force,
		"recipientCount": guestCount,
		"message":        message,
	}
	if event != nil {
		response["eventId"] = event.EventID
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handlePostSyncToGuest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"userId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "userId is required"})
		return
	}

	now := a.serverNow()
	a.mu.RLock()
	cfg := a.cfg
	enabled := a.syncEnabled
	lastLocal := a.lastLocal
	room := a.room
	guest, ok := room.Guests[req.UserID]
	a.mu.RUnlock()
	if cfg.Role != protocol.RoleHost {
		writeJSON(w, http.StatusForbidden, apiError{Error: "sync to guest is only available for host role"})
		return
	}
	if !enabled || cfg.RoomID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "sync must be enabled and room configured"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "guest not found"})
		return
	}
	if guest.UserID == "" {
		guest.UserID = req.UserID
	}
	if !a.isFreshParticipant(&guest, now, guestStaleAfter) {
		writeJSON(w, http.StatusConflict, apiError{Error: "guest is offline"})
		return
	}
	if guest.TimeReliable == false || !validPlaybackTime(guest.CurrentTime) {
		writeJSON(w, http.StatusConflict, apiError{Error: "guest time is not reliable"})
		return
	}

	hostState := lastLocal
	if !validPlaybackTime(hostState.CurrentTime) && room.Host != nil {
		hostState = *room.Host
	}
	targetTime := projectedParticipantTime(guest, now)
	host := a.normalizeParticipant(cfg, hostState, now)
	host.CurrentTime = targetTime
	host.IsBuffering = false

	force := protocol.ForceSync{
		SyncID:            fmt.Sprintf("%s_to_%s_%d_%06d", cfg.UserID, guest.UserID, now, rand.Intn(1000000)),
		IssuedAt:          now,
		IssuedBy:          cfg.UserID,
		Reason:            forceSyncReasonToGuest,
		SourceUserID:      guest.UserID,
		SourceDisplayName: guest.DisplayName,
		ApplyToHost:       true,
		CurrentTime:       targetTime,
		IsPlaying:         host.IsPlaying,
		IsBuffering:       false,
		Duration:          host.Duration,
		SampledAt:         now,
	}

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

	a.mu.Lock()
	a.lastLocal = host
	a.room.Host = &host
	a.room.ForceSync = &force
	a.room.Status = "active"
	a.room.UpdatedAt = now
	a.mu.Unlock()
	a.publishState()

	sourceName := displayNameOrID(guest.DisplayName, guest.UserID)
	message := "Synced room to " + sourceName
	event := a.publishRoomEvent(context.Background(), cfg.RoomID, eventSyncToGuest, message, cfg.UserID, "success")
	response := map[string]any{
		"forceSync":         force,
		"sourceUserId":      guest.UserID,
		"sourceDisplayName": sourceName,
		"message":           message,
	}
	if event != nil {
		response["eventId"] = event.EventID
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handlePostTrackSync(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	lastLocal := a.lastLocal
	enabled := a.syncEnabled
	a.mu.RUnlock()
	if cfg.Role != protocol.RoleHost {
		writeJSON(w, http.StatusForbidden, apiError{Error: "track sync is only available for host role"})
		return
	}
	if !enabled || cfg.RoomID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "sync must be enabled and room configured"})
		return
	}

	now := a.serverNow()
	trackSync := protocol.TrackSync{
		SyncID:     fmt.Sprintf("%s_tracks_%d_%06d", cfg.UserID, now, rand.Intn(1000000)),
		IssuedAt:   now,
		IssuedBy:   cfg.UserID,
		AudioID:    strings.TrimSpace(lastLocal.AudioID),
		SubtitleID: strings.TrimSpace(lastLocal.SubtitleID),
	}
	event := a.newRoomEvent(eventTracksSynced, trackSyncMessage(trackSync), cfg.UserID, "success")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	patch := map[string]any{
		"trackSync": trackSync,
		"events": map[string]any{
			"latest": event,
		},
		"updatedAt": now,
	}
	if err := a.firebase.Patch(ctx, roomPath(cfg.RoomID), patch, nil); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}

	a.mu.Lock()
	a.room.TrackSync = &trackSync
	a.room.Events.Latest = &event
	a.mu.Unlock()
	a.publishState()

	writeJSON(w, http.StatusOK, map[string]any{
		"trackSync": trackSync,
		"eventId":   event.EventID,
		"message":   event.Message,
	})
}

func trackSyncMessage(trackSync protocol.TrackSync) string {
	return fmt.Sprintf("Pushed audio %s and subtitles %s", displayTrackID(trackSync.AudioID), displayTrackID(trackSync.SubtitleID))
}

func displayTrackID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	if value == "no" {
		return "off"
	}
	return value
}

func projectedParticipantTime(state protocol.ParticipantState, now int64) float64 {
	currentTime := state.CurrentTime
	if state.IsPlaying && !state.IsBuffering && state.SampledAt > 0 {
		currentTime += math.Max(0, float64(now-state.SampledAt)/1000)
	}
	if currentTime < 0 {
		return 0
	}
	if state.Duration > 0 && currentTime > state.Duration {
		return state.Duration
	}
	return currentTime
}

func validPlaybackTime(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func displayNameOrID(displayName string, userID string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName != "" {
		return displayName
	}
	userID = strings.TrimSpace(userID)
	if userID != "" {
		return userID
	}
	return "guest"
}

func decodeForceSyncRequest(w http.ResponseWriter, r *http.Request) (protocol.ParticipantState, bool, string, bool) {
	defer r.Body.Close()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		if err == io.EOF {
			return protocol.ParticipantState{}, false, "", true
		}
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return protocol.ParticipantState{}, false, "", false
	}
	reason := ""
	if rawReason, ok := raw["reason"]; ok {
		_ = json.Unmarshal(rawReason, &reason)
		reason = strings.TrimSpace(reason)
		delete(raw, "reason")
	}
	if _, ok := raw["currentTime"]; !ok {
		return protocol.ParticipantState{}, false, reason, true
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return protocol.ParticipantState{}, false, "", false
	}
	var state protocol.ParticipantState
	if err := json.Unmarshal(payload, &state); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return protocol.ParticipantState{}, false, "", false
	}
	return state, true, reason, true
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
	a.publishRoomEvent(context.Background(), cfg.RoomID, eventGuestPruned, "Removed offline guest", userID, "info")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
			case err, ok := <-errs:
				if !ok {
					return
				}
				if err != nil {
					if firebase.IsNotFound(err) {
						slog.Warn("firebase database path returned 404; check FIREBASE_DATABASE_URL points to Realtime Database, not authDomain/projectId/storageBucket", "room", roomID, "error", err)
						return
					}
					slog.Warn("firebase stream error", "room", roomID, "error", err)
				}
			case event, ok := <-events:
				if !ok {
					return
				}
				if err := a.applyRoomStreamEvent(roomID, event); err != nil {
					slog.Warn("failed to apply firebase stream event; refreshing room", "room", roomID, "error", err)
					a.refreshRoom(ctx, roomID)
				}
			}
		}
	}()
}

func (a *App) applyRoomStreamEvent(roomID string, event firebase.StreamEvent) error {
	if event.Event != "put" && event.Event != "patch" {
		return nil
	}

	var payload struct {
		Path string          `json:"path"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return err
	}

	value, err := decodeStreamValue(payload.Data)
	if err != nil {
		return err
	}

	a.mu.Lock()
	if a.cfg.RoomID != roomID {
		a.mu.Unlock()
		return nil
	}

	var document any
	if raw, err := json.Marshal(a.room); err != nil {
		a.mu.Unlock()
		return err
	} else if err := json.Unmarshal(raw, &document); err != nil {
		a.mu.Unlock()
		return err
	}

	path := streamPathSegments(payload.Path)
	if event.Event == "patch" {
		patchJSONAtPath(&document, path, value)
	} else {
		setJSONAtPath(&document, path, value)
	}

	var nextRoom protocol.Room
	if document != nil {
		raw, err := json.Marshal(document)
		if err != nil {
			a.mu.Unlock()
			return err
		}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &nextRoom); err != nil {
				a.mu.Unlock()
				return err
			}
		}
	}
	if nextRoom.Guests == nil {
		nextRoom.Guests = map[string]protocol.ParticipantState{}
	}

	now := a.serverNowLocked()
	var cleanupCfg config.Config
	if a.cfg.Role == protocol.RoleGuest && a.syncEnabled && !a.isFreshParticipant(nextRoom.Host, now, hostPresenceGrace) {
		a.syncEnabled = false
		cleanupCfg = a.cfg
	}
	a.room = nextRoom
	a.mu.Unlock()

	if cleanupCfg.RoomID != "" && cleanupCfg.UserID != "" {
		go a.removeGuestParticipant(context.Background(), cleanupCfg)
	}
	a.publishState()
	return nil
}

func (a *App) serverNowLocked() int64 {
	return protocol.NowMillis() + a.serverTimeOffset
}

func decodeStreamValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func streamPathSegments(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func patchJSONAtPath(document *any, path []string, value any) {
	patch, ok := value.(map[string]any)
	if !ok {
		setJSONAtPath(document, path, value)
		return
	}
	for key, childValue := range patch {
		setJSONAtPath(document, append(append([]string{}, path...), key), childValue)
	}
}

func setJSONAtPath(document *any, path []string, value any) {
	if len(path) == 0 {
		*document = value
		return
	}
	if *document == nil {
		*document = map[string]any{}
	}
	node, ok := (*document).(map[string]any)
	if !ok {
		node = map[string]any{}
		*document = node
	}
	key := path[0]
	if len(path) == 1 {
		if value == nil {
			delete(node, key)
			return
		}
		node[key] = value
		return
	}
	child := node[key]
	setJSONAtPath(&child, path[1:], value)
	if child == nil {
		delete(node, key)
		return
	}
	node[key] = child
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
		w.Header().Set("Access-Control-Allow-Origin", "http://"+config.DefaultAddr)
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
