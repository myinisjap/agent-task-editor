package handlers

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // register GIF decoding for image.Decode/DecodeConfig
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only WebP support
)

// maxImageWidth and maxImageHeight bound the pixel dimensions of a stored
// task attachment image. Claude Code's Read tool rejects images larger than
// 2000x2000px outright ("Unable to resize image — dimensions exceed the
// 2000x2000px limit and image processing failed."), so we downscale on
// upload instead of letting the agent hit that error mid-run.
const (
	maxImageWidth  = 2000
	maxImageHeight = 2000
)

// jpegQuality is used when re-encoding a downscaled JPEG.
const jpegQuality = 90

// maxDecodePixels caps the pixel count (width * height, as reported by the
// cheap image.DecodeConfig header parse) we're willing to hand to
// image.Decode/draw.CatmullRom.Scale, which both allocate roughly one RGBA
// pixel (4 bytes) per source/destination pixel. Without this guard, a small
// compressed file with an enormous declared resolution (e.g. a ~700KB
// 15000x15000 solid-color PNG, comfortably under the existing 10MB per-file
// upload cap) can force a ~900MB in-memory allocation per file — and
// multiple such files can be attached to a single request. 4096x4096
// (~16.8M pixels, ~67MB decoded as RGBA) comfortably covers any real photo
// or screenshot while keeping worst-case per-file decode memory bounded.
const maxDecodePixels = 4096 * 4096

// resizedImage is the result of shrinkImageToBounds.
type resizedImage struct {
	// Data holds the encoded, downscaled image bytes. Only meaningful when
	// Resized is true.
	Data []byte
	// Ext is the file extension (including the leading dot, e.g. ".png")
	// matching the encoding of Data. Only meaningful when Resized is true.
	Ext string
	// Resized is false when the caller should store the original bytes
	// unchanged: the image already fit within the bounds, its format
	// couldn't be decoded, or re-encoding failed.
	Resized bool
}

// shrinkImageToBounds decodes src and, if either pixel dimension exceeds
// maxImageWidth/maxImageHeight, rescales it (preserving aspect ratio) and
// re-encodes it. It returns Resized=false — never an error the caller must
// act on — when the image already fits, when the format can't be decoded, or
// if re-encoding fails; callers should then store the original bytes
// unchanged. The returned error is informational only (for logging).
func shrinkImageToBounds(src []byte) (resizedImage, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return resizedImage{}, fmt.Errorf("decode image config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return resizedImage{}, fmt.Errorf("invalid image dimensions %dx%d", cfg.Width, cfg.Height)
	}

	w2, h2 := fitDimensions(cfg.Width, cfg.Height, maxImageWidth, maxImageHeight)
	if w2 == cfg.Width && h2 == cfg.Height {
		// Already within bounds; store the original bytes untouched.
		return resizedImage{Resized: false}, nil
	}

	// Guard against decoding an image whose declared pixel count would
	// force an oversized in-memory allocation (image.Decode and
	// draw.CatmullRom.Scale both allocate ~4 bytes/pixel for source and
	// destination buffers) — a small compressed file can still declare a
	// huge resolution. Bail out to the "store original bytes" fallback
	// rather than attempting the decode at all.
	if pixels := int64(cfg.Width) * int64(cfg.Height); pixels > maxDecodePixels {
		return resizedImage{}, fmt.Errorf("image dimensions %dx%d (%d px) exceed the %d px decode limit", cfg.Width, cfg.Height, pixels, maxDecodePixels)
	}

	srcImg, decodedFormat, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return resizedImage{}, fmt.Errorf("decode image: %w", err)
	}

	dst := image.NewRGBA(image.Rect(0, 0, w2, h2))
	draw.CatmullRom.Scale(dst, dst.Bounds(), srcImg, srcImg.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	var ext string
	switch decodedFormat {
	case "jpeg":
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return resizedImage{}, fmt.Errorf("encode jpeg: %w", err)
		}
		ext = ".jpg"
	case "png", "gif", "webp":
		// GIF's stdlib encoder only writes single-frame paletted images and
		// there is no WebP encoder in golang.org/x/image, so both fall back
		// to PNG when resized.
		if err := png.Encode(&buf, dst); err != nil {
			return resizedImage{}, fmt.Errorf("encode png: %w", err)
		}
		ext = ".png"
	default:
		return resizedImage{}, fmt.Errorf("unsupported image format %q (decoded as %q)", format, decodedFormat)
	}

	return resizedImage{Data: buf.Bytes(), Ext: ext, Resized: true}, nil
}

// fitDimensions returns the largest w2,h2 <= maxW,maxH that preserves the
// w:h aspect ratio, without ever upscaling. Each dimension is clamped to at
// least 1px so an extreme aspect ratio never yields a 0-pixel dimension.
func fitDimensions(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 || maxW <= 0 || maxH <= 0 {
		return w, h
	}
	s := math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	if s >= 1 {
		return w, h
	}
	w2 := int(math.Round(float64(w) * s))
	h2 := int(math.Round(float64(h) * s))
	if w2 < 1 {
		w2 = 1
	}
	if h2 < 1 {
		h2 = 1
	}
	return w2, h2
}
