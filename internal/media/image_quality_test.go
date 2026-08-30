package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"os"
	"testing"
)

func TestPrepareImageUploadChecksBytesAndNormalizesPNG(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	payload := encoded.Bytes()
	prepared, cleanup, err := prepareImageUpload(context.Background(), UploadInput{
		Filename: "memory.bin", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	file, ok := prepared.Body.(*os.File)
	if !ok {
		t.Fatalf("prepared body type=%T, want *os.File", prepared.Body)
	}
	if prepared.MimeType != "image/png" || prepared.Filename != "memory.png" || prepared.Size != int64(len(payload)) {
		t.Fatalf("prepared metadata=%+v", prepared)
	}
	got, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("prepared bytes equal=%v err=%v", bytes.Equal(got, payload), err)
	}
	path := file.Name()
	cleanup()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged image remains: %s err=%v", path, err)
	}
}

func TestPrepareImageUploadRejectsForgedTruncatedAndMismatchedImages(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	payload := encoded.Bytes()
	for _, tc := range []struct {
		name    string
		mime    string
		payload []byte
	}{
		{name: "forged", mime: "image/png", payload: []byte("not an image")},
		{name: "truncated", mime: "image/png", payload: payload[:len(payload)/2]},
		{name: "mismatch", mime: "image/jpeg", payload: payload},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cleanup, err := prepareImageUpload(context.Background(), UploadInput{Filename: "image.bin", MimeType: tc.mime, Size: int64(len(tc.payload)), Body: bytes.NewReader(tc.payload)})
			cleanup()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err=%v, want ErrInvalid", err)
			}
		})
	}
}

func TestPrepareImageUploadRejectsImagePixelBombBeforeDecode(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// PNG's IHDR width and height are enough for DecodeConfig to identify the
	// allocation size; the CRC is intentionally left stale because the pixel
	// limit must be checked before full image decoding.
	payload := append([]byte(nil), encoded.Bytes()...)
	binary.BigEndian.PutUint32(payload[16:20], uint32(MaxImagePixels/2))
	binary.BigEndian.PutUint32(payload[20:24], 3)
	binary.BigEndian.PutUint32(payload[29:33], crc32.ChecksumIEEE(payload[12:29]))
	_, cleanup, err := prepareImageUpload(context.Background(), UploadInput{Filename: "bomb.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)})
	cleanup()
	if !errors.Is(err, ErrImagePixelLimit) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v, want pixel and invalid errors", err)
	}
}

func TestJPEGEXIFOrientationParserAndImageDimensions(t *testing.T) {
	for orientation := 1; orientation <= 8; orientation++ {
		t.Run(string(rune('0'+orientation)), func(t *testing.T) {
			if got := jpegExifOrientation(jpegWithOrientation(orientation)); got != orientation {
				t.Fatalf("orientation=%d got=%d", orientation, got)
			}
			width, height := orientedDimensions(7, 3, orientation)
			wantWidth, wantHeight := 7, 3
			if orientation >= 5 && orientation <= 8 {
				wantWidth, wantHeight = 3, 7
			}
			if width != wantWidth || height != wantHeight {
				t.Fatalf("dimensions=%dx%d want=%dx%d", width, height, wantWidth, wantHeight)
			}
		})
	}
}

func TestApplyEXIFOrientationAndHighQualityScale(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 3))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	oriented := applyExifOrientation(source, 6)
	if got := oriented.Bounds(); got.Dx() != 3 || got.Dy() != 2 {
		t.Fatalf("orientation 6 bounds=%v", got)
	}
	if got := oriented.At(2, 0); got != source.At(0, 0) {
		t.Fatalf("orientation 6 moved pixel to %v, want %v", got, source.At(0, 0))
	}
	transparent := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	result := scaleToFit(transparent, 2)
	if got := result.Bounds(); got.Dx() != 2 || got.Dy() != 2 {
		t.Fatalf("scaled bounds=%v", got)
	}
	r, g, b, a := result.At(0, 0).RGBA()
	if r < 0xff00 || g < 0xff00 || b < 0xff00 || a != 0xffff {
		t.Fatalf("transparent PNG was not composited over white: rgba=%04x,%04x,%04x,%04x", r, g, b, a)
	}
}

func TestPrepareImageUploadAcceptsWebP(t *testing.T) {
	// Small lossless WebP fixture from x/image's test corpus.
	payload, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := prepareImageUpload(context.Background(), UploadInput{Filename: "gopher.png", MimeType: "image/webp", Size: int64(len(payload)), Body: bytes.NewReader(payload)})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if prepared.MimeType != "image/webp" || prepared.Filename != "gopher.webp" {
		t.Fatalf("prepared metadata=%+v", prepared)
	}
	if _, _, err := image.Decode(prepared.Body); err != nil {
		t.Fatalf("decode prepared WebP: %v", err)
	}
}

func TestValidateGIFStructureRejectsPixelBudget(t *testing.T) {
	var encoded bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	if err := gif.EncodeAll(&encoded, &gif.GIF{Image: []*image.Paletted{frame}, Delay: []int{0}}); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(nil), encoded.Bytes()...)
	// Inflate the logical screen and frame dimensions without adding pixel
	// data. validateGIFStructure must reject this before DecodeAll allocates.
	for _, offset := range []int{6, 8, 24, 26} {
		binary.LittleEndian.PutUint16(payload[offset:offset+2], 5000)
	}
	file, err := os.CreateTemp(t.TempDir(), "gif-budget-*.gif")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(validateGIFStructure(file), ErrImagePixelLimit) {
		t.Fatal("oversized GIF frame was accepted")
	}
	file.Close()
}

func jpegWithOrientation(orientation int) []byte {
	tiff := make([]byte, 26)
	tiff[0], tiff[1] = 'I', 'I'
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], uint16(orientation))
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segmentLength := len(payload) + 2
	return append([]byte{0xff, 0xd8, 0xff, 0xe1, byte(segmentLength >> 8), byte(segmentLength)}, payload...)
}
