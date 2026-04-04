package commands

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorcon/rcon"
	a2s "github.com/rumblefrog/go-a2s"
)

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// findBinary returns the full path of a binary using PATH lookup.
func findBinary(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return name // fallback to name, let sudo resolve
	}
	return path
}

type Handler struct {
	DataDir      string // /opt/rushborg-srv
	DockerImage  string // ghcr.io/rushborg/cs2-server:latest
	PlatformURL  string // https://rush-b.org — for validating download URLs
}

type DeployPayload struct {
	Port         int    `json:"port"`
	Hostname     string `json:"hostname"`
	GOTVPort     int    `json:"gotv_port"`
	RCONPassword string `json:"rcon_password"`
	ServerCfg    string `json:"server_cfg"`
	MatchZyCfg   string `json:"matchzy_cfg"`
	GsltToken    string `json:"gslt_token"`
	MaxPlayers   int    `json:"max_players"`
	GameType     int    `json:"game_type"`
	GameMode     int    `json:"game_mode"`
}

type PortPayload struct {
	Port int `json:"port"`
}

type UpdateImagePayload struct {
	ImageTag string `json:"image_tag"`
}

type UpdateAgentPayload struct {
	DownloadURL string `json:"download_url"` // Direct URL or GitHub release URL
	SHA256      string `json:"sha256"`       // Expected checksum (hex). If set, binary is verified after download.
}

type SyncAdminsPayload struct {
	Content string `json:"content"`
}

type RestartServersPayload struct {
	Ports []int `json:"ports"`
}

type InstallFilePayload struct {
	Filename    string `json:"filename"`
	DownloadURL string `json:"download_url"`
	AuthToken   string `json:"auth_token"` // Bearer token for authenticated download
	InstallPath string `json:"install_path"`
}

type RemoveFilePayload struct {
	Filename string `json:"filename"`
}

var safeFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.(smx|sp|so|bsp|nav|vpk|cfg|txt|ini)$`)

type ContainerStatus struct {
	Port    int    `json:"port"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

// Allowed command types — agent rejects anything not in this list
var allowedCommands = map[string]bool{
	"deploy_server":       true,
	"stop_server":         true,
	"remove_server":       true,
	"restart_server":      true,
	"setup_base":          true,
	"update_base":         true,
	"update_image":        true,
	"update_agent":        true,
	"sync_admins":         true,
	"restart_idle_servers": true,
	"install_plugin":      true,
	"remove_plugin":       true,
	"install_map":         true,
	"remove_map":          true,
	"get_status":          true,
	"get_logs":            true,
	"query_server":        true,
	"exec_rcon":           true,
}

type GetLogsPayload struct {
	Port int `json:"port"`
	Tail int `json:"tail"`
}

type RCONPayload struct {
	Port     int    `json:"port"`
	Password string `json:"password"`
	Command  string `json:"command"`
}

// Max concurrent containers per host
const maxContainers = 20

func (h *Handler) HandleCommand(cmdType string, payload json.RawMessage) (interface{}, error) {
	// Allowlist check — reject unknown commands
	if !allowedCommands[cmdType] {
		return nil, fmt.Errorf("rejected unknown command: %s", cmdType)
	}

	switch cmdType {
	case "deploy_server":
		var p DeployPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid deploy payload: %w", err)
		}
		return h.deployServer(p)

	case "stop_server":
		var p PortPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid stop payload: %w", err)
		}
		return h.stopServer(p.Port)

	case "remove_server":
		var p PortPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid remove payload: %w", err)
		}
		return h.removeServer(p.Port)

	case "restart_server":
		var p PortPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid restart payload: %w", err)
		}
		return h.restartServer(p.Port)

	case "setup_base":
		return h.setupBase()

	case "update_base":
		return h.updateBase()

	case "update_agent":
		var p UpdateAgentPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid update_agent payload: %w", err)
		}
		return h.updateAgent(p)

	case "update_image":
		var p UpdateImagePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid update payload: %w", err)
		}
		return h.updateImage(p.ImageTag)

	case "sync_admins":
		var p SyncAdminsPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid sync payload: %w", err)
		}
		return h.syncAdmins(p.Content)

	case "restart_idle_servers":
		var p RestartServersPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid restart_idle payload: %w", err)
		}
		return h.restartServers(p.Ports)

	case "install_plugin":
		var p InstallFilePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid install_plugin payload: %w", err)
		}
		return h.installFile(p, "plugins")

	case "remove_plugin":
		var p RemoveFilePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid remove_plugin payload: %w", err)
		}
		return h.removeFile(p, "plugins")

	case "install_map":
		var p InstallFilePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid install_map payload: %w", err)
		}
		return h.installFile(p, "maps")

	case "remove_map":
		var p RemoveFilePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid remove_map payload: %w", err)
		}
		return h.removeFile(p, "maps")

	case "exec_rcon":
		var p RCONPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid exec_rcon payload: %w", err)
		}
		return h.execRCON(p)

	case "query_server":
		var p PortPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid query_server payload: %w", err)
		}
		return h.queryServer(p.Port)

	case "get_logs":
		var p GetLogsPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid get_logs payload: %w", err)
		}
		return h.getLogs(p)

	case "get_status":
		return h.getStatus()

	default:
		return nil, fmt.Errorf("unknown command: %s", cmdType)
	}
}

