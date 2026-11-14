// Package testutil has helpers shared by the package tests.
package testutil

import (
	"image"
	"image/color"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Chopped streams m in strips of n rows, so stages see awkward strip sizes.
func Chopped(m image.Image, n int) pipeline.Source {
	return &chopped{m: m, n: n, y: m.Bounds().Min.Y}
}

// ChoppedEnv is Chopped with a pipeline Env attached.
func ChoppedEnv(m image.Image, n int, env *pipeline.Env) pipeline.Source {
	return &chopped{m: m, n: n, y: m.Bounds().Min.Y, env: env}
}

type chopped struct {
	m    image.Image
	n, y int
	env  *pipeline.Env
}

func (c *chopped) Env() *pipeline.Env { return c.env }

func (c *chopped) Bounds() image.Rectangle { return c.m.Bounds() }
func (c *chopped) ColorModel() color.Model { return c.m.ColorModel() }
func (c *chopped) Close() error            { return nil }
func (c *chopped) Next() (image.Image, error) {
	b := c.m.Bounds()
	if c.y >= b.Max.Y {
		return nil, io.EOF
	}
	r := image.Rect(b.Min.X, c.y, b.Max.X, min(c.y+c.n, b.Max.Y))
	c.y = r.Max.Y
	return c.m.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(r), nil
}

// Images returns one image of every common pixel type, w × h, with a mix of
// smooth gradients and noise.
func Images(w, h int) map[string]image.Image {
	r := rand.New(rand.NewSource(7))
	rect := image.Rect(0, 0, w, h)
	px := func(x, y int) (uint8, uint8, uint8, uint8) {
		n := uint8(r.Intn(40))
		return uint8(x*255/w) + n, uint8(y*255/h) + n, uint8((x+y)*3) ^ n, uint8(255 - (x*y)%200)
	}
	gray, gray16 := image.NewGray(rect), image.NewGray16(rect)
	rgba, nrgba, nrgba64 := image.NewRGBA(rect), image.NewNRGBA(rect), image.NewNRGBA64(rect)
	opaque := image.NewRGBA(rect)
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.NRGBA{uint8(i), uint8(i * 5), uint8(255 - i), uint8(100 + i/2)}
	}
	paletted := image.NewPaletted(rect, pal)
	ratios := map[string]image.YCbCrSubsampleRatio{
		"ycbcr444": image.YCbCrSubsampleRatio444, "ycbcr422": image.YCbCrSubsampleRatio422,
		"ycbcr420": image.YCbCrSubsampleRatio420, "ycbcr440": image.YCbCrSubsampleRatio440,
		"ycbcr411": image.YCbCrSubsampleRatio411,
	}
	out := map[string]image.Image{}
	for name, ratio := range ratios {
		m := image.NewYCbCr(rect, ratio)
		for i := range m.Y {
			m.Y[i] = uint8(i*7/w) + uint8(r.Intn(30))
		}
		for i := range m.Cb {
			m.Cb[i], m.Cr[i] = uint8(r.Intn(256)), uint8(i)
		}
		out[name] = m
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			R, G, B, A := px(x, y)
			gray.SetGray(x, y, color.Gray{R})
			gray16.SetGray16(x, y, color.Gray16{uint16(R)<<8 | uint16(G)})
			nrgba.SetNRGBA(x, y, color.NRGBA{R, G, B, A})
			rgba.Set(x, y, color.NRGBA{R, G, B, A})
			opaque.SetRGBA(x, y, color.RGBA{R, G, B, 255})
			nrgba64.SetNRGBA64(x, y, color.NRGBA64{uint16(R) << 8, uint16(G)<<8 | 77, uint16(B) << 8, uint16(A)<<8 | 3})
			paletted.SetColorIndex(x, y, R^B)
		}
	}
	out["gray"], out["gray16"], out["rgba"], out["rgba-opaque"] = gray, gray16, rgba, opaque
	out["nrgba"], out["nrgba64"], out["paletted"] = nrgba, nrgba64, paletted
	return out
}

// XImageTestdata returns the testdata directory of the golang.org/x/image
// module in use, or "" when it cannot be found.
func XImageTestdata() string {
	dir := XImageDir()
	if dir == "" {
		return ""
	}
	dir = filepath.Join(dir, "testdata")
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// XImageDir returns the directory of the golang.org/x/image module in use
// (its test images are what the decoders are checked against), or "" when
// it cannot be found. Every test that wants those images asks here.
func XImageDir() string { return xImageDir() }

var xImageDir = sync.OnceValue(func() string {
	// The go command knows.
	if out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "golang.org/x/image").Output(); err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			if _, err := os.Stat(dir); err == nil {
				return dir
			}
		}
	}
	// Without it (a test binary run by itself, on a machine without Go):
	// the version go.mod asks for, in the module cache.
	version := ""
	for dir, _ := os.Getwd(); version == ""; {
		if mod, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for _, line := range strings.Split(string(mod), "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[len(f)-2] == "golang.org/x/image" {
					version = f[len(f)-1]
				}
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if version == "" {
		return ""
	}
	caches := filepath.SplitList(os.Getenv("GOMODCACHE"))
	for _, gopath := range filepath.SplitList(os.Getenv("GOPATH")) {
		caches = append(caches, filepath.Join(gopath, "pkg", "mod"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		caches = append(caches, filepath.Join(home, "go", "pkg", "mod"))
	}
	for _, cache := range caches {
		dir := filepath.Join(cache, "golang.org", "x", "image@"+version)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	return ""
})
