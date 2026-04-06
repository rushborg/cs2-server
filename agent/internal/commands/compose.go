package commands

import (
	"fmt"
	"regexp"
)

var safeHostname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// GenerateComposeFile creates a docker-compose.yml for a CS2 server instance.
func GenerateComposeFile(port, gotvPort int, image, hostname, gsltToken, rconPassword, dataDir string, maxPlayers, gameType, gameMode int) (string, error) {
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
	rconEnv := ""
	if rconPassword != "" {
		rconEnv = fmt.Sprintf("\n      - CS2_RCON_PASSWORD=%s", rconPassword)
	}

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
      - CS2_STEAM_PORT=%d
      - CS2_GOTV_PORT=%d
      - CS2_MAP=de_mirage
      - CS2_MAXPLAYERS=%d
      - CS2_GAME_TYPE=%d
      - CS2_GAME_MODE=%d
      - DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1%s%s
    volumes:
      - %s/cs2-data:/home/steam/cs2-dedicated
      - %s/config:/instance/config:ro
      - %s/data:/instance/data
      - %s:/shared:ro
      - %s/plugins:/custom/plugins:ro
      - %s/maps:/custom/maps:ro
      - %s/demos:/demos
      - /etc/localtime:/etc/localtime:ro
    labels:
      - "rushborg.managed=true"
      - "rushborg.port=%d"
      - "rushborg.hostname=%s"
`, image, port, port, port-10, gotvPort, maxPlayers, gameType, gameMode, gsltEnv, rconEnv,
		instDir, instDir, instDir, sharedDir, sharedDir, sharedDir, instDir,
		port, hostname), nil
}
