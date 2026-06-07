package protocol

import "time"

const (
	RoleHost  = "host"
	RoleGuest = "guest"
)

type ParticipantState struct {
	UserID       string  `json:"userId"`
	DisplayName  string  `json:"displayName"`
	CurrentTime  float64 `json:"currentTime"`
	IsPlaying    bool    `json:"isPlaying"`
	IsBuffering  bool    `json:"isBuffering"`
	Duration     float64 `json:"duration"`
	AudioID      string  `json:"aid,omitempty"`
	SubtitleID   string  `json:"sid,omitempty"`
	TimeReliable bool    `json:"timeReliable"`
	SampledAt    int64   `json:"sampledAt"`
	LastUpdated  int64   `json:"lastUpdated"`
	LastSeen     int64   `json:"lastSeen"`
	Connected    bool    `json:"connected"`
}

type ForceSync struct {
	SyncID            string  `json:"syncId"`
	IssuedAt          int64   `json:"issuedAt"`
	IssuedBy          string  `json:"issuedBy"`
	Reason            string  `json:"reason,omitempty"`
	SourceUserID      string  `json:"sourceUserId,omitempty"`
	SourceDisplayName string  `json:"sourceDisplayName,omitempty"`
	ApplyToHost       bool    `json:"applyToHost,omitempty"`
	CurrentTime       float64 `json:"currentTime"`
	IsPlaying         bool    `json:"isPlaying"`
	IsBuffering       bool    `json:"isBuffering"`
	Duration          float64 `json:"duration"`
	SampledAt         int64   `json:"sampledAt"`
}

type TrackSync struct {
	SyncID     string `json:"syncId"`
	IssuedAt   int64  `json:"issuedAt"`
	IssuedBy   string `json:"issuedBy"`
	AudioID    string `json:"aid,omitempty"`
	SubtitleID string `json:"sid,omitempty"`
}

type Permissions struct {
	ControllerID string `json:"controllerId"`
}

type RoomEvent struct {
	EventID string `json:"eventId"`
	Type    string `json:"type"`
	Message string `json:"message"`
	UserID  string `json:"userId,omitempty"`
	Level   string `json:"level,omitempty"`
	At      int64  `json:"at"`
}

type RoomEvents struct {
	Latest *RoomEvent `json:"latest,omitempty"`
}

type PollingSettings struct {
	CommandInterval     float64 `json:"commandInterval"`
	AdaptivePolling     bool    `json:"adaptivePolling"`
	IdleInterval        float64 `json:"idleInterval"`
	ActiveInterval      float64 `json:"activeInterval"`
	ReconnectBackoffMax float64 `json:"reconnectBackoffMax"`
}

type SyncSettings struct {
	SeekLock            bool    `json:"seekLock"`
	SeekLockThreshold   float64 `json:"seekLockThreshold"`
	AutoForceSyncOnSeek bool    `json:"autoForceSyncOnSeek"`
	HostSeekThreshold   float64 `json:"hostSeekThreshold"`
	HostSeekCooldown    float64 `json:"hostSeekCooldown"`
}

type RoomSettings struct {
	Polling PollingSettings `json:"polling"`
	Sync    SyncSettings    `json:"sync"`
}

type Room struct {
	RoomID      string                      `json:"roomId"`
	VideoURL    string                      `json:"videoURL,omitempty"`
	Host        *ParticipantState           `json:"host,omitempty"`
	Guests      map[string]ParticipantState `json:"guests,omitempty"`
	Permissions Permissions                 `json:"permissions"`
	ForceSync   *ForceSync                  `json:"forceSync,omitempty"`
	TrackSync   *TrackSync                  `json:"trackSync,omitempty"`
	Events      RoomEvents                  `json:"events,omitempty"`
	Settings    *RoomSettings               `json:"settings,omitempty"`
	Status      string                      `json:"status"`
	UpdatedAt   int64                       `json:"updatedAt"`
}

type CommandSnapshot struct {
	Role        string            `json:"role"`
	UserID      string            `json:"userId"`
	RoomID      string            `json:"roomId"`
	SyncEnabled bool              `json:"syncEnabled"`
	Host        *ParticipantState `json:"host,omitempty"`
	ForceSync   *ForceSync        `json:"forceSync,omitempty"`
	TrackSync   *TrackSync        `json:"trackSync,omitempty"`
	LatestEvent *RoomEvent        `json:"latestEvent,omitempty"`
	Settings    *RoomSettings     `json:"settings,omitempty"`
	ServerNow   int64             `json:"serverNow"`
}

func NowMillis() int64 {
	return time.Now().UnixMilli()
}
