package arboocr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

	// Accuracy/throughput knobs added in arboOCR v0.2.0. Each has a CLI-side
	// default, so the zero value means "don't pass the flag, use the binary's
	// default" — never "pass 0".
	MinConfidence   float64 // drop lines below this recognition confidence; CLI default 0.5
	RecBatchNum     int     // crops per recognition inference call; CLI default 6
	DetLimitSideLen int     // longest image side for detection resize; CLI default 960

	// LogLevel opts arboocr_demo into stderr logging: debug|info|warn|error.
	// Empty (the default) leaves it silent, which is v0.2.0's behaviour.
	LogLevel string

	// WordBoxes requests a polygon per word (per character for CJK),
	// surfaced as LineResult.Words. Off by default: it makes the JSON
	// noticeably larger and most callers only want line text.
	WordBoxes bool

	// Model auto-download passthroughs. These need the arboOCR release that
	// adds model auto-download — installer.EnsureInstalled still pins
	// v0.2.0, which predates it and exits 1 on an unknown option. Both are
	// therefore strictly opt-in: at their zero value flagsFromConfig emits
	// nothing at all, so a caller who never touches them builds the exact
	// same argv as before and keeps working against the pinned binary.
	//
	// NoDownload makes the binary fail instead of fetching a missing model —
	// the flag form of the ARBOOCR_OFFLINE=1 environment variable, useful for
	// air-gapped runs that must not silently reach the network.
	NoDownload bool

	// ModelsURL is a directory URL missing models are fetched from instead of
	// the default upstream location — point it at an internal mirror. Same
	// knob as the ARBOOCR_MODELS_URL environment variable.
	ModelsURL string
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

// EnsureModels runs `arboocr_demo --download-models` (plus the same
// Config-derived flags Recognize passes) to fetch the models for this
// Engine's OcrVersion/ModelType into arboOCR's model cache, then returns —
// the binary downloads and exits without doing any OCR. Call it from a
// Docker build step or at process startup so the first Recognize doesn't
// pay for the download.
//
// Deliberately the same shape as installer.EnsureInstalled: one blocking
// call, no progress reporting, idempotent — an already-cached model is a
// no-op, and the binary's own precedence rules still apply (an explicit
// DetModelPath/RecModelPath is never substituted by a download, and a file
// already present in ModelsDir wins without touching the network).
//
// Requires the arboOCR release that adds model auto-download.
// installer.EnsureInstalled still pins v0.2.0, which has no
// --download-models flag and will exit non-zero with a usage error — so
// until that pin is bumped, this only works against a newer binary supplied
// via Config.BinPath.
func (e *Engine) EnsureModels() error {
	args := append([]string{"--download-models"}, e.flagsFromConfig()...)

	cmd := exec.Command(e.binPath, args...)

	// Same buffered-capture rationale as Recognize: arboocr_demo's stderr can
	// run well past a pipe's OS buffer, and os/exec drains both streams
	// concurrently when they're plain io.Writer values.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			return &OcrError{
				Message:  fmt.Sprintf("arboocr_demo --download-models exited with code %d", exitCode),
				ExitCode: exitCode,
				Stderr:   stderr.String(),
			}
		}
		return &OcrError{
			Message: fmt.Sprintf("could not start process: %v", err),
		}
	}

	return nil
}

// flagsFromConfig mirrors Engine.php's flagsFromOptions(): string fields
// emit "--flag-name", "<value>" only when non-empty, and numeric fields
// only when non-zero, so an unset field leaves arboocr_demo's own default
// in place. The five original bool fields always emit
// "--flag-name", "true"/"false" since Config has no way to represent
// "unset" for a bool, and their CLI defaults are known-stable; WordBoxes
// and NoDownload are the exceptions and emit only when true (see below).
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
		{e.cfg.LogLevel, "log-level"},
		// --models-url postdates v0.2.0; the empty-string rule above is
		// exactly what keeps it off the argv for callers on the pinned binary.
		{e.cfg.ModelsURL, "models-url"},
	}
	for _, sf := range stringFlags {
		if sf.value != "" {
			flags = append(flags, "--"+sf.flag, sf.value)
		}
	}

	// Numeric flags follow the string rule, not the bool rule: arboocr_demo
	// gives each a non-zero default (min-confidence 0.5, rec-batch-num 6,
	// det-limit-side-len 960), so emitting the Go zero value would silently
	// override the binary's default for every caller who never set the field.
	// Only a non-zero value is an actual opt-in.
	if e.cfg.MinConfidence != 0 {
		flags = append(flags, "--min-confidence", strconv.FormatFloat(e.cfg.MinConfidence, 'g', -1, 64))
	}
	if e.cfg.RecBatchNum != 0 {
		flags = append(flags, "--rec-batch-num", strconv.Itoa(e.cfg.RecBatchNum))
	}
	if e.cfg.DetLimitSideLen != 0 {
		flags = append(flags, "--det-limit-side-len", strconv.Itoa(e.cfg.DetLimitSideLen))
	}

	// --word-boxes is emitted only when true, unlike the always-emitted bool
	// flags below: it is new in arboOCR v0.2.0, and unconditionally passing
	// "--word-boxes=false" would make an explicitly-set Config.BinPath
	// pointing at an older binary fail on an unknown option.
	if e.cfg.WordBoxes {
		flags = append(flags, "--word-boxes=true")
	}

	// --no-download follows the same only-when-true rule as --word-boxes, and
	// for a stronger version of the same reason: it postdates v0.2.0
	// entirely, so emitting "--no-download=false" would break every caller
	// still on the pinned binary — including the ones who never asked for
	// anything to do with downloads.
	if e.cfg.NoDownload {
		flags = append(flags, "--no-download=true")
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
		// cxxopts only binds a bool flag's value via "=" — "--angle" "false"
		// as two argv tokens leaves --angle implicitly true (bare flag) and
		// "false" as an ignored stray positional. Single-token form is the
		// only form that actually works.
		flags = append(flags, "--"+bf.flag+"="+boolToString(bf.value))
	}

	return flags
}

func boolToString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
