package overlays

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"strconv"
)

//go:embed anton.ttf
var rankDisplayFont []byte

// Ranked shares the existing bounded, coalescing durable overlay cache. It is
// invoked only on an authorized image request, never during row construction.
func (s *Service) Ranked(ctx context.Context, source []byte, rank int, wide bool) ([]byte, string, error) {
	if rank < 1 || rank > 10 {
		return nil, "", errors.New("invalid rank")
	}
	hash := sha256.New()
	_, _ = hash.Write(source)
	_, _ = fmt.Fprintf(hash, "ranked-v4:anton:%d:%t:jpeg88", rank, wide)
	data, err := s.generated.get(ctx, hex.EncodeToString(hash.Sum(nil)), func(ctx context.Context) ([]byte, error) {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(source))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
			return nil, errors.New("invalid ranked poster dimensions")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		img, _, err := image.Decode(bytes.NewReader(source))
		if err != nil {
			return nil, err
		}
		dst := rankedComposite(img, rank, wide)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var out bytes.Buffer
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 88})
		return out.Bytes(), err
	})
	return data, "image/jpeg", err
}

func rankedComposite(poster image.Image, rank int, wide bool) *image.RGBA {
	if !wide {
		return rankedPosterComposite(poster, rank)
	}
	w, h := 960, 540
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{14, 15, 19, 255}), image.Point{}, draw.Src)
	rect := image.Rect(600, 0, 960, 540)
	rankedNumber(dst, rank, image.Rect(20, 20, 650, 540))
	// Soft shadow belongs behind the poster, not on its title treatment.
	for x := max(0, rect.Min.X-24); x < rect.Min.X; x++ {
		alpha := uint8(85 * float64(x-(rect.Min.X-24)) / 24)
		draw.Draw(dst, image.Rect(x, rect.Min.Y, rect.Min.X, rect.Max.Y), image.NewUniform(color.NRGBA{A: alpha}), image.Point{}, draw.Over)
	}
	xdraw.CatmullRom.Scale(dst, rect, poster, poster.Bounds(), draw.Over, nil)
	return dst
}

// rankedPosterComposite keeps the whole poster as the tile and inlays the
// rank in its bottom-right corner. A corner shade keeps the numeral legible
// on bright artwork without darkening the title treatment.
func rankedPosterComposite(poster image.Image, rank int) *image.RGBA {
	w, h := 500, 750
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{14, 15, 19, 255}), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), poster, poster.Bounds(), draw.Over, nil)
	for y := h / 2; y < h; y++ {
		for x := w / 3; x < w; x++ {
			// 0 along the shade's edge, 1 in the corner.
			t := math.Max(0, math.Min(1, (float64(x-w/3)/float64(w-w/3)+float64(y-h/2)/float64(h-h/2)-0.6)/1.4))
			shade := 1 - 0.62*t*t
			c := dst.RGBAAt(x, y)
			c.R = uint8(float64(c.R) * shade)
			c.G = uint8(float64(c.G) * shade)
			c.B = uint8(float64(c.B) * shade)
			dst.SetRGBA(x, y, c)
		}
	}
	// Bottom-right quarter: large enough to read on a TV shelf, small enough
	// to leave the poster's subject and title visible even for "10".
	rankedNumber(dst, rank, image.Rect(w/2, h*7/10, w-6, h-6))
	return dst
}

// Heavy display numerals, unslashed zero, thick outer stroke and shaded fill.
// Right alignment makes the poster occlude roughly 10% of the numeral edge.
func rankedNumber(dst *image.RGBA, rank int, area image.Rectangle) {
	parsed, err := opentype.Parse(rankDisplayFont)
	if err != nil {
		return
	}
	text := strconv.Itoa(rank)
	size := float64(area.Dy()) * 1.4
	var face font.Face
	var bounds fixed.Rectangle26_6
	for {
		face, err = opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return
		}
		bounds, _ = font.BoundString(face, text)
		if (bounds.Max.Y-bounds.Min.Y).Ceil() <= area.Dy()-12 && (bounds.Max.X-bounds.Min.X).Ceil() <= area.Dx()-12 {
			break
		}
		_ = face.Close()
		size *= .97
	}
	defer face.Close()
	x := area.Max.X - 8 - bounds.Max.X.Ceil()
	y := area.Max.Y - 8 - bounds.Max.Y.Ceil()
	radius := max(4, dst.Bounds().Dx()/90)
	mask := image.NewAlpha(dst.Bounds())
	d := font.Drawer{Dst: mask, Face: face, Src: image.White}
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius {
				d.Dot = fixed.P(x+dx, y+dy)
				d.DrawString(text)
			}
		}
	}
	draw.DrawMask(dst, dst.Bounds(), image.NewUniform(color.RGBA{205, 209, 220, 255}), image.Point{}, mask, image.Point{}, draw.Over)
	fill := image.NewRGBA(dst.Bounds())
	for y := 0; y < dst.Bounds().Dy(); y++ {
		v := uint8(12 + 27*(1-float64(y)/float64(dst.Bounds().Dy())))
		draw.Draw(fill, image.Rect(0, y, dst.Bounds().Dx(), y+1), image.NewUniform(color.RGBA{v, v + 2, v + 5, 255}), image.Point{}, draw.Src)
	}
	inner := image.NewAlpha(dst.Bounds())
	d.Dst = inner
	d.Dot = fixed.P(x, y)
	d.DrawString(text)
	draw.DrawMask(dst, dst.Bounds(), fill, image.Point{}, inner, image.Point{}, draw.Over)
}
