package handlers

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
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

// buildHugeDimensionPNG constructs a syntactically valid but minimal PNG
// (empty IDAT stream) whose IHDR declares an enormous width/height, without
// actually allocating or encoding that many real pixels. This lets tests
// exercise the maxDecodePixels guard cheaply: image.DecodeConfig reads the
// declared dimensions from IHDR alone (a few dozen bytes), so this file is
// tiny on disk yet claims to be huge — the same "small file, giant claimed
// resolution" shape a memory-exhaustion attack would use.
func buildHugeDimensionPNG(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})

	writeChunk := func(typ string, data []byte) {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
		buf.Write(lenBuf[:])
		buf.WriteString(typ)
		buf.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(typ))
		crc.Write(data)
		var crcBuf [4]byte
		binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
		buf.Write(crcBuf[:])
	}

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8  // bit depth
	ihdr[9] = 6  // color type: truecolor with alpha
	ihdr[10] = 0 // compression method
	ihdr[11] = 0 // filter method
	ihdr[12] = 0 // interlace method
	writeChunk("IHDR", ihdr)

	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib.Close: %v", err)
	}
	writeChunk("IDAT", zbuf.Bytes())
	writeChunk("IEND", nil)
	return buf.Bytes()
}

func TestShrinkImageToBounds_HugeDeclaredDimensionsRejectedBeforeDecode(t *testing.T) {
	// 15000x15000 declared in an ~80-byte file: well under the 10MB
	// per-file upload cap, but would force a ~900MB RGBA allocation if
	// handed to image.Decode/draw.CatmullRom.Scale unguarded.
	src := buildHugeDimensionPNG(t, 15000, 15000)
	if len(src) > 1<<10 {
		t.Fatalf("test fixture should be tiny on disk, got %d bytes", len(src))
	}

	res, err := shrinkImageToBounds(src)
	if err == nil {
		t.Fatalf("expected an error for an image exceeding maxDecodePixels")
	}
	if res.Resized {
		t.Fatalf("expected Resized=false so the caller falls back to storing the original bytes")
	}
}

func TestShrinkImageToBounds_JustUnderPixelCapStillResizes(t *testing.T) {
	// 4000x4000 = 16,000,000 px, under maxDecodePixels (4096*4096 =
	// 16,777,216) and over the 2000x2000 bound, so it should still be
	// decoded and downscaled rather than rejected by the pixel-count guard.
	src := encodePNG(t, 4000, 4000)
	res, err := shrinkImageToBounds(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Resized {
		t.Fatalf("expected Resized=true for a 4000x4000 image just under the pixel cap")
	}
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
