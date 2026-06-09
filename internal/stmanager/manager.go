// Package stmanager owns WeSync's dedicated Syncthing instance.
//
// Layout (all paths relative to the wesync executable):
//
//	wesync.exe
//	syncthing.exe
//	data/
//	  wesync.db
//	  syncthing/        ← Syncthing home (config, keys, index)
//	    config.xml
//	    cert.pem
//	    key.pem
//
// This keeps WeSync portable: copy the folder anywhere and it just works.
// When installed, the installer registers a Task Scheduler task so Syncthing
// starts at boot without requiring a user login.
package stmanager

import (
	"encoding/xml"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Internal handle to the running ST subprocess. Lets us Stop() it on
// demand without tearing down the rest of the backend. nil = not running.
//
// `running` is the authoritative "is ST up right now?" flag. It is NOT the
// same as `current != nil`: when Start adopts an already-running ST on the
// port we have no *exec.Cmd to hold, yet ST is very much running. Callers
// (the power gate) read IsRunning() to decide whether to start ST, so it
// must report true in the adopt case too — otherwise the gate re-"starts"
// ST every poll and spams st_start events.
var (
	mu      sync.Mutex
	current *exec.Cmd
	running bool
	// starting is true while a Start() is in flight — between claiming the latch
	// and the process becoming ready. It serializes Start so two concurrent
	// callers (e.g. the power gate poll and a UI action) can't both launch a
	// second Syncthing and orphan the first.
	starting bool
)

const (
	// APIPort is the dedicated port for WeSync's Syncthing instance.
	// Using 8385 avoids clashing with a user's personal Syncthing on 8384.
	APIPort = 8385
	APIURL  = "http://127.0.0.1:8385"

	// Lifecycle timings.
	dialTimeout    = time.Second           // probe whether the API port is up
	startDeadline  = 30 * time.Second      // give ST this long to answer after launch
	readyPollEvery = 500 * time.Millisecond // how often to re-probe during startup
	portFreeWait   = 5 * time.Second        // wait for a killed occupant to release the port
)

// ExeDir returns the directory containing the running wesync executable.
func ExeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// DBPath returns the WeSync database path in the shared data directory.
func DBPath() (string, error) {
	dir := DataDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "wesync.db"), nil
}

// SyncthingPathOverride lets the embedding host (notably the Android
// wrapper) point us at a specific Syncthing binary that isn't co-located
// with the wesync executable. On Android the binary ships inside the APK
// as `lib/arm64-v8a/libsyncthing.so` and lands in the app's native library
// dir at install time — there is no "ExeDir" in any meaningful sense.
//
// Set before any stmanager call. Empty string means "use ExeDir lookup",
// matching desktop behaviour.
var SyncthingPathOverride string

// FindSyncthing returns the path to syncthing(.exe) alongside wesync.
// Returns an error if it doesn't exist — call ExtractEmbedded first if you have
// the binary embedded.
func FindSyncthing() (string, error) {
	if SyncthingPathOverride != "" {
		if _, err := os.Stat(SyncthingPathOverride); err != nil {
			return "", fmt.Errorf("syncthing override path missing: %s", SyncthingPathOverride)
		}
		return SyncthingPathOverride, nil
	}
	dir, err := ExeDir()
	if err != nil {
		return "", err
	}
	name := "syncthing"
	if runtime.GOOS == "windows" {
		name = "syncthing.exe"
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("syncthing not found at %s", path)
	}
	return path, nil
}

// ExtractEmbedded writes the embedded Syncthing binary next to wesync if it
// doesn't already exist. Returns the path to the extracted binary.
func ExtractEmbedded(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("no embedded Syncthing binary for this platform")
	}
	dir, err := ExeDir()
	if err != nil {
		return "", err
	}
	name := "syncthing"
	if runtime.GOOS == "windows" {
		name = "syncthing.exe"
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil // already extracted
	}
	log.Printf("stmanager: extracting bundled Syncthing to %s", path)
	if err := os.WriteFile(path, data, 0755); err != nil {
		return "", fmt.Errorf("extract syncthing: %w", err)
	}
	return path, nil
}

// home is the Syncthing home directory inside WeSync's shared data dir.
func home() string {
	return filepath.Join(DataDir(), "syncthing")
}

// APIKey reads the Syncthing API key from WeSync's Syncthing config.
func APIKey() (string, error) {
	return readAPIKey(filepath.Join(home(), "config.xml"))
}

