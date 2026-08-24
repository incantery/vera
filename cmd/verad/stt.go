// Speech to text, on this machine.
//
// The phone captures audio — the one thing a phone's microphone does
// reliably — and sends it here. Recognition happens on the Mac, not on
// the phone and not in a cloud: Apple's on-device recogniser fought us
// with session caps and dropped audio, and a cloud would break the one
// promise this project has kept, that what you say does not leave your
// machines on the way between them.
//
// The engine is Parakeet (parakeet-mlx), which vera INSTALLS and MANAGES
// rather than requires: uv puts it in its own environment with its own
// Python, so nothing on the system is touched, and the model downloads
// once. `Status` is what the "download and install" surface reads;
// `Install` is what its button runs.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// STTStatus is the managed engine's state, for the install surface.
type STTStatus struct {
	Engine     string `json:"engine"`
	UV         bool   `json:"uv"`          // the manager is present
	Installed  bool   `json:"installed"`   // the engine is installed
	ModelReady bool   `json:"model_ready"` // the model is downloaded
	Ready      bool   `json:"ready"`
	Detail     string `json:"detail,omitempty"`
	// Installing is set while an install is under way, so two taps do
	// not start two downloads.
	Installing bool `json:"installing,omitempty"`
}

// Transcriber turns audio into words.
type Transcriber interface {
	// Transcribe reads an audio file (any format ffmpeg can) and returns
	// what was said.
	Transcribe(ctx context.Context, audioPath string) (string, error)
	Status(ctx context.Context) STTStatus
	// Install sets the engine up, reporting progress in words a person
	// can read. It is safe to call when already installed.
	Install(ctx context.Context, progress func(string)) error
}

const (
	parakeetModel = "mlx-community/parakeet-tdt-0.6b-v3"
	parakeetPy    = "3.12" // MLX has no wheels for the system's newest Python
)

// Parakeet is the parakeet-mlx engine, managed through uv.
type Parakeet struct {
	installing bool
}

func newParakeet() *Parakeet { return &Parakeet{} }

func (p *Parakeet) Status(ctx context.Context) STTStatus {
	s := STTStatus{Engine: "parakeet", Installing: p.installing}
	uv, err := exec.LookPath("uv")
	if err != nil {
		s.Detail = "needs uv (the installer) — https://docs.astral.sh/uv"
		return s
	}
	s.UV = true
	if bin := parakeetBin(); bin != "" {
		s.Installed = true
		if p.modelReady() {
			s.ModelReady = true
			s.Ready = true
			s.Detail = bin
		} else {
			s.Detail = "installed; the model downloads on first use"
		}
	} else {
		s.Detail = "not installed (" + uv + " will fetch it)"
	}
	return s
}

// parakeetBin finds the installed executable, uv's bin dir included —
// it is not always on this process's PATH.
func parakeetBin() string {
	if bin, err := exec.LookPath("parakeet-mlx"); err == nil {
		return bin
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, ".local", "bin", "parakeet-mlx"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// modelReady looks for the downloaded model in the HuggingFace cache.
func (p *Parakeet) modelReady() bool {
	home, _ := os.UserHomeDir()
	// hub stores "models--mlx-community--parakeet-tdt-0.6b-v3".
	dir := filepath.Join(home, ".cache", "huggingface", "hub",
		"models--"+strings.ReplaceAll(parakeetModel, "/", "--"))
	if entries, err := os.ReadDir(filepath.Join(dir, "snapshots")); err == nil && len(entries) > 0 {
		return true
	}
	return false
}

func (p *Parakeet) Install(ctx context.Context, progress func(string)) error {
	if p.installing {
		return fmt.Errorf("already installing")
	}
	p.installing = true
	defer func() { p.installing = false }()

	uv, err := exec.LookPath("uv")
	if err != nil {
		return fmt.Errorf("uv is not installed — get it from https://docs.astral.sh/uv, then try again")
	}

	if parakeetBin() == "" {
		progress("Installing Parakeet…")
		if err := runLines(ctx, progress, uv, "tool", "install", "parakeet-mlx", "--python", parakeetPy); err != nil {
			return fmt.Errorf("installing parakeet-mlx: %w", err)
		}
	}
	if !p.modelReady() {
		progress("Downloading the speech model (about 600 MB, once)…")
		if err := p.warm(ctx, progress); err != nil {
			return fmt.Errorf("downloading the model: %w", err)
		}
	}
	progress("Ready.")
	return nil
}

// warm runs one transcription of a moment of silence, which is what
// pulls the model down and loads it — turning "installed" into "ready".
func (p *Parakeet) warm(ctx context.Context, progress func(string)) error {
	silence, err := os.CreateTemp("", "vera-warm-*.wav")
	if err != nil {
		return err
	}
	defer os.Remove(silence.Name())
	silence.Close()
	// A half second of silence: ffmpeg is already a dependency of the
	// audio path, so leaning on it here costs nothing new.
	if err := runLines(ctx, progress, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "0.5", "-y", silence.Name()); err != nil {
		return err
	}
	_, err = p.Transcribe(ctx, silence.Name())
	return err
}

// Transcribe normalises the audio to 16 kHz mono with ffmpeg — the phone
// sends whatever it recorded — and runs parakeet-mlx over it.
func (p *Parakeet) Transcribe(ctx context.Context, audioPath string) (string, error) {
	bin := parakeetBin()
	if bin == "" {
		return "", fmt.Errorf("parakeet is not installed")
	}
	work, err := os.MkdirTemp("", "vera-stt-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)

	wav := filepath.Join(work, "in.wav")
	if err := run(ctx, "ffmpeg", "-v", "error", "-i", audioPath, "-ar", "16000", "-ac", "1", "-y", wav); err != nil {
		return "", fmt.Errorf("normalising audio: %w", err)
	}

	// parakeet writes <basename>.txt into the output dir.
	if err := run(ctx, bin, wav, "--output-format", "txt", "--output-dir", work); err != nil {
		return "", fmt.Errorf("parakeet: %w", err)
	}
	out, err := os.ReadFile(filepath.Join(work, "in.txt"))
	if err != nil {
		return "", fmt.Errorf("parakeet produced no transcript: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// run executes a command, discarding output, bounded by ctx.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", name, trim(string(out), 300))
	}
	return nil
}

// runLines streams a command's output to progress, line by line, so an
// install the person is watching does not look hung.
func runLines(ctx context.Context, progress func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	scan := bufio.NewScanner(stdout)
	var last string
	for scan.Scan() {
		last = strings.TrimSpace(scan.Text())
		if last != "" {
			progress(last)
		}
	}
	if err := cmd.Wait(); err != nil {
		if last != "" {
			return fmt.Errorf("%s (%w)", last, err)
		}
		return err
	}
	return nil
}
