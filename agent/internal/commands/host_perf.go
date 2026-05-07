package commands

// host_perf.go — applies the embedded bootstrap-host-perf.sh script via
// sudo to fix CS2 "UNEXPECTED LONG FRAME DETECTED" stalls on the host.
//
// Why this lives in the agent (instead of being a separate setup step the
// operator runs manually):
//
//   - We want zero-touch setup after the very first sudoers entry. Once the
//     operator allows the agent to run THIS specific script via passwordless
//     sudo, every subsequent host gets tuned automatically by the agent on
//     first start, and re-tuned whenever the script changes.
//
//   - The script is embedded into the binary (`//go:embed`) so a `go install`
//     of a fresh agent build always carries the latest tuning logic — no
//     external file to forget about.
//
//   - We hash the embedded script and write it to a fixed path
//     (`/opt/rushborg-srv/.bootstrap-host-perf.sh`). The operator's sudoers
//     entry only allows sudo on that path, so even if the agent is
//     compromised it cannot escalate to arbitrary root execution beyond the
//     fixed-content tuning script.
//
// Required sudoers entry (one-time, on each host):
//
//   rushborgsrv ALL=(root) NOPASSWD: /opt/rushborg-srv/.bootstrap-host-perf.sh
//
// Without it, ApplyHostPerf returns an error explaining what's missing —
// it never silently fails.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rushborg/agent/internal/commands/perf"
)

// ApplyHostPerfPayload — input for the apply_host_perf command. Force=true
// re-runs even if the marker shows the current script hash already applied.
type ApplyHostPerfPayload struct {
	Force bool `json:"force"`
}

// ApplyHostPerfResult — what the agent reports back to the platform after
// running the bootstrap script (or skipping it).
type ApplyHostPerfResult struct {
	Status      string `json:"status"`        // applied | already_applied | skipped | error
	ScriptHash  string `json:"script_hash"`   // sha256 of the script that was (or would be) executed
	Output      string `json:"output,omitempty"`
	Error       string `json:"error,omitempty"`
	NeedsSudoers bool  `json:"needs_sudoers"` // true if sudo -n failed and operator must add sudoers entry
	StartedAt   int64  `json:"started_at"`
	FinishedAt  int64  `json:"finished_at"`
}

// scriptPath — fixed path that the sudoers entry whitelists. Must match
// the path documented in the README and the install script.
func (h *Handler) hostPerfScriptPath() string {
	return filepath.Join(h.DataDir, ".bootstrap-host-perf.sh")
}

// markerPath stores the sha256 of the script that was last successfully
// applied. When the embedded script changes (new agent release with new
// tuning), the hash mismatch triggers a re-apply on next agent start.
func (h *Handler) hostPerfMarkerPath() string {
	return filepath.Join(h.DataDir, ".host-perf-applied")
}

// scriptHash returns the sha256 of the embedded script as hex.
func scriptHash() string {
	sum := sha256.Sum256([]byte(perf.HostPerfScript))
	return hex.EncodeToString(sum[:])
}