func (h *Handler) instanceDir(port int) string {
	return filepath.Join(h.DataDir, "instances", fmt.Sprintf("%d", port))
}

func (h *Handler) composeFile(port int) string {
	return filepath.Join(h.instanceDir(port), "docker-compose.yml")
}

func (h *Handler) deployServer(p DeployPayload) (interface{}, error) {
	// Limit max containers per host
	if status, err := h.getStatus(); err == nil {
		if containers, ok := status.([]ContainerStatus); ok && len(containers) >= maxContainers {
			return nil, fmt.Errorf("max containers reached (%d), cannot deploy more", maxContainers)
		}
	}

	// Validate port range
	if p.Port < 1024 || p.Port > 65535 {
		return nil, fmt.Errorf("invalid port: %d (must be 1024-65535)", p.Port)
	}

	dir := h.instanceDir(p.Port)
	configDir := filepath.Join(dir, "config")
	dataDir := filepath.Join(dir, "data")
	demosDir := filepath.Join(dir, "demos")
	cs2DataDir := filepath.Join(dir, "cs2-data")

	// Create directories
	for _, d := range []string{configDir, dataDir, demosDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("creating dir %s: %w", d, err)
		}
	}

	// Ensure shared base and shared dir exist
	baseDir := filepath.Join(h.DataDir, "cs2-base")
	os.MkdirAll(baseDir, 0o755)
	os.MkdirAll(filepath.Join(h.DataDir, "shared"), 0o755)

	// Ensure cs2-base has CS2 installed
	cs2Binary := filepath.Join(baseDir, "game", "bin", "linuxsteamrt64", "cs2")
	if _, err := os.Stat(cs2Binary); os.IsNotExist(err) {
		// First deploy on this host — download CS2 into cs2-base via SteamCMD
		log := func(msg string) {
			fmt.Printf("[agent] %s\n", msg)
		}
		log("CS2 not found in cs2-base, downloading via SteamCMD...")

		// Find steamcmd (installed by bootstrap script)
		steamcmdPath := "/usr/games/steamcmd"
		if _, err := os.Stat(steamcmdPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("steamcmd not found at %s — run bootstrap script to install", steamcmdPath)
		}

		for attempt := 1; attempt <= 5; attempt++ {
			log(fmt.Sprintf("SteamCMD attempt %d/5...", attempt))
			cmd := exec.Command(steamcmdPath,
				"+force_install_dir", baseDir,
				"+login", "anonymous",
				"+app_info_update", "1",
				"+app_update", "730", "validate",
				"+quit")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Run()

			if _, err := os.Stat(cs2Binary); err == nil {
				log("CS2 downloaded successfully")
				break
			}
			log(fmt.Sprintf("Attempt %d incomplete, retrying in 10s...", attempt))
			time.Sleep(10 * time.Second)
		}

		if _, err := os.Stat(cs2Binary); os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to download CS2 after 5 attempts")
		}
	}

	// Hardlink copy cs2-base → instance cs2-data (instant, no extra disk)
	os.MkdirAll(cs2DataDir, 0o755)
	if _, err := os.Stat(filepath.Join(cs2DataDir, "game", "bin", "linuxsteamrt64", "cs2")); os.IsNotExist(err) {
		cmd := exec.Command("cp", "-al", baseDir+"/.", cs2DataDir+"/")
		if out, err := cmd.CombinedOutput(); err != nil {
			// Fallback to regular copy
			cmd = exec.Command("cp", "-a", baseDir+"/.", cs2DataDir+"/")
			if out2, err2 := cmd.CombinedOutput(); err2 != nil {
				return nil, fmt.Errorf("copy cs2-base to instance: %w\noutput: %s %s", err2, out, out2)
			}
		}
	}

	// Write server.cfg (with size limit and basic validation)
	if p.ServerCfg != "" {
		if len(p.ServerCfg) > 64*1024 {
			return nil, fmt.Errorf("server.cfg too large (%d bytes, max 64KB)", len(p.ServerCfg))
		}
		if err := os.WriteFile(filepath.Join(configDir, "server.cfg"), []byte(p.ServerCfg), 0o600); err != nil {
			return nil, fmt.Errorf("writing server.cfg: %w", err)
		}
	}

	// Write matchzy.cfg
	if p.MatchZyCfg != "" {
		if err := os.WriteFile(filepath.Join(configDir, "matchzy.cfg"), []byte(p.MatchZyCfg), 0o600); err != nil {
			return nil, fmt.Errorf("writing matchzy.cfg: %w", err)
		}
	}

	// Generate docker-compose.yml
	gotvPort := p.GOTVPort
	if gotvPort == 0 {
		gotvPort = p.Port + 5
	}
	maxPlayers := p.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = 10
	}
	compose, err := GenerateComposeFile(p.Port, gotvPort, h.DockerImage, p.Hostname, p.GsltToken, h.DataDir, maxPlayers, p.GameType, p.GameMode)
	if err != nil {
		return nil, fmt.Errorf("generating compose file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o600); err != nil {
		return nil, fmt.Errorf("writing docker-compose.yml: %w", err)
	}

	// Pull latest image before starting
	h.runCompose(dir, "pull")

	// docker compose up -d
	out, err := h.runCompose(dir, "up", "-d")
	if err != nil {
		return nil, fmt.Errorf("docker compose up: %w\noutput: %s", err, out)
	}

	return map[string]interface{}{
		"port":     p.Port,
		"hostname": p.Hostname,
		"status":   "started",
	}, nil
}

