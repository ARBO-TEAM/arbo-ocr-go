package arboocr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ARBO-TEAM/arbo-ocr-go/installer"
)

// Config configures an Engine: where to find the arboocr_demo binary and
// which CLI flags to pass it on every Recognize call.
type Config struct {
	BinPath      string // explicit path to arboocr_demo; empty = lazily download via installer.EnsureInstalled into the default cache dir
	ModelsDir    string
	OcrVersion   string
	ModelType    string
	UseAngleCls  bool
	UseCuda      bool
	UseTensorrt  bool
	UseFp16      bool
	UseClahe     bool
	DetModelPath string
	ClsModelPath string
	RecModelPath string
	DictPath     string
}

// Engine runs the prebuilt arboocr_demo binary via os/exec and parses its
// --json output. Requires no C++ build — only the binary
// installer.EnsureInstalled downloaded (or one you point at manually via
// Config.BinPath).
type Engine struct {
	binPath string
	cfg     Config
}

// NewEngine resolves the binary path — using cfg.BinPath as-is if set, or
// lazily downloading via installer.EnsureInstalled("") if cfg.BinPath is
// empty — and returns a ready Engine, or an error if the binary can't be
// found/installed.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.BinPath != "" {
		if _, err := os.Stat(cfg.BinPath); err != nil {
			return nil, &OcrError{
				Message: fmt.Sprintf("arboocr_demo binary not found at %s. Pass a valid BinPath or leave it empty to auto-install.", cfg.BinPath),
			}
		}
		return &Engine{binPath: cfg.BinPath, cfg: cfg}, nil
	}

	binPath, err := installer.EnsureInstalled("")
	if err != nil {
		return nil, err
	}
	return &Engine{binPath: binPath, cfg: cfg}, nil
}

// Recognize runs `arboocr_demo --image <imagePath> --json` (plus flags
// derived from Config) as a subprocess and parses its JSON stdout into a
// PageResult. Returns a non-nil error (of type *OcrError) only when the
// process can't be started, exits non-zero, or its stdout isn't valid JSON.
// An empty PageResult.Lines is a normal, successful result — not an error.
func (e *Engine) Recognize(imagePath string) (*PageResult, error) {
	args := []string{"--image", imagePath, "--json"}
	args = append(args, e.flagsFromConfig()...)

	cmd := exec.Command(e.binPath, args...)

	// Buffer-based capture (cmd.Stdout/cmd.Stderr set to plain io.Writer
	// values, then cmd.Run()) rather than cmd.StdoutPipe()/cmd.StderrPipe()
	// plus sequential reads. arboocr_demo can write ~200KB of ONNXRuntime
	// schema-registration warnings to stderr before producing any stdout —
	// well past a pipe's OS buffer (~64KB) — so reading stdout to
	// completion before touching stderr would deadlock the child (blocked
	// writing a full stderr pipe) against the parent (blocked waiting on
	// stdout that never arrives). os/exec copies both streams concurrently
	// via internal goroutines whenever Stdout/Stderr are plain io.Writer
	// values, so this pattern has no deadlock risk at all.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			return nil, &OcrError{
				Message:  fmt.Sprintf("arboocr_demo exited with code %d", exitCode),
				ExitCode: exitCode,
				Stderr:   stderr.String(),
			}
		}
		return nil, &OcrError{
			Message: fmt.Sprintf("could not start process: %v", err),
		}
	}

	trimmed := strings.TrimSpace(stdout.String())
	var result PageResult
	if jsonErr := json.Unmarshal([]byte(trimmed), &result); jsonErr != nil {
		raw := stdout.Bytes()
		if len(raw) > 500 {
			raw = raw[:500]
		}
		return nil, &OcrError{
			Message: fmt.Sprintf("arboocr_demo --json produced unparseable output: %s", raw),
		}
	}

	return &result, nil
}

// flagsFromConfig mirrors Engine.php's flagsFromOptions(): string fields
// emit "--flag-name", "<value>" only when non-empty; the five bool fields
// always emit "--flag-name", "true"/"false" since Config has no way to
// represent "unset" for a bool.
func (e *Engine) flagsFromConfig() []string {
	var flags []string

	stringFlags := []struct {
		value string
		flag  string
	}{
		{e.cfg.ModelsDir, "models-dir"},
		{e.cfg.OcrVersion, "ocr-version"},
		{e.cfg.ModelType, "model-type"},
		{e.cfg.DetModelPath, "det-model"},
		{e.cfg.ClsModelPath, "cls-model"},
		{e.cfg.RecModelPath, "rec-model"},
		{e.cfg.DictPath, "dict"},
	}
	for _, sf := range stringFlags {
		if sf.value != "" {
			flags = append(flags, "--"+sf.flag, sf.value)
		}
	}

	boolFlags := []struct {
		value bool
		flag  string
	}{
		{e.cfg.UseAngleCls, "angle"},
		{e.cfg.UseCuda, "cuda"},
		{e.cfg.UseTensorrt, "tensorrt"},
		{e.cfg.UseFp16, "fp16"},
		{e.cfg.UseClahe, "clahe"},
	}
	for _, bf := range boolFlags {
		flags = append(flags, "--"+bf.flag, boolToString(bf.value))
	}

	return flags
}

func boolToString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
