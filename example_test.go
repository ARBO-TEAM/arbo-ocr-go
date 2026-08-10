package arboocr_test

import (
	"fmt"
	"log"

	arboocr "github.com/ARBO-TEAM/arbo-ocr-go"
)

// Example shows the typical Recognize workflow: construct an Engine, run it
// on an image, and read the results. Every Config field is optional — the
// binary is downloaded by installer.EnsureInstalled and the models by the
// binary itself. It's compiled (so it stays valid against the real API) but
// not executed by `go test` — it downloads a real binary and needs a real
// image, neither of which belongs in a unit test.
func Example() {
	engine, err := arboocr.NewEngine(arboocr.Config{
		// ModelsDir:   "/path/to/models", // optional — a populated dir wins over downloading
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
// doesn't pay for the download — e.g. from a Docker build step, alongside
// installer.EnsureInstalled, which bakes in the binary but not the weights.
// Like Example above it's compiled but not executed.
func ExampleEngine_EnsureModels() {
	engine, err := arboocr.NewEngine(arboocr.Config{
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