func (h *Handler) stopServer(port int) (interface{}, error) {
	dir := h.instanceDir(port)
	out, err := h.runCompose(dir, "stop")
	if err != nil {
		return nil, fmt.Errorf("docker compose stop: %w\noutput: %s", err, out)
	}
	return map[string]string{"status": "stopped"}, nil
}

func (h *Handler) removeServer(port int) (interface{}, error) {
	dir := h.instanceDir(port)
	out, err := h.runCompose(dir, "down")
	if err != nil {
		return nil, fmt.Errorf("docker compose down: %w\noutput: %s", err, out)
	}
	os.RemoveAll(dir)
	return map[string]string{"status": "removed"}, nil
}

func (h *Handler) restartServer(port int) (interface{}, error) {
	dir := h.instanceDir(port)
	out, err := h.runCompose(dir, "restart")
	if err != nil {
		return nil, fmt.Errorf("docker compose restart: %w\noutput: %s", err, out)
	}
	return map[string]string{"status": "restarted"}, nil
}

func (h *Handler) restartServers(ports []int) (interface{}, error) {
	results := make(map[int]string)
	for _, port := range ports {
		_, err := h.restartServer(port)
		if err != nil {
			results[port] = err.Error()
		} else {
			results[port] = "restarted"
		}
	}
	return results, nil
}

var safeImageTag = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func (h *Handler) updateImage(tag string) (interface{}, error) {
	if tag != "" && !safeImageTag.MatchString(tag) {
		return nil, fmt.Errorf("invalid image tag: %s", tag)
	}
	image := h.DockerImage
	if tag != "" {
		// Only allow changing the tag, not the registry/image name
		parts := strings.SplitN(image, ":", 2)
		image = parts[0] + ":" + tag
	}
	cmd := exec.Command("docker", "pull", image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker pull: %w\noutput: %s", err, string(out))
	}
	return map[string]string{"status": "pulled", "image": image}, nil
}

