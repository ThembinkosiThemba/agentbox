// Command genicon draws the agentbox icon and writes it as PNG and ICO files.
// The shapes are defined in code so every size is rendered from the vector
// description rather than scaled from a bitmap:
//
//	go run ./tools/genicon
//
// then `go-winres make` (see winres/) to embed it in the Windows binary.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

type vec struct{ x, y float64 }

type rgba struct{ r, g, b, a float64 } // straight alpha, 0..1

func hex(s string) rgba {
	var v uint32
	for _, c := range s[1:] {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint32(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint32(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			v |= uint32(c - 'A' + 10)
		}
	}
	return rgba{float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, 1}
}

func mix(a, b rgba, t float64) rgba {
	return rgba{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

// over composites src over dst.
func over(dst, src rgba) rgba {
	a := src.a + dst.a*(1-src.a)
	if a == 0 {
		return rgba{}
	}
	f := func(s, d float64) float64 { return (s*src.a + d*dst.a*(1-src.a)) / a }
	return rgba{f(src.r, dst.r), f(src.g, dst.g), f(src.b, dst.b), a}
}

// Palette, matching the dashboard's accent.
var (
	bgTop    = hex("#2B2266")
	bgBottom = hex("#120E2E")
	rim      = hex("#4A3DA8")
	faceTop  = hex("#E4DEFF")
	faceLeft = hex("#A496FF")
	faceRite = hex("#6A55F0")
	prompt   = hex("#1A1442")
	live     = hex("#3FB950")
)

// roundedRect is the signed distance to a rounded square centred at 0.5.
func roundedRect(p vec, half, r float64) float64 {
	qx := math.Abs(p.x-0.5) - half + r
	qy := math.Abs(p.y-0.5) - half + r
	out := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	return out + math.Min(math.Max(qx, qy), 0) - r
}

// inPoly reports whether p is inside a convex polygon.
func inPoly(p vec, poly []vec) bool {
	sign := 0.0
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		c := (b.x-a.x)*(p.y-a.y) - (b.y-a.y)*(p.x-a.x)
		if c == 0 {
			continue
		}
		if sign == 0 {
			sign = c
		} else if (c > 0) != (sign > 0) {
			return false
		}
	}
	return true
}

// shrink pulls a polygon toward its centroid, opening seams between faces.
func shrink(poly []vec, by float64) []vec {
	var c vec
	for _, p := range poly {
		c.x += p.x / float64(len(poly))
		c.y += p.y / float64(len(poly))
	}
	out := make([]vec, len(poly))
	for i, p := range poly {
		dx, dy := p.x-c.x, p.y-c.y
		d := math.Hypot(dx, dy)
		out[i] = vec{p.x - dx/d*by, p.y - dy/d*by}
	}
	return out
}

// segDist is the distance from p to the segment ab.
func segDist(p, a, b vec) float64 {
	abx, aby := b.x-a.x, b.y-a.y
	t := ((p.x-a.x)*abx + (p.y-a.y)*aby) / (abx*abx + aby*aby)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.x-a.x-abx*t, p.y-a.y-aby*t)
}

type scene struct {
	size                 int
	top, left, right     []vec
	chevron              [3]vec
	cursor               [2]vec
	stroke               float64
	dot                  vec
	dotR, ringR, bgHalf  float64
	bgRadius, rimWidth   float64
	showPrompt, showRing bool
}

// newScene lays the icon out in unit coordinates. Small sizes get thicker
// strokes and no fine detail, the way hand-tuned icons do.
func newScene(size int) *scene {
	s := &scene{size: size}
	px := 1 / float64(size)
	s.bgHalf = 0.5 - math.Max(0.02, px)
	if size <= 24 {
		s.bgHalf = 0.5
	}
	s.bgRadius = 0.23
	s.rimWidth = math.Max(0.012, 1.0*px)

	// An isometric box, slightly below centre so it sits optically centred.
	cx, cy, r := 0.5, 0.53, 0.30
	switch {
	case size <= 24:
		cx, cy, r = 0.46, 0.56, 0.40
	case size <= 48:
		r = 0.34
	}
	k := math.Cos(math.Pi / 6)
	T := vec{cx, cy - r}
	UL := vec{cx - r*k, cy - r/2}
	UR := vec{cx + r*k, cy - r/2}
	C := vec{cx, cy}
	LL := vec{cx - r*k, cy + r/2}
	LR := vec{cx + r*k, cy + r/2}
	B := vec{cx, cy + r}
	seam := math.Max(0.012, 0.8*px)
	s.top = shrink([]vec{T, UR, C, UL}, seam)
	s.left = shrink([]vec{UL, C, B, LL}, seam)
	s.right = shrink([]vec{C, UR, LR, B}, seam)

	// A shell prompt on the front-left face, drawn in the face's own
	// isometric plane: x runs along UL→C, y runs down.
	u := vec{(C.x - UL.x), (C.y - UL.y)} // across the face
	v := vec{0, r}                       // down the face
	at := func(a, b float64) vec { return vec{UL.x + u.x*a + v.x*b, UL.y + u.y*a + v.y*b} }
	s.chevron = [3]vec{at(0.22, 0.30), at(0.44, 0.50), at(0.22, 0.70)}
	s.cursor = [2]vec{at(0.54, 0.74), at(0.78, 0.74)}
	s.stroke = math.Max(0.026, 1.1*px)
	s.showPrompt = size > 48

	// The "live" dot from the dashboard, top right.
	s.dot = vec{0.80, 0.20}
	s.dotR = 0.075
	s.ringR = s.dotR + math.Max(0.03, 1.2*px)
	s.showRing = true
	if size <= 24 {
		s.dot, s.dotR = vec{0.83, 0.17}, 0.12
		s.ringR = s.dotR + 1.0*px
	}
	return s
}

func (s *scene) sample(p vec) rgba {
	out := rgba{}
	d := roundedRect(p, s.bgHalf, s.bgRadius)
	if d > 0 {
		return out
	}
	bg := mix(bgTop, bgBottom, p.y)
	// A soft glow behind the box.
	glow := math.Max(0, 1-math.Hypot(p.x-0.5, p.y-0.5)/0.45)
	bg = mix(bg, hex("#3A2E8C"), glow*glow*0.6)
	if s.size > 24 && d > -s.rimWidth {
		bg = mix(bg, rim, 0.8)
	}
	out = over(out, bg)

	switch {
	case inPoly(p, s.top):
		out = over(out, faceTop)
	case inPoly(p, s.left):
		out = over(out, faceLeft)
		if s.showPrompt {
			c := s.chevron
			if segDist(p, c[0], c[1]) < s.stroke || segDist(p, c[1], c[2]) < s.stroke ||
				segDist(p, s.cursor[0], s.cursor[1]) < s.stroke {
				out = over(out, prompt)
			}
		}
	case inPoly(p, s.right):
		out = over(out, faceRite)
	}

	if s.showRing && math.Hypot(p.x-s.dot.x, p.y-s.dot.y) < s.ringR {
		out = over(out, bgTop)
	}
	if math.Hypot(p.x-s.dot.x, p.y-s.dot.y) < s.dotR {
		out = over(out, live)
	}
	return out
}

func render(size int) *image.NRGBA {
	const ss = 8 // supersamples per axis
	s := newScene(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					p := vec{(float64(x) + (float64(i)+0.5)/ss) / float64(size), (float64(y) + (float64(j)+0.5)/ss) / float64(size)}
					c := s.sample(p)
					r += c.r * c.a
					g += c.g * c.a
					b += c.b * c.a
					a += c.a
				}
			}
			n := float64(ss * ss)
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(r / a * 255)), G: uint8(math.Round(g / a * 255)), B: uint8(math.Round(b / a * 255)),
				A: uint8(math.Round(a / n * 255)),
			})
		}
	}
	return img
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}

