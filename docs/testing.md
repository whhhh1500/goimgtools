English | [中文](zh-CN/testing.md)

# Testing

```bash
go test ./...
```

The full run takes about 3–5 minutes (packages run in parallel). Cross-checking the WebP encoder against libwebp needs Python with Pillow installed; without it, those tests are skipped automatically.

The behaviour tests of the library itself are all in `tests/`, and they use the library only through its public interface; the tests of the individual parts (each format under `internal/`, `pipeline`) sit next to their code.

**Systems it has been verified on.** All of the following actually run the tests, not just compile them:

| System | Processor | What was run | Where it was run |
|---|---|---|---|
| Windows | x86-64 | All tests | GitHub Actions |
| Linux | x86-64 | All tests | GitHub Actions |
| Linux | arm64 | All tests | GitHub Actions |
| macOS | arm64 | All tests | GitHub Actions |
| Linux | 32-bit x86 | All tests, including the groups for 60,000-pixel widths and heights and for the largest side length of each format | GitHub Actions |
| FreeBSD 15, OpenBSD 8, NetBSD 11, DragonFly BSD 6.4, Solaris 11.4, illumos (OmniOS) | x86-64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations, command line | A virtual machine for each system in GitHub Actions |
| Android 11 | x86-64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations, command line | Android emulator in GitHub Actions (built with the NDK, pushed and run with adb) |
| iOS | arm64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations | iOS simulator in GitHub Actions (packaged as an app and run with Go's own `go_ios_exec`) |
| Linux | 32-bit ARM, MIPS (big-endian), s390x (big-endian), riscv64, ppc64le, loong64 | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations | GitHub Actions, emulated with qemu |
| WebAssembly (WASI) | wasm | Library behaviour tests, `pipeline`, `inspect`, resizing, basic operations | GitHub Actions, wasmtime |
| WebAssembly (`GOOS=js`, for browsers / node) | wasm | As above; node peak memory 425–455MB for the whole behaviour test suite | GitHub Actions, node 18, 20 and 22 on Linux |

BSD, Solaris, Android, iOS, the emulated processors and WebAssembly run only the packages above, not the format decoders' and encoders' own tests (those are too slow under emulation); but the behaviour tests themselves read and write all six formats. The same image gives byte-for-byte identical output files on Linux and Windows.

The CI configuration is in [.github/workflows/test.yml](../.github/workflows/test.yml). Every push runs the tests in the table above, plus race detection, short runs of the four fuzz tests, and builds and static checks for the remaining targets.

Only built and statically checked, **not actually run**: AIX and Plan 9 (no test machines available).

Besides the tests that compare each format byte for byte with the standard library, there are four kinds that specifically guard the edges:

- **Limit cases** ([tests/limits_test.go](../tests/limits_test.go)): sizes 1×1, 1×N, N×1 and sizes near the 8 / 16 pixel block boundaries, with each pixel type written to each format and read back; these small images go through every operation and every resize algorithm; the largest side length each format can hold (JPEG and GIF 65535, lossy WebP 16383, lossless WebP 16384) and the error at one pixel more; images 60,000 pixels wide / high run to completion within the default memory limit; over a hundred sets of extreme parameter values (lower and upper bounds, infinity and NaN for quality, number of colours, angle, ratio, offset and font size); input streams that give one byte at a time, and input streams that break off midway; file system cases such as overwriting the input file and a missing output directory.
- **Extension interfaces** ([tests/extend_test.go](../tests/extend_test.go), [example_test.go](../example_test.go)): operations, inputs and file formats written using only the public packages; the examples are compiled and run outside the package.
- **Multithreading and cross-platform** ([tests/threads_test.go](../tests/threads_test.go), [tests/platform_test.go](../tests/platform_test.go)): operations that share out work give byte-for-byte identical results with 0, 1, 3 and 7 helpers, both when the data fits in memory and when it is written to temporary files, and the whole budget is returned; the output of new operations and of each encoder is compared with checksums taken on x86-64, and CI runs this test on every processor.
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

`FuzzDecode` decodes arbitrary bytes as an image, `FuzzChain` runs arbitrary bytes through an arbitrary combination of operations and writes the result in an arbitrary format, `FuzzText` draws arbitrary text, and `FuzzExif` parses arbitrary bytes as EXIF (requiring every value read to be within a sensible range). Requirements: no panic, no hang, the strips cover the whole image exactly, the memory budget is never exceeded and is fully returned, and no temporary files are left behind. The seeds are the test images that come with Go and `x/image`, plus small images in each format written by this library.
