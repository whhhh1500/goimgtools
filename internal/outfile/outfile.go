// Package outfile lets an encoder put bytes back where it wrote them, when
// it writes to a file the package itself created (ToFile): what a format
// can only know at the end (the place of a TIFF's directory, an offset in
// a header) is then filled in, instead of the data being staged first to
// learn it. A writer a caller gives (ToWriter) never offers this: it may
// be a file written at its end, or with data of its own before ours.
package outfile

// Patcher is a writer of an output that can be written over: Patch writes
// p at off bytes from the start of the output, before or past what Write
// has written (a gap past it reads as zeros until it is written). Writes
// after a Patch go on from where Write had got to.
type Patcher interface {
	Write(p []byte) (int, error)
	Patch(p []byte, off int64) error
}
