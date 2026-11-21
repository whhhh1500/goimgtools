#!/bin/sh
# Makes ../module.go: libjxl 0.12.0 (with the highway, brotli and skcms it
# names) compiled to WebAssembly by wasi-sdk with the entry points of
# shim.c, then translated to Go by wasm2go. Needs git, cmake, ninja, Go, and
# wasi-sdk 34 (its directory in WASI_SDK).
#
#   WASI_SDK=/opt/wasi-sdk-34 ./build.sh
#
# The translation is written by the Go side, so it can also run on another
# machine: WASM_ONLY=1 stops after the module, at /tmp/goimgtools-libjxl.wasm.
set -eu

WASM2GO=github.com/ncruces/wasm2go@v0.4.22
WASI_SDK=${WASI_SDK:?set WASI_SDK to wasi-sdk 34}

here=$(cd "$(dirname "$0")" && pwd)
# A fixed directory: the build keeps source paths (assertion messages), so
# another directory would give another module.
work=/tmp/goimgtools-libjxl
rm -rf "$work"
mkdir -p "$work"
trap 'rm -rf "$work"' EXIT
src=$work/libjxl

git clone -q -b v0.12.0 --depth 1 https://github.com/libjxl/libjxl "$src"
git -C "$src" submodule update -q --init --depth 1 third_party/highway third_party/brotli third_party/skcms

# No threads: libjxl's runner (jxl_threads) is not built, and the library
# runs on the calling thread.
sed -i '/^set(THREADS\|^find_package(Threads/d' "$src/CMakeLists.txt"
sed -i '/^include(jxl_threads/d' "$src/lib/CMakeLists.txt"
sed -i '/Threads::Threads/d' "$src/lib/jxl.cmake"
# highway's scalar target (not its default here, EMU128, which emulates
# vectors of four lanes in memory): a third faster once translated, the
# same files.
# brotli's command-line tool does not build for WASI and is not used.
echo 'int main(int argc, char** argv) { return 0; }' > "$src/third_party/brotli/c/tools/brotli.c"

cmake -S "$src" -B "$src/build" -G Ninja -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=0 -DBUILD_TESTING=0 -DJPEGXL_ENABLE_TOOLS=0 -DJPEGXL_ENABLE_JPEGLI=0 \
  -DJPEGXL_ENABLE_JPEGLI_LIBJPEG=0 -DJPEGXL_ENABLE_DOXYGEN=0 -DJPEGXL_ENABLE_MANPAGES=0 \
  -DJPEGXL_ENABLE_BENCHMARK=0 -DJPEGXL_ENABLE_EXAMPLES=0 -DJPEGXL_ENABLE_SJPEG=0 \
  -DJPEGXL_ENABLE_OPENEXR=0 -DJPEGXL_ENABLE_WASM_THREADS=0 -DJPEGXL_ENABLE_JNI=0 \
  -DJPEGXL_ENABLE_PLUGINS=0 -DJPEGXL_ENABLE_TRANSCODE_JPEG=1 -DJPEGXL_ENABLE_BOXES=1 \
  -DJPEGXL_ENABLE_SKCMS=1 -DJPEGXL_BUNDLE_SKCMS=1 -DJPEGXL_ENABLE_VIEWERS=0 \
  -DCMAKE_CXX_FLAGS=-DHWY_COMPILE_ONLY_SCALAR=1 -DCMAKE_TOOLCHAIN_FILE="$WASI_SDK/share/cmake/wasi-sdk-p1.cmake"
ninja -C "$src/build" jxl jxl_cms hwy brotlicommon brotlidec brotlienc

b=$src/build
"$WASI_SDK/bin/clang++" --target=wasm32-wasip1 --sysroot="$WASI_SDK/share/wasi-sysroot" \
  -O2 -fno-exceptions -mexec-model=reactor -mnontrapping-fptoint \
  -Wl,--no-entry -z stack-size=1048576 \
  -Wl,--export=malloc -Wl,--export=free -Wl,--export=dec_open -Wl,--export=dec_get \
  -Wl,--export=dec_blob -Wl,--export=dec_close -Wl,--export=enc \
  -I "$src/lib/include" -I "$b/lib/include" -x c "$here/shim.c" -x none \
  "$b/lib/libjxl.a" "$b/lib/libjxl_cms.a" "$b/third_party/highway/libhwy.a" \
  "$b/third_party/brotli/libbrotlienc.a" "$b/third_party/brotli/libbrotlidec.a" \
  "$b/third_party/brotli/libbrotlicommon.a" -o "$work/jxl.wasm"

if [ "${WASM_ONLY:-}" ]; then
  cp "$work/jxl.wasm" /tmp/goimgtools-libjxl.wasm
  exit 0
fi
GOBIN="$work/bin" go install "$WASM2GO"
"$work/bin/wasm2go" -pkg libjxl -o "$here/../module.go" "$work/jxl.wasm"
# The translation writes one return after a goto, which go vet calls
# unreachable.
sed -i '/^\t*goto [A-Za-z0-9_]*$/{n;/^\t*return$/d;}' "$here/../module.go"
