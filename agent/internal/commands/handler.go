package commands

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	"get_base_status":     true,
	"get_agent_logs":      true,
	"validate_base":       true,
}

type GetLogsPayload struct {
	Port int `json:"port"`
	Tail int `json:"tail"`
}

type GetAgentLogsPayload struct {
	Lines int `json:"lines"`
}

type RCONPayload struct {
	Port     int    `json:"port"`
	Password string `json:"password"`
	Command  string `json:"command"`
}

// Max concurrent containers per host
const maxContainers = 20

// steamcmdProgressRe matches lines like:
//
//	Update state (0x61) downloading, progress: 98.77 (61582416353 / 62348865193)
//
// SteamCMD prints these roughly twice per second during download/verify.
var steamcmdProgressRe = regexp.MustCompile(`Update state \(0x([0-9a-fA-F]+)\)[^,]*,\s*progress:\s*([0-9.]+)\s*\(\s*(\d+)\s*/\s*(\d+)\s*\)`)

// SteamCMDProgress is a snapshot of current CS2 download progress. It is
// populated by steamcmdProgressWriter while downloadCS2ViaSteamCMD is running
// and read by get_base_status so the admin panel can render a live progress
// bar without tailing the log.
type SteamCMDProgress struct {
	Active    bool      `json:"active"`
	State     string    `json:"state"`      // "downloading" | "verifying" | "idle"
	StateCode string    `json:"state_code"` // raw hex like "61", "81"
	Percent   float64   `json:"percent"`
	Current   int64     `json:"current_bytes"`
	Total     int64     `json:"total_bytes"`
	SpeedBps  float64   `json:"speed_bps"`
	EtaSec    int64     `json:"eta_seconds"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	steamcmdProgressMu sync.Mutex
	steamcmdProgressCur *SteamCMDProgress
)

func setSteamcmdProgress(p SteamCMDProgress) {
	steamcmdProgressMu.Lock()
	cp := p
	steamcmdProgressCur = &cp
	steamcmdProgressMu.Unlock()
}

// getSteamcmdProgress returns a copy of the latest progress snapshot (or nil
// if there has been none). Marks Active=false if no update was received in the
// last 20s — useful to stop the UI "stuck on 42%" if steamcmd died silently.
func getSteamcmdProgress() *SteamCMDProgress {
	steamcmdProgressMu.Lock()
	defer steamcmdProgressMu.Unlock()
	if steamcmdProgressCur == nil {
		return nil
	}
	cp := *steamcmdProgressCur
	if time.Since(cp.UpdatedAt) > 20*time.Second {
		cp.Active = false
	}
	return &cp
}

func clearSteamcmdProgress() {
	steamcmdProgressMu.Lock()
	steamcmdProgressCur = nil
	steamcmdProgressMu.Unlock()
}

// steamcmdProgressWriter wraps a real writer and parses SteamCMD progress lines
// on-the-fly. Every ~2 seconds (or every ≥1% progress jump) it prints a human
// readable summary line with percent, downloaded/total bytes, speed and ETA —
// so it shows up in the agent logs tab in the admin panel during CS2 downloads.
type steamcmdProgressWriter struct {
	inner      io.Writer
	buf        []byte
	lastLogAt  time.Time
	lastBytes  int64
	lastPct    float64
	lastSpeedAt time.Time
}

func newSteamcmdProgressWriter(inner io.Writer) *steamcmdProgressWriter {
	return &steamcmdProgressWriter{inner: inner, lastSpeedAt: time.Now()}
}

func humanBytes(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	}
	return fmt.Sprintf("%d B", n)
}

func humanDuration(d time.Duration) string {
	if d < 0 || d > 24*time.Hour {
		return "—"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
}

func (w *steamcmdProgressWriter) Write(p []byte) (int, error) {
	// Forward raw bytes unchanged to keep full steamcmd output available.
	n, err := w.inner.Write(p)

	// Parse newly received text line-by-line to find progress updates.
	w.buf = append(w.buf, p...)
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' || b == '\r' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		w.handleLine(line)
	}
	return n, err
}

func (w *steamcmdProgressWriter) handleLine(line string) {
	m := steamcmdProgressRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	state := m[1]
	pct := 0.0
	fmt.Sscanf(m[2], "%f", &pct)
	var cur, total int64
	fmt.Sscanf(m[3], "%d", &cur)
	fmt.Sscanf(m[4], "%d", &total)

	now := time.Now()

	// Always refresh the shared snapshot so the admin panel sees live values
	// even between log flush intervals. Speed uses a short moving delta.
	var snapSpeed float64
	if !w.lastSpeedAt.IsZero() && w.lastBytes > 0 && cur >= w.lastBytes {
		if elapsed := now.Sub(w.lastSpeedAt).Seconds(); elapsed > 0 {
			snapSpeed = float64(cur-w.lastBytes) / elapsed
		}
	}
	label := "downloading"
	if strings.HasPrefix(state, "8") {
		label = "verifying"
	} else if state == "0" {
		label = "idle"
	}
	var snapEta time.Duration
	if snapSpeed > 0 && total-cur > 0 {
		snapEta = time.Duration(float64(total-cur)/snapSpeed) * time.Second
	}
	setSteamcmdProgress(SteamCMDProgress{
		Active:    true,
		State:     label,
		StateCode: state,
		Percent:   pct,
		Current:   cur,
		Total:     total,
		SpeedBps:  snapSpeed,
		EtaSec:    int64(snapEta.Seconds()),
		UpdatedAt: now,
	})

	// Throttle stdout log: at most every 2s, or on ≥1% jump.
	if !w.lastLogAt.IsZero() && now.Sub(w.lastLogAt) < 2*time.Second && pct-w.lastPct < 1.0 {
		w.lastBytes = cur
		w.lastSpeedAt = now
		return
	}

	speedStr := "—"
	if snapSpeed > 0 {
		speedStr = humanBytes(int64(snapSpeed)) + "/s"
	}

	fmt.Fprintf(os.Stdout,
		"[agent] CS2 %s: %.2f%% (%s / %s) @ %s  ETA %s\n",
		label, pct, humanBytes(cur), humanBytes(total), speedStr, humanDuration(snapEta),
	)

	w.lastLogAt = now
	w.lastPct = pct
	w.lastBytes = cur
	w.lastSpeedAt = now
}

// downloadCS2ViaSteamCMD runs steamcmd to install/update CS2 (app 730) into baseDir.
// Retries up to 5 times. Critically — checks SteamCMD output for failure markers
// (e.g. "Error! App '730' state is 0x602") instead of only checking if the binary
// exists, because a partial/corrupt install can leave the binary behind.
func downloadCS2ViaSteamCMD(baseDir string) error {
	steamcmdPath := "/usr/games/steamcmd"
	if _, err := os.Stat(steamcmdPath); os.IsNotExist(err) {
		return fmt.Errorf("steamcmd not found at %s — run bootstrap script to install", steamcmdPath)
	}

	// Reset and clear shared progress on exit so the admin UI stops showing it.
	setSteamcmdProgress(SteamCMDProgress{Active: true, State: "starting", UpdatedAt: time.Now()})
	defer clearSteamcmdProgress()

	cs2Binary := filepath.Join(baseDir, "game", "bin", "linuxsteamrt64", "cs2")
	var lastErr error

	for attempt := 1; attempt <= 5; attempt++ {
		fmt.Printf("[agent] SteamCMD CS2 install attempt %d/5 into %s\n", attempt, baseDir)
		cmd := exec.Command(steamcmdPath,
			"+force_install_dir", baseDir,
			"+login", "anonymous",
			"+app_info_update", "1",
			"+app_update", "730", "validate",
			"+quit",
		)
		// Tee output to our stdout (wrapped in a progress parser) AND capture it
		// to detect error strings at the end of the run.
		var buf strings.Builder
		progress := newSteamcmdProgressWriter(os.Stdout)
		cmd.Stdout = io.MultiWriter(progress, &buf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
		runErr := cmd.Run()
		output := buf.String()

		// Detect SteamCMD "Error! App '730' state is 0x..." markers. Any such line means
		// the install is broken even if the cs2 binary happens to exist from a prior run.
		if strings.Contains(output, "Error! App '730'") || strings.Contains(output, "Error! App \"730\"") {
			lastErr = fmt.Errorf("steamcmd reported app 730 error (see log above)")
			fmt.Printf("[agent] SteamCMD attempt %d: detected app 730 error, will retry\n", attempt)
		} else if runErr != nil {
			lastErr = fmt.Errorf("steamcmd exit: %w", runErr)
			fmt.Printf("[agent] SteamCMD attempt %d: exit error: %v\n", attempt, runErr)
		} else if _, err := os.Stat(cs2Binary); err != nil {
			lastErr = fmt.Errorf("cs2 binary missing after steamcmd: %w", err)
			fmt.Printf("[agent] SteamCMD attempt %d: binary missing at %s\n", attempt, cs2Binary)
		} else if strings.Contains(output, "Success! App '730' fully installed") ||
			strings.Contains(output, "Success! App '730' already up to date") ||
			strings.Contains(output, "fully installed.") {
			fmt.Printf("[agent] SteamCMD attempt %d: success\n", attempt)
			return nil
		} else {
			// Binary exists and no explicit error — accept with a warning
			fmt.Printf("[agent] SteamCMD attempt %d: binary exists, no success marker — accepting\n", attempt)
			return nil
		}

		if attempt < 5 {
			fmt.Printf("[agent] retrying SteamCMD in 10s...\n")
			time.Sleep(10 * time.Second)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("steamcmd failed after 5 attempts")
	}
	return fmt.Errorf("failed to install CS2 via steamcmd: %w", lastErr)
}

func (h *Handler) HandleCommand(cmdType string, payload json.RawMessage) (result interface{}, err error) {
	// Allowlist check — reject unknown commands
	if !allowedCommands[cmdType] {
		return nil, fmt.Errorf("rejected unknown command: %s", cmdType)
	}

	fmt.Printf("[agent] ▶ command received: %s (%d bytes payload)\n", cmdType, len(payload))
	startedAt := time.Now()
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[agent] ✖ command %s PANIC after %s: %v\n", cmdType, time.Since(startedAt), r)
			err = fmt.Errorf("panic in %s: %v", cmdType, r)
		} else if err != nil {
			fmt.Printf("[agent] ✖ command %s failed after %s: %v\n", cmdType, time.Since(startedAt), err)
		} else {
			fmt.Printf("[agent] ✓ command %s ok in %s\n", cmdType, time.Since(startedAt))
		}
	}()

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

	case "get_base_status":
		return h.getBaseStatus()

	case "get_agent_logs":
		var p GetAgentLogsPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("invalid get_agent_logs payload: %w", err)
		}
		return h.getAgentLogs(p)

	case "validate_base":
		return h.validateBase()

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
		fmt.Println("[agent] CS2 not found in cs2-base, downloading via SteamCMD...")
		if err := downloadCS2ViaSteamCMD(baseDir); err != nil {
			return nil, err
		}
	}

	// If a previous deploy left a crash-looping container on this port,
	// it is still bind-mounting cs2-data and holds file handles inside it
	// (and may have written new files owned by the container's steam uid).
	// Any attempt to wipe/refresh cs2-data while that container is alive
	// results in `unlinkat ... permission denied` on files inside the mount.
	// Tear down any existing container for this port before touching
	// cs2-data. Best-effort: ignore errors if there's nothing to stop.
	composePath := filepath.Join(dir, "docker-compose.yml")
	if _, err := os.Stat(composePath); err == nil {
		if out, derr := h.runCompose(dir, "down", "--remove-orphans", "--timeout", "10"); derr != nil {
			fmt.Printf("[agent] deploy %d: pre-deploy compose down warning: %v\n%s\n", p.Port, derr, out)
		}
	}
	// Belt-and-suspenders: force-remove the named container if it still
	// exists (compose file might be missing/broken but container alive).
	containerName := fmt.Sprintf("cs2-%d", p.Port)
	exec.Command("docker", "rm", "-f", containerName).Run()

	// Hardlink copy cs2-base → instance cs2-data (instant, no extra disk).
	// Uses a completion marker + critical-file verification so that a
	// previously interrupted copy (OOM, EIO, agent restart, SIGKILL) is
	// detected and the instance dir is rebuilt from scratch. Without this,
	// a half-copied instance with just the cs2 binary could slip past and
	// the container would crash-loop with "Can't find csgo/gameinfo.gi".
	if err := syncCs2DataFromBase(cs2DataDir, baseDir, h.DockerImage); err != nil {
		return nil, fmt.Errorf("sync cs2-base to instance: %w", err)
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
	fmt.Printf("[agent] deploy %d: docker compose pull\n", p.Port)
	if pullOut, pullErr := h.runCompose(dir, "pull"); pullErr != nil {
		// Non-fatal — image may already be present locally. Log and continue.
		fmt.Printf("[agent] deploy %d: compose pull warning: %v\n%s\n", p.Port, pullErr, string(pullOut))
	}

	// docker compose up -d
	fmt.Printf("[agent] deploy %d: docker compose up -d\n", p.Port)
	out, err := h.runCompose(dir, "up", "-d")
	if err != nil {
		return nil, fmt.Errorf("docker compose up: %w\noutput: %s", err, out)
	}
	fmt.Printf("[agent] deploy %d: container started\n%s\n", p.Port, string(out))

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

	// Check if container exists first
	checkCmd := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", containerName)
	stateOut, checkErr := checkCmd.CombinedOutput()

	if checkErr != nil {
		// Container doesn't exist at all
		return map[string]string{
			"container": containerName,
			"logs":      fmt.Sprintf("[agent] Контейнер %s не найден. Сервер ещё не был развёрнут или был удалён.", containerName),
			"lines":     "0",
		}, nil
	}

	state := strings.TrimSpace(string(stateOut))

	cmd := exec.Command("docker", "logs", "--tail", fmt.Sprintf("%d", tail), containerName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]string{
			"container": containerName,
			"logs":      fmt.Sprintf("[agent] Ошибка чтения логов (контейнер: %s): %v", state, err),
			"lines":     "0",
		}, nil
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
// Agent downloads CS2 via SteamCMD, then runs a temporary container
// to install plugins (MetaMod, CSSharp, MatchZy).

// setupBase ensures CS2 is installed via SteamCMD and plugins are set up.
func (h *Handler) setupBase() (interface{}, error) {
	base := filepath.Join(h.DataDir, "cs2-base")
	os.MkdirAll(base, 0o755)

	cs2Binary := filepath.Join(base, "game", "bin", "linuxsteamrt64", "cs2")

	// Check if already installed (и не битый — downloadCS2ViaSteamCMD сам пропустит если up-to-date)
	if _, err := os.Stat(cs2Binary); err == nil {
		fmt.Println("[agent] setup_base: cs2 binary already present, running SteamCMD validate to ensure integrity")
	}

	// Step 1: Download/validate CS2 via SteamCMD
	if err := downloadCS2ViaSteamCMD(base); err != nil {
		return nil, err
	}

	// Step 2: Run a temporary container to install plugins (MetaMod, CSSharp, MatchZy)
	// Remove stale setup container if exists
	exec.Command("docker", "rm", "-f", "cs2-base-setup").Run()

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
		// as long as plugins were installed before that
		_ = out
	}

	return map[string]string{"status": "base_installed", "path": base}, nil
}

// updateBase stops all servers, updates CS2 base via SteamCMD, reinstalls plugins, restarts.
func (h *Handler) updateBase() (interface{}, error) {
	instances, _ := h.listInstancePorts()
	base := filepath.Join(h.DataDir, "cs2-base")

	// 1. Stop all containers
	for _, port := range instances {
		h.stopServer(port)
	}

	// 2. Update CS2 via SteamCMD (validate re-downloads changed files)
	if err := downloadCS2ViaSteamCMD(base); err != nil {
		return nil, err
	}

	// 3. Remove plugin marker to force plugin reinstall
	os.Remove(filepath.Join(base, "game", "csgo", "addons", ".rushborg-plugins-installed"))

	// 4. Run temporary container to reinstall plugins
	exec.Command("docker", "rm", "-f", "cs2-base-setup").Run()
	cmd := exec.Command("docker", "run", "--rm",
		"--name", "cs2-base-setup",
		"-v", base+":/home/steam/cs2-dedicated",
		"-e", "CS2_PORT=0",
		"--security-opt", "seccomp=unconfined",
		"-e", "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1",
		h.DockerImage,
	)
	cmd.CombinedOutput() // exits when CS2 tries to bind port 0

	// 5. Recreate per-instance hardlink copies from updated base. Force a
	// full rebuild by removing the marker first, so the helper always
	// reruns the copy (base has new files).
	for _, port := range instances {
		cs2Data := filepath.Join(h.instanceDir(port), "cs2-data")
		os.Remove(filepath.Join(cs2Data, ".rushborg-cs2-ready"))
		if err := syncCs2DataFromBase(cs2Data, base, h.DockerImage); err != nil {
			fmt.Printf("[agent] force-update: sync instance %d failed: %v\n", port, err)
		}
	}

	// 6. Restart all
	for _, port := range instances {
		dir := h.instanceDir(port)
		h.runCompose(dir, "up", "-d")
	}

	return map[string]string{"status": "updated", "path": base}, nil
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

// getBaseStatus returns info about the shared cs2-base directory.
func (h *Handler) getBaseStatus() (interface{}, error) {
	base := filepath.Join(h.DataDir, "cs2-base")
	cs2Binary := filepath.Join(base, "game", "bin", "linuxsteamrt64", "cs2")

	result := map[string]interface{}{
		"installed": false,
		"path":      base,
	}

	if _, err := os.Stat(cs2Binary); err == nil {
		result["installed"] = true

		// Get directory size via du
		cmd := exec.Command("du", "-sh", base)
		out, err := cmd.CombinedOutput()
		if err == nil {
			parts := strings.Fields(string(out))
			if len(parts) >= 1 {
				result["size"] = parts[0]
			}
		}

		// Check for version file or game info
		versionFile := filepath.Join(base, "game", "csgo", "steam.inf")
		if data, err := os.ReadFile(versionFile); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "PatchVersion=") {
					result["version"] = strings.TrimPrefix(line, "PatchVersion=")
					break
				}
			}
		}
	}

	// Attach live SteamCMD download progress if a download is in-flight.
	if prog := getSteamcmdProgress(); prog != nil {
		eta := time.Duration(prog.EtaSec) * time.Second
		download := map[string]interface{}{
			"active":        prog.Active,
			"state":         prog.State,
			"state_code":    prog.StateCode,
			"percent":       prog.Percent,
			"current_bytes": prog.Current,
			"total_bytes":   prog.Total,
			"current_human": humanBytes(prog.Current),
			"total_human":   humanBytes(prog.Total),
			"speed_bps":     prog.SpeedBps,
			"speed_human":   "—",
			"eta_seconds":   prog.EtaSec,
			"eta_human":     humanDuration(eta),
			"updated_at":    prog.UpdatedAt.Format(time.RFC3339),
		}
		if prog.SpeedBps > 0 {
			download["speed_human"] = humanBytes(int64(prog.SpeedBps)) + "/s"
		}
		result["download"] = download
	}

	return result, nil
}

// getAgentLogs returns the last N lines from journalctl for the agent service.
func (h *Handler) getAgentLogs(p GetAgentLogsPayload) (interface{}, error) {
	lines := p.Lines
	if lines <= 0 || lines > 500 {
		lines = 100
	}
	cmd := exec.Command("journalctl", "-u", "rushborg-agent", "--no-pager", "-n", fmt.Sprintf("%d", lines))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("journalctl: %w\noutput: %s", err, string(out))
	}
	return map[string]interface{}{
		"logs":  string(out),
		"lines": lines,
	}, nil
}

// validateBase runs steamcmd validate on the cs2-base in background.
func (h *Handler) validateBase() (interface{}, error) {
	base := filepath.Join(h.DataDir, "cs2-base")

	steamcmdPath := "/usr/games/steamcmd"
	if _, err := os.Stat(steamcmdPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("steamcmd not found at %s", steamcmdPath)
	}

	// Run in background
	go func() {
		cmd := exec.Command(steamcmdPath,
			"+force_install_dir", base,
			"+login", "anonymous",
			"+app_info_update", "1",
			"+app_update", "730", "validate",
			"+quit")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("[agent] validate_base error: %v\n", err)
		} else {
			fmt.Printf("[agent] validate_base completed\n")
		}
	}()

	return map[string]string{"status": "validating", "message": "validation started in background"}, nil
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

// cs2CriticalFiles are files which MUST exist in a working instance dir.
// Used to detect half-finished copies (interrupted cp, OOM, disk full, etc.)
// and force a clean re-copy. The cs2 binary alone is not a reliable marker
// because `cp -a` copies alphabetically and `bin/` comes before `csgo/`.
var cs2CriticalFiles = []string{
	"game/bin/linuxsteamrt64/cs2",
	"game/csgo/gameinfo.gi",
}

const cs2ReadyMarker = ".rushborg-cs2-ready"

// syncCs2DataFromBase ensures cs2DataDir contains a complete copy of baseDir.
// It uses a marker file written only after a successful copy; if the marker
// is missing or any critical file is missing, the instance dir is wiped and
// rebuilt from scratch. Copy is hardlinked (cp -al) when possible, with a
// full deep-copy fallback for cross-filesystem cases.
//
// dockerImage is used as a last-resort privileged cleanup image when the
// agent's own user can't remove the directory (e.g. because the cs2
// container's entrypoint chown'd the tree to uid=1000/steam and the agent
// runs as a different uid). The agent is in the docker group, so spawning
// a throwaway container gives us root-on-host privileges for this one
// operation without touching sudoers.
func syncCs2DataFromBase(cs2DataDir, baseDir, dockerImage string) error {
	if err := os.MkdirAll(cs2DataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", cs2DataDir, err)
	}

	// Check if we already have a good copy: marker present AND all critical
	// files present. If any check fails, we treat the dir as dirty and
	// rebuild.
	if isCs2DataReady(cs2DataDir) {
		return nil
	}

	// Dirty state: wipe the instance dir before re-copying. We don't want
	// `cp -al` to fail mid-way because stale files from a previous attempt
	// are already present — hardlinks can't overwrite regular files in all
	// cp versions, and leftover half-copied files waste inodes.
	fmt.Printf("[agent] cs2-data %s is incomplete, rebuilding from base\n", cs2DataDir)
	if err := removeCs2DataTree(cs2DataDir, dockerImage); err != nil {
		return fmt.Errorf("cleanup stale cs2-data: %w", err)
	}
	if err := os.MkdirAll(cs2DataDir, 0o755); err != nil {
		return fmt.Errorf("recreate cs2-data: %w", err)
	}

	// Prefer hardlink copy (instant, no extra disk). Fall back to deep copy
	// if source and destination are on different filesystems.
	cmd := exec.Command("cp", "-al", baseDir+"/.", cs2DataDir+"/")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("[agent] cp -al failed (%v), falling back to cp -a: %s\n", err, string(out))
		cmd = exec.Command("cp", "-a", baseDir+"/.", cs2DataDir+"/")
		if out2, err2 := cmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("cp -a failed: %w\noutput: %s", err2, string(out2))
		}
	}

	// Verify critical files actually landed. If not, something is wrong
	// with the base directory itself — surface the error instead of writing
	// the marker and letting the container crash-loop.
	for _, rel := range cs2CriticalFiles {
		p := filepath.Join(cs2DataDir, rel)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("post-copy verification failed: missing %s (%w)", rel, err)
		}
	}

	// Write marker only after all checks pass. Marker presence = copy is
	// trustworthy.
	markerPath := filepath.Join(cs2DataDir, cs2ReadyMarker)
	if err := os.WriteFile(markerPath, []byte(""), 0o644); err != nil {
		return fmt.Errorf("write ready marker: %w", err)
	}
	return nil
}

// removeCs2DataTree wipes cs2DataDir, escalating through progressively
// more privileged strategies until something works.
//
//  1. os.RemoveAll — fast path, works when the agent owns everything.
//  2. chmod -R u+rwX && rm -rf — handles the case where the agent owns
//     files but has left them with restricted perms.
//  3. docker run --rm --user 0:0 -v parent:/t image sh -c 'rm -rf /t/<name>'
//     — last resort. The cs2-server container's entrypoint does
//     `chown -R steam:steam` on the bind-mounted cs2-data, giving every
//     file uid=1000. If the agent's `rushborgsrv` user has a different
//     uid (common on hosts that had an `ubuntu` user first), plain `rm`
//     will fail with EPERM/EACCES even recursively. Docker is always
//     accessible to the agent (it's in the docker group) and runs as
//     root on the host, which gives us a privilege-escalation path that
//     doesn't require sudoers changes.
func removeCs2DataTree(cs2DataDir, dockerImage string) error {
	// Fast path.
	if err := os.RemoveAll(cs2DataDir); err == nil {
		return nil
	} else {
		fmt.Printf("[agent] RemoveAll failed (%v), trying chmod + rm -rf\n", err)
	}

	// chmod + shell rm.
	exec.Command("chmod", "-R", "u+rwX", cs2DataDir).Run()
	if out, err := exec.Command("rm", "-rf", cs2DataDir).CombinedOutput(); err == nil {
		return nil
	} else {
		fmt.Printf("[agent] shell rm -rf failed (%v, %s), trying docker-as-root cleanup\n", err, string(out))
	}

	// Docker-as-root last resort.
	if dockerImage == "" {
		return fmt.Errorf("cannot remove %s and no docker image provided for privileged cleanup", cs2DataDir)
	}
	parent := filepath.Dir(cs2DataDir)
	base := filepath.Base(cs2DataDir)
	if parent == "" || parent == "/" || base == "" || base == "/" {
		return fmt.Errorf("refusing privileged cleanup on unsafe path %s", cs2DataDir)
	}
	// Sanity: base must not contain shell metacharacters.
	if strings.ContainsAny(base, "` $();|&<>\"'\\\n\r") {
		return fmt.Errorf("refusing privileged cleanup on unsafe name %q", base)
	}
	dockerCmd := exec.Command("docker", "run", "--rm",
		"--user", "0:0",
		"--entrypoint", "sh",
		"-v", parent+":/target",
		dockerImage,
		"-c", fmt.Sprintf("rm -rf -- /target/%s && mkdir -p /target/%s && chmod 0755 /target/%s", base, base, base),
	)
	if out, err := dockerCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker-as-root cleanup failed: %w\noutput: %s", err, string(out))
	}
	// Ensure the empty dir we just created is owned by the agent user,
	// not root. Best-effort: use docker again to chown to current uid.
	uid := os.Getuid()
	gid := os.Getgid()
	chownCmd := exec.Command("docker", "run", "--rm",
		"--user", "0:0",
		"--entrypoint", "sh",
		"-v", parent+":/target",
		dockerImage,
		"-c", fmt.Sprintf("chown %d:%d /target/%s", uid, gid, base),
	)
	chownCmd.Run() // best-effort
	return nil
}

// isCs2DataReady returns true if cs2DataDir has a ready marker and all
// critical files present. Any missing piece means "not ready".
func isCs2DataReady(cs2DataDir string) bool {
	if _, err := os.Stat(filepath.Join(cs2DataDir, cs2ReadyMarker)); err != nil {
		return false
	}
	for _, rel := range cs2CriticalFiles {
		if _, err := os.Stat(filepath.Join(cs2DataDir, rel)); err != nil {
			return false
		}
	}
	return true
}