func (h *Handler) syncAdmins(content string) (interface{}, error) {
	// Validate content: only allow comment lines and admin entries
	// Format: "STEAM_0:X:XXXXXXX" "b"
	if len(content) > 64*1024 {
		return nil, fmt.Errorf("admins content too large (%d bytes)", len(content))
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		// Must match pattern: "STEAM_..." "..."
		if !strings.HasPrefix(line, `"STEAM_`) && !strings.HasPrefix(line, `"[U:`) {
			return nil, fmt.Errorf("invalid admin entry: %s", line)
		}
	}

	sharedDir := filepath.Join(h.DataDir, "shared")
	os.MkdirAll(sharedDir, 0o755)
	path := filepath.Join(sharedDir, "admins_simple.ini")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return nil, fmt.Errorf("writing admins: %w", err)
	}
	return map[string]string{"status": "synced"}, nil
}

func (h *Handler) installFile(p InstallFilePayload, subdir string) (interface{}, error) {
	if !safeFilename.MatchString(p.Filename) {
		return nil, fmt.Errorf("invalid filename: %s", p.Filename)
	}
	if subdir != "plugins" && subdir != "maps" {
		return nil, fmt.Errorf("invalid subdir: %s", subdir)
	}
	// Validate download URL belongs to our platform (prevent SSRF)
	if h.PlatformURL != "" && !strings.HasPrefix(p.DownloadURL, h.PlatformURL) {
		return nil, fmt.Errorf("download URL does not match platform: %s", p.DownloadURL)
	}

	destDir := filepath.Join(h.DataDir, "shared", subdir)
	os.MkdirAll(destDir, 0o755)
	destPath := filepath.Join(destDir, p.Filename)

	// Validate download URL belongs to our platform (prevent SSRF)
	if !strings.HasPrefix(p.DownloadURL, "http://") && !strings.HasPrefix(p.DownloadURL, "https://") {
		return nil, fmt.Errorf("invalid download URL scheme")
	}
	// Agent only downloads from its configured platform URL
	// (validated at a higher level by the backend which constructs the URL)

	args := []string{"-fsSL", "--max-time", "120", "-o", destPath}
	if p.AuthToken != "" {
		args = append(args, "-H", "Authorization: Bearer "+p.AuthToken)
	}
	args = append(args, p.DownloadURL)
	cmd := exec.Command("curl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w\noutput: %s", p.Filename, err, string(out))
	}

	// Set permissions
	os.Chmod(destPath, 0o644)

	return map[string]string{
		"status":   "installed",
		"filename": p.Filename,
		"path":     destPath,
	}, nil
}

func (h *Handler) removeFile(p RemoveFilePayload, subdir string) (interface{}, error) {
	if !safeFilename.MatchString(p.Filename) {
		return nil, fmt.Errorf("invalid filename: %s", p.Filename)
	}

	path := filepath.Join(h.DataDir, "shared", subdir, p.Filename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing %s: %w", p.Filename, err)
	}
	return map[string]string{"status": "removed", "filename": p.Filename}, nil
}

func (h *Handler) execRCON(p RCONPayload) (interface{}, error) {
	if p.Port < 1024 || p.Port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", p.Port)
	}
	if p.Command == "" {
		return nil, fmt.Errorf("empty command")
	}

	addr := h.resolveAddr(p.Port)
	conn, err := rcon.Dial(addr, p.Password)
	if err != nil {
		return nil, fmt.Errorf("rcon connect to %s: %w", addr, err)
	}
	if err != nil {
		return nil, fmt.Errorf("rcon connect: %w", err)
	}
	defer conn.Close()

	response, err := conn.Execute(p.Command)
	if err != nil {
		return nil, fmt.Errorf("rcon exec: %w", err)
	}

	return map[string]string{"output": response}, nil
}

