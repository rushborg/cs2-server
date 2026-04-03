package commands

import (
	"fmt"
	"regexp"
)

var safeHostname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// GenerateComposeFile creates a docker-compose.yml for a CS2 server instance.
func GenerateComposeFile(port, gotvPort int, image, hostname, gsltToken, dataDir string, maxPlayers, gameType, gameMode int) (string, error) {
	if port < 1024 || port > 65535 {
		return "", fmt.Errorf("invalid port: %d", port)
	}
	if gotvPort < 1024 || gotvPort > 65535 {
		return "", fmt.Errorf("invalid gotv port: %d", gotvPort)
	}
	if !safeHostname.MatchString(hostname) {
		hostname = fmt.Sprintf("cs2-%d", port)
	}
	if maxPlayers <= 0 {
		maxPlayers = 10
	}

	gsltEnv := ""
	if gsltToken != "" {
		gsltEnv = fmt.Sprintf("\n      - CS2_GSLT=%s", gsltToken)
	}

	baseDir := fmt.Sprintf("%s/cs2-base", dataDir)
	instDir := fmt.Sprintf("%s/instances/%d", dataDir, port)
	sharedDir := fmt.Sprintf("%s/shared", dataDir)

	return fmt.Sprintf(`services:
  cs2:
    image: %s
    container_name: cs2-%d
    network_mode: host
    restart: unless-stopped
    security_opt:
      - seccomp:unconfined
    environment:
      - CS2_PORT=%d
      - CS2_GOTV_PORT=%d
      - CS2_MAP=de_mirage
      - CS2_MAXPLAYERS=%d
      - CS2_GAME_TYPE=%d
      - CS2_GAME_MODE=%d
      - DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1%s
    volumes:
      - %s:/home/steam/cs2-dedicated
      - %s/config:/instance/config:ro
      - %s/data:/instance/data
      - %s:/shared:ro
      - %s/plugins:/custom/plugins:ro
      - %s/maps:/custom/maps:ro
      - %s/demos:/demos
    labels:
      - "rushborg.managed=true"
      - "rushborg.port=%d"
      - "rushborg.hostname=%s"
`, image, port, port, gotvPort, maxPlayers, gameType, gameMode, gsltEnv,
		baseDir, instDir, instDir, sharedDir, sharedDir, sharedDir, instDir,
		port, hostname), nil
}
