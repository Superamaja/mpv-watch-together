package server

import (
	"encoding/json"
	"testing"

	"mpv-watch-together/helper/internal/config"
	"mpv-watch-together/helper/internal/firebase"
	"mpv-watch-together/helper/internal/protocol"
)

func TestApplyRoomStreamEventUpdatesNestedFields(t *testing.T) {
	app := &App{
		cfg: config.Config{Role: protocol.RoleHost, RoomID: "room123"},
		room: protocol.Room{
			RoomID: "room123",
			Host: &protocol.ParticipantState{
				UserID:      "host",
				CurrentTime: 10,
				IsPlaying:   true,
			},
			Guests: map[string]protocol.ParticipantState{
				"guest": {UserID: "guest", CurrentTime: 9},
			},
		},
		subscribers: map[chan []byte]struct{}{},
	}

	event := streamEvent(t, "patch", "/host", map[string]any{
		"currentTime": 42,
		"isPlaying":   false,
	})
	if err := app.applyRoomStreamEvent("room123", event); err != nil {
		t.Fatal(err)
	}

	if app.room.Host == nil {
		t.Fatal("host was removed")
	}
	if app.room.Host.CurrentTime != 42 {
		t.Fatalf("host currentTime = %v, want 42", app.room.Host.CurrentTime)
	}
	if app.room.Host.IsPlaying {
		t.Fatal("host isPlaying = true, want false")
	}
	if _, ok := app.room.Guests["guest"]; !ok {
		t.Fatal("existing guest was removed")
	}
}

func TestApplyRoomStreamEventDeletesNestedFields(t *testing.T) {
	app := &App{
		cfg: config.Config{Role: protocol.RoleHost, RoomID: "room123"},
		room: protocol.Room{
			RoomID: "room123",
			Guests: map[string]protocol.ParticipantState{
				"guest": {UserID: "guest"},
			},
		},
		subscribers: map[chan []byte]struct{}{},
	}

	event := streamEvent(t, "put", "/guests/guest", nil)
	if err := app.applyRoomStreamEvent("room123", event); err != nil {
		t.Fatal(err)
	}

	if _, ok := app.room.Guests["guest"]; ok {
		t.Fatal("guest still exists after stream delete")
	}
}

func TestProjectedParticipantTimeUsesElapsedPlayingTime(t *testing.T) {
	projected := projectedParticipantTime(protocol.ParticipantState{
		CurrentTime: 10,
		IsPlaying:   true,
		Duration:    60,
		SampledAt:   1_000,
	}, 3_500)

	if projected != 12.5 {
		t.Fatalf("projected time = %v, want 12.5", projected)
	}
}

func TestProjectedParticipantTimeClampsToDuration(t *testing.T) {
	projected := projectedParticipantTime(protocol.ParticipantState{
		CurrentTime: 58,
		IsPlaying:   true,
		Duration:    60,
		SampledAt:   1_000,
	}, 6_000)

	if projected != 60 {
		t.Fatalf("projected time = %v, want 60", projected)
	}
}

func TestDisplayNameOrIDFallsBackToGuest(t *testing.T) {
	if got := displayNameOrID("", ""); got != "guest" {
		t.Fatalf("display name = %q, want guest", got)
	}
}

func streamEvent(t *testing.T, eventName string, path string, data any) firebase.StreamEvent {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"path": path,
		"data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return firebase.StreamEvent{Event: eventName, Data: payload}
}
