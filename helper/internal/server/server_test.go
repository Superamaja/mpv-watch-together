package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestCloseRemovesSyncedGuestParticipant(t *testing.T) {
	fb, recorder := newRecordingFirebase(t)
	app := &App{
		cfg: config.Config{
			Role:   protocol.RoleGuest,
			RoomID: "room123",
			UserID: "guest",
		},
		firebase:    fb,
		syncEnabled: true,
		subscribers: map[chan []byte]struct{}{},
	}

	app.Close()

	if _, ok := recorder.find(http.MethodDelete, "/rooms/room123/guests/guest.json"); !ok {
		t.Fatalf("guest participant was not removed; requests = %+v", recorder.snapshot())
	}
}

func TestCloseRemovesSyncedHostParticipant(t *testing.T) {
	fb, recorder := newRecordingFirebase(t)
	app := &App{
		cfg: config.Config{
			Role:   protocol.RoleHost,
			RoomID: "room123",
			UserID: "host",
		},
		firebase:    fb,
		syncEnabled: true,
		subscribers: map[chan []byte]struct{}{},
	}

	app.Close()

	if _, ok := recorder.find(http.MethodDelete, "/rooms/room123/host.json"); !ok {
		t.Fatalf("host participant was not removed; requests = %+v", recorder.snapshot())
	}
	req, ok := recorder.find(http.MethodPatch, "/rooms/room123.json")
	if !ok {
		t.Fatalf("room was not marked inactive; requests = %+v", recorder.snapshot())
	}
	var patch map[string]any
	if err := json.Unmarshal(req.Body, &patch); err != nil {
		t.Fatal(err)
	}
	if patch["status"] != "inactive" {
		t.Fatalf("room status = %v, want inactive", patch["status"])
	}
	if _, ok := patch["updatedAt"]; !ok {
		t.Fatal("room inactive patch is missing updatedAt")
	}
}

func TestGetMPVCommandsDisablesGuestSyncWhenHostIsStale(t *testing.T) {
	fb, recorder := newRecordingFirebase(t)
	app := &App{
		cfg: config.Config{
			Role:   protocol.RoleGuest,
			RoomID: "room123",
			UserID: "guest",
		},
		firebase:    fb,
		syncEnabled: true,
		room: protocol.Room{
			Host: &protocol.ParticipantState{
				UserID:   "host",
				LastSeen: 1,
			},
		},
		subscribers: map[chan []byte]struct{}{},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/mpv/commands", nil)
	app.handleGetMPVCommands(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var snapshot protocol.CommandSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SyncEnabled {
		t.Fatal("command snapshot still has sync enabled")
	}
	if app.syncEnabled {
		t.Fatal("app syncEnabled still true")
	}
	if _, ok := recorder.find(http.MethodDelete, "/rooms/room123/guests/guest.json"); !ok {
		t.Fatalf("stale host did not trigger guest cleanup; requests = %+v", recorder.snapshot())
	}
}

func TestCoordinatorTickDisablesHostSyncWhenMPVStopsReporting(t *testing.T) {
	fb, recorder := newRecordingFirebase(t)
	app := &App{
		cfg: config.Config{
			Role:        protocol.RoleHost,
			RoomID:      "room123",
			UserID:      "host",
			DisplayName: "Host",
		},
		firebase:    fb,
		syncEnabled: true,
		lastLocal: protocol.ParticipantState{
			UserID:   "host",
			LastSeen: 1,
		},
		appCtx:              context.Background(),
		subscribers:         map[chan []byte]struct{}{},
		guestBufferingSince: map[string]int64{},
	}

	app.runCoordinatorTick()

	if app.syncEnabled {
		t.Fatal("host syncEnabled still true")
	}
	if _, ok := recorder.find(http.MethodDelete, "/rooms/room123/host.json"); !ok {
		t.Fatalf("stale host did not remove host participant; requests = %+v", recorder.snapshot())
	}
	req, ok := recorder.find(http.MethodPatch, "/rooms/room123.json")
	if !ok {
		t.Fatalf("stale host did not mark room inactive; requests = %+v", recorder.snapshot())
	}
	var patch map[string]any
	if err := json.Unmarshal(req.Body, &patch); err != nil {
		t.Fatal(err)
	}
	if patch["status"] != "inactive" {
		t.Fatalf("room status = %v, want inactive", patch["status"])
	}
}

func TestPostSyncSeedsHostPresenceGrace(t *testing.T) {
	fb, recorder := newRecordingFirebase(t)
	app := &App{
		cfg: config.Config{
			Role:        protocol.RoleHost,
			RoomID:      "room123",
			UserID:      "host",
			DisplayName: "Host",
		},
		firebase:            fb,
		subscribers:         map[chan []byte]struct{}{},
		guestBufferingSince: map[string]int64{},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(`{"enabled":true}`))
	app.handlePostSync(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	app.runCoordinatorTick()

	if !app.syncEnabled {
		t.Fatal("host sync was disabled before presence grace elapsed")
	}
	if _, ok := recorder.find(http.MethodDelete, "/rooms/room123/host.json"); ok {
		t.Fatalf("host participant was removed before presence grace elapsed; requests = %+v", recorder.snapshot())
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

type recordedFirebaseRequest struct {
	Method string
	Path   string
	Body   []byte
}

type firebaseRecorder struct {
	mu       sync.Mutex
	requests []recordedFirebaseRequest
}

func newRecordingFirebase(t *testing.T) (*firebase.Client, *firebaseRecorder) {
	t.Helper()
	recorder := &firebaseRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		recorder.add(recordedFirebaseRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)

	client, err := firebase.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client, recorder
}

func (r *firebaseRecorder) add(request recordedFirebaseRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

func (r *firebaseRecorder) find(method string, path string) (recordedFirebaseRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, request := range r.requests {
		if request.Method == method && request.Path == path {
			return request, true
		}
	}
	return recordedFirebaseRequest{}, false
}

func (r *firebaseRecorder) snapshot() []recordedFirebaseRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedFirebaseRequest(nil), r.requests...)
}
