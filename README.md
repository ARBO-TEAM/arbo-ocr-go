# arbo-ocr-go

Go wrapper for [arboOCR](https://github.com/wafik/ArboOCR) — runs the
prebuilt `arboocr_demo` binary via `os/exec`, no C++ build required.

## Install

```bash
go get github.com/ARBO-TEAM/arbo-ocr-go
```

`NewEngine` downloads the matching arboOCR release binary (Windows or Linux,
auto-detected) the first time it's used — see "How it works" below. The pinned
release is [`v0.4.0`](https://github.com/wafik/ArboOCR/releases/tag/v0.4.0),
and this auto-download is live and verified working end to end — no manual
binary step needed. If it fails anyway (offline, unsupported OS), download a
release manually from the
[arboOCR releases page](https://github.com/wafik/ArboOCR/releases) and pass
`Config.BinPath` explicitly.

The OCR models are handled the same way. arboOCR doesn't bundle them in the
release archive, but as of `v0.3.0` the binary fetches the ones it needs on
first run, verifies each by SHA-256, and caches them — so there's no manual
model step either. See [Models](#models) below for which files each
`ModelType` uses, how to pre-populate them for a zero-network start, and how
to switch the download off entirely.

## Models

arboOCR doesn't bundle OCR models in the release archive — the pinned `v0.4.0`
binary downloads the ones it needs on first run instead, verifies each against
a built-in SHA-256, and caches them (see
[Model cache directory](#model-cache-directory)). That makes
`Config.ModelsDir` optional: point it at a folder that already holds the files
and those win with zero network, leave it unset and the files are fetched. See
[Automatic download](#automatic-download) below for the full precedence and
for how to refuse the network outright. Only the recognizer has size variants;
the detector is always one file regardless of `ModelType`:

| File | Needed for | Varies by `ModelType`? |
|---|---|---|
| `PP-OCRv6_det.onnx` | detection | no — always this one file |
| `PP-OCRv6_rec_tiny.onnx` + `PP-OCRv6_rec_tiny_dict.txt` | `ModelType: "tiny"` | yes |
| `PP-OCRv6_rec_small.onnx` + `PP-OCRv6_rec_small_dict.txt` | `ModelType: "small"` (default) | yes |
| `PP-OCRv6_rec_medium.onnx` + `PP-OCRv6_rec_medium_dict.txt` | `ModelType: "medium"` | yes |
| `PP-OCRv6_cls.onnx` | angle classification, only if `UseAngleCls` | no |

Only the size(s) you actually use are involved — for `ModelType: "small"`
alone that's `PP-OCRv6_det.onnx` + `PP-OCRv6_rec_small.onnx` +
`PP-OCRv6_rec_small_dict.txt`, and nothing else is downloaded or looked for.
Switching sizes later is just changing `ModelType`; `ModelsDir` can hold all
three sizes side by side if you want to switch freely.

**Supplying the files yourself** — optional now, not a prerequisite. A
populated `ModelsDir` (baked into an image, mounted as a volume, or just
sitting on disk) wins over any download, which is how you get a first run that
never touches the network. The defaults are published at
`https://github.com/ARBO-TEAM/arbo-ocr-models/releases/download/models-v1/`;
to bring your own instead, pick whichever applies:
- Already have a Python `rapidocr` install? Copy its `models/` directory
  over, renaming files to match the layout above.
- Have your own PP-OCRv6 ONNX export? Place/rename the files as above.
- A local arboOCR checkout's `models/` directory already has the detector,
  classifier, and all three recognizer sizes — handy for local dev (see the
  tiny-model example below).

### Automatic download

Live as of arboOCR
[`v0.3.0`](https://github.com/wafik/ArboOCR/releases/tag/v0.3.0) and still live in `v0.4.0`, the release
`installer.EnsureInstalled` pins — so this works out of the box, with no
`Config.BinPath` of your own. The binary fetches missing models itself and
verifies them by SHA-256, which is what makes `ModelsDir` optional. Its
precedence, per file:

1. An explicit model path (`DetModelPath`, `ClsModelPath`, `RecModelPath`,
   `DictPath`) is used exactly as given and is never substituted by a
   download.
2. Otherwise a file already sitting in `ModelsDir` wins — zero network.
3. Only then is the file downloaded into the model cache and verified.

`Config` fields drive it. All are opt-in, and that still matters: left at
their zero value they emit no CLI flag at all, which is what keeps a
`Config.BinPath` pointed at a pre-`v0.3.0` binary working (an unknown option
makes it exit 1 with a usage error).

| Field | CLI flag | Meaning |
|---|---|---|
| `NoDownload bool` | `--no-download` | Never fetch a missing model — fail instead. For runs that must not silently reach the network. |
| `ModelsURL string` | `--models-url <url>` | Directory URL to fetch missing models from, e.g. an internal mirror, instead of the default `https://github.com/ARBO-TEAM/arbo-ocr-models/releases/download/models-v1/`. |
| `MinDetBoxArea *float64` | `--min-det-box-area <float>` | **v0.4.0+** Drop det boxes at or below this area in detector-input pixels. `0` disables the cut; `nil` (unset) leaves the binary's default `20`. |
| `SpaceRecovery bool` | `--space-recovery` | **v0.4.0+** Emit the inter-word spaces a greedy CTC decode swallows. |
| `EnableCPUMemArena bool` | `--enable-cpu-mem-arena` | **v0.4.0+** Leave ORT's CPU memory arena on: faster, higher RSS. |

The three **v0.4.0+** rows require arboOCR `v0.4.0` or newer — the pinned
release, and the first with these options. They are also the strictest case of
the opt-in rule above: each is emitted **only** when you set it, so a
`Config.BinPath` pointed at an older binary never sees them. `MinDetBoxArea` is
a `*float64` rather than a `float64` precisely so that `0` — a real setting
that disables the cut — stays distinguishable from unset; set any non-nil
value, `0` included, and it reaches argv verbatim. The two bools emit a single
`--flag=true` token for an explicit `true` and **nothing at all** for `false`
or unset, since `false` is the binary's own default and `--flag=false` is the
token a pre-`v0.4.0` binary would reject.

`Engine.EnsureModels()` prefetches the models for the configured
`OcrVersion`/`ModelType` so the first `Recognize` doesn't pay for the
download — useful in a Docker build step or at process startup:

```go
engine, err := arboocr.NewEngine(arboocr.Config{
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
already-cached model is a no-op.

The two are meant to be used together in a Docker build step.
`installer.EnsureInstalled` bakes in the binary but not the weights, so on its
own it leaves the first request in the running container paying for the model
download; adding `EnsureModels` (or a direct `arboocr_demo --download-models`)
puts the weights in the image beside it. Set `ARBOOCR_CACHE_DIR` to a path
that survives into the final image if you build in stages, and set
`ARBOOCR_OFFLINE=1` at runtime to turn any missed model into a loud failure
instead of a silent fetch. If you point `Config.BinPath` at a pre-`v0.3.0`
binary, `EnsureModels` returns an `*arboocr.OcrError` carrying its usage
error, since the flag doesn't exist there.

### Environment variables

`Recognize` and `EnsureModels` run `arboocr_demo` as a child process, so it
inherits the parent's environment. These need no `Config` field, and are read
by the pinned `v0.4.0` binary directly:

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
		// Every field is optional: the binary is downloaded for you, and so
		// are the models the first time they're needed.
		// ModelsDir:   "/path/to/models", // a populated dir wins over downloading
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

`UseCuda` / `UseTensorrt` ask for a GPU execution provider, and
`result.Backend` reports which one actually ran. Both need arboOCR `v0.3.0`
or newer: release archives before it shipped without the
`onnxruntime_providers_shared` library, so `--cuda`/`--tensorrt` could not
load a GPU provider from a release archive at all. A working CUDA/TensorRT
install on the host is still your side of the deal.

### Many images in one process — `RecognizeBatch`

`Recognize` starts a fresh `arboocr_demo` for every image, and the process
start plus model load dominates a short page. `RecognizeBatch` runs **one**
process over a whole list instead:

```go
pages, err := engine.RecognizeBatch([]string{
	"/scans/001.jpg",
	"/scans/002.jpg",
	"/scans/003.jpg",
})
if err != nil {
	log.Fatal(err)
}
for i, page := range pages {
	fmt.Printf("%s: %d lines\n", page.Image, len(page.Lines))
	_ = i // pages[i] belongs to the i-th path passed in
}
```

On 5 SROIE receipts the saving measured 13.0% at `tiny`, 28.5% at `small`,
13.6% at `medium` (same text on every image), which is `bench_batch_go.py` in
the internal `compare/` harness. That share is `(process start + model load) /
total`, so it moves with the model size and the number of images — it is not a
fixed percentage. A batch is worth it whenever the list is longer than one and
the images are individually small.

Results are matched to inputs **by position**, and the count must agree —
`arboocr_demo` reports only an image's basename, so two same-named files in
different directories would be indistinguishable. A mismatch is returned as
an error rather than a shifted list. For the same reason a path that cannot
survive the newline-delimited list format (empty, containing a newline, or
starting with `#`, which the binary reads as a comment and would skip) is
rejected before anything runs.

A batch exits `1` when *any* image came back with no text. That is an
ordinary outcome, not a failure, and is tolerated as long as the JSON array
is still on stdout — a usage error (unknown flag) exits `1` too but leaves
stdout empty, and that one is returned as an `*OcrError`.

## Quick example (tiny model, fastest)

For a fast local smoke test, use `ModelType: "tiny"` — the smallest/fastest
PP-OCRv6 recognizer. It's fetched on first use like any other size; setting
`ModelsDir` is only worth it if you have an arboOCR checkout handy, whose
`models/` folder already contains the tiny det/rec/cls ONNX files and so skips
the download:

```go
engine, err := arboocr.NewEngine(arboocr.Config{
	ModelsDir: "/path/to/arboOCR/models", // optional: a local arboOCR checkout's models/ dir
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
returned path as `Config.BinPath`. That gets you the binary but not the model
weights — pair it with `Engine.EnsureModels()` in the same step (see
[Automatic download](#automatic-download)) if you want the image to start with
nothing left to fetch.

Like the PHP package, OCR models are never bundled in the release archive —
but the pinned `v0.4.0` binary fetches them itself on first use, SHA-256
verified and cached, so there's no manual model step either.
`Config.ModelsDir`, `Config.NoDownload` and `Config.ModelsURL` are how you
override that: a populated models dir wins over any download, `NoDownload`
refuses to reach the network at all, and `ModelsURL` redirects it at a mirror.
See [Models](#models) above for exactly which files each `ModelType` uses.

`Recognize` captures the subprocess's output with buffered `exec.Cmd.Run()`
(`cmd.Stdout`/`cmd.Stderr` set to plain `io.Writer` values, which Go's stdlib
drains concurrently) rather than sequential pipe reads. `arboocr_demo` can
write ~200KB of ONNXRuntime warnings to stderr before any stdout appears —
enough to deadlock a naive pipe-based reader, a class of bug the PHP wrapper
had to explicitly work around.

## Benchmark

`arbo-ocr-go` was benchmarked against the other five arbo wrapper arms —
`arbo-cpp`, `arbo-php`, `arbo-rust`, `arbo-python`, `arbo-js` — on a
40-image SROIE sample. All six drive the **same pinned `arboocr_demo`
v0.4.0 binary**, so accuracy is identical across the arms by construction
(84.6 / 86.1 / 86.3% at tiny/small/medium) and the only thing left to
compare is each wrapper's own per-call cost:

| Arm | tiny | small | medium |
|-----|-----:|------:|-------:|
| arbo-cpp (raw binary, no wrapper) | 322 / 179 | 662 / 478 | 1825 / 1578 |
| arbo-php | 358 / 169 | 718 / 487 | 1875 / 1569 |
| arbo-go | 302 / 167 | 753 / 544 | 1877 / 1619 |
| arbo-rust | 300 / 167 | 657 / 481 | 1866 / 1613 |
| arbo-python | 381 / 171 | 744 / 492 | 2006 / 1663 |
| arbo-js | 427 / 220 | 744 / 515 | 1948 / 1645 |

Average wall ms / engine ms per image; `engine ms` is `arboocr_demo`'s own
reported inference time, and the `arbo-cpp` row is the raw binary with no
wrapper process in between — the floor the wrappers sit on. What the table
does *not* support is a ranking of the wrappers: on this run the raw-binary
row is slower than both compiled wrappers at `tiny`, and the `arbo-go` arm
picked up a slow tail on a few `small` images (`engine ms` 544 for it
against 478–515 for the other five arms, on the same binary and the same
images). An earlier round of this comparison did rank the wrappers — PHP
~55–65 ms above Go/Rust — but each package then installed its own arboOCR
release, so that spread was engine-version drift between arms, not wrapper
overhead. Every arbo arm here still beats the `ppu-paddle-ocr` Node/Bun
reference on both similarity and wall time at every size (82.8 / 83.5 /
84.7% at 581 / 944 / 2040 ms).

Absolute milliseconds come from one session on one machine; thermal state
and background load move every row, so these figures are comparable within
this table only — never against another session's numbers.

Measured by the internal `compare/` harness (`bench_wrappers.py`, one
process per image, each calling the pinned binary once) in its 2026-09-14
run; raw results in `out/bench_wrappers_n40.json`. The harness and its
output are not published in this repo.

## License

Apache-2.0