// markerHash reads the previously-applied script hash. Empty string if
// the marker is missing or unreadable — caller treats that as "not yet
// applied" and will run the script.
func (h *Handler) markerHash() string {
	data, err := os.ReadFile(h.hostPerfMarkerPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeMarker persists the just-applied script hash so we can short-circuit
// the next run unless the embedded script has changed.
func (h *Handler) writeMarker(hash string) error {
	return os.WriteFile(h.hostPerfMarkerPath(), []byte(hash+"\n"), 0o644)
}

// writeScript materialises the embedded script onto disk at the fixed path
// the sudoers entry whitelists. Permissions are 0o750 — readable and
// executable by root (via sudo) but not world-writable. We always rewrite
// the file so the on-disk content matches the embedded version (otherwise
// an attacker with non-root write access could swap the body and bypass
// the fixed-path sudoers protection).
func (h *Handler) writeScript() error {
	if err := os.MkdirAll(h.DataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}
	path := h.hostPerfScriptPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(perf.HostPerfScript), 0o750); err != nil {
		return fmt.Errorf("write script: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename script: %w", err)
	}
	return nil
}

// ApplyHostPerf is the public entry point. Idempotent: if the embedded
// script hash matches what's recorded in the marker file and force=false,
// it short-circuits with status=already_applied.
//
// The actual sudo execution is short (apt update + a few systemctl calls,
// usually <30s on a fresh host, <1s on already-applied hosts) so it runs
// inline rather than detaching. Caller can wrap it in a goroutine if it
// needs non-blocking behaviour (the agent startup path does exactly that).
func (h *Handler) ApplyHostPerf(force bool) (ApplyHostPerfResult, error) {
	res := ApplyHostPerfResult{
		ScriptHash: scriptHash(),
		StartedAt:  time.Now().Unix(),
	}

	if !force && h.markerHash() == res.ScriptHash {
		res.Status = "already_applied"
		res.FinishedAt = time.Now().Unix()
		return res, nil
	}

	if err := h.writeScript(); err != nil {
		res.Status = "error"
		res.Error = err.Error()
		res.FinishedAt = time.Now().Unix()
		return res, err
	}

	scriptPath := h.hostPerfScriptPath()
	bashPath := findBinary("bash")

	// Try direct execution first (works if agent is root). Otherwise sudo -n
	// against the fixed scriptPath — this is what the sudoers entry should
	// allow.
	var cmd *exec.Cmd
	var asRoot bool
	if os.Geteuid() == 0 {
		cmd = exec.Command(bashPath, scriptPath)
		asRoot = true
	} else {
		cmd = exec.Command("sudo", "-n", scriptPath)
	}

	out, err := cmd.CombinedOutput()
	res.Output = strings.TrimSpace(string(out))
	res.FinishedAt = time.Now().Unix()

	if err != nil {
		res.Status = "error"
		res.Error = err.Error()
		// Detect missing sudoers vs script failure. sudo -n with no
		// matching sudoers entry prints "a password is required" or
		// "sudo: a terminal is required" on stderr.
		if !asRoot {
			combined := strings.ToLower(res.Output + " " + err.Error())
			if strings.Contains(combined, "password is required") ||
				strings.Contains(combined, "no tty present") ||
				strings.Contains(combined, "terminal is required") ||
				strings.Contains(combined, "may not run") {
				res.NeedsSudoers = true
				res.Error = fmt.Sprintf(
					"sudoers entry missing. Add this one line to /etc/sudoers.d/rushborg-agent on the host:\n"+
						"  $(whoami) ALL=(root) NOPASSWD: %s",
					scriptPath,
				)
			}
		}
		return res, fmt.Errorf("apply host perf: %w", err)
	}

	if err := h.writeMarker(res.ScriptHash); err != nil {
		// Script ran but we can't persist marker. Don't fail the call —
		// next agent start will re-run, idempotent script handles that.
		fmt.Printf("[agent] host-perf marker write failed: %v\n", err)
	}
	res.Status = "applied"
	return res, nil
}

// MaybeApplyHostPerfOnStartup is the boot-time hook. Runs in a goroutine
// from main; never blocks the agent's main loop. Logs a friendly summary
// (success or "needs sudoers") and stays silent on already_applied.
func (h *Handler) MaybeApplyHostPerfOnStartup() {
	go func() {
		// Small delay so the WS connection has time to come up. Not
		// strictly required, just keeps the startup log readable.
		time.Sleep(3 * time.Second)

		res, err := h.ApplyHostPerf(false)
		switch res.Status {
		case "already_applied":
			// Quiet success — host is already tuned.
			return
		case "applied":
			fmt.Printf("[agent] host-perf: applied (hash=%s, %ds)\n",
				res.ScriptHash[:12], res.FinishedAt-res.StartedAt)
			return
		case "error":
			if res.NeedsSudoers {
				fmt.Printf("[agent] host-perf: skipped — sudoers not configured.\n%s\n", res.Error)
			} else {
				fmt.Printf("[agent] host-perf: failed: %v\noutput:\n%s\n", err, res.Output)
			}
		}
	}()
}
