package goimgtools

import (
	"context"
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
// their number. The operations and settings of a page (Resize, Density,
// ToProfile, ...) go on its image: on the document they are an error.
// WithContext on the document or on a page stops it. Read as an image
// (Metadata, ToImage) the document is its first page.
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
	return &Image{pages: imgs, pagesBase: len(first.chain), open: first.open, chain: append([]operation(nil), first.chain...), once: first.once}
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
	if len(m.chain) > m.pagesBase || len(m.toProfile) > 0 || m.toSRGB || m.stripICC || m.densitySet || m.maxBytes > 0 || m.minSSIM > 0 {
		return errors.New("goimgtools: Pages: operations and settings (Resize, Density, ToProfile, MaxFileSize, ...) go on the image of each page, not on the document")
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
	if p, err := pg.asJPEGPage(po, m.ctx); p != nil || err != nil {
		if err != nil {
			return err
		}
		return pg.writeJPEGPage(doc, p)
	}
	env := m.newEnv()
	src, closer, _, meta, err := pg.build(env, false)
	if err != nil {
		return err
	}
	defer closer.Close()
	out := pg.outputMeta(meta)
	src, out.ICC = pg.manageColour(src, out.ICC, "pdf")
	for _, ctx := range []context.Context{m.ctx, pg.ctx} {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				src.Close()
				return err
			}
			src = cancelable{src, ctx}
		}
	}
	defer src.Close()
	return doc.AddPage(src, &pdfwrite.Options{Quality: po.Quality, Density: out.Density, ICC: out.ICC})
}

// jpegPage is a page holding a JPEG as it is: its size, whether it is grey,
// and the profile and resolution of the page.
type jpegPage struct {
	w, h    int
	grey    bool
	icc     []byte
	density float64
}

// asJPEGPage returns m as a page holding its JPEG as it is, or nil when it
// cannot be one: for a page stored without loss (no quality), when m is a
// JPEG that a PDF reader takes as it is (pdfwrite.ScanJPEG), read through
// no operation and no change of its colours. That keeps every pixel as it
// is decoded, where Flate would store a third of the decoded pixels' size
// or more, and takes no decoding. Nothing is decoded to find out.
func (m *Image) asJPEGPage(po PDFOptions, ctx context.Context) (*jpegPage, error) {
	if po.Quality != 0 || m.once || m.animated || m.autoOrient || len(m.chain) > 0 || len(m.hooks) > 0 || m.toSRGB || len(m.toProfile) > 0 || m.maxBytes > 0 || m.minSSIM > 0 {
		return nil, nil
	}
	for _, c := range []context.Context{ctx, m.ctx} {
		if c != nil {
			if err := c.Err(); err != nil {
				return nil, err
			}
		}
	}
	f, format, err := m.openInput()
	if err != nil {
		return nil, err
	}
	if format != "jpeg" {
		f.Close()
		return nil, nil
	}
	w, h, grey, ok := pdfwrite.ScanJPEG(f.br)
	f.Close()
	if !ok {
		return nil, nil
	}
	// The profile and the resolution, as the page of the pixels would have
	// them.
	src, closer, _, meta, err := m.build(nil, true)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	src.Close()
	closer.Close()
	if b.Dx() != w || b.Dy() != h {
		return nil, nil
	}
	out := m.outputMeta(meta)
	return &jpegPage{w: w, h: h, grey: grey, icc: out.ICC, density: out.Density}, nil
}

// writeJPEGPage adds the page p of m (asJPEGPage) to doc: the file as it is.
func (m *Image) writeJPEGPage(doc *pdfwrite.Document, p *jpegPage) error {
	f, _, err := m.openInput()
	if err != nil {
		return err
	}
	defer f.Close()
	return doc.AddJPEG(f.br, p.w, p.h, p.grey, p.icc, p.density)
}
