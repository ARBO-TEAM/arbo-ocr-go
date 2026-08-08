# arbo-ocr-go

Go wrapper for [arboOCR](https://github.com/wafik/ArboOCR) — runs the
prebuilt `arboocr_demo` binary via `os/exec`, no C++ build required.

## Install

```bash
go get github.com/ARBO-TEAM/arbo-ocr-go
```

`NewEngine` downloads the matching arboOCR release binary (Windows or Linux,
auto-detected) the first time it's used — see "How it works" below. As of
[`v0.2.0`](https://github.com/wafik/ArboOCR/releases/tag/v0.2.0)
(published), this auto-download is live and verified working end to end —
no manual binary step needed. If it fails anyway (offline, unsupported OS),
download a release manually from the
[arboOCR releases page](https://github.com/wafik/ArboOCR/releases) and pass
`Config.BinPath` explicitly.

You also need the OCR models — arboOCR does not bundle them, and the release
binary this package currently pins does not fetch them either. See
[Models](#models) below for exactly which files each `ModelType` needs, where
to get them, and the auto-download options that land with the next arboOCR
release.

## Models

arboOCR doesn't bundle OCR models. With the pinned release binary you point
`Config.ModelsDir` at a folder of PP-OCRv6 ONNX files and a missing file is an
error — see [Automatic download](#automatic-download-needs-the-next-arboocr-release)
below for what changes with the next arboOCR release. Only the recognizer has
size variants; the detector is always one file regardless of `ModelType`:

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

### Automatic download (needs the next arboOCR release)

> **Not live yet — read before wiring this in.** `installer.EnsureInstalled`
> pins arboOCR
> [`v0.2.0`](https://github.com/wafik/ArboOCR/releases/tag/v0.2.0), which
> predates model auto-download. Install this package today and models are
> still entirely your job: `ModelsDir` is required and a missing file is an
> error. The `Config` fields and `Engine.EnsureModels` below exist and are
> wired up, but they only do anything against a newer binary you supply
> yourself via `Config.BinPath`. When the arboOCR release carrying the feature
> ships, the pinned tag bumps and this starts working out of the box with no
> code change on your side.

The next arboOCR release teaches the binary to fetch missing models itself and
verify them by SHA-256, which makes `ModelsDir` optional. Its precedence, per
file:

1. An explicit model path (`DetModelPath`, `ClsModelPath`, `RecModelPath`,
   `DictPath`) is used exactly as given and is never substituted by a
   download.
2. Otherwise a file already sitting in `ModelsDir` wins — zero network.
3. Only then is the file downloaded into the model cache and verified.

Two `Config` fields drive it. Both are opt-in, and that matters: left at their
zero value they emit no CLI flag at all, which is what keeps this package
working against the pinned v0.2.0 binary (an unknown option makes it exit 1
with a usage error).

| Field | CLI flag | Meaning |
|---|---|---|
| `NoDownload bool` | `--no-download` | Never fetch a missing model — fail instead. For runs that must not silently reach the network. |
| `ModelsURL string` | `--models-url <url>` | Directory URL to fetch missing models from, e.g. an internal mirror, instead of the default upstream location. |

`Engine.EnsureModels()` prefetches the models for the configured
`OcrVersion`/`ModelType` so the first `Recognize` doesn't pay for the
download — useful in a Docker build step or at process startup:

```go
engine, err := arboocr.NewEngine(arboocr.Config{
	BinPath:   "/path/to/newer/arboocr_demo", // until the pinned tag is bumped
	ModelType: "small",
})
if err != nil {
	log.Fatal(err)
}

if err := engine.EnsureModels(); err != nil {
	log.Fatal(err)
}
```

It runs `arboocr_demo --download-models`, which downloads and exits without
doing any OCR. Deliberately the same shape as `installer.EnsureInstalled` is
for the binary: one blocking call, no progress reporting, idempotent — an
already-cached model is a no-op. Against the pinned v0.2.0 binary it returns
an `*arboocr.OcrError` carrying the binary's usage error, since the flag
doesn't exist there yet.

### Environment variables

`Recognize` and `EnsureModels` run `arboocr_demo` as a child process, so it
inherits the parent's environment. These need no `Config` field — and, like
the fields above, need the arboOCR release that adds auto-download:

| Variable | Effect |
|---|---|
| `ARBOOCR_OFFLINE=1` | Never download a missing model — the environment form of `Config.NoDownload`. |
| `ARBOOCR_CACHE_DIR` | Override the model cache directory (below). |
| `ARBOOCR_MODELS_URL` | Directory URL to fetch missing models from — the environment form of `Config.ModelsURL`. |

### Model cache directory

Downloaded models land in a tag-scoped cache directory, so a future change to
the model set is a cache miss rather than a silent stale hit — the same
reasoning behind this package's versioned *binary* cache path (see
[How it works](#how-it-works)):

| OS | Path |
|---|---|
| Windows | `%LOCALAPPDATA%\arboOCR\models\models-v1` |
| macOS | `~/Library/Caches/arboOCR/models/models-v1` |
| Linux | `$XDG_CACHE_HOME/arboOCR/models/models-v1`, or `~/.cache/arboOCR/models/models-v1` when `XDG_CACHE_HOME` is unset |

That is arboOCR's own model cache, separate from this package's binary cache
under `os.UserCacheDir()/arbo-ocr-go/<arboocr-version>/<platform>/`. The macOS
row applies only to a binary you supply yourself: `installer.DetectPlatform`
covers Windows and Linux x64 only, matching the published release assets.

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
(`os.UserCacheDir()/arbo-ocr-go/<arboocr-version>/<platform>/`) rather than
anywhere inside the module itself — Go's module cache is often read-only, so
it can't be written into the way Composer's `vendor/` can. The arboOCR
release tag is part of that path deliberately: the extracted binary is named
`arboocr_demo` in every release, so a version-less cache directory would make
every version collide on one path, and the installer's "already installed"
check would then pin users to whatever binary they downloaded first. With the
version in the path, bumping the pinned tag is a cache miss and the new
binary is actually fetched. Old version directories are left in place rather
than deleted. `installer.DetectPlatform` reports
which release asset matches the current OS/arch; call
`installer.EnsureInstalled(binDir)` yourself (e.g. in a Docker build step) if
you want to control exactly when the download happens, then pass the
returned path as `Config.BinPath`.

Like the PHP package, OCR models are never bundled in the release archive, and
the pinned `v0.2.0` binary doesn't fetch them either — supply them yourself via
`Config.ModelsDir`. The next arboOCR release adds model auto-download to the
binary; `Config.NoDownload`, `Config.ModelsURL` and `Engine.EnsureModels` are
already wired up to drive it, and start working once the pinned tag is bumped.
See [Models](#models) above for exactly which files you need either way.

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
