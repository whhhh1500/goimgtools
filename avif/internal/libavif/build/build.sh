#!/bin/sh
# Makes ../module.go: libavif 1.4.2 with aom (encoding), dav1d (decoding)
# and libyuv, at the versions libavif 1.4.2 names, compiled to WebAssembly by
# wasi-sdk with the entry points of shim.c, then translated to Go by
# wasm2go. Needs git, cmake, ninja, meson, Go, and wasi-sdk 34 (its
# directory in WASI_SDK).
#
#   WASI_SDK=/opt/wasi-sdk-34 ./build.sh
set -eu

WASM2GO=github.com/ncruces/wasm2go@v0.4.22
WASI_SDK=${WASI_SDK:?set WASI_SDK to wasi-sdk 34}

here=$(cd "$(dirname "$0")" && pwd)
# A fixed directory: dav1d's build keeps source paths (debug information,
# assertion messages), so another directory would give another module.
work=/tmp/goimgtools-libavif
rm -rf "$work"
mkdir -p "$work"
trap 'rm -rf "$work"' EXIT
src=$work/libavif

git clone -q -b v1.4.2 --depth 1 https://github.com/AOMediaCodec/libavif "$src"
git clone -q -b v3.14.1 --depth 1 https://aomedia.googlesource.com/aom "$src/ext/aom"
git clone -q -b 1.5.3 --depth 1 https://code.videolan.org/videolan/dav1d.git "$src/ext/dav1d"
git clone -q --single-branch https://chromium.googlesource.com/libyuv/libyuv "$src/ext/libyuv"
git -C "$src/ext/libyuv" checkout -q 644251f25

# aom and dav1d include <setjmp.h>, which wasi-libc declares only with
# __wasm_exception_handling__; shim.c defines setjmp and longjmp.
cat > "$work/toolchain.cmake" <<EOF
include($WASI_SDK/share/cmake/wasi-sdk-p1.cmake)
set(CMAKE_C_FLAGS "\${CMAKE_C_FLAGS} -D__wasm_exception_handling__")
set(CMAKE_CXX_FLAGS "\${CMAKE_CXX_FLAGS} -D__wasm_exception_handling__")
EOF
cat > "$work/wasi.meson" <<EOF
[binaries]
c = '$WASI_SDK/bin/clang'
ar = '$WASI_SDK/bin/llvm-ar'
strip = '$WASI_SDK/bin/llvm-strip'

[host_machine]
system = 'wasi'
cpu_family = 'wasm32'
cpu = 'wasm32'
endian = 'little'

[built-in options]
c_args = ['--target=wasm32-wasip1', '--sysroot=$WASI_SDK/share/wasi-sysroot', '-D__wasm_exception_handling__']
c_link_args = ['--target=wasm32-wasip1', '--sysroot=$WASI_SDK/share/wasi-sysroot']
EOF

cmake -S "$src/ext/aom" -B "$src/ext/aom/build.libavif" -G Ninja -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DBUILD_SHARED_LIBS=0 -DENABLE_DOCS=0 -DENABLE_EXAMPLES=0 -DENABLE_TESTDATA=0 -DENABLE_TESTS=0 \
  -DENABLE_TOOLS=0 -DAOM_TARGET_CPU=generic -DCONFIG_RUNTIME_CPU_DETECT=0 -DCONFIG_MULTITHREAD=0 \
  -DCONFIG_WEBM_IO=0 -DCONFIG_AV1_DECODER=0 -DCONFIG_AV1_ENCODER=1 \
  -DCMAKE_TOOLCHAIN_FILE="$work/toolchain.cmake"
ninja -C "$src/ext/aom/build.libavif"

# dav1d decodes on the calling thread: no -pthread, which would turn on
# WebAssembly's atomics (wasi-libc's stubs stand in for pthreads).
sed -i "s/'emscripten'/'emscripten', 'wasi'/g" "$src/ext/dav1d/meson.build"
sed -i "s/^\( *\)thread_dependency = dependency('threads')$/\1thread_dependency = host_machine.system() == 'wasi' ? declare_dependency() : dependency('threads')/" "$src/ext/dav1d/meson.build"
meson setup "$src/ext/dav1d/build" "$src/ext/dav1d" --default-library=static --buildtype=minsize \
  --cross-file="$work/wasi.meson" -Denable_tools=false -Denable_tests=false -Denable_asm=false
ninja -C "$src/ext/dav1d/build"

sed -i '/^ADD_EXECUTABLE\|^TARGET_LINK_LIBRARIES/d' "$src/ext/libyuv/CMakeLists.txt"
cmake -S "$src/ext/libyuv" -B "$src/ext/libyuv/build" -G Ninja -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DCMAKE_POLICY_VERSION_MINIMUM=3.5 -DBUILD_SHARED_LIBS=0 -DCMAKE_TOOLCHAIN_FILE="$work/toolchain.cmake"
ninja -C "$src/ext/libyuv/build" yuv

cmake -S "$src" -B "$src/build" -G Ninja -DCMAKE_BUILD_TYPE=MinSizeRel -DBUILD_SHARED_LIBS=0 \
  -DAVIF_CODEC_DAV1D=LOCAL -DAVIF_CODEC_AOM=LOCAL -DAVIF_CODEC_AOM_DECODE=0 -DAVIF_CODEC_AOM_ENCODE=1 \
  -DAVIF_LIBYUV=LOCAL -DCMAKE_TOOLCHAIN_FILE="$work/toolchain.cmake"
ninja -C "$src/build"

"$WASI_SDK/bin/clang" --target=wasm32-wasip1 --sysroot="$WASI_SDK/share/wasi-sysroot" \
  -O3 -D__wasm_exception_handling__ -mexec-model=reactor -mnontrapping-fptoint \
  -Wl,--no-entry -z stack-size=1048576 \
  -Wl,--export=malloc -Wl,--export=free -Wl,--export=dec_open -Wl,--export=dec_get \
  -Wl,--export=dec_blob -Wl,--export=dec_pixels -Wl,--export=dec_close -Wl,--export=enc \
  -I "$src/include" -o "$work/avif.wasm" "$here/shim.c" "$src/build/libavif.a" \
  "$src/ext/libyuv/build/libyuv.a" "$src/ext/dav1d/build/src/libdav1d.a" \
  "$src/ext/aom/build.libavif/libaom.a" -lm

GOBIN="$work/bin" go install "$WASM2GO"
"$work/bin/wasm2go" -pkg libavif -o "$here/../module.go" "$work/avif.wasm"
# The translation writes one return after a goto, which go vet calls
# unreachable.
sed -i '/^\t*goto [A-Za-z0-9_]*$/{n;/^\t*return$/d;}' "$here/../module.go"
