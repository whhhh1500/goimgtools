// Package svg renders SVG documents a band of rows at a time: shapes and
// paths, transforms, fills and strokes (joins, caps, dashes), gradients,
// opacity, use and symbol, nested viewports and CSS from style sheets, on
// the anti-aliased rasterizer of internal/raster. The scene is built once
// from the document; each band of the image is then drawn from it, so the
// memory a picture takes does not grow with its size.
package svg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Limits on what a document may hold, against files made to exhaust
// memory or time.
const (
	maxNodes = 200_000
	maxDepth = 256
)

var errFormat = errors.New("goimgtools: svg: not a valid SVG document")

// node is an element of the document.
type node struct {
	name     string // local name, svg namespace assumed
	attrs    map[string]string
	children []*node
	parent   *node
	text     string // character data (style sheets, text)
	// The declarations that apply to it from the style sheets, by
	// property, with their specificity and order.
	sheet map[string]decl
}

func (n *node) attr(name string) (string, bool) {
	v, ok := n.attrs[name]
	return v, ok
}

// Match reports whether head starts what may be an SVG document: an svg
// element, or the XML declaration, a doctype or a comment that may come
// before it (the document read tells).
func Match(head []byte) bool {
	h := bytes.TrimLeft(head, " \t\r\n")
	h = bytes.TrimPrefix(h, []byte("\xef\xbb\xbf"))
	h = bytes.TrimLeft(h, " \t\r\n")
	for _, p := range []string{"<svg", "<?xml", "<!DOCTYPE svg", "<!doctype svg", "<!--"} {
		if bytes.HasPrefix(h, []byte(p)) || len(h) < len(p) && bytes.HasPrefix([]byte(p), h) && len(h) > 1 {
			return true
		}
	}
	return false
}

// nodeBytes is about what an element of the tree takes.
const nodeBytes = 320

// parse reads a document into its tree; reserve is called as it grows.
func parse(data []byte, reserve func(int64) error) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var root *node
	var stack []*node
	count := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if root != nil {
				break // what was read before the error is drawn
			}
			return nil, errFormat
		}
		switch t := tok.(type) {
		case xml.StartElement:
			count++
			if count > maxNodes || len(stack) >= maxDepth {
				return nil, errors.New("goimgtools: svg: the document is too large or too deep")
			}
			if count%1024 == 0 {
				if err := reserve(1024 * nodeBytes); err != nil {
					return nil, err
				}
			}
			n := &node{name: t.Name.Local, attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				k := a.Name.Local
				if a.Name.Space == "xlink" || strings.HasSuffix(a.Name.Space, "/xlink") {
					k = "xlink:" + k
				}
				if a.Name.Space == "xml" || a.Name.Space == "http://www.w3.org/XML/1998/namespace" {
					k = "xml:" + k
				}
				n.attrs[k] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				n.parent = p
				p.children = append(p.children, n)
			} else if root == nil {
				if n.name != "svg" {
					return nil, errFormat
				}
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				// Only a style element's is read: kept for others, the text
				// between the elements of a large document added up again
				// and again.
				if top.name == "style" && len(top.text) < 1<<20 {
					top.text += string(t)
				}
				// In text, character data keeps its place among the tspans.
				if top.name == "text" || top.name == "tspan" || top.name == "a" || top.name == "textPath" {
					count++
					top.children = append(top.children, &node{name: "#text", text: string(t), parent: top, attrs: map[string]string{}})
				}
			}
		}
	}
	if root == nil {
		return nil, errFormat
	}
	return root, nil
}
