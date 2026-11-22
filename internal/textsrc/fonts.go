// Package textsrc renders text as a strip source and finds the fonts to do
// it with: font files, fonts installed on the system, and the Go font that
// is built in.
package textsrc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"

	"github.com/whhhh1500/goimgtools/internal/colorfont"
	"github.com/whhhh1500/goimgtools/internal/shape"
)

// FontInfo describes one font: a font file, or one font of a collection
// file (.ttc), which holds several.
type FontInfo struct {
	Path   string // the file; empty for the built-in font
	Index  int    // which font of a collection file; 0 otherwise
	Family string // "Microsoft YaHei"
	Style  string // "Regular", "Bold", ...
	Full   string // "Microsoft YaHei Bold"
	// CJK reports whether the font has Chinese characters.
	CJK bool
}

// Spec is how the font is named where a font is asked for: the file, with
// "#n" for the n-th font of a collection.
func (f FontInfo) Spec() string {
	if f.Path == "" {
		return BuiltIn
	}
	if f.Index > 0 {
		return f.Path + "#" + strconv.Itoa(f.Index)
	}
	return f.Path
}

// BuiltIn names the font that is compiled in: Go Regular (Latin, Greek and
// Cyrillic; no Chinese).
const BuiltIn = "Go Regular"

// Font is a loaded font.
type Font struct {
	Info FontInfo
	f    *sfnt.Font
	file *os.File // kept open: glyphs are read from it on demand
	data []byte   // a font held in memory
	// The shaping tables, read the first time text of a script that needs
	// them is laid out in the font.
	shaping    *shape.Face
	shapeTried bool
	// Its colour glyphs (as colour emoji), read the first time a glyph of
	// it is drawn.
	colour      *colorfont.Font
	colourTried bool
}

// Close releases the font file.
func (f *Font) Close() {
	if f.file != nil {
		f.file.Close()
		f.file = nil
	}
}

// SFNT is the parsed font, for its outlines and metrics.
func (f *Font) SFNT() *sfnt.Font { return f.f }

// Has reports whether the font has a glyph for r.
func (f *Font) Has(r rune) bool {
	var buf sfnt.Buffer
	return f.has(&buf, r)
}

// has reports whether the font has a glyph for r.
func (f *Font) has(buf *sfnt.Buffer, r rune) bool {
	i, err := f.f.GlyphIndex(buf, r)
	return err == nil && i != 0
}

func describe(f *sfnt.Font, path string, index int) FontInfo {
	var buf sfnt.Buffer
	name := func(id sfnt.NameID) string {
		s, err := f.Name(&buf, id)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}
	info := FontInfo{Path: path, Index: index, Family: name(sfnt.NameIDFamily), Style: name(sfnt.NameIDSubfamily), Full: name(sfnt.NameIDFull)}
	if info.Family == "" {
		info.Family = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if info.Full == "" {
		info.Full = strings.TrimSpace(info.Family + " " + info.Style)
	}
	i, err := f.GlyphIndex(&buf, '中')
	info.CJK = err == nil && i != 0
	return info
}

// builtIn returns the compiled-in font.
func builtIn() *Font {
	f, err := sfnt.Parse(goregular.TTF)
	if err != nil {
		panic(err) // the embedded font is known to be valid
	}
	return &Font{Info: FontInfo{Family: "Go", Style: "Regular", Full: BuiltIn}, f: f, data: goregular.TTF}
}

// openFile opens font number index of a font file. Only the tables that
// are needed are read, when they are needed, so a 20MB collection of
// Chinese fonts costs no such memory.
func openFile(path string, index int) (*Font, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fonts, err := parseFile(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("goimgtools: %s is not a font file: %w", path, err)
	}
	if index < 0 || index >= len(fonts) {
		file.Close()
		return nil, fmt.Errorf("goimgtools: %s holds %d fonts, there is no #%d", path, len(fonts), index)
	}
	return &Font{Info: describe(fonts[index], path, index), f: fonts[index], file: file}, nil
}

// parseFile returns the fonts of a font or collection file.
func parseFile(file *os.File) ([]*sfnt.Font, error) {
	var magic [4]byte
	if _, err := file.ReadAt(magic[:], 0); err != nil {
		return nil, errors.New("too short")
	}
	if string(magic[:]) != "ttcf" {
		f, err := sfnt.ParseReaderAt(file)
		if err != nil {
			return nil, err
		}
		return []*sfnt.Font{f}, nil
	}
	c, err := sfnt.ParseCollectionReaderAt(file)
	if err != nil {
		return nil, err
	}
	fonts := make([]*sfnt.Font, c.NumFonts())
	for i := range fonts {
		if fonts[i], err = c.Font(i); err != nil {
			return nil, err
		}
	}
	return fonts, nil
}

// OpenData parses a font, or the first font of a collection, held in
// memory.
func OpenData(data []byte) (*Font, error) {
	var f *sfnt.Font
	var err error
	if len(data) >= 4 && string(data[:4]) == "ttcf" {
		var c *sfnt.Collection
		if c, err = sfnt.ParseCollection(data); err == nil {
			f, err = c.Font(0)
		}
	} else {
		f, err = sfnt.Parse(data)
	}
	if err != nil {
		return nil, fmt.Errorf("goimgtools: not a font: %w", err)
	}
	return &Font{Info: describe(f, "", 0), f: f, data: data}, nil
}

// Open loads the font spec names: the built-in font, a font file
// ("C:/fonts/a.ttf"), one font of a collection file ("msyh.ttc#1"), or the
// family or full name of a font installed on the system ("Microsoft YaHei",
// "SimHei"), whatever the letter case.
func Open(spec string) (*Font, error) {
	if strings.EqualFold(spec, BuiltIn) {
		return builtIn(), nil
	}
	path, index := spec, 0
	if i := strings.LastIndexByte(spec, '#'); i > 0 {
		if n, err := strconv.Atoi(spec[i+1:]); err == nil {
			path, index = spec[:i], n
		}
	}
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return openFile(path, index)
	}
	var byFamily *FontInfo
	for _, f := range System() {
		f := f
		if strings.EqualFold(f.Full, spec) {
			return openFile(f.Path, f.Index)
		}
		// A family name alone means its regular style.
		if strings.EqualFold(f.Family, spec) && (byFamily == nil || isRegular(f.Style) && !isRegular(byFamily.Style)) {
			byFamily = &f
		}
	}
	if byFamily != nil {
		return openFile(byFamily.Path, byFamily.Index)
	}
	return nil, fmt.Errorf("goimgtools: font %q is neither a file nor a font installed on this system", spec)
}

