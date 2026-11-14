// Package tests holds the tests of what goimgtools does: every file here
// uses the library the way a program that imports it does, through its
// public packages only. They are kept apart from the library so that its
// own directory shows its source files and nothing else.
//
//	go test ./tests
//
// The tests of the pieces the library is made of are next to those pieces
// (internal/..., pipeline); the examples shown in the documentation are in
// example_test.go beside the library.
package tests