// EnsureReady generates Syncthing's config if it doesn't exist yet and
// patches the GUI port to APIPort (8385).
func EnsureReady(stExe string) error {
	h := home()
	configPath := filepath.Join(h, "config.xml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("stmanager: first run — generating Syncthing config in %s", h)
		if err := os.MkdirAll(h, 0700); err != nil {
			return fmt.Errorf("create syncthing home: %w", err)
		}
		out, err := exec.Command(stExe, "generate", "--home="+h).CombinedOutput()
		if err != nil {
			return fmt.Errorf("syncthing generate: %w\n%s", err, out)
		}
		log.Printf("stmanager: config generated")
	}

	return patchPort(configPath)
}

// Start launches Syncthing as a subprocess and waits until its API is reachable.
// If Syncthing is already running on APIPort AND responds to our API key, Start
// returns immediately. Otherwise it kills the occupant and starts fresh.
//
// The subprocess is independent of any context: it keeps running until Stop()
// is called or the host process exits. This lets the power-gate logic restart
// ST at will without tearing down the backend goroutine.
func Start(stExe string) error {
	// Claim the start latch. `running` and `starting` are both mu-guarded; we
	// avoid current.ProcessState (owned by cmd.Wait in the reaper, so reading it
	// would race). Holding the latch for the whole Start serializes concurrent
	// callers so they can't each launch a second Syncthing.
	mu.Lock()
	if running || starting {
		mu.Unlock()
		return nil // already running, or another Start is in flight — idempotent
	}
	starting = true
	mu.Unlock()
	defer func() {
		mu.Lock()
		starting = false
		mu.Unlock()
	}()

	// Something already on our port (e.g. survived a host crash)? If it's
	// verifiably ours, adopt it; otherwise kill the stale instance first.
	if portOpen() {
		if apiKeyValid() {
			log.Printf("stmanager: Syncthing already running on :%d (verified) — adopting", APIPort)
			mu.Lock()
			running = true
			mu.Unlock()
			return nil
		}
		log.Printf("stmanager: port %d occupied with wrong API key — killing stale Syncthing", APIPort)
		killOurSyncthing(stExe)
		waitPortFree()
	}

	cmd, err := launch(stExe)
	if err != nil {
		return err
	}
	return awaitReady(cmd)
}

// launch starts the Syncthing subprocess, records it as current+running, and
// registers the reaper goroutine immediately so a process that dies during the
// readiness wait is still reaped and clears the state.
func launch(stExe string) (*exec.Cmd, error) {
	h := home()
	log.Printf("stmanager: starting Syncthing (home=%s port=%d)", h, APIPort)

	cmd := exec.Command(stExe,
		"serve",
		"--home="+h,
		"--no-browser",
		"--no-restart",
		"--no-upgrade",
	)
	// Stdout/Stderr left nil — exec connects them to the null device, discarding
	// Syncthing's own logging (it writes to its home dir as well).
	configureCmd(cmd) // suppress console window on Windows
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start syncthing: %w", err)
	}
	log.Printf("stmanager: Syncthing pid=%d", cmd.Process.Pid)

	mu.Lock()
	current = cmd
	running = true
	mu.Unlock()

	// Reap on exit so we don't accumulate zombies and so state clears when ST
	// dies on its own. Stop() also clears current to keep Start idempotent.
	go func() {
		cmd.Wait() //nolint:errcheck
		log.Printf("stmanager: Syncthing exited (pid=%d)", cmd.Process.Pid)
		mu.Lock()
		if current == cmd {
			current = nil
			running = false
		}
		mu.Unlock()
	}()
	return cmd, nil
}

// awaitReady waits for the launched Syncthing's API to answer. It bails early if
// the process exits or Stop() clears the state underneath it, and kills the
// half-started process if the deadline passes so the next Start can try fresh.
func awaitReady(cmd *exec.Cmd) error {
	deadline := time.Now().Add(startDeadline)
	for {
		mu.Lock()
		aborted := !running || current != cmd
		mu.Unlock()
		if aborted {
			return fmt.Errorf("syncthing start aborted (process exited or stopped)")
		}
		if portOpen() {
			log.Printf("stmanager: Syncthing API ready on :%d", APIPort)
			return nil
		}
		if time.Now().After(deadline) {
			Stop() //nolint:errcheck
			return fmt.Errorf("timed out waiting for Syncthing on :%d", APIPort)
		}
		time.Sleep(readyPollEvery)
	}
}

// portOpen reports whether something is accepting connections on the API port.
func portOpen() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", APIPort), dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitPortFree blocks until the API port stops accepting connections, or until
// portFreeWait elapses — so we don't try to launch before a killed occupant has
// released the port.
func waitPortFree() {
	deadline := time.Now().Add(portFreeWait)
	for portOpen() {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(readyPollEvery)
	}
}

