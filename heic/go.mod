module github.com/whhhh1500/goimgtools/heic

go 1.26.4

require (
	github.com/gen2brain/h265 v0.2.5
	github.com/whhhh1500/goimgtools v0.2.0
)

require (
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// While the two are developed together, the one next to it.
replace github.com/whhhh1500/goimgtools => ../
