package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"mpv-watch-together/helper/internal/protocol"
)

const (
	minCommandInterval     = 0.25
	maxCommandInterval     = 3.0
	stepCommandInterval    = 0.05
	minActiveInterval      = 0.25
	maxActiveInterval      = 2.0
	stepActiveInterval     = 0.05
	minIdleInterval        = 0.5
	maxIdleInterval        = 5.0
	stepIdleInterval       = 0.05
	minReconnectBackoffMax = 1.0
	maxReconnectBackoffMax = 15.0
	stepReconnectBackoff   = 0.25
	minSeekLockThreshold   = 0.5
	maxSeekLockThreshold   = 10.0
	stepSeekLockThreshold  = 0.1
	minHostSeekThreshold   = 0.5
	maxHostSeekThreshold   = 10.0
	stepHostSeekThreshold  = 0.1
	minHostSeekCooldown    = 0.25
	maxHostSeekCooldown    = 10.0
	stepHostSeekCooldown   = 0.25
)

type roomSettingsRequest struct {
	Polling pollingSettingsRequest `json:"polling"`
	Sync    syncSettingsRequest    `json:"sync"`
}

type pollingSettingsRequest struct {
	CommandInterval     *float64 `json:"commandInterval"`
	AdaptivePolling     *bool    `json:"adaptivePolling"`
	IdleInterval        *float64 `json:"idleInterval"`
	ActiveInterval      *float64 `json:"activeInterval"`
	ReconnectBackoffMax *float64 `json:"reconnectBackoffMax"`
}

type syncSettingsRequest struct {
	SeekLock            *bool    `json:"seekLock"`
	SeekLockThreshold   *float64 `json:"seekLockThreshold"`
	AutoForceSyncOnSeek *bool    `json:"autoForceSyncOnSeek"`
	HostSeekThreshold   *float64 `json:"hostSeekThreshold"`
	HostSeekCooldown    *float64 `json:"hostSeekCooldown"`
}

type roomSettingsConstraints struct {
	Polling pollingSettingsConstraints `json:"polling"`
	Sync    syncSettingsConstraints    `json:"sync"`
}

type pollingSettingsConstraints struct {
	CommandInterval     numberSettingConstraint `json:"commandInterval"`
	IdleInterval        numberSettingConstraint `json:"idleInterval"`
	ActiveInterval      numberSettingConstraint `json:"activeInterval"`
	ReconnectBackoffMax numberSettingConstraint `json:"reconnectBackoffMax"`
}

type syncSettingsConstraints struct {
	SeekLockThreshold numberSettingConstraint `json:"seekLockThreshold"`
	HostSeekThreshold numberSettingConstraint `json:"hostSeekThreshold"`
	HostSeekCooldown  numberSettingConstraint `json:"hostSeekCooldown"`
}

type numberSettingConstraint struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Step float64 `json:"step"`
}

func defaultRoomSettings() protocol.RoomSettings {
	return protocol.RoomSettings{
		Polling: protocol.PollingSettings{
			CommandInterval:     0.5,
			AdaptivePolling:     false,
			IdleInterval:        1.25,
			ActiveInterval:      0.35,
			ReconnectBackoffMax: 8.0,
		},
		Sync: protocol.SyncSettings{
			SeekLock:            true,
			SeekLockThreshold:   3.0,
			AutoForceSyncOnSeek: true,
			HostSeekThreshold:   2.5,
			HostSeekCooldown:    1.5,
		},
	}
}

func defaultRoomSettingsConstraints() roomSettingsConstraints {
	return roomSettingsConstraints{
		Polling: pollingSettingsConstraints{
			CommandInterval:     numberSettingConstraint{Min: minCommandInterval, Max: maxCommandInterval, Step: stepCommandInterval},
			IdleInterval:        numberSettingConstraint{Min: minIdleInterval, Max: maxIdleInterval, Step: stepIdleInterval},
			ActiveInterval:      numberSettingConstraint{Min: minActiveInterval, Max: maxActiveInterval, Step: stepActiveInterval},
			ReconnectBackoffMax: numberSettingConstraint{Min: minReconnectBackoffMax, Max: maxReconnectBackoffMax, Step: stepReconnectBackoff},
		},
		Sync: syncSettingsConstraints{
			SeekLockThreshold: numberSettingConstraint{Min: minSeekLockThreshold, Max: maxSeekLockThreshold, Step: stepSeekLockThreshold},
			HostSeekThreshold: numberSettingConstraint{Min: minHostSeekThreshold, Max: maxHostSeekThreshold, Step: stepHostSeekThreshold},
			HostSeekCooldown:  numberSettingConstraint{Min: minHostSeekCooldown, Max: maxHostSeekCooldown, Step: stepHostSeekCooldown},
		},
	}
}

