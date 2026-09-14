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

	// Model auto-download passthroughs, live against the pinned binary:
	// installer.EnsureInstalled pins arboOCR v0.4.0, which includes the v0.3.0
	// release that added the feature. Both stay strictly opt-in anyway: at
	// their zero value
	// flagsFromConfig emits nothing at all, so a caller who never touches
	// them builds the exact same argv as before, which is what keeps a
	// Config.BinPath pointed at a pre-v0.3.0 binary working — cxxopts exits 1
	// on an unknown option.
	//
	// NoDownload makes the binary fail instead of fetching a missing model —
	// the flag form of the ARBOOCR_OFFLINE=1 environment variable, useful for
	// air-gapped runs that must not silently reach the network.
	NoDownload bool

	// ModelsURL is a directory URL missing models are fetched from instead of
	// the default upstream location — point it at an internal mirror. Same
	// knob as the ARBOOCR_MODELS_URL environment variable.
	ModelsURL string

	// The three knobs below are new in arboOCR v0.4.0. Each is emitted only
	// when the caller explicitly opts in, the same rule WordBoxes/NoDownload
	// follow and for the same load-bearing reason: cxxopts exits 1 with a
	// usage error on an unknown option, so an unconditional
	// "--min-det-box-area 20"/"--space-recovery=false" would break every
	// caller who points Config.BinPath at an older binary.
	//
	// MinDetBoxArea drops det boxes at or below this area in detector-input
	// pixels. It is a pointer, not a plain float64, because 0 is a meaningful
	// value here — it disables the cut ("0 disables; ppu default 20") — so a
	// float64's zero value could not distinguish "disable the filter" from
	// "unset". nil means unset (emit nothing, keep the binary's default); any
	// non-nil value, 0 included, is an explicit opt-in emitted verbatim.
	MinDetBoxArea *float64

	// SpaceRecovery emits the inter-word spaces a greedy CTC decode swallows.
	// Only an explicit true reaches argv: false is the binary's own default,
	// so false and unset are indistinguishable, and a pre-v0.4.0 binary has no
	// such option at all.
	SpaceRecovery bool

	// EnableCPUMemArena leaves ORT's CPU memory arena on: faster, higher RSS.
	// Opt-in only, exactly like SpaceRecovery — true emits
	// "--enable-cpu-mem-arena=true", false and unset emit nothing.
	EnableCPUMemArena bool
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

