# arbo-ocr-go

Go wrapper for [arboOCR](https://github.com/wafik/ArboOCR) — runs the
prebuilt `arboocr_demo` binary via `os/exec`, no C++ build required.

## Install

```bash
go get github.com/ARBO-TEAM/arbo-ocr-go
```

`NewEngine` downloads the matching arboOCR release binary (Windows or Linux,
auto-detected) the first time it's used — see "How it works" below. If the
auto-download fails (offline, unsupported OS), download a release manually
from the [arboOCR releases page](https://github.com/wafik/ArboOCR/releases)
and pass `Config.BinPath` explicitly.

You also need the OCR models — arboOCR does not bundle them. See
[arboOCR's Models section](https://github.com/wafik/ArboOCR#models) for
download instructions, then point `Config.ModelsDir` at the folder.

## Usage

```go
package main

import (
	"fmt"
	"log"

	arboocr "github.com/ARBO-TEAM/arbo-ocr-go"
)

func main() {
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
```

An empty `result.Lines` slice means no text was found — not an error.
`Engine.Recognize` returns a non-nil error (of type `*arboocr.OcrError`) only
when the process itself fails to start, exits non-zero, or produces
unparseable output.

## Quick example (tiny model, fastest)

For a fast local smoke test, use `ModelType: "tiny"` — the smallest/fastest
PP-OCRv6 recognizer. If you have an arboOCR checkout handy, its `models/`
folder already contains the tiny det/rec/cls ONNX files (no extra download):

```go
engine, err := arboocr.NewEngine(arboocr.Config{
	ModelsDir: "/path/to/arboOCR/models", // e.g. a local arboOCR checkout's models/ dir
	ModelType: "tiny",
})
if err != nil {
	log.Fatal(err)
}

result, err := engine.Recognize("/path/to/receipt.jpg")
if err != nil {
	log.Fatal(err)
}

fmt.Printf("backend=%s lines=%d elapsedMs=%.1f\n", result.Backend, len(result.Lines), result.ElapsedMs)
for _, line := range result.Lines {
	fmt.Printf("  %-40s score=%.3f\n", line.Text, line.Score)
}
```

The `tiny` model trades some accuracy for speed — good for quick local
testing; switch to `small` (the default) or `medium` for production-quality
recognition.

## How it works

This package never builds or vendors arboOCR's C++ source. It downloads the
exact same prebuilt release asset the PHP package uses
(`arboocr-windows-x64.zip` / `arboocr-linux-x64.tar.gz` from the
[wafik/ArboOCR releases](https://github.com/wafik/ArboOCR/releases)) — the
compiled binary and its DLLs are language-agnostic, this package just runs
the same CLI tool via `os/exec` instead of PHP's `proc_open`.

Composer has a post-install hook that downloads the binary into the
project's own `vendor/` directory at `composer install` time. Go modules
have no equivalent build-time hook, so `arbo-ocr-go` downloads lazily
instead: `NewEngine` only triggers a download (via the `installer` package —
kept separate from the root package so "how to get the binary" stays
independent of "how to run it") when `Config.BinPath` is left empty, caching
the result under the OS user cache directory
(`os.UserCacheDir()/arbo-ocr-go/<platform>/`) rather than anywhere inside the
module itself — Go's module cache is often read-only, so it can't be written
into the way Composer's `vendor/` can. `installer.DetectPlatform` reports
which release asset matches the current OS/arch; call
`installer.EnsureInstalled(binDir)` yourself (e.g. in a Docker build step) if
you want to control exactly when the download happens, then pass the
returned path as `Config.BinPath`.

Like the PHP package, OCR models are never bundled or auto-downloaded — see
arboOCR's [Models section](https://github.com/wafik/ArboOCR#models) for
download instructions.

`Recognize` captures the subprocess's output with buffered `exec.Cmd.Run()`
(`cmd.Stdout`/`cmd.Stderr` set to plain `io.Writer` values, which Go's stdlib
drains concurrently) rather than sequential pipe reads. `arboocr_demo` can
write ~200KB of ONNXRuntime warnings to stderr before any stdout appears —
enough to deadlock a naive pipe-based reader, a class of bug the PHP wrapper
had to explicitly work around.

## License

Apache-2.0
