package arboocr_test

import (
	"fmt"
	"log"

	arboocr "github.com/ARBO-TEAM/arbo-ocr-go"
)

// Example shows the typical Recognize workflow: construct an Engine
// pointing at a models directory, run it on an image, and read the
// results. It's compiled (so it stays valid against the real API) but not
// executed by `go test` — it downloads a real binary and needs a real
// image/models directory, neither of which belong in a unit test.
func Example() {
	engine, err := arboocr.NewEngine(arboocr.Config{
		ModelsDir: "/path/to/models",
		// BinPath:     "/custom/path/to/arboocr_demo", // optional override
		// ModelType:   "small", // tiny/small/medium — default small
		// UseAngleCls: true,
		// UseCuda:     true,
	})
	if err != nil {
		log.Fatal(err)
	}

	result, err := engine.Recognize("/path/to/image.jpg")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Backend) // cpu / cuda / tensorrt
	for _, line := range result.Lines {
		fmt.Printf("%s (%.3f)\n", line.Text, line.Score)
	}
}

// ExampleEngine_EnsureModels shows prefetching models so the first Recognize
// doesn't pay for the download — e.g. from a Docker build step. Like Example
// above it's compiled but not executed.
//
// This needs the arboOCR release that adds model auto-download; the version
// installer.EnsureInstalled currently pins predates it, so BinPath has to
// point at a newer binary until that pin is bumped.
func ExampleEngine_EnsureModels() {
	engine, err := arboocr.NewEngine(arboocr.Config{
		BinPath:   "/path/to/newer/arboocr_demo",
		ModelType: "small",
		// NoDownload: true,                              // fail rather than fetch
		// ModelsURL:  "https://mirror.internal/models/", // fetch from an internal mirror
	})
	if err != nil {
		log.Fatal(err)
	}

	// Downloads into the model cache and returns; does no OCR. Idempotent —
	// an already-cached model is a no-op.
	if err := engine.EnsureModels(); err != nil {
		log.Fatal(err)
	}
}
