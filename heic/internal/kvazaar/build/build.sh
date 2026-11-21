#!/bin/sh
# Makes ../module.go: Kvazaar compiled to WebAssembly by wasi-sdk with the
# entry points of shim.c, then translated to Go by wasm2go. Needs git, cmake,
# ninja, Go, and wasi-sdk 34 (its directory in WASI_SDK).
#
#   WASI_SDK=/opt/wasi-sdk-34 ./build.sh
set -eu

KVAZAAR=https://github.com/ultravideo/kvazaar
KVAZAAR_COMMIT=c997f08c805bd7ec82233654e417e06fe7c83cba
WASM2GO=github.com/ncruces/wasm2go@v0.4.22
WASI_SDK=${WASI_SDK:?set WASI_SDK to wasi-sdk 34}

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git clone -q "$KVAZAAR" "$work/kvazaar"
git -C "$work/kvazaar" checkout -q "$KVAZAAR_COMMIT"
# kvazaar.patch: WebAssembly has neither threads (the pictures are coded on
# the calling thread) nor the x86 assembler option; and a QP offset for Cb
# and Cr (option chroma-qp-offset), which Kvazaar does not have.
git -C "$work/kvazaar" apply "$here/kvazaar.patch"

cmake -S "$work/kvazaar" -B "$work/build" -G Ninja \
  -DCMAKE_TOOLCHAIN_FILE="$WASI_SDK/share/cmake/wasi-sdk-p1.cmake" \
  -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DBUILD_TESTS=OFF \
  -DBUILD_KVAZAAR_BINARY=OFF -DKVZ_BIT_DEPTHS=8 -DUSE_CRYPTO=OFF
cmake --build "$work/build"

"$WASI_SDK/bin/clang" --target=wasm32-wasip1 --sysroot="$WASI_SDK/share/wasi-sysroot" \
  -O3 -mexec-model=reactor -mnontrapping-fptoint -DKVZ_STATIC_LIB \
  -I "$work/kvazaar/src" -Wl,--no-entry -z stack-size=1048576 \
  -Wl,--export=malloc -Wl,--export=free -Wl,--export=picture -Wl,--export=plane \
  -Wl,--export=stride -Wl,--export=release -Wl,--export=deltas -Wl,--export=encode \
  -o "$work/kvazaar.wasm" "$here/shim.c" "$work/build/libkvazaar.a"

GOBIN="$work/bin" go install "$WASM2GO"
"$work/bin/wasm2go" -pkg kvazaar -o "$here/../module.go" "$work/kvazaar.wasm"