package handlers

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestFitDimensions(t *testing.T) {
	cases := []struct {
		name       string
		w, h       int
		maxW, maxH int
		wantW      int
		wantH      int
	}{
		{"landscape oversized", 4000, 1000, 2000, 2000, 2000, 500},
		{"portrait oversized", 1000, 4000, 2000, 2000, 500, 2000},
		{"square oversized", 3000, 3000, 2000, 2000, 2000, 2000},
		{"already fitting", 100, 100, 2000, 2000, 100, 100},
		{"exactly at bound", 2000, 2000, 2000, 2000, 2000, 2000},
		{"extreme ratio", 10000, 2, 2000, 2000, 2000, 1},
		{"extreme ratio inverse", 2, 10000, 2000, 2000, 1, 2000},
		{"zero width guarded", 0, 100, 2000, 2000, 0, 100},
		{"negative height guarded", 100, -1, 2000, 2000, 100, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotW, gotH := fitDimensions(tc.w, tc.h, tc.maxW, tc.maxH)
			if gotW != tc.wantW || gotH != tc.wantH {
				t.Errorf("fitDimensions(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
					tc.w, tc.h, tc.maxW, tc.maxH, gotW, gotH, tc.wantW, tc.wantH)
			}
			if gotW > 0 && gotH > 0 {
				if gotW > tc.maxW || gotH > tc.maxH {
					t.Errorf("fitDimensions(%d,%d,%d,%d) = (%d,%d) exceeds bounds", tc.w, tc.h, tc.maxW, tc.maxH, gotW, gotH)
				}
			}
		})
	}
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.White, color.Black})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("gif.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestShrinkImageToBounds_SmallImagePassthrough(t *testing.T) {
	src := encodePNG(t, 50, 50)
	res, err := shrinkImageToBounds(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Resized {
		t.Fatalf("expected Resized=false for a small image")
	}
}

func TestShrinkImageToBounds_DownscalesPreservingRatio(t *testing.T) {
	src := encodePNG(t, 3000, 1500)
	res, err := shrinkImageToBounds(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Resized {
		t.Fatalf("expected Resized=true for an oversized image")
	}
	img, _, err := image.Decode(bytes.NewReader(res.Data))
	if err != nil {
		t.Fatalf("failed to decode resized data: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 2000 || b.Dy() != 1000 {
		t.Errorf("expected 2000x1000, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestShrinkImageToBounds_JPEGStaysJPEG(t *testing.T) {
	src := encodeJPEG(t, 3000, 3000)
	res, err := shrinkImageToBounds(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Resized {
		t.Fatalf("expected Resized=true")
	}
	if res.Ext != ".jpg" {
		t.Errorf("expected .jpg extension, got %q", res.Ext)
	}
	if _, format, err := image.Decode(bytes.NewReader(res.Data)); err != nil || format != "jpeg" {
		t.Errorf("expected decodable jpeg output, format=%q err=%v", format, err)
	}
}

func TestShrinkImageToBounds_GIFBecomesPNG(t *testing.T) {
	src := encodeGIF(t, 3000, 3000)
	res, err := shrinkImageToBounds(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Resized {
		t.Fatalf("expected Resized=true")
	}
	if res.Ext != ".png" {
		t.Errorf("expected .png extension, got %q", res.Ext)
	}
	if _, format, err := image.Decode(bytes.NewReader(res.Data)); err != nil || format != "png" {
		t.Errorf("expected decodable png output, format=%q err=%v", format, err)
	}
}

func TestShrinkImageToBounds_GarbageBytesReturnsNotResized(t *testing.T) {
	res, err := shrinkImageToBounds([]byte("this is definitely not an image"))
	if err == nil {
		t.Fatalf("expected an error for undecodable input")
	}
	if res.Resized {
		t.Fatalf("expected Resized=false for undecodable input")
	}
}
