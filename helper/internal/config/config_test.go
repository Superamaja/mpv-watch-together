package config

import (
	"testing"

	"mpv-watch-together/helper/internal/protocol"
)

func TestLoadUsesFixedBundleRole(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MPV_WATCH_ROLE", protocol.RoleGuest)

	cfg, err := Load(nil, "https://example.firebaseio.com", protocol.RoleHost)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != protocol.RoleHost {
		t.Fatalf("role = %q, want fixed bundle role %q", cfg.Role, protocol.RoleHost)
	}
}

func TestLoadAllowsRoleFlagForDevelopmentBuild(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MPV_WATCH_ROLE", "")

	cfg, err := Load([]string{"-role", protocol.RoleHost}, "https://example.firebaseio.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != protocol.RoleHost {
		t.Fatalf("role = %q, want development override %q", cfg.Role, protocol.RoleHost)
	}
}
