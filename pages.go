package goimgtools

import (
	"errors"
	"fmt"
	"io"

	"github.com/whhhh1500/goimgtools/internal/pdfwrite"
)

// Pages makes a document of several pages from images, one page each, in
// order: a PDF (the output must be PDF; PDFOptions apply to every page),
// each page as large as its image at its resolution, with its own colour
// profile and transparency. Each image is read through its own recipe
// while its page is written, so memory holds one page at a time, whatever
// their number. Read as an image (Metadata, ToImage, another format) it is
// its first page.
func Pages(images []*Image) *Image {
	if len(images) == 0 {
		return &Image{err: errors.New("goimgtools: Pages needs images")}
	}
	for _, m := range images {
		if m == nil {
			return &Image{err: errors.New("goimgtools: Pages was given a nil image")}
		}
	}
	imgs := append([]*Image(nil), images...)
	first := imgs[0]
	return &Image{pages: imgs, open: first.open, chain: append([]operation(nil), first.chain...), once: first.once}
}

// writePages writes the pages of Pages as a PDF document.
func (m *Image) writePages(w io.Writer, ext string) error {
	format, err := m.outputFormat(ext, "pdf")
	if err != nil {
		return err
	}
	if format != "pdf" {
		return fmt.Errorf("goimgtools: Pages makes a PDF document, not %s", format)
	}
	po, err := optionsOf[PDFOptions](m.options, "pdf")
	if err != nil {
		return err
	}
	doc := pdfwrite.NewDocument(w)
	for i, pg := range m.pages {
		if err := m.addPage(doc, pg, po); err != nil {
			return fmt.Errorf("goimgtools: Pages: page %d: %w", i+1, err)
		}
	}
	return doc.Close()
}

// addPage writes one page, its image read through its own recipe in the
// memory limit of the document.
func (m *Image) addPage(doc *pdfwrite.Document, pg *Image, po PDFOptions) error {
	if pg.err != nil {
		return pg.err
	}
	env := m.newEnv()
	src, closer, _, meta, err := pg.build(env, false)
	if err != nil {
		return err
	}
	defer closer.Close()
	out := pg.outputMeta(meta)
	src, out.ICC = pg.manageColour(src, out.ICC, "pdf")
	defer src.Close()
	return doc.AddPage(src, &pdfwrite.Options{Quality: po.Quality, Density: out.Density, ICC: out.ICC})
}