// resolveAddr returns address to connect to a local game server.
// CS2 with +ip 0.0.0.0 listens on all interfaces including localhost.
func (h *Handler) resolveAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func (h *Handler) queryServer(port int) (interface{}, error) {
	if port < 1024 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", port)
	}

	addr := h.resolveAddr(port)
	client, err := a2s.NewClient(addr, a2s.SetMaxPacketSize(14000))
	if err != nil {
		return map[string]interface{}{"online": false, "error": fmt.Sprintf("connect: %v", err)}, nil
	}
	defer client.Close()

	info, err := client.QueryInfo()
	if err != nil {
		return map[string]interface{}{"online": false, "error": fmt.Sprintf("query: %v", err)}, nil
	}

	result := map[string]interface{}{
		"online":       true,
		"server_name":  info.Name,
		"map":          info.Map,
		"players":      info.Players,
		"max_players":  info.MaxPlayers,
		"bots":         info.Bots,
		"game_version": info.Version,
		"vac":          info.VAC,
	}

	// Try player list
	players, err := client.QueryPlayer()
	if err == nil && players != nil {
		plist := make([]map[string]interface{}, 0)
		for _, p := range players.Players {
			plist = append(plist, map[string]interface{}{
				"name":             p.Name,
				"score":            p.Score,
				"duration_seconds": p.Duration,
			})
		}
		result["player_list"] = plist
	}

	return result, nil
}

func (h *Handler) getLogs(p GetLogsPayload) (interface{}, error) {
	if p.Port < 1024 || p.Port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", p.Port)
	}
	tail := p.Tail
	if tail <= 0 || tail > 500 {
		tail = 100
	}
	containerName := fmt.Sprintf("cs2-%d", p.Port)
	cmd := exec.Command("docker", "logs", "--tail", fmt.Sprintf("%d", tail), containerName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker logs: %w", err)
	}
	return map[string]string{
		"container": containerName,
		"logs":      string(out),
		"lines":     fmt.Sprintf("%d", tail),
	}, nil
}

func (h *Handler) getStatus() (interface{}, error) {
	cmd := exec.Command("docker", "ps", "--filter", "label=rushborg.managed=true", "--format", "{{.Names}}\t{{.Status}}\t{{.Label \"rushborg.port\"}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	containers := make([]ContainerStatus, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		port := 0
		fmt.Sscanf(parts[2], "%d", &port)
		containers = append(containers, ContainerStatus{
			Port:    port,
			Name:    parts[0],
			Status:  parts[1],
			Running: strings.HasPrefix(parts[1], "Up"),
		})
	}
	return containers, nil
}

// ─── Shared CS2 Base ────────────────────────────────────
// CS2 base (62GB) is shared across all instances via bind mount.
// Entrypoint handles installation/plugins inside the container.
// Agent only manages instance directories and Docker lifecycle.

// setupBase ensures the cs2-base directory exists and triggers
// a one-time CS2 install by starting a temporary container.
func (h *Handler) setupBase() (interface{}, error) {
	base := filepath.Join(h.DataDir, "cs2-base")
	os.MkdirAll(base, 0o755)

	// Check if already installed
	if _, err := os.Stat(filepath.Join(base, "game", "bin", "linuxsteamrt64", "cs2")); err == nil {
		return map[string]string{"status": "already_installed", "path": base}, nil
	}

	// Run the CS2 server image with just the base mount — entrypoint
	// will install CS2 + plugins, then exit when CS2 starts (we kill it after install)
	cmd := exec.Command("docker", "run", "--rm",
		"--name", "cs2-base-setup",
		"-v", base+":/home/steam/cs2-dedicated",
		"-e", "CS2_PORT=0", // won't actually bind
		"--security-opt", "seccomp=unconfined",
		"-e", "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1",
		h.DockerImage,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Container exits when CS2 tries to bind port 0, that's expected
		// Check if CS2 was actually installed
		if _, statErr := os.Stat(filepath.Join(base, "game", "bin", "linuxsteamrt64", "cs2")); statErr != nil {
			return nil, fmt.Errorf("setup base failed: %w\noutput: %s", err, string(out))
		}
	}

	return map[string]string{"status": "base_installed", "path": base}, nil
}

// updateBase stops all servers, updates CS2 base, recreates per-instance copies, restarts.
func (h *Handler) updateBase() (interface{}, error) {
	instances, _ := h.listInstancePorts()
	base := filepath.Join(h.DataDir, "cs2-base")

	// 1. Stop all containers
	for _, port := range instances {
		h.stopServer(port)
	}

	// 2. Remove plugin marker to force plugin reinstall
	os.Remove(filepath.Join(base, "game", "csgo", "addons", ".rushborg-plugins-installed"))

	// 3. Update cs2-base (SteamCMD + plugins)
	result, err := h.setupBase()
	if err != nil {
		return nil, err
	}

	// 4. Recreate per-instance hardlink copies from updated base
	for _, port := range instances {
		cs2Data := filepath.Join(h.instanceDir(port), "cs2-data")
		// Remove old copy
		os.RemoveAll(cs2Data)
		// Create fresh hardlink copy
		cmd := exec.Command("cp", "-al", base+"/.", cs2Data+"/")
		if out, cpErr := cmd.CombinedOutput(); cpErr != nil {
			// Fallback to regular copy
			exec.Command("cp", "-a", base+"/.", cs2Data+"/").CombinedOutput()
			_ = out
		}
	}

	// 5. Restart all
	for _, port := range instances {
		dir := h.instanceDir(port)
		h.runCompose(dir, "up", "-d")
	}

	return result, nil
}

