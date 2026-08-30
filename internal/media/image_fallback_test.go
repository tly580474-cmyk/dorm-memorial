package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"io"
	"testing"
)

func TestStoredImageDisplayFailureFallsBackToOriginal(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, false)
	descriptor.DisplayPath = "/missing-display.jpg"
	descriptor.DisplayMIME = "image/jpeg"
	descriptor.DisplaySize = 10
	content, err := store.OpenDescriptor(context.Background(), descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	got, err := io.ReadAll(content.Body)
	if err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	want := append([]byte(nil), objects.objects[descriptor.ObjectPath]...)
	objects.mu.Unlock()
	if content.MimeType != "image/png" || !bytes.Equal(got, want) {
		t.Fatalf("fallback content=%+v", content)
	}
}

func TestLegacyImageWithLargeMetadataFallsBackToOriginal(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, false)
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	var original bytes.Buffer
	original.Write(jpegBytes.Bytes()[:2]) // SOI
	segment := make([]byte, 65537)
	segment[0], segment[1], segment[2], segment[3] = 0xff, 0xe2, 0xff, 0xff // APP2
	for original.Len() <= maxImageHeaderBytes {
		original.Write(segment)
	}
	original.Write(jpegBytes.Bytes()[2:])
	descriptor.MimeType = "image/jpeg"
	descriptor.Size = int64(original.Len())
	objects.mu.Lock()
	objects.objects[descriptor.ObjectPath] = original.Bytes()
	objects.mu.Unlock()
	content, err := store.OpenDescriptor(context.Background(), descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	got, err := io.ReadAll(content.Body)
	if err != nil || content.MimeType != "image/jpeg" || !bytes.Equal(got, original.Bytes()) {
		t.Fatalf("legacy fallback lost original: mime=%s err=%v", content.MimeType, err)
	}
}

func TestGIFDisplayRetainsAllAnimationFramesEvenWithLegacyVariant(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, false)
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.SetColorIndex(0, 0, 1)
	var original bytes.Buffer
	if err := gif.EncodeAll(&original, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	descriptor.MimeType = "image/gif"
	descriptor.Size = int64(original.Len())
	descriptor.DisplayPath = "/legacy-still.jpg"
	descriptor.DisplayMIME = "image/jpeg"
	objects.mu.Lock()
	objects.objects[descriptor.ObjectPath] = original.Bytes()
	objects.objects[descriptor.DisplayPath] = []byte("legacy still image")
	objects.mu.Unlock()
	content, err := store.OpenDescriptor(context.Background(), descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	got, err := io.ReadAll(content.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(got))
	if err != nil || len(decoded.Image) != 2 || content.MimeType != "image/gif" || !bytes.Equal(got, original.Bytes()) {
		t.Fatalf("animation lost: mime=%s decode=%v", content.MimeType, err)
	}
}
