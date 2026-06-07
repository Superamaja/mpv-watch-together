package server

import "testing"

func TestApplyRoomSettingsRequestClampsValues(t *testing.T) {
	commandInterval := 10.0
	activeInterval := 0.1
	idleInterval := 10.0
	reconnectBackoffMax := 30.0
	seekLockThreshold := 0.1
	hostSeekThreshold := 30.0
	hostSeekCooldown := 0.1

	settings := applyRoomSettingsRequest(defaultRoomSettings(), roomSettingsRequest{
		Polling: pollingSettingsRequest{
			CommandInterval:     &commandInterval,
			ActiveInterval:      &activeInterval,
			IdleInterval:        &idleInterval,
			ReconnectBackoffMax: &reconnectBackoffMax,
		},
		Sync: syncSettingsRequest{
			SeekLockThreshold: &seekLockThreshold,
			HostSeekThreshold: &hostSeekThreshold,
			HostSeekCooldown:  &hostSeekCooldown,
		},
	})

	if settings.Polling.CommandInterval != maxCommandInterval {
		t.Fatalf("command interval = %v, want %v", settings.Polling.CommandInterval, maxCommandInterval)
	}
	if settings.Polling.ActiveInterval != minActiveInterval {
		t.Fatalf("active interval = %v, want %v", settings.Polling.ActiveInterval, minActiveInterval)
	}
	if settings.Polling.IdleInterval != maxIdleInterval {
		t.Fatalf("idle interval = %v, want %v", settings.Polling.IdleInterval, maxIdleInterval)
	}
	if settings.Polling.ReconnectBackoffMax != maxReconnectBackoffMax {
		t.Fatalf("reconnect backoff = %v, want %v", settings.Polling.ReconnectBackoffMax, maxReconnectBackoffMax)
	}
	if settings.Sync.SeekLockThreshold != minSeekLockThreshold {
		t.Fatalf("seek lock threshold = %v, want %v", settings.Sync.SeekLockThreshold, minSeekLockThreshold)
	}
	if settings.Sync.HostSeekThreshold != maxHostSeekThreshold {
		t.Fatalf("host seek threshold = %v, want %v", settings.Sync.HostSeekThreshold, maxHostSeekThreshold)
	}
	if settings.Sync.HostSeekCooldown != minHostSeekCooldown {
		t.Fatalf("host seek cooldown = %v, want %v", settings.Sync.HostSeekCooldown, minHostSeekCooldown)
	}
}

func TestApplyRoomSettingsRequestPreservesUnspecifiedFields(t *testing.T) {
	adaptivePolling := true
	seekLock := false
	current := defaultRoomSettings()
	settings := applyRoomSettingsRequest(current, roomSettingsRequest{
		Polling: pollingSettingsRequest{AdaptivePolling: &adaptivePolling},
		Sync:    syncSettingsRequest{SeekLock: &seekLock},
	})

	if !settings.Polling.AdaptivePolling {
		t.Fatal("adaptive polling = false, want true")
	}
	if settings.Sync.SeekLock {
		t.Fatal("seek lock = true, want false")
	}
	if settings.Polling.CommandInterval != current.Polling.CommandInterval {
		t.Fatalf("command interval = %v, want %v", settings.Polling.CommandInterval, current.Polling.CommandInterval)
	}
	if settings.Sync.AutoForceSyncOnSeek != current.Sync.AutoForceSyncOnSeek {
		t.Fatalf("auto force sync = %v, want %v", settings.Sync.AutoForceSyncOnSeek, current.Sync.AutoForceSyncOnSeek)
	}
}

func TestEffectiveRoomSettingsUsesDefaultsForNilSettings(t *testing.T) {
	got := effectiveRoomSettings(nil)
	want := defaultRoomSettings()

	if got != want {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
}
