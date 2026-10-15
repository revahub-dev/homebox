package vision

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register decoders
	"image/jpeg"
	_ "image/png"

	"github.com/evanoberholster/imagemeta"
	_ "github.com/gen2brain/heic"
	_ "github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"

	"github.com/sysadminsmedia/homebox/backend/pkgs/utils"
)

const (
	// SceneMaxEdge is the long edge sent to the model.
	SceneMaxEdge = 1600
	// CropMaxEdge is the long edge of stored crops.
	CropMaxEdge = 512
	cropPad     = 0.08
	jpegQuality = 82
	// maxPixels guards against decompression bombs (~64 MP).
	maxPixels = 64_000_000
)

// Decode decodes an image, applies EXIF orientation, and returns it.
func Decode(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("vision: not a supported image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("vision: image dimensions out of range (%dx%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("vision: decode: %w", err)
	}
	if meta, err := imagemeta.Decode(bytes.NewReader(data)); err == nil {
		if o := uint16(meta.IFD0.Orientation); o > 1 {
			img = utils.ApplyOrientation(img, o)
		}
	}
	return img, nil
}

// Normalise decodes, orients, downsizes (long edge SceneMaxEdge) and
// re-encodes as JPEG. Re-encoding also drops EXIF (including GPS).
func Normalise(data []byte) (jpg []byte, w, h int, err error) {
	img, err := Decode(data)
	if err != nil {
		return nil, 0, 0, err
	}
	img = fit(img, SceneMaxEdge)
	out, err := encode(img)
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	return out, b.Dx(), b.Dy(), nil
}

// Crop returns a JPEG of the box (padded by 8%) from a decoded scene image,
// limited to CropMaxEdge.
func Crop(img image.Image, b BBox) ([]byte, error) {
	r := img.Bounds()
	w, h := float64(r.Dx()), float64(r.Dy())
	x1 := (b.X - b.W*cropPad) * w
	y1 := (b.Y - b.H*cropPad) * h
	x2 := (b.X + b.W*(1+cropPad)) * w
	y2 := (b.Y + b.H*(1+cropPad)) * h
	rect := image.Rect(int(x1), int(y1), int(x2+0.5), int(y2+0.5)).Add(r.Min).Intersect(r)
	if rect.Dx() < 8 || rect.Dy() < 8 {
		return nil, fmt.Errorf("vision: crop too small")
	}
	sub := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	xdraw.Copy(sub, image.Point{}, img, rect, xdraw.Src, nil)
	return encode(fit(sub, CropMaxEdge))
}

func fit(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return flatten(img)
	}
	var nw, nh int
	if w >= h {
		nw, nh = maxEdge, h*maxEdge/w
	} else {
		nw, nh = w*maxEdge/h, maxEdge
	}
	if nh < 1 {
		nh = 1
	}
	if nw < 1 {
		nw = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// flatten paints transparent pixels (PNG/WebP) on white so JPEG output is sane.
func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
	xdraw.Draw(dst, dst.Bounds(), img, b.Min, xdraw.Over)
	return dst
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
