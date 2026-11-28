package childweb

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// The app icon is drawn here rather than shipped as a binary file: a blue
// square with a white speech bubble and three dots. The square is opaque to
// the edges (iOS fills transparency with black) and the bubble stays inside
// the central safe zone of a maskable icon.
var (
	iconBlue  = color.RGBA{0x33, 0x55, 0xaa, 0xff}
	iconWhite = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// inBubble reports whether a point in unit coordinates (0..1) is white.
func inBubble(x, y float64) bool {
	dx, dy := (x-0.5)/0.3, (y-0.46)/0.22
	inside := dx*dx+dy*dy <= 1 || inTriangle(x, y, 0.33, 0.58, 0.45, 0.65, 0.28, 0.76)
	if !inside {
		return false
	}
	for _, cx := range []float64{0.38, 0.5, 0.62} {
		ex, ey := x-cx, y-0.46
		if ex*ex+ey*ey <= 0.045*0.045 {
			return false
		}
	}
	return true
}

func inTriangle(x, y, ax, ay, bx, by, cx, cy float64) bool {
	side := func(px, py, qx, qy float64) float64 { return (x-qx)*(py-qy) - (px-qx)*(y-qy) }
	d1, d2, d3 := side(ax, ay, bx, by), side(bx, by, cx, cy), side(cx, cy, ax, ay)
	negative := d1 < 0 || d2 < 0 || d3 < 0
	positive := d1 > 0 || d2 > 0 || d3 > 0
	return !(negative && positive)
}

// drawIcon renders the icon with 4×4 supersampling for smooth edges.
func drawIcon(size int) []byte {
	const samples = 4
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			white := 0
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/samples) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/samples) / float64(size)
					if inBubble(x, y) {
						white++
					}
				}
			}
			mix := func(a, b uint8) uint8 {
				return uint8((int(a)*(samples*samples-white) + int(b)*white) / (samples * samples))
			}
			img.SetRGBA(px, py, color.RGBA{mix(iconBlue.R, iconWhite.R), mix(iconBlue.G, iconWhite.G), mix(iconBlue.B, iconWhite.B), 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err) // encoding an in-memory RGBA image cannot fail
	}
	return b.Bytes()
}

var (
	iconsOnce sync.Once
	icons     map[int][]byte
)

// icon returns the PNG for one of the served sizes; they are drawn once.
func icon(size int) []byte {
	iconsOnce.Do(func() {
		icons = map[int][]byte{}
		for _, s := range []int{180, 192, 512} {
			icons[s] = drawIcon(s)
		}
	})
	return icons[size]
}