// RecognizeBatch runs one arboocr_demo process over many images
// (`--images-from <list> --json`) and returns one PageResult per input, in
// input order.
//
// The saving is the process start and model load, which a one-shot Recognize
// pays in full on every call — the recognizer/detector init dominates a short
// page. Measured over 5 SROIE receipts it was 13.0% of wall time at ModelType
// "tiny", 28.5% at "small" and 13.6% at "medium", with identical text on every
// image (compare/bench_batch_go.py). That share is (process start + model
// load) / total, so it varies with the model size and the list length rather
// than being a fixed percentage.
//
// Results are matched to inputs by position: the binary's "image" field
// carries only a basename, so two same-named files in different directories
// would be indistinguishable. Positional matching is only sound when the
// counts agree, so a mismatch is returned as an error rather than a shifted
// list.
//
// An empty PageResult.Lines is a normal, successful result here exactly as in
// Recognize. So is the process exit status: a batch exits 1 when *any* image
// came back empty, which is tolerated as long as stdout still holds the JSON
// array.
func (e *Engine) RecognizeBatch(imagePaths []string) ([]PageResult, error) {
	if len(imagePaths) == 0 {
		return nil, nil
	}

	// The list file is newline-delimited and the binary treats blank lines and
	// '#' lines as comments, so a path in either shape would be silently
	// dropped and shift every later result onto the wrong input. Rejecting
	// beats mis-attributing text to the wrong file.
	for i, p := range imagePaths {
		switch {
		case p == "":
			return nil, &OcrError{
				Message: fmt.Sprintf("RecognizeBatch: imagePaths[%d] is empty", i),
			}
		case strings.ContainsAny(p, "\r\n"):
			return nil, &OcrError{
				Message: fmt.Sprintf("RecognizeBatch: imagePaths[%d] contains a newline, which the image list format cannot represent: %q", i, p),
			}
		case strings.HasPrefix(strings.TrimLeft(p, " \t"), "#"):
			return nil, &OcrError{
				Message: fmt.Sprintf("RecognizeBatch: imagePaths[%d] starts with '#', which arboocr_demo reads as a comment and would skip: %q", i, p),
			}
		}
	}

	listFile, err := os.CreateTemp("", "arbo-ocr-go-*.txt")
	if err != nil {
		return nil, &OcrError{Message: fmt.Sprintf("could not create image list: %v", err)}
	}
	listPath := listFile.Name()
	defer os.Remove(listPath)

	if _, err := listFile.WriteString(strings.Join(imagePaths, "\n") + "\n"); err != nil {
		listFile.Close()
		return nil, &OcrError{Message: fmt.Sprintf("could not write image list: %v", err)}
	}
	if err := listFile.Close(); err != nil {
		return nil, &OcrError{Message: fmt.Sprintf("could not write image list: %v", err)}
	}

	args := append([]string{"--images-from", listPath, "--json"}, e.flagsFromConfig()...)
	cmd := exec.Command(e.binPath, args...)

	// Same buffered-capture rationale as Recognize: cmd.Stdout/cmd.Stderr as
	// plain io.Writer values let os/exec drain both concurrently, so a full
	// stderr pipe can't deadlock the child against the parent.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	trimmed := strings.TrimSpace(stdout.String())

	if runErr != nil {
		// The binary overloads exit 1: "a page had no text" and "you passed a
		// bad flag" share it. Only the first leaves the JSON array on stdout,
		// so requiring that payload keeps a usage error an error.
		ok := false
		exitErr, isExitErr := runErr.(*exec.ExitError)
		if isExitErr && exitErr.ExitCode() == 1 {
			ok = strings.HasPrefix(trimmed, "[")
		}
		if !ok {
			if isExitErr {
				exitCode := exitErr.ExitCode()
				return nil, &OcrError{
					Message:  fmt.Sprintf("arboocr_demo exited with code %d", exitCode),
					ExitCode: exitCode,
					Stderr:   stderr.String(),
				}
			}
			return nil, &OcrError{
				Message: fmt.Sprintf("could not start process: %v", runErr),
			}
		}
	}

	var results []PageResult
	if jsonErr := json.Unmarshal([]byte(trimmed), &results); jsonErr != nil {
		raw := trimmed
		if len(raw) > 500 {
			raw = raw[:500]
		}
		return nil, &OcrError{
			Message: fmt.Sprintf("arboocr_demo --images-from produced unparseable output: %s", raw),
		}
	}

	if len(results) != len(imagePaths) {
		return nil, &OcrError{
			Message: fmt.Sprintf(
				"arboocr_demo returned %d results for %d images; cannot match results to inputs by position",
				len(results), len(imagePaths),
			),
		}
	}

	return results, nil
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
// Works out of the box against the binary installer.EnsureInstalled
// downloads, which pins arboOCR v0.4.0 — a release that includes the v0.3.0
// version that added --download-models. A pre-v0.3.0 binary supplied via
// Config.BinPath has no such flag and exits non-zero with a usage error,
// returned here as an *OcrError carrying that stderr.
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
// "unset" for a bool, and their CLI defaults are known-stable; WordBoxes,
// NoDownload, SpaceRecovery and EnableCPUMemArena are the exceptions and
// emit only when true (see below), and MinDetBoxArea is the numeric
// exception: pointer-nil, not zero, is what marks it unset.
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
		// --models-url needs a v0.3.0-or-newer binary; the empty-string rule
		// above is exactly what keeps it off the argv for callers who point
		// Config.BinPath at an older one.
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

	// --min-det-box-area breaks the numeric rule above on purpose. The flag is
	// new in arboOCR v0.4.0, so it must never be emitted for a caller who
	// didn't ask for it (an unknown option makes cxxopts exit 1), and its
	// "unset" test can't be "!= 0" either, because 0 is a real setting here —
	// it disables the box-area cut. The pointer is what tells the two apart:
	// nil = unset = emit nothing, non-nil = emit the value verbatim, 0
	// included, so "--min-det-box-area 0" is expressible.
	if e.cfg.MinDetBoxArea != nil {
		flags = append(flags, "--min-det-box-area", strconv.FormatFloat(*e.cfg.MinDetBoxArea, 'g', -1, 64))
	}

	// --word-boxes is emitted only when true, unlike the always-emitted bool
	// flags below: it is new in arboOCR v0.2.0, and unconditionally passing
	// "--word-boxes=false" would make an explicitly-set Config.BinPath
	// pointing at an older binary fail on an unknown option.
	if e.cfg.WordBoxes {
		flags = append(flags, "--word-boxes=true")
	}

	// --no-download follows the same only-when-true rule as --word-boxes, and
	// for a stronger version of the same reason: it does not exist at all
	// before v0.3.0, so emitting "--no-download=false" would break every
	// caller pointing Config.BinPath at an older binary — including the ones
	// who never asked for anything to do with downloads.
	if e.cfg.NoDownload {
		flags = append(flags, "--no-download=true")
	}

	// Same opt-in-only rule for the two v0.4.0 bools. false is the binary's
	// own default, so "--space-recovery=false" carries no information the
	// absent flag doesn't — and it is exactly the token a pre-v0.4.0 binary
	// would reject with a usage error. Only an explicit true is emitted, as a
	// single "--flag=true" token (cxxopts binds a bool's value only via "=",
	// same as the --word-boxes case above).
	if e.cfg.SpaceRecovery {
		flags = append(flags, "--space-recovery=true")
	}
	if e.cfg.EnableCPUMemArena {
		flags = append(flags, "--enable-cpu-mem-arena=true")
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