// Stop terminates the managed Syncthing process if running. Safe to call
// multiple times. Blocks briefly while the SIGTERM/SIGKILL is delivered;
// returns once the process has exited or the kill failed.
func Stop() error {
	mu.Lock()
	c := current
	current = nil
	running = false
	mu.Unlock()
	// NOTE: do NOT early-return when c == nil — the whole point is to still sweep
	// an adopted/orphaned instance below in that case.
	if c != nil && c.Process != nil {
		log.Printf("stmanager: stopping Syncthing (pid=%d)", c.Process.Pid)
		if err := c.Process.Kill(); err != nil {
			log.Printf("stmanager: kill: %v", err)
		}
		_, _ = c.Process.Wait()
	}
	// Belt-and-braces (Android): also terminate any Syncthing started with OUR
	// home dir that we DON'T hold a handle for — one orphaned when the OS
	// reclaimed the app process and then adopted on a later Start (current==nil).
	// The handle kill above can't touch that, so without this an adopted ST kept
	// running forever while the gate believed it had stopped — the peer saw us
	// connected the whole time. This makes Stop() mean "our Syncthing is gone".
	killSyncthingByHome(home())
	return nil
}

// IsRunning reports whether Start has succeeded and Stop hasn't.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return running
}

// apiKeyValid returns true if a Syncthing on APIPort responds to our stored key.
func apiKeyValid() bool {
	key, err := APIKey()
	if err != nil {
		return false
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/rest/system/ping", APIPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-API-Key", key)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// killOurSyncthing terminates the syncthing process that lives next to wesync.
// It does NOT kill syncthing processes from other locations.
func killOurSyncthing(stExe string) {
	abs, err := filepath.Abs(stExe)
	if err != nil {
		return
	}
	// Use os.FindProcess-style check: walk processes, kill matching path.
	// Cross-platform: on Windows we use taskkill with a path filter via PowerShell.
	// On Linux/macOS we use pkill with the full path.
	killByPath(abs)
}

// ── helpers ───────────────────────────────────────────────────────────────────

type stGUIConfig struct {
	XMLName xml.Name `xml:"configuration"`
	GUI     struct {
		APIKey  string `xml:"apikey"`
		Address string `xml:"address"`
	} `xml:"gui"`
}

func readAPIKey(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var cfg stGUIConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	if cfg.GUI.APIKey == "" {
		return "", fmt.Errorf("no API key in %s", configPath)
	}
	return cfg.GUI.APIKey, nil
}

// STFolder is the subset of a Syncthing folder the power gate needs while ST
// is asleep: where it lives (to watch) and its direction (to decide whether a
// backstop tick has anything to do — a sendonly folder with nothing pending
// has nothing to receive, so there's no reason to wake ST for it).
type STFolder struct {
	ID   string
	Path string
	Type string // sendonly | receiveonly | sendreceive (empty ⇒ ST default sendreceive)
}

// Folders reads the folder set straight from Syncthing's config.xml. It works
// while ST is stopped — the poll snapshot scan needs folder paths without the
// REST API being up. config.xml is ST's own source of truth, so we read it
// where it lives rather than caching a copy.
func Folders() ([]STFolder, error) {
	return readFolders(filepath.Join(home(), "config.xml"))
}

func readFolders(configPath string) ([]STFolder, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		XMLName xml.Name `xml:"configuration"`
		Folders []struct {
			ID   string `xml:"id,attr"`
			Path string `xml:"path,attr"`
			Type string `xml:"type,attr"`
		} `xml:"folder"`
	}
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	out := make([]STFolder, 0, len(cfg.Folders))
	for _, f := range cfg.Folders {
		out = append(out, STFolder{ID: f.ID, Path: f.Path, Type: f.Type})
	}
	return out, nil
}

// patchPort rewrites the GUI <address> in config.xml to 127.0.0.1:APIPort.
func patchPort(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	target := fmt.Sprintf("127.0.0.1:%d", APIPort)

	var cfg stGUIConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.GUI.Address == target {
		return nil // already correct
	}

	// Replace the address tag value in the raw XML to preserve all other content.
	from := fmt.Sprintf("<address>%s</address>", cfg.GUI.Address)
	to := fmt.Sprintf("<address>%s</address>", target)
	content := strings.ReplaceAll(string(data), from, to)
	if content == string(data) {
		// The literal tag we parsed wasn't found verbatim (e.g. extra whitespace
		// or attributes), so the port was NOT patched — fail loudly rather than
		// silently leaving ST on the wrong port.
		return fmt.Errorf("patchPort: could not find %q in %s", from, configPath)
	}

	log.Printf("stmanager: patched Syncthing GUI address %s → %s", cfg.GUI.Address, target)
	return os.WriteFile(configPath, []byte(content), 0600)
}
