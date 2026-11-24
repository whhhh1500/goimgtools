English | [中文](zh-CN/testing.md)

# Testing

```bash
go test ./...
```

A full run takes about 3–5 minutes (packages are tested in parallel). Cross-checking the WebP encoder against libwebp requires Python with Pillow installed; otherwise, those tests are skipped automatically.

The behaviour tests of the library itself are all located in `tests/` and use the library solely through its public interface; the tests of individual components (each format under `internal/`, and `pipeline`) are located alongside their code.

**Verified platforms.** On all of the following, the tests are actually run, not merely compiled:

| System | Processor | Tests run | Environment |
|---|---|---|---|
| Windows | x86-64 | All tests | GitHub Actions |
| Linux | x86-64 | All tests | GitHub Actions |
| Linux | arm64 | All tests | GitHub Actions |
| macOS | arm64 | All tests | GitHub Actions |
| Linux | 32-bit x86 | All tests, including the groups for 60,000-pixel widths and heights and for the maximum side length of each format | GitHub Actions |
| FreeBSD 15, OpenBSD 8, NetBSD 11, DragonFly BSD 6.4, Solaris 11.4, illumos (OmniOS) | x86-64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations, command line | One virtual machine per system on GitHub Actions |
| Android 11 | x86-64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations, command line | Android emulator on GitHub Actions (built with the NDK; pushed and run via adb) |
| iOS | arm64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations | iOS simulator on GitHub Actions (packaged as an app and run with Go's bundled `go_ios_exec`) |
| Linux | 32-bit ARM, MIPS (big-endian), s390x (big-endian), riscv64, ppc64le, loong64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations | GitHub Actions, emulated with qemu |
| WebAssembly (WASI) | wasm | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations | GitHub Actions, wasmtime |
| WebAssembly (`GOOS=js`, for browsers / node) | wasm | As above; peak node memory 425–455MB for the full behaviour test suite | GitHub Actions, node 18, 20 and 22 on Linux |

BSD, Solaris, Android, iOS, the emulated processors and WebAssembly run only the packages listed above, not the format decoders' and encoders' own tests (which are too slow under emulation); the behaviour tests themselves, however, read and write every format. The same image produces byte-for-byte identical output files on Linux and Windows.

The CI configuration is in [.github/workflows/test.yml](../.github/workflows/test.yml). Every push runs the tests in the table above with the latest Go 1.27, and the full suite with the oldest supported Go (the version in `go.mod`, 1.26), together with race detection, short runs of the fuzz tests, builds and static checks for the remaining targets, and a comparison of the exported API with the last release, which fails on any incompatible change (see [CONTRIBUTING](../.github/CONTRIBUTING.md)).

Built and statically checked only, **not actually run**: AIX and Plan 9 (no test machines available).

In addition to the tests that compare each format byte for byte with the standard library, four categories of tests specifically cover edge cases:

- **Limit cases** ([tests/limits_test.go](../tests/limits_test.go)): sizes 1×1, 1×N, N×1 and sizes near the 8 / 16 pixel block boundaries, with each pixel type written to each format and read back; these small images are passed through every operation and every resize algorithm; the maximum side length each format supports (JPEG and GIF 65535, lossy WebP 16383, lossless WebP 16384) and the error returned at one pixel beyond it; images 60,000 pixels wide / high are processed to completion within the default memory limit; over a hundred sets of extreme parameter values (lower and upper bounds, infinity and NaN for quality, number of colours, angle, ratio, offset and font size); input streams that deliver one byte at a time, and input streams that terminate midway; file system cases such as overwriting the input file and a missing output directory.
- **Extension interfaces** ([tests/extend_test.go](../tests/extend_test.go), [example_test.go](../example_test.go)): operations, inputs and file formats implemented using only the public packages; the examples are compiled and run outside the package.
- **Multithreading and cross-platform** ([tests/threads_test.go](../tests/threads_test.go), [tests/platform_test.go](../tests/platform_test.go)): operations that divide their work produce byte-for-byte identical results with 0, 1, 3 and 7 helper threads, both when the data fits in memory and when it is written to temporary files, and the budget is fully returned; the output of new operations and of each encoder is compared against checksums recorded on x86-64, and CI runs this test on every processor. CI also compiles the library for the processors with fused multiply-add (ARM64, POWER, s390x, RISC-V, LoongArch) and fails if the compiler fused a floating-point multiplication with an addition anywhere, which could make results differ from x86's where no test image happens to show it.
- **Fuzz testing** ([tests/fuzz_test.go](../tests/fuzz_test.go)):

```bash
go test -run '^$' -fuzz FuzzDecode -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzChain -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzText -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzExif -fuzztime 5m ./tests
```

`FuzzDecode` decodes arbitrary bytes as an image; `FuzzChain` passes arbitrary bytes through an arbitrary combination of operations and writes the result in an arbitrary format; `FuzzText` renders arbitrary text; `FuzzExif` parses arbitrary bytes as EXIF (requiring every value read to fall within a reasonable range). Requirements: no panic, no hang, the strips cover the whole image exactly, the memory budget is never exceeded and is fully returned, and no temporary files are left behind. The seeds are the test images shipped with Go and `x/image`, plus small images in each format written by this library.
