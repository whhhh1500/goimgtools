//go:build !(amd64 || (arm64 && go1.27)) || purego

package vp8l

const predSIMD = false

var predAdd [16]func(cur, top *byte, n int)
