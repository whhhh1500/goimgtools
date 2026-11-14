#!/bin/sh
# Runs the test programs built for Android on the emulator the workflow
# started: pushes them, the test images they read and the test data of
# the repository, then runs each in the directory of its package.
set -e
dev=/data/local/tmp
adb shell "rm -rf $dev/ci $dev/repo $dev/tmp && mkdir -p $dev/repo $dev/tmp"
adb push ci "$dev/" > /dev/null
adb push tests/testdata "$dev/repo/tests/testdata" > /dev/null
adb shell "chmod 755 $dev/ci/bin/*"
adb shell getprop ro.build.version.release
adb shell uname -a
failed=0
for p in tests pipeline inspect internal/resize internal/ops cmd/goimgtools; do
  bin=$(echo "$p" | tr / _).test
  echo "=== $p"
  out=$(adb shell "mkdir -p $dev/repo/$p && cd $dev/repo/$p && GOROOT=$dev/ci/goroot GOMODCACHE=$dev/ci/mod TMPDIR=$dev/tmp $dev/ci/bin/$bin -test.timeout 40m; echo EXIT=\$?" 2>&1)
  echo "$out" | tail -25
  case "$out" in
  *EXIT=0*) ;;
  *) failed=1 ;;
  esac
done
exit $failed
