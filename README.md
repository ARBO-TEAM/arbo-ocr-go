# arbo-ocr-go

Go wrapper for [arboOCR](https://github.com/wafik/ArboOCR) — runs the
prebuilt `arboocr_demo` binary via `os/exec`, no C++ build required.

## Install

```bash
go get github.com/ARBO-TEAM/arbo-ocr-go
```

`NewEngine` downloads the matching arboOCR release binary (Windows or Linux,
auto-detected) the first time it's used — see "How it works" below. As of
[`v0.1.0-php1`](https://github.com/wafik/ArboOCR/releases/tag/v0.1.0-php1)
(published), this auto-download is live and verified working end to end —
no manual binary step needed. If it fails anyway (offline, unsupported OS),
download a release manually from the
[arboOCR releases page](https://github.com/wafik/ArboOCR/releases) and pass
`Config.BinPath` explicitly.

You also need the OCR models — arboOCR does not bundle them. See
[Models](#models) below for exactly which files each `ModelType` needs and
where to get them.

## Models

arboOCR doesn't bundle OCR models — you point `Config.ModelsDir` at a folder
of PP-OCRv6 ONNX files. Only the recognizer has size variants; the detector
is always one file regardless of `ModelType`:

| File | Needed for | Varies by `ModelType`? |
|---|---|---|
| `PP-OCRv6_det.onnx` | detection | no — always this one file |
| `PP-OCRv6_rec_tiny.onnx` + `PP-OCRv6_rec_tiny_dict.txt` | `ModelType: "tiny"` | yes |
| `PP-OCRv6_rec_small.onnx` + `PP-OCRv6_rec_small_dict.txt` | `ModelType: "small"` (default) | yes |
| `PP-OCRv6_rec_medium.onnx` + `PP-OCRv6_rec_medium_dict.txt` | `ModelType: "medium"` | yes |
| `PP-OCRv6_cls.onnx` | angle classification, only if `UseAngleCls` | no |

You only need the recognizer size(s) you'll actually use — e.g. for
`ModelType: "small"` alone, `ModelsDir` just needs `PP-OCRv6_det.onnx` +
`PP-OCRv6_rec_small.onnx` + `PP-OCRv6_rec_small_dict.txt`. Switching sizes
later is just changing `ModelType`; `ModelsDir` can hold all three sizes
side by side if you want to switch freely.

**Getting the files** — arboOCR doesn't host default download URLs (see its
own [Models section](https://github.com/wafik/ArboOCR#models)), so pick
whichever applies:
- Already have a Python `rapidocr` install? Copy its `models/` directory
  over, renaming files to match the layout above.
- Have your own PP-OCRv6 ONNX export? Place/rename the files as above.
- A local arboOCR checkout's `models/` directory already has the detector,
  classifier, and all three recognizer sizes — handy for local dev (see the
  tiny-model example below).

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
[Models](#models) above for exactly which files you need.

`Recognize` captures the subprocess's output with buffered `exec.Cmd.Run()`
(`cmd.Stdout`/`cmd.Stderr` set to plain `io.Writer` values, which Go's stdlib
drains concurrently) rather than sequential pipe reads. `arboocr_demo` can
write ~200KB of ONNXRuntime warnings to stderr before any stdout appears —
enough to deadlock a naive pipe-based reader, a class of bug the PHP wrapper
had to explicitly work around.

## Benchmark

`arbo-ocr-go` was compared against arbo-ocr-php and arbo-ocr-rust on the
same 5-image SROIE smoke set — all three call the identical `arboocr_demo`
binary, so accuracy is the same across all three; this measures wrapper
overhead only (subprocess spawn − arboocr_demo's own reported time):

| Size | arbo-php | arbo-go | arbo-rust |
|--------|----------:|---------:|-----------:|
| tiny | 193 ms | 137 ms | 131 ms |
| small | 231 ms | 171 ms | 172 ms |
| medium | 303 ms | 248 ms | 249 ms |

Go and Rust overhead is essentially tied — both are compiled binaries
paying only process-spawn cost, no interpreter startup. PHP runs ~55–65ms
higher (`php.exe` interpreter startup on top of `proc_open`). Same accuracy
across all three; all three match or beat a PP-OCRv6-based Node/Bun
reference implementation on this sample at every size. Full methodology in
the "wrapper benchmark" section of the internal `compare/RESULTS.md`
companion doc (not published in this repo).

## License

Apache-2.0