func isRegular(style string) bool {
	s := strings.ToLower(style)
	return s == "" || s == "regular" || s == "normal" || s == "book" || s == "roman"
}

// fontDirs are the places the operating system keeps fonts.
func fontDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		dirs := []string{filepath.Join(os.Getenv("WINDIR"), "Fonts")}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
		}
		return dirs
	case "darwin":
		return []string{"/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library", "Fonts")}
	}
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".fonts"), filepath.Join(home, ".local", "share", "fonts")}
}

var (
	systemOnce  sync.Once
	systemFonts []FontInfo
)

// System lists the fonts installed on this system, sorted by name. The
// font directories are read once per process.
func System() []FontInfo {
	systemOnce.Do(func() {
		for _, dir := range fontDirs() {
			if dir == "" {
				continue
			}
			filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				switch strings.ToLower(filepath.Ext(path)) {
				case ".ttf", ".otf", ".ttc", ".otc":
				default:
					return nil
				}
				file, err := os.Open(path)
				if err != nil {
					return nil
				}
				defer file.Close()
				fonts, err := parseFile(file)
				if err != nil {
					return nil // not a font this library reads
				}
				for i, f := range fonts {
					systemFonts = append(systemFonts, describe(f, path, i))
				}
				return nil
			})
		}
		sort.SliceStable(systemFonts, func(i, j int) bool {
			a, b := systemFonts[i], systemFonts[j]
			if a.Family != b.Family {
				return strings.ToLower(a.Family) < strings.ToLower(b.Family)
			}
			return a.Full < b.Full
		})
	})
	return systemFonts
}

// preferred are the families used when no font is named, in this order:
// the usual Chinese-capable interface fonts of Windows, macOS and Linux.
// Each also covers Latin text.
var preferred = []string{
	"Microsoft YaHei", "PingFang SC", "Noto Sans CJK SC", "Noto Sans SC", "Source Han Sans SC",
	"Source Han Sans CN", "WenQuanYi Micro Hei", "WenQuanYi Zen Hei", "Hiragino Sans GB", "STHeiti",
	"DengXian", "SimHei", "SimSun", "NSimSun", "Microsoft JhengHei",
}

// knownFiles are where those fonts usually are, so that finding the
// defaults does not take reading every font on the system.
func knownFiles() []string {
	switch runtime.GOOS {
	case "windows":
		dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
		return []string{filepath.Join(dir, "msyh.ttc"), filepath.Join(dir, "Deng.ttf"), filepath.Join(dir, "simhei.ttf"), filepath.Join(dir, "simsun.ttc")}
	case "darwin":
		return []string{"/System/Library/Fonts/PingFang.ttc", "/System/Library/Fonts/Hiragino Sans GB.ttc", "/System/Library/Fonts/STHeiti Light.ttc", "/Library/Fonts/Arial Unicode.ttf"}
	}
	return []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc", "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc", "/usr/share/fonts/wenquanyi/wqy-microhei/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	}
}

