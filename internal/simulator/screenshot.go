package simulator

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/proto"
)

// Shot is a screenshot sized in device points.
type Shot struct {
	PNG    []byte
	Width  int
	Height int
}

// Screenshot captures a booted device. The image is reduced by the device's
// integer scale so its pixel size equals the size in device points. It needs
// no lease.
func (s *Service) Screenshot(ctx context.Context, caller proto.SessionID, device string) (Shot, error) {
	d, err := s.resolve(ctx, caller, device)
	if err != nil {
		return Shot{}, err
	}
	if err := requireBooted(d); err != nil {
		return Shot{}, err
	}
	if d.Scale <= 0 {
		return Shot{}, fmt.Errorf("screen scale of device %q is unavailable, so the image cannot be sized in device points; use a newer device type", d.Name)
	}
	dir, err := os.MkdirTemp("", "hoplet-shot-")
	if err != nil {
		return Shot{}, fmt.Errorf("create screenshot directory: %w", err)
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "screen.png")
	if _, err := s.simctlWithin(ctx, s.opLimit, "io", d.UDID, "screenshot", "--type=png", file); err != nil {
		return Shot{}, err
	}
	data, err := os.ReadFile(file) //nolint:gosec // file lives in a directory this function created
	if err != nil {
		return Shot{}, fmt.Errorf("read screenshot: %w", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Shot{}, fmt.Errorf("decode screenshot: %w", err)
	}
	b := img.Bounds()
	if b.Dx()%d.Scale != 0 || b.Dy()%d.Scale != 0 {
		return Shot{}, fmt.Errorf("screenshot is %dx%d pixels, not a multiple of scale %d", b.Dx(), b.Dy(), d.Scale)
	}
	w, h := b.Dx()/d.Scale, b.Dy()/d.Scale
	if d.Scale == 1 {
		return Shot{PNG: data, Width: w, Height: h}, nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, boxDownscale(img, d.Scale)); err != nil {
		return Shot{}, fmt.Errorf("encode screenshot: %w", err)
	}
	return Shot{PNG: buf.Bytes(), Width: w, Height: h}, nil
}

// boxDownscale averages each factor x factor block into one pixel.
func boxDownscale(src image.Image, factor int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx()/factor, b.Dy()/factor
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	n := uint64(factor) * uint64(factor) //nolint:gosec // factor is a positive device scale
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a uint64
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					c := color.NRGBAModel.Convert(src.At(b.Min.X+x*factor+dx, b.Min.Y+y*factor+dy)).(color.NRGBA)
					r += uint64(c.R)
					g += uint64(c.G)
					bl += uint64(c.B)
					a += uint64(c.A)
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: mean(r, n), G: mean(g, n), B: mean(bl, n), A: mean(a, n)})
		}
	}
	return dst
}

// mean returns the rounded average of n 8-bit samples summing to sum.
func mean(sum, n uint64) uint8 {
	return uint8(min((sum+n/2)/n, 255))
}
