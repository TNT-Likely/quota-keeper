// gen_icons 由同一份几何定义渲染托盘与 README 用的 PNG(透明背景)。
// 用途: 替代 QuickLook 的白底缩略图。用法: 在仓库根目录 go run ./tools/gen_icons
// 彩色版 = 琥珀渐变圆角方块 + 白色 QK 字标(选项 A); template 版 = 黑色字形、透明底。
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	out   = 256.0 // 输出尺寸(像素)
	scale = 8.0   // 超采样倍数
	vb    = 512.0 // viewBox 逻辑尺寸
)

type seg struct{ x1, y1, x2, y2, w float64 }

var (
	ringC = [2]float64{196, 286}
	ringR = 92.0
	ringW = 36.0
	segs  = []seg{
		// Q 的分针与时针(尾巴)
		{196, 286, 196, 212, 36},
		{196, 286, 296, 384, 36},
		// K 的三笔
		{318, 217, 318, 352, 30},
		{318, 290, 400, 217, 30},
		{318, 290, 408, 352, 30},
		// 星芒(8 端)
		{396, 84, 396, 132, 12},
		{372, 108, 420, 108, 12},
		{379, 91, 413, 125, 12},
		{379, 125, 413, 91, 12},
	}
)

func segDist(x, y float64, s seg) float64 {
	dx, dy := s.x2-s.x1, s.y2-s.y1
	l2 := dx*dx + dy*dy
	t := ((x-s.x1)*dx + (y-s.y1)*dy) / l2
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return math.Hypot(x-(s.x1+t*dx), y-(s.y1+t*dy))
}

func glyphAt(x, y float64) bool {
	if math.Abs(math.Hypot(x-ringC[0], y-ringC[1])-ringR) <= ringW/2 {
		return true
	}
	for _, s := range segs {
		if segDist(x, y, s) <= s.w/2 {
			return true
		}
	}
	return false
}

// tileAt 返回圆角方块内的渐变色; 不在方块内返回 false。
func tileAt(x, y float64) (r, g, b float64, in bool) {
	qx := math.Abs(x-256) - 128
	qy := math.Abs(y-256) - 128
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - 112
	if d > 0 {
		return 0, 0, 0, false
	}
	t := math.Min(math.Max((y-16)/480, 0), 1)
	return 255 + (240-255)*t, 193 + (138-193)*t, 77 + (0-77)*t, true
}

func render(template bool) *image.RGBA {
	n := int(out * scale)
	src := image.NewRGBA(image.Rect(0, 0, n, n))
	k := vb / (out * scale)
	for py := 0; py < n; py++ {
		y := (float64(py) + 0.5) * k
		for px := 0; px < n; px++ {
			x := (float64(px) + 0.5) * k
			var c color.RGBA
			if glyphAt(x, y) {
				if template {
					c = color.RGBA{0, 0, 0, 255}
				} else {
					c = color.RGBA{255, 255, 255, 255}
				}
			} else if !template {
				if r, g, b, in := tileAt(x, y); in {
					c = color.RGBA{uint8(r), uint8(g), uint8(b), 255}
				}
			}
			src.SetRGBA(px, py, c)
		}
	}
	return src
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// down 盒式降采样(按 alpha 预乘平均, 避免边缘白边)。
func down(src *image.RGBA, n int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	f := src.Bounds().Dx() / n
	for dy := 0; dy < n; dy++ {
		for dx := 0; dx < n; dx++ {
			var r, g, b, a float64
			for sy := 0; sy < f; sy++ {
				for sx := 0; sx < f; sx++ {
					c := src.RGBAAt(dx*f+sx, dy*f+sy)
					r += float64(c.R) * float64(c.A)
					g += float64(c.G) * float64(c.A)
					b += float64(c.B) * float64(c.A)
					a += float64(c.A)
				}
			}
			kk := float64(f * f)
			am := a / kk / 255
			var ro, gg, bo uint8
			if a > 0 {
				ro = uint8(clamp(r / a))
				gg = uint8(clamp(g / a))
				bo = uint8(clamp(b / a))
			}
			dst.SetRGBA(dx, dy, color.RGBA{ro, gg, bo, uint8(clamp(am * 255))})
		}
	}
	return dst
}

func save(p string, img *image.RGBA) {
	f, err := os.Create(p)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-debug" {
		for _, p := range [][2]float64{{256, 16}, {256, 100}, {256, 256}, {256, 480}, {100, 100}, {440, 440}, {196, 286}} {
			r, g, b, in := tileAt(p[0], p[1])
			fmt.Printf("v(%4.0f,%4.0f) glyph=%-5v tile=%-5v tileRGB=(%.0f,%.0f,%.0f)\n", p[0], p[1], glyphAt(p[0], p[1]), in, r, g, b)
		}
		return
	}
	save("tray/icon.png", down(render(false), int(out)))
	save("tray/icon-template.png", down(render(true), int(out)))
	save("assets/icon-256.png", down(render(false), int(out)))
	println("written tray/icon.png, tray/icon-template.png, assets/icon-256.png")
}