// fallbackFiles are fonts of symbols and of more scripts, tried, after the
// default fonts, for a character none of those has: opened only then.
func fallbackFiles() []string {
	switch runtime.GOOS {
	case "windows":
		dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
		var out []string
		for _, f := range []string{"segoeui.ttf", "tahoma.ttf", "seguisym.ttf", "seguiemj.ttf", "seguihis.ttf", "Nirmala.ttf", "ebrima.ttf", "gadugi.ttf",
			"LeelawUI.ttf", "malgun.ttf", "YuGothR.ttc", "himalaya.ttf", "monbaiti.ttf", "taile.ttf", "sylfaen.ttf", "arialuni.ttf"} {
			out = append(out, filepath.Join(dir, f))
		}
		return out
	case "darwin":
		return []string{"/System/Library/Fonts/Apple Symbols.ttf", "/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
			"/Library/Fonts/Arial Unicode.ttf", "/System/Library/Fonts/Supplemental/STIXTwoMath.otf"}
	}
	return []string{
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/noto/NotoSansSymbols-Regular.ttf", "/usr/share/fonts/truetype/noto/NotoSansSymbols2-Regular.ttf",
		"/usr/share/fonts/noto/NotoSansSymbols-Regular.ttf", "/usr/share/fonts/noto/NotoSansSymbols2-Regular.ttf",
		"/usr/share/fonts/google-noto/NotoSansSymbols-Regular.ttf", "/usr/share/fonts/google-noto/NotoSansSymbols2-Regular.ttf",
	}
}

var (
	defaultsOnce sync.Once
	defaultFonts []FontInfo
)

// Defaults lists the fonts text is drawn with when none is named, in the
// order they are tried for each character: the Chinese-capable fonts found
// in their usual places on this system (or, if none is there, the first
// such font among all installed ones), the built-in font, and last the
// fonts of symbols and other scripts found in their usual places.
func Defaults() []FontInfo {
	defaultsOnce.Do(func() {
		for _, path := range knownFiles() {
			if st, err := os.Stat(path); err != nil || st.IsDir() {
				continue
			}
			if f, err := openFile(path, 0); err == nil {
				defaultFonts = append(defaultFonts, f.Info)
				f.Close()
			}
		}
		if len(defaultFonts) == 0 {
			sys := System()
			pick := func(match func(FontInfo) bool) bool {
				for _, f := range sys {
					if match(f) && isRegular(f.Style) {
						defaultFonts = append(defaultFonts, f)
						return true
					}
				}
				return false
			}
			found := false
			for _, name := range preferred {
				if pick(func(f FontInfo) bool { return strings.EqualFold(f.Family, name) }) {
					found = true
					break
				}
			}
			if !found {
				pick(func(f FontInfo) bool { return f.CJK })
			}
		}
		defaultFonts = append(defaultFonts, builtIn().Info)
		// Then fonts of symbols and other scripts, for characters the
		// fonts above lack (they are opened only for such a character):
		// after the built-in font, so that what it draws stays as it is.
		for _, path := range fallbackFiles() {
			if st, err := os.Stat(path); err != nil || st.IsDir() {
				continue
			}
			if f, err := openFile(path, 0); err == nil {
				defaultFonts = append(defaultFonts, f.Info)
				f.Close()
			}
		}
	})
	return defaultFonts
}

// FontBytes is what one open font is counted as in the memory budget. A
// font opened from a file keeps its character map in memory and reads
// glyphs from the file as they are drawn: measured at 0.07 to 0.13MB for
// 9 to 19MB Chinese fonts.
const FontBytes = 256 << 10

// Set is the fonts for one piece of text: the ones asked for, opened at
// once, and the defaults, each opened only when a character turns up that
// no font before it has.
// First is the set's first font, whose lines (ascent, x-height,
// underline) text set in it keeps whatever font draws a character.
func (s *Set) First() *Font {
	if len(s.fonts) == 0 {
		return nil
	}
	return s.fonts[0]
}

type Set struct {
	fonts   []*Font
	pending []FontInfo
	reserve func(n int64) error
	held    int64
	buf     sfnt.Buffer
}

// Load opens the fonts named by specs and data, in that order. reserve is
// called with FontBytes for every font opened, now or later; release, at
// Close, with the total.
func Load(specs []string, data [][]byte, reserve func(n int64) error) (*Set, error) {
	s := &Set{reserve: reserve}
	for _, spec := range specs {
		if err := s.add(func() (*Font, error) { return Open(spec) }); err != nil {
			s.Close(nil)
			return nil, err
		}
	}
	for _, d := range data {
		if err := s.add(func() (*Font, error) { return OpenData(d) }); err != nil {
			s.Close(nil)
			return nil, err
		}
	}
	s.pending = Defaults()
	if len(s.fonts) == 0 { // text needs at least one font for its metrics
		if err := s.more(); err != nil {
			s.Close(nil)
			return nil, err
		}
	}
	return s, nil
}