// writeICO packs PNG images into an .ico, which Windows Vista and later
// read at every size.
func writeICO(path string, sizes []int) {
	var hdr, body bytes.Buffer
	binary.Write(&hdr, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for _, sz := range sizes {
		data := encode(render(sz))
		dim := uint8(sz)
		if sz >= 256 {
			dim = 0
		}
		binary.Write(&hdr, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(data)), uint32(offset)})
		offset += len(data)
		body.Write(data)
	}
	write(path, append(hdr.Bytes(), body.Bytes()...))
}

func write(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote", path)
}

func main() {
	preview := flag.String("preview", "", "also write a contact sheet of the small sizes, enlarged, to this file")
	flag.Parse()

	writeICO("assets/icon/agentbox.ico", []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256})
	write("assets/icon/agentbox.png", encode(render(512)))
	// Embedded in the binary for the Linux desktop entry.
	write("internal/launcher/icon.png", encode(render(256)))

	if *preview != "" {
		// Each size scaled up 8x with hard pixels, beside the 256 render.
		sizes := []int{16, 24, 32, 48}
		const zoom, gap = 8, 16
		w := gap
		for _, sz := range sizes {
			w += sz*zoom + gap
		}
		w += 256 + gap
		sheet := image.NewNRGBA(image.Rect(0, 0, w, 48*zoom+2*gap))
		for i := range sheet.Pix {
			sheet.Pix[i] = 0xff // white, to judge edges
		}
		x := gap
		for _, sz := range sizes {
			img := render(sz)
			for y := 0; y < sz*zoom; y++ {
				for xx := 0; xx < sz*zoom; xx++ {
					c := img.NRGBAAt(xx/zoom, y/zoom)
					bg := sheet.NRGBAAt(x+xx, gap+y)
					a := float64(c.A) / 255
					bl := func(f, b uint8) uint8 { return uint8(float64(f)*a + float64(b)*(1-a)) }
					sheet.SetNRGBA(x+xx, gap+y, color.NRGBA{bl(c.R, bg.R), bl(c.G, bg.G), bl(c.B, bg.B), 255})
				}
			}
			x += sz*zoom + gap
		}
		big := render(256)
		for y := 0; y < 256; y++ {
			for xx := 0; xx < 256; xx++ {
				c := big.NRGBAAt(xx, y)
				a := float64(c.A) / 255
				bl := func(f uint8) uint8 { return uint8(float64(f)*a + 255*(1-a)) }
				sheet.SetNRGBA(x+xx, gap+y, color.NRGBA{bl(c.R), bl(c.G), bl(c.B), 255})
			}
		}
		write(*preview, encode(sheet))
	}
}
