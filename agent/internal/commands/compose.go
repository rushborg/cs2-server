package commands

import (
	"fmt"
	"regexp"
)

var safeHostname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// GenerateComposeFile creates a docker-compose.yml for a CS2 server instance.
func GenerateComposeFile(port, gotvPort int, image, hostname, gsltToken string) (string, error) {
	if port < 1024 || port > 65535 {
		return "", fmt.Errorf("invalid port: %d", port)
	}
	if gotvPort < 1024 || gotvPort > 65535 {
		return "", fmt.Errorf("invalid gotv port: %d", gotvPort)
	}
	if !safeHostname.MatchString(hostname) {
		hostname = fmt.Sprintf("cs2-%d", port) // safe fallback
	}

	gsltEnv := ""
	if gsltToken != "" {
		gsltEnv = fmt.Sprintf("\n      - CS2_GSLT=%s", gsltToken)
	}

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
      - DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1%s
    volumes:
      - cs2-%d-data:/home/steam/cs2-dedicated
      - ./config:/instance/config:ro
      - ../../shared:/shared:ro
      - ../../shared/plugins:/custom/plugins:ro
      - ../../shared/maps:/custom/maps:ro
      - cs2-%d-demos:/demos
    labels:
      - "rushborg.managed=true"
      - "rushborg.port=%d"
      - "rushborg.hostname=%s"

volumes:
  cs2-%d-data:
  cs2-%d-demos:
`, image, port, port, gotvPort, gsltEnv, port, port, port, hostname, port, port), nil
}
