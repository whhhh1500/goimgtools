//go:build !amd64 || purego

package vp8l

const predSIMD = false

var predAdd [16]func(cur, top *byte, n int)