func (s *Set) add(open func() (*Font, error)) error {
	if err := s.reserve(FontBytes); err != nil {
		return err
	}
	s.held += FontBytes
	f, err := open()
	if err != nil {
		return err
	}
	s.fonts = append(s.fonts, f)
	return nil
}

// more opens the next default font, skipping ones that fail to open.
func (s *Set) more() error {
	for len(s.pending) > 0 {
		info := s.pending[0]
		s.pending = s.pending[1:]
		if err := s.reserve(FontBytes); err != nil {
			return err
		}
		s.held += FontBytes
		f := builtIn()
		if info.Path != "" {
			var err error
			if f, err = openFile(info.Path, info.Index); err != nil {
				continue // its reservation is given back at Close
			}
		}
		s.fonts = append(s.fonts, f)
		return nil
	}
	return nil
}

// pick returns the index of the first font that has r, opening default
// fonts as needed; 0 when none has it (the font then draws its "missing"
// box).
func (s *Set) pick(r rune) (int, error) {
	for i := 0; ; i++ {
		if i == len(s.fonts) {
			if len(s.pending) == 0 {
				return 0, nil
			}
			if err := s.more(); err != nil {
				return 0, err
			}
			if i == len(s.fonts) {
				return 0, nil
			}
		}
		if s.fonts[i].has(&s.buf, r) {
			return i, nil
		}
	}
}

// wantsColour reports whether character i of rs is shown as a colour emoji:
// one of the emoji that are by default (Unicode's Emoji_Presentation) or one asked
// to be by the variation selector that follows it, not one asked to be
// text.
func wantsColour(rs []rune, i int) bool {
	if !EmojiBase(rs[i]) {
		return false
	}
	next := rune(0)
	if i+1 < len(rs) {
		next = rs[i+1]
	}
	switch next {
	case 0xFE0F:
		return true
	case 0xFE0E:
		return false
	}
	r := rs[i]
	k := sort.Search(len(emojiPresentationTable), func(k int) bool { return emojiPresentationTable[k][1] >= r })
	return k < len(emojiPresentationTable) && emojiPresentationTable[k][0] <= r
}

// EmojiBase reports whether r can be shown as an emoji: a keycap's digit,
// # or *, © and ®, or a symbol from U+2000 on (letters cannot).
func EmojiBase(r rune) bool {
	return r >= 0x2000 || r >= '0' && r <= '9' || r == '#' || r == '*' || r == 0xA9 || r == 0xAE
}

// pickFor is the font for character i of rs: for a colour emoji, the first
// font with a colour glyph for it, if one has; else the first with a
// glyph.
func (s *Set) pickFor(rs []rune, i int) (int, error) {
	if !wantsColour(rs, i) {
		return s.pick(rs[i])
	}
	for k := 0; ; k++ {
		if k == len(s.fonts) {
			if len(s.pending) == 0 {
				break
			}
			if err := s.more(); err != nil {
				return 0, err
			}
			if k == len(s.fonts) {
				break
			}
		}
		id, err := s.fonts[k].f.GlyphIndex(&s.buf, rs[i])
		if err != nil || id == 0 {
			continue
		}
		c, err := s.colourFont(k)
		if err != nil {
			return 0, err
		}
		if len(c.Layers(uint16(id), nil)) > 0 {
			return k, nil
		}
	}
	return s.pick(rs[i])
}

// PickFor is the font of the set for character i of rs, as Pick, but for a
// colour emoji the first font with a colour glyph for it, if one has.
func (s *Set) PickFor(rs []rune, i int) (*Font, error) {
	k, err := s.pickFor(rs, i)
	if err != nil || len(s.fonts) == 0 {
		return nil, err
	}
	return s.fonts[k], nil
}

// Pick is the first font of the set that has r, default fonts opened as
// needed; the first font when none has it.
func (s *Set) Pick(r rune) (*Font, error) {
	i, err := s.pick(r)
	if err != nil || len(s.fonts) == 0 {
		return nil, err
	}
	return s.fonts[i], nil
}

// Fonts lists the fonts opened so far.
func (s *Set) Fonts() []FontInfo {
	out := make([]FontInfo, len(s.fonts))
	for i, f := range s.fonts {
		out[i] = f.Info
	}
	return out
}

// Close closes the fonts and hands what was reserved to release.
func (s *Set) Close(release func(n int64)) {
	for _, f := range s.fonts {
		f.Close()
	}
	s.fonts = nil
	if release != nil && s.held != 0 {
		release(s.held)
	}
	s.held = 0
}
