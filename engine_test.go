package arboocr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cannedJSON is the canned successful PageResult JSON emitted by
// runFakeArboocrDemo for the "TESTOK" and "TESTNOISY" scenarios. It is
// deliberately the exact same payload for both, since TESTNOISY only exists
// to prove Engine.Recognize doesn't deadlock on a full stderr pipe — its
// stdout result should be indistinguishable from the plain happy path.
const cannedJSON = `{"backend":"cpu","image":"TESTOK","elapsedMs":12.5,"lines":[{"text":"hello","score":0.9,"detScore":0.8,"polygon":[{"x":1,"y":2}]}]}` + "\n"

// TestMain lets this test binary double as a stand-in for arboocr_demo.
// When Engine.Recognize (invoked with Config.BinPath == os.Args[0]) re-execs
// this binary, the child inherits ARBOOCR_TEST_HELPER=1 from the parent's
// environment and is routed straight into runFakeArboocrDemo instead of the
// normal go test machinery — no -test.run flag or wrapper script needed.
func TestMain(m *testing.M) {
	if os.Getenv("ARBOOCR_TEST_HELPER") == "1" {
		runFakeArboocrDemo()
		return // unreached: runFakeArboocrDemo always calls os.Exit
	}
	os.Exit(m.Run())
}

// runFakeArboocrDemo stands in for arboocr_demo. It inspects os.Args (the
// real argv Engine.Recognize builds: "--image", "<value>", "--json", plus
// Config-derived flags) and dispatches on the value passed after "--image".
// These values are sentinel strings the tests below pass as the imagePath
// argument to Recognize — not real image paths — since Recognize forwards
// imagePath into argv verbatim, giving a clean way to select fake scenarios
// without needing any extra flags Recognize doesn't otherwise expose.
func runFakeArboocrDemo() {
	// --download-models has no --image to dispatch on: it's a download-and-exit
	// mode, so it gets its own branch ahead of the image switch. Recording argv
	// to a file (path handed over by the parent via ARBOOCR_TEST_ARGV_FILE) is
	// how the test inspects the invocation — stdout is the wrong channel, since
	// the real binary writes nothing useful there in this mode.
	if hasArg("--download-models") {
		if path := os.Getenv("ARBOOCR_TEST_ARGV_FILE"); path != "" {
			_ = os.WriteFile(path, []byte(strings.Join(os.Args[1:], " ")), 0o644)
		}
		if os.Getenv("ARBOOCR_TEST_DOWNLOAD_FAILS") == "1" {
			// What a pre-v0.3.0 binary actually does with an unknown option.
			os.Stderr.WriteString("Option '--download-models' does not exist\n")
			os.Exit(1)
		}
		os.Exit(0)
	}

	// --images-from is batch mode: one process over a newline-delimited list
	// file, one JSON array on stdout in list order — which is what lets the
	// tests below assert positional matching by echoing each path back as its
	// line's text.
	if listPath := argValue("--images-from"); listPath != "" {
		// What a bad flag actually does: exit 1 with no JSON on stdout. The
		// engine must not confuse this with the ordinary "a page came back
		// empty" exit 1, which does carry the array.
		if os.Getenv("ARBOOCR_TEST_BATCH_USAGE") == "1" {
			os.Stderr.WriteString("Option '--images-from' does not exist\n")
			os.Exit(1)
		}
		raw, err := os.ReadFile(listPath)
		if err != nil {
			os.Stderr.WriteString("cannot read image list\n")
			os.Exit(2)
		}
		var paths []string
		for _, l := range strings.Split(string(raw), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				paths = append(paths, l)
			}
		}
		if os.Getenv("ARBOOCR_TEST_BATCH_SHORT") == "1" && len(paths) > 0 {
			paths = paths[:len(paths)-1]
		}
		var b strings.Builder
		b.WriteString("[")
		for i, p := range paths {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"backend":"cpu","image":%q,"elapsedMs":12.5,"lines":[{"text":%q,"score":0.9,"detScore":0.8,"polygon":[{"x":1,"y":2}]}]}`,
				filepath.Base(p), p)
		}
		b.WriteString("]\n")
		os.Stdout.WriteString(b.String())

		// A batch exits 1 when *any* image came back empty — an ordinary
		// outcome, still carrying the JSON the caller asked for.
		if os.Getenv("ARBOOCR_TEST_BATCH_EXIT1") == "1" {
			os.Exit(1)
		}
		os.Exit(0)
	}

	image := ""
	for i, a := range os.Args {
		if a == "--image" && i+1 < len(os.Args) {
			image = os.Args[i+1]
			break
		}
	}

	switch image {
	case "TESTFAIL":
		os.Stderr.WriteString("simulated engine failure\n")
		os.Exit(2)
	case "TESTGARBAGE":
		os.Stdout.WriteString("not json\n")
		os.Exit(0)
	case "TESTNOISY":
		// Past a pipe's ~64KB OS buffer, written before any stdout — this is
		// what a sequential (read-stdout-then-stderr) implementation would
		// deadlock on, and what bytes.Buffer+cmd.Run() handles fine.
		os.Stderr.WriteString(strings.Repeat("noise\n", 20000))
		os.Stdout.WriteString(cannedJSON)
		os.Exit(0)
	default:
		// "TESTOK" and anything else (e.g. flag-building tests that don't
		// care about the scenario) get the plain canned success payload.
		os.Stdout.WriteString(cannedJSON)
		os.Exit(0)
	}
}

// hasArg reports whether argv contains name, either bare ("--download-models")
// or in cxxopts' single-token form ("--download-models=true").
func hasArg(name string) bool {
	for _, a := range os.Args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// argValue returns the value following a bare name in argv, or "" when absent.
func argValue(name string) string {
	for i, a := range os.Args {
		if a == name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func TestRecognizeParsesSuccessfulJSON(t *testing.T) {
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	result, err := eng.Recognize("TESTOK")
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}

	if result.Backend != "cpu" {
		t.Errorf("Backend = %q, want %q", result.Backend, "cpu")
	}
	if result.Image != "TESTOK" {
		t.Errorf("Image = %q, want %q", result.Image, "TESTOK")
	}
	if result.ElapsedMs != 12.5 {
		t.Errorf("ElapsedMs = %v, want %v", result.ElapsedMs, 12.5)
	}
	if len(result.Lines) != 1 {
		t.Fatalf("len(Lines) = %d, want 1", len(result.Lines))
	}

	line := result.Lines[0]
	if line.Text != "hello" {
		t.Errorf("Text = %q, want %q", line.Text, "hello")
	}
	if line.Score != 0.9 {
		t.Errorf("Score = %v, want %v", line.Score, 0.9)
	}
	if line.DetScore != 0.8 {
		t.Errorf("DetScore = %v, want %v", line.DetScore, 0.8)
	}
	if len(line.Polygon) != 1 || line.Polygon[0] != (Point{X: 1, Y: 2}) {
		t.Errorf("Polygon[0] = %+v, want {X:1 Y:2}", line.Polygon)
	}
}

func TestRecognizeReturnsErrorOnNonZeroExit(t *testing.T) {
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = eng.Recognize("TESTFAIL")
	if err == nil {
		t.Fatal("Recognize: want error, got nil")
	}

	var ocrErr *OcrError
	if !errors.As(err, &ocrErr) {
		t.Fatalf("error type = %T, want *OcrError (err: %v)", err, err)
	}
	if ocrErr.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", ocrErr.ExitCode)
	}
	if !strings.Contains(ocrErr.Error(), "exited with code") {
		t.Errorf("Error() = %q, want it to contain %q", ocrErr.Error(), "exited with code")
	}
}

func TestRecognizeReturnsErrorOnUnparseableOutput(t *testing.T) {
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = eng.Recognize("TESTGARBAGE")
	if err == nil {
		t.Fatal("Recognize: want error, got nil")
	}

	var ocrErr *OcrError
	if !errors.As(err, &ocrErr) {
		t.Fatalf("error type = %T, want non-nil *OcrError (err: %v)", err, err)
	}
	if ocrErr == nil {
		t.Fatal("OcrError: want non-nil")
	}
}

func TestNewEngineErrorsWhenBinPathMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-binary")

	eng, err := NewEngine(Config{BinPath: missing})
	if err != nil {
		// engine.go validates BinPath eagerly inside NewEngine — that alone
		// satisfies this test, and there's nothing further to check.
		return
	}
	if eng == nil {
		t.Fatal("NewEngine: got nil error and nil *Engine")
	}

	// engine.go defers validation to first use — Recognize must be the one
	// to error, and it must do so without hanging or reaching the network.
	if _, err := eng.Recognize("TESTOK"); err == nil {
		t.Fatal("Recognize: want error for missing binary, got nil")
	}
}

func TestRecognizeDoesNotDeadlockOnLargeStderr(t *testing.T) {
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	type outcome struct {
		result *PageResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := eng.Recognize("TESTNOISY")
		done <- outcome{result, err}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("Recognize: %v", o.err)
		}
		if o.result.Backend != "cpu" {
			t.Errorf("Backend = %q, want %q", o.result.Backend, "cpu")
		}
		if o.result.Image != "TESTOK" {
			t.Errorf("Image = %q, want %q", o.result.Image, "TESTOK")
		}
		if o.result.ElapsedMs != 12.5 {
			t.Errorf("ElapsedMs = %v, want %v", o.result.ElapsedMs, 12.5)
		}
		if len(o.result.Lines) != 1 || o.result.Lines[0].Text != "hello" {
			t.Errorf("Lines = %+v, want one line with Text %q", o.result.Lines, "hello")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Recognize deadlocked")
	}
}

func TestBoolFlagsUseSingleTokenForm(t *testing.T) {
	// Regression test: cxxopts binds a bool flag's value only via "=".
	// "--angle" "false" as two argv tokens leaves --angle implicitly true
	// (confirmed against the real arboocr_demo binary) — flagsFromConfig
	// must never emit that shape.
	eng := &Engine{cfg: Config{
		UseAngleCls: false,
		UseCuda:     true,
		UseTensorrt: false,
		UseFp16:     false,
		UseClahe:    true,
	}}
	flags := eng.flagsFromConfig()

	want := map[string]bool{
		"--angle=false":    false,
		"--cuda=true":      false,
		"--tensorrt=false": false,
		"--fp16=false":     false,
		"--clahe=true":     false,
	}
	for _, f := range flags {
		if strings.HasPrefix(f, "--angle") || strings.HasPrefix(f, "--cuda") ||
			strings.HasPrefix(f, "--tensorrt") || strings.HasPrefix(f, "--fp16") ||
			strings.HasPrefix(f, "--clahe") {
			if !strings.Contains(f, "=") {
				t.Errorf("bool flag %q must use --flag=value form, not a bare flag", f)
			}
			if _, ok := want[f]; !ok {
				t.Errorf("unexpected flag %q", f)
			}
			delete(want, f)
		}
	}
	for missing := range want {
		t.Errorf("missing expected flag %q", missing)
	}
}

func TestConfigFlagsReachSubprocess(t *testing.T) {
	// Indirect check, mirroring EngineTest.php's
	// testFlagsFromOptionsMapToCliFlags: the fake process only reads
	// --image, ignoring any other well-formed flags, so this mainly proves
	// flag-building from Config doesn't corrupt the invocation.
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	eng, err := NewEngine(Config{
		BinPath:     os.Args[0],
		ModelsDir:   "models",
		OcrVersion:  "PP-OCRv4",
		ModelType:   "mobile",
		UseAngleCls: true,
		UseCuda:     false,
		UseTensorrt: false,
		UseFp16:     false,
		UseClahe:    true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	result, err := eng.Recognize("TESTOK")
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if result.Backend != "cpu" {
		t.Errorf("Backend = %q, want %q", result.Backend, "cpu")
	}
}

func TestZeroValueTuningFlagsAreOmitted(t *testing.T) {
	// arboocr_demo defaults min-confidence to 0.5, rec-batch-num to 6 and
	// det-limit-side-len to 960. Emitting the Go zero value would override
	// each of those for callers who never set the field, so an unset field
	// must produce no flag at all. --word-boxes and --log-level are likewise
	// opt-in only.
	eng := &Engine{cfg: Config{}}
	joined := strings.Join(eng.flagsFromConfig(), " ")

	for _, flag := range []string{
		"--min-confidence", "--rec-batch-num", "--det-limit-side-len",
		"--word-boxes", "--log-level",
		"--min-det-box-area", "--space-recovery", "--enable-cpu-mem-arena",
	} {
		if strings.Contains(joined, flag) {
			t.Errorf("zero-value Config emitted %s (flags: %s)", flag, joined)
		}
	}

	// The other direction for the v0.4.0 fields, spelled out: an explicit
	// false on either bool must be just as invisible as an unset one. false
	// is the binary's own default, so "--space-recovery=false" adds nothing
	// but is precisely the token a pre-v0.4.0 binary exits 1 on — and nil
	// MinDetBoxArea is the only way to say "leave the cut at the binary's
	// default 20".
	eng = &Engine{cfg: Config{
		MinDetBoxArea:     nil,
		SpaceRecovery:     false,
		EnableCPUMemArena: false,
	}}
	joined = strings.Join(eng.flagsFromConfig(), " ")
	for _, flag := range []string{
		"--min-det-box-area", "--space-recovery", "--enable-cpu-mem-arena",
	} {
		if strings.Contains(joined, flag) {
			t.Errorf("explicit-false/unset v0.4.0 Config emitted %s (flags: %s)", flag, joined)
		}
	}
}

func TestZeroValueEmitsNoModelDownloadFlags(t *testing.T) {
	// The load-bearing one for anybody pointing Config.BinPath at an older
	// binary: --no-download and --models-url only exist from arboOCR v0.3.0,
	// and cxxopts exits 1 with a usage error on an unknown option. A caller
	// who never sets NoDownload/ModelsURL must therefore produce argv that is
	// byte-for-byte what it was before those fields existed — not
	// "--no-download=false".
	eng := &Engine{cfg: Config{}}
	flags := eng.flagsFromConfig()
	joined := strings.Join(flags, " ")

	for _, flag := range []string{"--no-download", "--models-url"} {
		if strings.Contains(joined, flag) {
			t.Errorf("zero-value Config emitted %s (flags: %s)", flag, joined)
		}
	}

	// Pin the whole argv, not just the absence of the two new flags: a future
	// unconditional append anywhere in flagsFromConfig breaks pre-v0.3.0
	// binaries the same way, and only an exact-match assertion catches that.
	want := []string{
		"--angle=false", "--cuda=false", "--tensorrt=false",
		"--fp16=false", "--clahe=false",
	}
	if len(flags) != len(want) {
		t.Fatalf("zero-value Config produced %d flags, want exactly %d: %v", len(flags), len(want), flags)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flags[%d] = %q, want %q (full: %v)", i, flags[i], want[i], flags)
		}
	}
}

func TestModelDownloadFlagsEmittedWhenSet(t *testing.T) {
	eng := &Engine{cfg: Config{
		NoDownload: true,
		ModelsURL:  "https://mirror.internal/arboocr/models-v1/",
	}}
	joined := strings.Join(eng.flagsFromConfig(), " ")

	for _, want := range []string{
		// Single-token form for the bool, same as --word-boxes: cxxopts binds
		// a bool's value only via "=".
		"--no-download=true",
		"--models-url https://mirror.internal/arboocr/models-v1/",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("flags missing %q (got: %s)", want, joined)
		}
	}
}

func TestEnsureModelsPassesDownloadModelsFlag(t *testing.T) {
	// The fake arboocr_demo records its argv when invoked with
	// --download-models, so this asserts the real subprocess invocation
	// rather than just the flag builder.
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("ARBOOCR_TEST_ARGV_FILE", argvFile)

	eng, err := NewEngine(Config{
		BinPath:    os.Args[0],
		ModelType:  "small",
		OcrVersion: "PP-OCRv6",
		ModelsURL:  "https://mirror.internal/models/",
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := eng.EnsureModels(); err != nil {
		t.Fatalf("EnsureModels: %v", err)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading recorded argv: %v", err)
	}
	argv := string(raw)

	for _, want := range []string{
		"--download-models",
		"--model-type small",
		"--ocr-version PP-OCRv6",
		"--models-url https://mirror.internal/models/",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv missing %q (got: %s)", want, argv)
		}
	}
	if strings.Contains(argv, "--image") {
		t.Errorf("--download-models invocation must not pass --image (got: %s)", argv)
	}
}

func TestEnsureModelsReturnsOcrErrorOnNonZeroExit(t *testing.T) {
	// A pre-v0.3.0 binary has no --download-models flag and exits non-zero
	// with a usage error; callers must get a typed *OcrError carrying that
	// stderr, not a bare exec error.
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	t.Setenv("ARBOOCR_TEST_DOWNLOAD_FAILS", "1")

	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	err = eng.EnsureModels()
	if err == nil {
		t.Fatal("EnsureModels: want error, got nil")
	}

	var ocrErr *OcrError
	if !errors.As(err, &ocrErr) {
		t.Fatalf("error type = %T, want *OcrError (err: %v)", err, err)
	}
	if ocrErr.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", ocrErr.ExitCode)
	}
	if !strings.Contains(ocrErr.Error(), "--download-models") {
		t.Errorf("Error() = %q, want it to mention --download-models", ocrErr.Error())
	}
}

func TestTuningFlagsEmittedWhenSet(t *testing.T) {
	minDetBoxArea := 20.0
	eng := &Engine{cfg: Config{
		MinConfidence:     0.75,
		RecBatchNum:       12,
		DetLimitSideLen:   1280,
		LogLevel:          "warn",
		WordBoxes:         true,
		MinDetBoxArea:     &minDetBoxArea,
		SpaceRecovery:     true,
		EnableCPUMemArena: true,
	}}
	flags := eng.flagsFromConfig()
	joined := strings.Join(flags, " ")

	for _, want := range []string{
		"--min-confidence 0.75",
		"--rec-batch-num 12",
		"--det-limit-side-len 1280",
		"--log-level warn",
		"--word-boxes=true",
		"--min-det-box-area 20",
		// Single-token form, same as --word-boxes: cxxopts binds a bool's
		// value only via "=".
		"--space-recovery=true",
		"--enable-cpu-mem-arena=true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("flags missing %q (got: %s)", want, joined)
		}
	}

	// An explicit 0 is a setting, not an absence: it disables the box-area
	// cut. This is the whole reason MinDetBoxArea is a *float64 — under a
	// plain "!= 0" numeric rule the value could never reach the binary.
	disabled := 0.0
	eng = &Engine{cfg: Config{MinDetBoxArea: &disabled}}
	if joined = strings.Join(eng.flagsFromConfig(), " "); !strings.Contains(joined, "--min-det-box-area 0") {
		t.Errorf("MinDetBoxArea = 0 must emit %q (got: %s)", "--min-det-box-area 0", joined)
	}
}

func TestRecognizeBatchParsesArrayInInputOrder(t *testing.T) {
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// The fake echoes each list entry back as its line's text, which is the
	// only way to prove the result at index i really belongs to input i — the
	// whole point of the count check in RecognizeBatch.
	in := []string{"a.png", "b.png", "c.png"}
	results, err := eng.RecognizeBatch(in)
	if err != nil {
		t.Fatalf("RecognizeBatch: %v", err)
	}
	if len(results) != len(in) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(in))
	}
	for i, want := range in {
		if len(results[i].Lines) != 1 {
			t.Fatalf("results[%d].Lines = %+v, want one line", i, results[i].Lines)
		}
		if got := results[i].Lines[0].Text; got != want {
			t.Errorf("results[%d] text = %q, want %q — results are not in input order", i, got, want)
		}
		if results[i].ElapsedMs != 12.5 {
			t.Errorf("results[%d].ElapsedMs = %v, want 12.5", i, results[i].ElapsedMs)
		}
	}
}

func TestRecognizeBatchEmptyInputMakesNoProcess(t *testing.T) {
	// No helper env and no BinPath on purpose: if this reached exec.Command it
	// would fail to start rather than return an empty, successful result.
	eng := &Engine{cfg: Config{}}
	results, err := eng.RecognizeBatch(nil)
	if err != nil {
		t.Fatalf("RecognizeBatch(nil): %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestRecognizeBatchRejectsUnlistablePath(t *testing.T) {
	// A path the newline-delimited list format cannot carry would be silently
	// dropped by the binary and shift every later result onto the wrong input.
	eng := &Engine{cfg: Config{}}
	for name, bad := range map[string]string{
		"empty":        "",
		"newline":      "a\nb.png",
		"carriage":     "a\rb.png",
		"comment-like": "#not-an-image.png",
		"indented #":   "  #not-an-image.png",
	} {
		if _, err := eng.RecognizeBatch([]string{"ok.png", bad}); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}

func TestRecognizeBatchToleratesExit1WithJSON(t *testing.T) {
	// A batch exits 1 when any image came back empty. That is an ordinary
	// outcome for a receipt with a blank region, not a failure, and the JSON
	// the caller asked for is still on stdout.
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	t.Setenv("ARBOOCR_TEST_BATCH_EXIT1", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	results, err := eng.RecognizeBatch([]string{"a.png"})
	if err != nil {
		t.Fatalf("RecognizeBatch: %v", err)
	}
	if len(results) != 1 || results[0].Lines[0].Text != "a.png" {
		t.Errorf("results = %+v, want one result for a.png", results)
	}
}

func TestRecognizeBatchUsageErrorIsAnError(t *testing.T) {
	// Exit 1 is overloaded: "a page had no text" (JSON present) and "you
	// passed a bad flag" (no JSON) share it. Only the JSON-bearing form may be
	// tolerated.
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	t.Setenv("ARBOOCR_TEST_BATCH_USAGE", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = eng.RecognizeBatch([]string{"a.png"})
	if err == nil {
		t.Fatal("RecognizeBatch: want error, got nil")
	}
	var ocrErr *OcrError
	if !errors.As(err, &ocrErr) {
		t.Fatalf("error type = %T, want *OcrError (err: %v)", err, err)
	}
	if ocrErr.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", ocrErr.ExitCode)
	}
}

func TestRecognizeBatchCountMismatchIsFatal(t *testing.T) {
	// Results are matched by position, so fewer results than inputs would
	// silently attribute text to the wrong file. Refusing beats returning a
	// shifted list.
	t.Setenv("ARBOOCR_TEST_HELPER", "1")
	t.Setenv("ARBOOCR_TEST_BATCH_SHORT", "1")
	eng, err := NewEngine(Config{BinPath: os.Args[0]})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = eng.RecognizeBatch([]string{"a.png", "b.png", "c.png"})
	if err == nil {
		t.Fatal("RecognizeBatch: want error, got nil")
	}
	if !strings.Contains(err.Error(), "by position") {
		t.Errorf("Error() = %q, want it to explain the positional mismatch", err.Error())
	}
}

func TestWordBoxesParseIntoLineResult(t *testing.T) {
	// --word-boxes makes arboOCR add a "words" array per line; without it the
	// key is absent entirely and Words must stay nil.
	const withWords = `{"backend":"cpu","image":"a.png","elapsedMs":1,"lines":` +
		`[{"text":"hi there","score":0.9,"detScore":0.8,"polygon":[{"x":1,"y":2}],` +
		`"words":[{"text":"hi","score":0.91,"polygon":[{"x":1,"y":2}]},` +
		`{"text":"there","score":0.89,"polygon":[{"x":3,"y":4}]}]}]}`

	var got PageResult
	if err := json.Unmarshal([]byte(withWords), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Lines) != 1 || len(got.Lines[0].Words) != 2 {
		t.Fatalf("Words = %+v, want 2 entries", got.Lines)
	}
	if got.Lines[0].Words[1].Text != "there" || got.Lines[0].Words[1].Score != 0.89 {
		t.Errorf("Words[1] = %+v, want {there 0.89 ...}", got.Lines[0].Words[1])
	}
	if len(got.Lines[0].Words[0].Polygon) != 1 || got.Lines[0].Words[0].Polygon[0].X != 1 {
		t.Errorf("Words[0].Polygon = %+v, want one point at x=1", got.Lines[0].Words[0].Polygon)
	}

	const withoutWords = `{"backend":"cpu","image":"a.png","elapsedMs":1,"lines":` +
		`[{"text":"hi","score":0.9,"detScore":0.8,"polygon":[{"x":1,"y":2}]}]}`
	var bare PageResult
	if err := json.Unmarshal([]byte(withoutWords), &bare); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if bare.Lines[0].Words != nil {
		t.Errorf("Words = %+v, want nil when --word-boxes was not passed", bare.Lines[0].Words)
	}
}
