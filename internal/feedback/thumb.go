package feedback

/**
 * [INPUT]: 依赖标准库 image/png|jpeg|gif 解码器、Attachment（model.go）
 * [OUTPUT]: 对外提供 Thumbnails（把已落盘的原图缩成小 JPEG，与输入等长、逐项对位；解不开的一项 DataBase64 为空）
 * [POS]: 历史里的图片缩略图（2026-11-09）：桌面与手机只读到这几 KB，从不为一个气泡拉整张 4 MiB 原图；纯函数，无 IO
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"

	_ "image/gif" // registers the decoders image.Decode dispatches on
	_ "image/png"
)

const (
	// thumbEdge is the longest side of a thumbnail in pixels (two tiles of 80 DIP on a 2x screen).
	thumbEdge = 160
	// maxThumbPixels refuses a decompression bomb: a 4 MiB PNG can hold far more pixels than it looks.
	maxThumbPixels = 40_000_000
)

// Thumbnails returns one small JPEG per attachment, in order. An image that is not PNG, JPEG or GIF, or is too large
// to decode safely, keeps its slot with empty DataBase64 so the page can draw a placeholder tile in its place.
func Thumbnails(in []Attachment) []Attachment {
	out := make([]Attachment, len(in))
	for i, a := range in {
		out[i] = Attachment{Name: a.Name}
		if b, ok := thumbnail(a.DataBase64); ok {
			out[i].MimeType, out[i].DataBase64 = "image/jpeg", b
		}
	}
	return out
}

func thumbnail(dataBase64 string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return "", false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxThumbPixels {
		return "", false
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, shrink(src, thumbEdge), &jpeg.Options{Quality: 72}); err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// shrink box-averages src down so its longest side is edge pixels (never up), composited on white so a transparent
// PNG does not turn black as a JPEG.
func shrink(src image.Image, edge int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if w >= h && w > edge {
		dw, dh = edge, max(1, h*edge/w)
	} else if h > w && h > edge {
		dw, dh = max(1, w*edge/h), edge
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := b.Min.Y+y*h/dh, b.Min.Y+max(y*h/dh+1, (y+1)*h/dh)
		for x := 0; x < dw; x++ {
			x0, x1 := b.Min.X+x*w/dw, b.Min.X+max(x*w/dw+1, (x+1)*w/dw)
			var r, g, bl, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA() // premultiplied, 16-bit
					// Over white: premultiplied colour + (1-alpha) of white.
					r += cr + (0xffff - ca)
					g += cg + (0xffff - ca)
					bl += cb + (0xffff - ca)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r / n >> 8), G: uint8(g / n >> 8), B: uint8(bl / n >> 8), A: 255})
		}
	}
	return dst
}
