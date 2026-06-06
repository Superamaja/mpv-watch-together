package config

import (
	"bufio"
	"errors"
	"flag"
	"os"
	"strings"
)

type Config struct {
	Addr                string
	Role                string
	RoomID              string
	DisplayName         string
	UserID              string
	FirebaseDatabaseURL string
}

func Load(args []string) (Config, error) {
	_ = loadDotEnv(".env")

	cfg := Config{
		Addr:                envOrDefault("MPV_WATCH_ADDR", "127.0.0.1:8765"),
		Role:                envOrDefault("MPV_WATCH_ROLE", "guest"),
		RoomID:              os.Getenv("MPV_WATCH_ROOM"),
		DisplayName:         envOrDefault("MPV_WATCH_DISPLAY_NAME", "mpv watcher"),
		UserID:              envOrDefault("MPV_WATCH_USER_ID", randomishID()),
		FirebaseDatabaseURL: os.Getenv("FIREBASE_DATABASE_URL"),
	}

	fs := flag.NewFlagSet("mpv-watch-helper", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "local HTTP listen address")
	fs.StringVar(&cfg.Role, "role", cfg.Role, "client role: host or guest")
	fs.StringVar(&cfg.RoomID, "room", cfg.RoomID, "watch room ID")
	fs.StringVar(&cfg.DisplayName, "name", cfg.DisplayName, "display name")
	fs.StringVar(&cfg.UserID, "user-id", cfg.UserID, "stable local user ID")
	fs.StringVar(&cfg.FirebaseDatabaseURL, "firebase-url", cfg.FirebaseDatabaseURL, "Firebase Realtime Database URL")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	cfg.Role = strings.ToLower(strings.TrimSpace(cfg.Role))
	cfg.RoomID = strings.TrimSpace(cfg.RoomID)
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.UserID = strings.TrimSpace(cfg.UserID)
	cfg.FirebaseDatabaseURL = strings.TrimRight(strings.TrimSpace(cfg.FirebaseDatabaseURL), "/")

	if cfg.Role != "host" && cfg.Role != "guest" {
		return Config{}, errors.New("role must be host or guest")
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "mpv watcher"
	}
	if cfg.UserID == "" {
		cfg.UserID = randomishID()
	}

	return cfg, nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func randomishID() string {
	host, _ := os.Hostname()
	host = strings.NewReplacer(" ", "-", ".", "-").Replace(strings.ToLower(host))
	if host == "" {
		host = "local"
	}
	return "mpv_" + host
}
