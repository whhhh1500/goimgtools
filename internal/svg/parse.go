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
	"regexp"
	"strings"
)

// Limits on what a document may hold, against files made to exhaust
// memory or time.
const (
	maxNodes = 200_000
	maxDepth = 256
)

var errFormat = errors.New("goimgtools: svg: not a valid SVG document")

const svgNS = "http://www.w3.org/2000/svg"

var errTooBig = errors.New("goimgtools: svg: the document is too large or too deep")

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
	if o := utf16Order(head); o != 0 {
		head = fromUTF16(head[:min(len(head), 512)], o)
	}
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

var entityDecl = regexp.MustCompile(`<!ENTITY\s+([A-Za-z_][\w.\-]*)\s+("([^"]*)"|'([^']*)')\s*>`)

// docEntities reads the entities of text a doctype declares: at most 1000,
// of 1MB in all; those that hold markup are left out.
func docEntities(d xml.Directive) map[string]string {
	s := string(d)
	if !strings.Contains(s, "ENTITY") {
		return nil
	}
	out := map[string]string{}
	size := 0
	for _, m := range entityDecl.FindAllStringSubmatch(s, 1000) {
		v := m[3]
		if v == "" {
			v = m[4]
		}
		if strings.ContainsAny(v, "<&") || size+len(v) > 1<<20 {
			continue
		}
		size += len(v)
		out[m[1]] = v
	}
	return out
}

// nodeBytes is about what an element of the tree takes.
const nodeBytes = 320

// parse reads a document into its tree; reserve is called as it grows.
func parse(data []byte, reserve func(int64) error) (*node, error) {
	if o := utf16Order(data); o != 0 {
		// UTF-16, read as UTF-8: up to half as large again.
		if err := reserve(int64(len(data)) * 3 / 2); err != nil {
			return nil, err
		}
		data = fromUTF16(data, o)
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charsetReader
	var root *node
	var stack []*node
	count := 0
	foreign := 0 // foreignObject elements open: their HTML's text is kept
	// grow counts a node of the tree: no more than maxNodes, those of each
	// 1024 reserved as they come.
	reserved := 0
	grow := func() error {
		count++
		if count > maxNodes {
			return errTooBig
		}
		for count >= reserved+1024 {
			if err := reserve(1024 * nodeBytes); err != nil {
				return err
			}
			reserved += 1024
		}
		return nil
	}
	rootNS := ""
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
		case xml.Directive:
			// The entities a doctype declares, as Illustrator writes them for
			// its namespaces and values: those of text, taken as they are.
			if root == nil {
				if ents := docEntities(t); len(ents) > 0 {
					m := make(map[string]string, len(xml.HTMLEntity)+len(ents))
					for k, v := range xml.HTMLEntity {
						m[k] = v
					}
					for k, v := range ents {
						m[k] = v
					}
					d.Entity = m
				}
			}
		case xml.StartElement:
			if len(stack) >= maxDepth {
				return nil, errTooBig
			}
			if err := grow(); err != nil {
				return nil, err
			}
			n := &node{name: t.Name.Local, attrs: make(map[string]string, len(t.Attr))}
			// An element of another namespace than SVG's (or the root's) is
			// not one of SVG's, whatever its name: not drawn (but HTML in a
			// foreignObject).
			if sp := t.Name.Space; root == nil {
				rootNS = sp
			} else if foreign == 0 && sp != "" && sp != svgNS && sp != rootNS && strings.Contains(sp, ":") &&
				!(t.Name.Local == "style" && sp == "http://www.w3.org/1999/xhtml") { // HTML's style sheets apply
				n.name = "\x00" + sp + " " + t.Name.Local
			}
			for _, a := range t.Attr {
				k := a.Name.Local
				switch sp := a.Name.Space; {
				case sp == "":
				case sp == "xlink" || strings.HasSuffix(sp, "/xlink"):
					k = "xlink:" + k
				case sp == "xml" || sp == "http://www.w3.org/XML/1998/namespace":
					k = "xml:" + k
				default:
					// Of another namespace (Inkscape's sodipodi:cx, a
					// namespace declared, svg:href): kept apart from the
					// attribute of that name.
					k = sp + ":" + k
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
			if n.name == "foreignObject" {
				foreign++
			}
		case xml.EndElement:
			if len(stack) > 0 {
				if stack[len(stack)-1].name == "foreignObject" {
					foreign--
				}
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
				if top.name == "text" || top.name == "tspan" || top.name == "a" || top.name == "textPath" || foreign > 0 {
					if err := grow(); err != nil {
						return nil, err
					}
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