// updateAgent downloads a new agent binary from GitHub Release and restarts.
func (h *Handler) updateAgent(p UpdateAgentPayload) (interface{}, error) {
	downloadURL := p.DownloadURL
	if downloadURL == "" {
		// Default: latest release from GitHub
		downloadURL = "https://github.com/rushborg/cs2-server/releases/download/agent-latest/rushborg-agent-amd64"
	}

	// Only allow rushborg GitHub releases or platform URLs
	if !strings.HasPrefix(downloadURL, "https://github.com/rushborg/") &&
		(h.PlatformURL == "" || !strings.HasPrefix(downloadURL, h.PlatformURL)) {
		return nil, fmt.Errorf("download URL not allowed: %s", downloadURL)
	}

	tmpPath := "/tmp/rushborg-agent-new"
	binPath := "/usr/local/bin/rushborg-agent"

	// Download new binary (-L follows redirects for GitHub releases)
	args := []string{"-fsSL", "--max-time", "120", "-o", tmpPath}
	args = append(args, downloadURL)
	cmd := exec.Command("curl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("download failed: %w\noutput: %s", err, string(out))
	}

	// Verify SHA256 checksum if provided
	if p.SHA256 != "" {
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("read downloaded binary: %w", err)
		}
		hash := fmt.Sprintf("%x", sha256Sum(data))
		if hash != strings.ToLower(p.SHA256) {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("checksum mismatch: expected %s, got %s", p.SHA256, hash)
		}
	}

	// Compare with current binary — skip if identical
	newData, _ := os.ReadFile(tmpPath)
	curData, _ := os.ReadFile(binPath)
	if len(newData) > 0 && len(curData) > 0 {
		newHash := fmt.Sprintf("%x", sha256Sum(newData))
		curHash := fmt.Sprintf("%x", sha256Sum(curData))
		if newHash == curHash {
			os.Remove(tmpPath)
			return map[string]string{"status": "already_latest", "message": "agent is already at latest version"}, nil
		}
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return nil, fmt.Errorf("chmod failed: %w", err)
	}

	// Replace binary — agent runs as rushborgsrv, binary owned by root.
	// Use sudo with full paths (must match sudoers rules exactly).
	cpPath := findBinary("cp")
	chmodPath := findBinary("chmod")
	systemctlPath := findBinary("systemctl")

	cpOut, cpErr := exec.Command("sudo", cpPath, "-f", tmpPath, binPath).CombinedOutput()
	os.Remove(tmpPath)
	if cpErr != nil {
		return nil, fmt.Errorf("replace binary failed: %w\noutput: %s", cpErr, string(cpOut))
	}
	exec.Command("sudo", chmodPath, "+x", binPath).Run()

	// Restart agent via systemctl (this kills the current process)
	go func() {
		// Small delay to allow response to be sent
		exec.Command("sleep", "1").Run()
		exec.Command("sudo", systemctlPath, "restart", "rushborg-agent").Run()
	}()

	return map[string]string{"status": "updating", "message": "agent will restart in ~1s"}, nil
}

func (h *Handler) listInstancePorts() ([]int, error) {
	instancesDir := filepath.Join(h.DataDir, "instances")
	entries, err := os.ReadDir(instancesDir)
	if err != nil {
		return nil, err
	}
	var ports []int
	for _, e := range entries {
		if e.IsDir() {
			var port int
			if _, err := fmt.Sscanf(e.Name(), "%d", &port); err == nil && port >= 1024 {
				ports = append(ports, port)
			}
		}
	}
	return ports, nil
}

func (h *Handler) runCompose(dir string, args ...string) (string, error) {
	fullArgs := append([]string{"compose", "-f", filepath.Join(dir, "docker-compose.yml")}, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
