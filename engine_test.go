package arboocr

import (
	"errors"
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
