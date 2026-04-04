package commands

import (
	"strings"
	"testing"
)

func TestGenerateComposeFile(t *testing.T) {
	compose, err := GenerateComposeFile(27015, 27020, "ghcr.io/rushborg/cs2-server:latest", "test-server", "GSLT123", "/opt/rushborg-srv", 10, 0, 1)
	if err != nil {
		t.Fatalf("GenerateComposeFile failed: %v", err)
	}

	checks := []struct {
		name string
		want string
	}{
		{"container name", "container_name: cs2-27015"},
		{"game port", "CS2_PORT=27015"},
		{"gotv port", "CS2_GOTV_PORT=27020"},
		{"max players", "CS2_MAXPLAYERS=10"},
		{"game type", "CS2_GAME_TYPE=0"},
		{"game mode", "CS2_GAME_MODE=1"},
		{"gslt env", "CS2_GSLT=GSLT123"},
		{"cs2-data mount", "/opt/rushborg-srv/instances/27015/cs2-data:/home/steam/cs2-dedicated"},
		{"config mount", "/opt/rushborg-srv/instances/27015/config:/instance/config:ro"},
		{"data mount", "/opt/rushborg-srv/instances/27015/data:/instance/data"},
		{"shared mount", "/opt/rushborg-srv/shared:/shared:ro"},
		{"plugins mount", "/opt/rushborg-srv/shared/plugins:/custom/plugins:ro"},
		{"maps mount", "/opt/rushborg-srv/shared/maps:/custom/maps:ro"},
		{"demos mount", "/opt/rushborg-srv/instances/27015/demos:/demos"},
		{"network host", "network_mode: host"},
		{"seccomp", "seccomp:unconfined"},
		{"label port", "rushborg.port=27015"},
	}

	for _, c := range checks {
		if !strings.Contains(compose, c.want) {
			t.Errorf("[%s] missing: %s", c.name, c.want)
		}
	}
}

func TestGenerateComposeNoGSLT(t *testing.T) {
	compose, err := GenerateComposeFile(27015, 27020, "test:latest", "server", "", "/data", 10, 0, 1)
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if strings.Contains(compose, "CS2_GSLT") {
		t.Error("GSLT env should not be present when token is empty")
	}
}

func TestGenerateComposeInvalidPort(t *testing.T) {
	_, err := GenerateComposeFile(80, 27020, "test:latest", "server", "", "/data", 10, 0, 1)
	if err == nil {
		t.Error("should reject port 80")
	}
}

func TestGenerateComposeSafeHostname(t *testing.T) {
	compose, err := GenerateComposeFile(27015, 27020, "test:latest", "unsafe hostname!!!", "", "/data", 10, 0, 1)
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	// Should fallback to cs2-27015
	if !strings.Contains(compose, "rushborg.hostname=cs2-27015") {
		t.Error("unsafe hostname should fallback to cs2-PORT")
	}
}
