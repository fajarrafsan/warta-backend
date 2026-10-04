package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// palette adalah pasangan warna gradasi dan satu warna aksen untuk sampul.
type palette struct {
	from, to, accent color.RGBA
}

func rgb(hex uint32) color.RGBA {
	return color.RGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 255}
}

var palettes = []palette{
	{rgb(0x0f3d3e), rgb(0x2a9d8f), rgb(0xe9c46a)},
	{rgb(0x1d3557), rgb(0x457b9d), rgb(0xf1faee)},
	{rgb(0x6d2e46), rgb(0xa26769), rgb(0xece2d0)},
	{rgb(0x264653), rgb(0x2a9d8f), rgb(0xf4a261)},
	{rgb(0x3d405b), rgb(0x81b29a), rgb(0xf2cc8f)},
	{rgb(0x22223b), rgb(0x4a4e69), rgb(0xc9ada7)},
	{rgb(0x5f0f40), rgb(0x9a031e), rgb(0xfb8b24)},
	{rgb(0x003049), rgb(0x669bbc), rgb(0xfdf0d5)},
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

type blob struct {
	x, y, r, strength float64
}

// cover membuat sampul 1200x630 (rasio pratinjau link): gradasi diagonal
// dengan beberapa lingkaran lembut berwarna aksen. seed membuat setiap
// sampul berbeda tapi selalu sama untuk seed yang sama.
func cover(p palette, seed int) []byte {
	const w, h = 1200, 630
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	blobs := make([]blob, 0, 4)
	for i := range 4 {
		f := float64(seed*7 + i*13)
		blobs = append(blobs, blob{
			x:        w * (0.15 + 0.7*frac(math.Sin(f)*43758.5453)),
			y:        h * (0.1 + 0.8*frac(math.Sin(f+1.7)*24634.6345)),
			r:        h * (0.18 + 0.32*frac(math.Sin(f+3.1)*12345.6789)),
			strength: 0.25 + 0.35*frac(math.Sin(f+5.3)*98765.4321),
		})
	}

	for y := range h {
		for x := range w {
			// Gradasi diagonal dari kiri atas ke kanan bawah.
			c := mix(p.from, p.to, (float64(x)/w+float64(y)/h)/2)
			for _, b := range blobs {
				d := math.Hypot(float64(x)-b.x, float64(y)-b.y) / b.r
				if d < 1 {
					// Tepi lingkaran memudar halus.
					c = mix(c, p.accent, b.strength*(1-d*d)*(1-d*d))
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return encode(img)
}

// avatar membuat foto profil 256x256: gradasi melingkar dengan cincin aksen.
func avatar(p palette) []byte {
	const size = 256
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	center := size / 2.0
	for y := range size {
		for x := range size {
			d := math.Hypot(float64(x)-center*0.8, float64(y)-center*0.7) / size
			c := mix(p.to, p.from, d*1.4)
			ring := math.Abs(math.Hypot(float64(x)-center, float64(y)-center) - size*0.3)
			if ring < 10 {
				c = mix(c, p.accent, 0.55*(1-ring/10))
			}
			img.SetRGBA(x, y, c)
		}
	}
	return encode(img)
}

func frac(v float64) float64 {
	return math.Abs(v - math.Floor(v))
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
