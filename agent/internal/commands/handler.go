package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gorcon/rcon"
	a2s "github.com/rumblefrog/go-a2s"
)

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
}

type PortPayload struct {
	Port int `json:"port"`
}

type UpdateImagePayload struct {
	ImageTag string `json:"image_tag"`
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

	// Ensure CS2 base is installed
	if !h.isBaseInstalled() {
		if _, err := h.setupBase(); err != nil {
			return nil, fmt.Errorf("setup base: %w", err)
		}
	}

	dir := h.instanceDir(p.Port)
	configDir := filepath.Join(dir, "config")
	demosDir := filepath.Join(dir, "demos")

	// Create directories
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating instance dir: %w", err)
	}
	os.MkdirAll(demosDir, 0o755)

	// Ensure shared dir exists
	sharedDir := filepath.Join(h.DataDir, "shared")
	os.MkdirAll(sharedDir, 0o755)

	// Mount overlayfs for this instance
	if err := h.mountOverlay(p.Port); err != nil {
		return nil, fmt.Errorf("mount overlay: %w", err)
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
	compose, err := GenerateComposeFile(p.Port, gotvPort, h.DockerImage, p.Hostname, p.GsltToken, h.DataDir)
	if err != nil {
		return nil, fmt.Errorf("generating compose file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o600); err != nil {
		return nil, fmt.Errorf("writing docker-compose.yml: %w", err)
	}

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
	// Stop and remove container (no -v since we use bind mounts now)
	out, err := h.runCompose(dir, "down")
	if err != nil {
		return nil, fmt.Errorf("docker compose down: %w\noutput: %s", err, out)
	}
	// Unmount overlay before removing directory
	h.unmountOverlay(port)
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

	addr := fmt.Sprintf("127.0.0.1:%d", p.Port)
	conn, err := rcon.Dial(addr, p.Password)
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

func (h *Handler) queryServer(port int) (interface{}, error) {
	if port < 1024 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", port)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
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

// ─── Shared CS2 Base + OverlayFS ────────────────────────

func (h *Handler) baseDir() string {
	return filepath.Join(h.DataDir, "cs2-base")
}

func (h *Handler) isBaseInstalled() bool {
	_, err := os.Stat(filepath.Join(h.baseDir(), "game", "bin", "linuxsteamrt64", "cs2"))
	return err == nil
}

// setupBase installs CS2 + plugins into a shared base directory.
// Run once per host; subsequent servers reuse this base via overlayfs.
func (h *Handler) setupBase() (interface{}, error) {
	base := h.baseDir()
	os.MkdirAll(base, 0o755)

	// 1. Install/update CS2 via SteamCMD Docker container
	steamcmdArgs := []string{
		"run", "--rm",
		"-v", base + ":/home/steam/cs2-dedicated",
		"cm2network/steamcmd:root",
		"/home/steam/steamcmd/steamcmd.sh",
		"+force_install_dir", "/home/steam/cs2-dedicated",
		"+login", "anonymous",
		"+app_update", "730", "validate",
		"+quit",
	}
	cmd := exec.Command("docker", steamcmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// SteamCMD often exits non-zero on first run (self-update), retry
		cmd = exec.Command("docker", steamcmdArgs...)
		out, err = cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("steamcmd install: %w\noutput: %s", err, string(out))
		}
	}

	csgoDir := filepath.Join(base, "game", "csgo")

	// 2. Fix steamclient.so
	steamclientSrc := filepath.Join(base, "..", "steamcmd", "linux64", "steamclient.so")
	steamclientDst := filepath.Join(base, "game", "bin", "linuxsteamrt64", "steamclient.so")
	// Try to copy from a SteamCMD installation if available
	exec.Command("docker", "run", "--rm",
		"-v", base+":/cs2",
		"cm2network/steamcmd:root",
		"cp", "/home/steam/steamcmd/linux64/steamclient.so", "/cs2/game/bin/linuxsteamrt64/steamclient.so",
	).Run()
	_ = steamclientSrc
	_ = steamclientDst

	// 3. Install MetaMod
	metamodURL := "https://mms.alliedmods.net/mmsdrop/2.0/mmsource-2.0.0-git1390-linux.tar.gz"
	exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL '%s' | tar xz -C '%s/'", metamodURL, csgoDir)).Run()

	// 4. Install CounterStrikeSharp
	cssharpURL := "https://github.com/roflmuffin/CounterStrikeSharp/releases/download/v1.0.364/counterstrikesharp-with-runtime-linux-1.0.364.zip"
	exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL -o /tmp/cssharp.zip '%s' && cd '%s' && unzip -o /tmp/cssharp.zip && rm /tmp/cssharp.zip", cssharpURL, csgoDir)).Run()

	// 5. Install MatchZy
	matchzyURL := "https://github.com/shobhit-pathak/MatchZy/releases/download/0.8.15/MatchZy-0.8.15.zip"
	exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL -o /tmp/matchzy.zip '%s' && cd '%s' && unzip -o /tmp/matchzy.zip && rm /tmp/matchzy.zip", matchzyURL, csgoDir)).Run()

	// 6. Patch gameinfo.gi for MetaMod
	gameinfoPath := filepath.Join(csgoDir, "gameinfo.gi")
	if data, err := os.ReadFile(gameinfoPath); err == nil {
		if !strings.Contains(string(data), "metamod") {
			patched := strings.Replace(string(data),
				"Game_LowViolence",
				"Game_LowViolence\tcsgo_lv // Perfect World content override\n\t\t\tGame\tcsgo/addons/metamod",
				1)
			// Only write if we actually changed something beyond what was there
			if strings.Contains(patched, "csgo/addons/metamod") {
				os.WriteFile(gameinfoPath, []byte(patched), 0o644)
			}
		}
	}

	// 7. Fix permissions — CS2 runs as uid 1000 (steam) inside container
	exec.Command("chmod", "-R", "755", filepath.Join(csgoDir, "addons")).Run()
	exec.Command("chown", "-R", "1000:1000", base).Run()

	// 8. Create core.json for CSSharp
	configsDir := filepath.Join(csgoDir, "addons", "counterstrikesharp", "configs")
	coreJSON := filepath.Join(configsDir, "core.json")
	coreExample := filepath.Join(configsDir, "core.example.json")
	if _, err := os.Stat(coreJSON); os.IsNotExist(err) {
		if data, err := os.ReadFile(coreExample); err == nil {
			os.WriteFile(coreJSON, data, 0o644)
		}
	}

	return map[string]string{"status": "base_installed", "path": base}, nil
}

// updateBase updates the shared CS2 base (SteamCMD + plugins).
// Stops all servers, updates, remounts overlays, restarts.
func (h *Handler) updateBase() (interface{}, error) {
	// 1. Get running instances
	instances, _ := h.listInstancePorts()

	// 2. Stop all containers
	for _, port := range instances {
		h.stopServer(port)
	}

	// 3. Unmount all overlays
	for _, port := range instances {
		h.unmountOverlay(port)
	}

	// 4. Update base
	result, err := h.setupBase()
	if err != nil {
		return nil, fmt.Errorf("update base failed: %w", err)
	}

	// 5. Remount overlays and restart
	for _, port := range instances {
		h.mountOverlay(port)
		dir := h.instanceDir(port)
		h.runCompose(dir, "up", "-d")
	}

	return result, nil
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

func (h *Handler) mountOverlay(port int) error {
	base := h.baseDir()
	inst := h.instanceDir(port)
	upper := filepath.Join(inst, "overlay-upper")
	work := filepath.Join(inst, "overlay-work")
	merged := filepath.Join(inst, "cs2-merged")

	os.MkdirAll(upper, 0o755)
	os.MkdirAll(work, 0o755)
	os.MkdirAll(merged, 0o755)

	// Check if already mounted
	if h.isOverlayMounted(port) {
		return nil
	}

	cmd := exec.Command("sudo", "mount", "-t", "overlay", "overlay",
		"-o", fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", base, upper, work),
		merged)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount overlay: %w\noutput: %s", err, string(out))
	}

	// Fix ownership — CS2 runs as uid 1000 (steam) inside container
	exec.Command("sudo", "chown", "-R", "1000:1000", upper).Run()

	return nil
}

func (h *Handler) unmountOverlay(port int) error {
	merged := filepath.Join(h.instanceDir(port), "cs2-merged")
	if !h.isOverlayMounted(port) {
		return nil
	}
	cmd := exec.Command("sudo", "umount", merged)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("umount overlay: %w\noutput: %s", err, string(out))
	}
	return nil
}

func (h *Handler) isOverlayMounted(port int) bool {
	merged := filepath.Join(h.instanceDir(port), "cs2-merged")
	out, err := exec.Command("sudo", "mountpoint", "-q", merged).CombinedOutput()
	_ = out
	return err == nil
}

// RemountAllOverlays re-mounts overlays for all instances.
// Called on agent startup to recover from host reboot.
func (h *Handler) RemountAllOverlays() {
	if !h.isBaseInstalled() {
		return
	}
	ports, err := h.listInstancePorts()
	if err != nil {
		return
	}
	for _, port := range ports {
		if _, err := os.Stat(filepath.Join(h.instanceDir(port), "overlay-upper")); err == nil {
			h.mountOverlay(port)
		}
	}
}

func (h *Handler) runCompose(dir string, args ...string) (string, error) {
	fullArgs := append([]string{"compose", "-f", filepath.Join(dir, "docker-compose.yml")}, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
