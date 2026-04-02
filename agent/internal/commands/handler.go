package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
	Get5Cfg      string `json:"get5_cfg"`
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

func (h *Handler) HandleCommand(cmdType string, payload json.RawMessage) (interface{}, error) {
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
	dir := h.instanceDir(p.Port)
	configDir := filepath.Join(dir, "config")

	// Create directories
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating instance dir: %w", err)
	}

	// Ensure shared dir exists
	sharedDir := filepath.Join(h.DataDir, "shared")
	os.MkdirAll(sharedDir, 0o755)

	// Write server.cfg
	if p.ServerCfg != "" {
		if err := os.WriteFile(filepath.Join(configDir, "server.cfg"), []byte(p.ServerCfg), 0o600); err != nil {
			return nil, fmt.Errorf("writing server.cfg: %w", err)
		}
	}

	// Write get5.cfg
	if p.Get5Cfg != "" {
		if err := os.WriteFile(filepath.Join(configDir, "get5.cfg"), []byte(p.Get5Cfg), 0o600); err != nil {
			return nil, fmt.Errorf("writing get5.cfg: %w", err)
		}
	}

	// Generate docker-compose.yml
	gotvPort := p.GOTVPort
	if gotvPort == 0 {
		gotvPort = p.Port + 5
	}
	compose, err := GenerateComposeFile(p.Port, gotvPort, h.DockerImage, p.Hostname)
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
	out, err := h.runCompose(dir, "down", "-v")
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

func (h *Handler) runCompose(dir string, args ...string) (string, error) {
	fullArgs := append([]string{"compose", "-f", filepath.Join(dir, "docker-compose.yml")}, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
