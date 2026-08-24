package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Keeping verad alive. `vera` is a nanny, not an owner: verad is
// spawned into its own session and outlives whichever `vera` started
// it, so closing the chat never takes the fleet down with it.

// runfile is verad's note of where it is (cmd/verad/runfile.go).
type runfile struct {
	PID     int       `json:"pid"`
	Addr    string    `json:"addr"`
	Started time.Time `json:"started"`
	Version string    `json:"version"`
}

func stateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "vera")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "vera")
}

func runfilePath() string  { return filepath.Join(stateDir(), "verad.json") }
func logPath() string      { return filepath.Join(stateDir(), "verad.log") }
func identityPath() string { return filepath.Join(stateDir(), "identity.json") }

func readRunfile() (*runfile, error) {
	b, err := os.ReadFile(runfilePath())
	if err != nil {
		return nil, err
	}
	var r runfile
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// baseURL is where verad answers, from the runfile's addr — ":4780"
// means loopback on that port.
func baseURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:4780"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// alive asks /ping, which needs no secret.
func alive(base string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/ping", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// current is verad if it is answering: the runfile, verified.
func current() (*runfile, bool) {
	r, err := readRunfile()
	if err != nil {
		return nil, false
	}
	return r, pidAlive(r.PID) && alive(baseURL(r.Addr))
}

func veradPath() string {
	if p, err := exec.LookPath("verad"); err == nil {
		return p
	}
	// Beside this binary, which is where `make install` puts both.
	if self, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(self), "verad"); fileExists(p) {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "verad")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ensure makes sure verad is answering and returns its base URL.
func ensure() (string, error) {
	if r, ok := current(); ok {
		return baseURL(r.Addr), nil
	}
	if err := start(nil, false); err != nil {
		return "", err
	}
	r, ok := current()
	if !ok {
		return "", errors.New("verad started but is not answering; see `vera log`")
	}
	return baseURL(r.Addr), nil
}

// start spawns verad detached with its output in the log, and waits
// until it answers. Extra args go to verad verbatim (`vera start
// --echo`). A verad that is already up is left alone.
func start(args []string, say bool) error {
	if r, ok := current(); ok {
		if say {
			fmt.Printf("verad is already running (pid %d, %s)\n", r.PID, baseURL(r.Addr))
		}
		return nil
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	bin := veradPath()
	if !fileExists(bin) {
		return fmt.Errorf("verad is not installed at %s (make install)", bin)
	}
	fmt.Fprintf(logf, "\n--- vera: starting %s %s at %s\n", bin, strings.Join(args, " "), time.Now().Format(time.RFC3339))
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start verad: %w", err)
	}
	// Not our child to wait on: it belongs to its own session now.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := current(); ok {
			if say {
				fmt.Printf("verad started (pid %d, %s)\n", r.PID, baseURL(r.Addr))
			}
			return nil
		}
		if !pidAlive(cmd.Process.Pid) {
			return fmt.Errorf("verad exited on start; the last lines of %s:\n%s", logPath(), tailLog(12))
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("verad did not answer within 15s; see `vera log`")
}

// stop asks verad to exit (SIGTERM, which it handles) and waits.
func stop() error {
	r, err := readRunfile()
	if err != nil || !pidAlive(r.PID) {
		fmt.Println("verad is not running")
		_ = os.Remove(runfilePath())
		return nil
	}
	if err := syscall.Kill(r.PID, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(r.PID) {
			fmt.Printf("verad stopped (was pid %d)\n", r.PID)
			_ = os.Remove(runfilePath())
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("verad (pid %d) did not exit within 10s", r.PID)
}

func status() error {
	r, err := readRunfile()
	if err != nil {
		fmt.Println("verad: not running")
		return nil
	}
	switch {
	case pidAlive(r.PID) && alive(baseURL(r.Addr)):
		fmt.Printf("verad: running · pid %d · %s · up %s · %s\n", r.PID, baseURL(r.Addr), roughDuration(time.Since(r.Started)), r.Version)
	case pidAlive(r.PID):
		fmt.Printf("verad: pid %d is alive but %s is not answering\n", r.PID, baseURL(r.Addr))
	default:
		fmt.Printf("verad: not running (crashed or killed; last pid %d). `vera log` has the end of it.\n", r.PID)
	}
	return nil
}

func showLog(follow bool) error {
	if !fileExists(logPath()) {
		return errors.New("no log yet at " + logPath())
	}
	args := []string{"-n", "60"}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.Command("tail", append(args, logPath())...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func tailLog(n int) string {
	out, _ := exec.Command("tail", "-n", fmt.Sprint(n), logPath()).Output()
	return string(out)
}

func showURL() error {
	r, ok := current()
	if !ok {
		return errors.New("verad is not running")
	}
	fmt.Println(baseURL(r.Addr) + "/")
	return nil
}

// --- launchd ------------------------------------------------------------

const launchdLabel = "com.incantery.verad"

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

// install writes the agent and bootstraps it. verad then starts at
// login and is restarted if it dies; `vera stop` still works between
// (launchd brings it back — use `vera uninstall` to really stop).
func install() error {
	bin := veradPath()
	if !fileExists(bin) {
		return fmt.Errorf("verad is not installed at %s (make install first)", bin)
	}
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>WorkingDirectory</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>%s</string>
    <key>PATH</key><string>%s/.local/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, launchdLabel, bin, home, home, home, logPath(), logPath())
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath(), []byte(plist), 0o644); err != nil {
		return err
	}
	uid := fmt.Sprint(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+launchdLabel).Run() // idempotent reload
	if out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, plistPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	fmt.Println("installed " + plistPath() + " — verad starts at login now")
	return nil
}

func uninstall() error {
	uid := fmt.Sprint(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+launchdLabel).Run()
	if err := os.Remove(plistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Println("removed the launchd agent; verad will not start at login")
	return nil
}

func roughDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