func effectiveRoomSettings(settings *protocol.RoomSettings) protocol.RoomSettings {
	effective := defaultRoomSettings()
	if settings == nil {
		return effective
	}
	effective.Polling.CommandInterval = positiveOrDefault(settings.Polling.CommandInterval, effective.Polling.CommandInterval)
	effective.Polling.AdaptivePolling = settings.Polling.AdaptivePolling
	effective.Polling.IdleInterval = positiveOrDefault(settings.Polling.IdleInterval, effective.Polling.IdleInterval)
	effective.Polling.ActiveInterval = positiveOrDefault(settings.Polling.ActiveInterval, effective.Polling.ActiveInterval)
	effective.Polling.ReconnectBackoffMax = positiveOrDefault(settings.Polling.ReconnectBackoffMax, effective.Polling.ReconnectBackoffMax)
	effective.Sync.SeekLock = settings.Sync.SeekLock
	effective.Sync.SeekLockThreshold = positiveOrDefault(settings.Sync.SeekLockThreshold, effective.Sync.SeekLockThreshold)
	effective.Sync.AutoForceSyncOnSeek = settings.Sync.AutoForceSyncOnSeek
	effective.Sync.HostSeekThreshold = positiveOrDefault(settings.Sync.HostSeekThreshold, effective.Sync.HostSeekThreshold)
	effective.Sync.HostSeekCooldown = positiveOrDefault(settings.Sync.HostSeekCooldown, effective.Sync.HostSeekCooldown)
	return clampRoomSettings(effective)
}

func applyRoomSettingsRequest(current protocol.RoomSettings, req roomSettingsRequest) protocol.RoomSettings {
	next := current
	if req.Polling.CommandInterval != nil {
		next.Polling.CommandInterval = *req.Polling.CommandInterval
	}
	if req.Polling.AdaptivePolling != nil {
		next.Polling.AdaptivePolling = *req.Polling.AdaptivePolling
	}
	if req.Polling.IdleInterval != nil {
		next.Polling.IdleInterval = *req.Polling.IdleInterval
	}
	if req.Polling.ActiveInterval != nil {
		next.Polling.ActiveInterval = *req.Polling.ActiveInterval
	}
	if req.Polling.ReconnectBackoffMax != nil {
		next.Polling.ReconnectBackoffMax = *req.Polling.ReconnectBackoffMax
	}
	if req.Sync.SeekLock != nil {
		next.Sync.SeekLock = *req.Sync.SeekLock
	}
	if req.Sync.SeekLockThreshold != nil {
		next.Sync.SeekLockThreshold = *req.Sync.SeekLockThreshold
	}
	if req.Sync.AutoForceSyncOnSeek != nil {
		next.Sync.AutoForceSyncOnSeek = *req.Sync.AutoForceSyncOnSeek
	}
	if req.Sync.HostSeekThreshold != nil {
		next.Sync.HostSeekThreshold = *req.Sync.HostSeekThreshold
	}
	if req.Sync.HostSeekCooldown != nil {
		next.Sync.HostSeekCooldown = *req.Sync.HostSeekCooldown
	}
	return clampRoomSettings(next)
}

func clampRoomSettings(settings protocol.RoomSettings) protocol.RoomSettings {
	settings.Polling.CommandInterval = clamp(settings.Polling.CommandInterval, minCommandInterval, maxCommandInterval)
	settings.Polling.IdleInterval = clamp(settings.Polling.IdleInterval, minIdleInterval, maxIdleInterval)
	settings.Polling.ActiveInterval = clamp(settings.Polling.ActiveInterval, minActiveInterval, maxActiveInterval)
	settings.Polling.ReconnectBackoffMax = clamp(settings.Polling.ReconnectBackoffMax, minReconnectBackoffMax, maxReconnectBackoffMax)
	settings.Sync.SeekLockThreshold = clamp(settings.Sync.SeekLockThreshold, minSeekLockThreshold, maxSeekLockThreshold)
	settings.Sync.HostSeekThreshold = clamp(settings.Sync.HostSeekThreshold, minHostSeekThreshold, maxHostSeekThreshold)
	settings.Sync.HostSeekCooldown = clamp(settings.Sync.HostSeekCooldown, minHostSeekCooldown, maxHostSeekCooldown)
	return settings
}

func positiveOrDefault(value float64, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func clamp(value float64, min float64, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (a *App) handlePostHostSettings(w http.ResponseWriter, r *http.Request) {
	var req roomSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	a.mu.RLock()
	cfg := a.cfg
	current := effectiveRoomSettings(a.room.Settings)
	a.mu.RUnlock()
	if cfg.Role != protocol.RoleHost {
		writeJSON(w, http.StatusForbidden, apiError{Error: "settings are only available for host role"})
		return
	}
	if cfg.RoomID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "room is required"})
		return
	}

	settings := applyRoomSettingsRequest(current, req)
	now := a.serverNow()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := a.firebase.Patch(ctx, roomPath(cfg.RoomID), map[string]any{
		"settings":  settings,
		"updatedAt": now,
	}, nil); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}

	a.mu.Lock()
	a.room.Settings = &settings
	a.room.UpdatedAt = now
	a.mu.Unlock()
	a.publishState()

	slog.Info("updated room settings", "room", cfg.RoomID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"settings": settings,
	})
}

func (a *App) handleDeleteHostSettings(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if cfg.Role != protocol.RoleHost {
		writeJSON(w, http.StatusForbidden, apiError{Error: "settings are only available for host role"})
		return
	}
	if cfg.RoomID == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "room is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := a.firebase.Delete(ctx, roomPath(cfg.RoomID)+"/settings"); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}

	a.mu.Lock()
	a.room.Settings = nil
	a.mu.Unlock()
	a.publishState()

	defaults := defaultRoomSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"settings": defaults,
	})
}
