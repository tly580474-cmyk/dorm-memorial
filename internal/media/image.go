package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"dorm-memorial/internal/storage"
)

const (
	// DecodeConfig is intentionally run before Decode. A 24 MP image can still
	// require about 100 MiB while decoding, but larger images are rejected
	// before a decoder is allowed to allocate their pixel buffer.
	MaxImagePixels      int64 = 24_000_000
	maxPreviewPixels          = MaxImagePixels
	maxImageHeaderBytes       = 8 << 20
	previewLongEdge           = 960
	displayLongEdge           = 2048
)

var (
	// Keeping MaxImagePixels before image.Decode prevents decompression bombs
	// from turning a small compressed file into an unbounded allocation.
	ErrImagePixelLimit = errors.New("image pixel limit exceeded")
	// ErrImageOriginalPreferred tells the delivery layer that a display
	// rendition is intentionally unavailable. This covers animated GIFs,
	// oversized images and legacy formats that this build cannot render.
	ErrImageOriginalPreferred = errors.New("image original preferred")
	imageDecodeSlots          = make(chan struct{}, 1)
)

type imageDetails struct {
	width       int
	height      int
	previewPath string
}

// SupportedImageMIMETypes returns the canonical image MIME types accepted by
// the upload pipeline. Return a fresh slice so callers cannot alter policy in
// another request.
func SupportedImageMIMETypes() []string {
	return []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
}

type detectedImage struct {
	format      string
	mimeType    string
	ext         string
	width       int
	height      int
	orientation int
}

func imageFormatInfo(format string) (mimeType, ext string, ok bool) {
	switch format {
	case "jpeg":
		return "image/jpeg", ".jpg", true
	case "png":
		return "image/png", ".png", true
	case "gif":
		return "image/gif", ".gif", true
	case "webp":
		return "image/webp", ".webp", true
	default:
		return "", "", false
	}
}

// prepareImageUpload copies the request body to a private local temporary
// file, verifies the actual image container and dimensions, then returns a
// rewindable body for the remote upload. The original bytes are never
// rewritten; only MIME and the filename suffix are normalized.
func prepareImageUpload(ctx context.Context, input UploadInput) (UploadInput, func(), error) {
	cleanup := func() {}
	if err := ctx.Err(); err != nil {
		return UploadInput{}, cleanup, err
	}
	if input.Body == nil || input.Size <= 0 || input.Size > MaxFileSize {
		return UploadInput{}, cleanup, ErrInvalid
	}
	declaredMime := strings.ToLower(strings.TrimSpace(strings.Split(input.MimeType, ";")[0]))
	if _, _, ok := imageFormatInfo(strings.TrimPrefix(declaredMime, "image/")); !ok {
		return UploadInput{}, cleanup, ErrInvalid
	}

	temp, err := os.CreateTemp("", "dorm-image-upload-*.part")
	if err != nil {
		return UploadInput{}, cleanup, fmt.Errorf("create image staging file: %w", errors.Join(ErrStorageUnavailable, err))
	}
	tempPath := temp.Name()
	cleanupTemp := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	written, copyErr := copyUploadBody(ctx, temp, input.Body, input.Size)
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil || written != input.Size {
		cleanupTemp()
		if copyErr != nil && !errors.Is(copyErr, context.Canceled) && !errors.Is(copyErr, context.DeadlineExceeded) {
			return UploadInput{}, func() {}, fmt.Errorf("stage image upload: %w", errors.Join(ErrInvalid, copyErr))
		}
		return UploadInput{}, func() {}, fmt.Errorf("stage image upload: %w", errors.Join(ErrInvalid, copyErr, closeErr))
	}

	file, err := os.Open(tempPath)
	if err != nil {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("open staged image: %w", errors.Join(ErrInvalid, err))
	}
	detected, inspectErr := inspectImage(ctx, file)
	_ = file.Close()
	if inspectErr != nil {
		cleanupTemp()
		return UploadInput{}, func() {}, inspectErr
	}
	if detected.mimeType != declaredMime {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("image MIME %q does not match detected %q: %w", declaredMime, detected.mimeType, ErrInvalid)
	}

	prepared, err := os.Open(tempPath)
	if err != nil {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("reopen staged image: %w", errors.Join(ErrInvalid, err))
	}
	preparedInput := input
	preparedInput.Body = prepared
	preparedInput.Size = written
	preparedInput.MimeType = detected.mimeType
	preparedInput.Filename = normalizeImageFilename(input.Filename, detected.ext)
	preparedInput.Width, preparedInput.Height = detected.width, detected.height
	return preparedInput, func() {
		_ = prepared.Close()
		_ = os.Remove(tempPath)
	}, nil
}

func copyUploadBody(ctx context.Context, dst io.Writer, src io.Reader, expected int64) (int64, error) {
	limited := io.LimitReader(src, expected+1)
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := limited.Read(buf)
		if n > 0 {
			written += int64(n)
			if _, err := dst.Write(buf[:n]); err != nil {
				return written, err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func normalizeImageFilename(filename, ext string) string {
	filename = cleanFilename(filename)
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "image"
	}
	return base + ext
}

func inspectImage(ctx context.Context, file io.ReadSeeker) (detectedImage, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return detectedImage{}, fmt.Errorf("inspect image: %w", errors.Join(ErrInvalid, err))
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return detectedImage{}, fmt.Errorf("decode image header: %w", errors.Join(ErrInvalid, err))
	}
	mimeType, ext, ok := imageFormatInfo(format)
	if !ok {
		return detectedImage{}, fmt.Errorf("unsupported image format %q: %w", format, ErrInvalid)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > MaxImagePixels/int64(config.Height) {
		return detectedImage{}, fmt.Errorf("image dimensions %dx%d exceed the %d pixel limit: %w", config.Width, config.Height, MaxImagePixels, errors.Join(ErrInvalid, ErrImagePixelLimit))
	}
	orientation := 1
	if format == "jpeg" {
		if _, err := file.Seek(0, io.SeekStart); err == nil {
			prefix, _ := io.ReadAll(io.LimitReader(file, maxImageHeaderBytes))
			orientation = jpegExifOrientation(prefix)
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return detectedImage{}, fmt.Errorf("rewind image: %w", errors.Join(ErrInvalid, err))
	}
	if err := acquireImageDecodeSlot(ctx); err != nil {
		return detectedImage{}, err
	}
	defer releaseImageDecodeSlot()
	if format == "gif" {
		// DecodeAll validates every frame and its LZW data. A GIF preview may
		// still use the first frame, while display delivery intentionally keeps
		// animation in the original file.
		if err := validateGIFStructure(file); err != nil {
			return detectedImage{}, fmt.Errorf("validate GIF: %w", errors.Join(ErrInvalid, err))
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return detectedImage{}, fmt.Errorf("rewind GIF: %w", errors.Join(ErrInvalid, err))
		}
		if _, err := gif.DecodeAll(file); err != nil {
			return detectedImage{}, fmt.Errorf("decode GIF: %w", errors.Join(ErrInvalid, err))
		}
	} else if _, _, err := image.Decode(file); err != nil {
		return detectedImage{}, fmt.Errorf("decode image: %w", errors.Join(ErrInvalid, err))
	}
	width, height := orientedDimensions(config.Width, config.Height, orientation)
	return detectedImage{format: format, mimeType: mimeType, ext: ext, width: width, height: height, orientation: orientation}, nil
}

func orientedDimensions(width, height, orientation int) (int, int) {
	if orientation >= 5 && orientation <= 8 {
		return height, width
	}
	return width, height
}

func imageDisplayPath(ownerID, mediaID, createdText string) string {
	createdAt, _ := time.Parse(time.RFC3339Nano, createdText)
	return "/display/" + remoteOwnerSegment(ownerID) + "/" + createdAt.UTC().Format("2006/01") + "/" + mediaID + ".jpg"
}

// Rendering must not wait for the derived file to be archived to remote storage.
func renderImageDisplay(ctx context.Context, objects storage.ObjectStorage, objectPath string) ([]byte, error) {
	body, err := objects.Open(ctx, objectPath)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	prefix := bytes.NewBuffer(nil)
	config, format, configErr := image.DecodeConfig(io.TeeReader(io.LimitReader(body, maxImageHeaderBytes), prefix))
	if configErr != nil {
		// Older originals can contain more metadata than the bounded header
		// reader permits. Rendering failure must not prevent original delivery.
		return nil, errors.Join(ErrImageOriginalPreferred, configErr)
	}
	if _, _, ok := imageFormatInfo(format); !ok {
		return nil, errors.Join(ErrImageOriginalPreferred, fmt.Errorf("unsupported image format %q", format))
	}
	if format == "gif" {
		return nil, errors.Join(ErrImageOriginalPreferred, fmt.Errorf("animated GIF display uses original"))
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > MaxImagePixels/int64(config.Height) {
		return nil, errors.Join(ErrImageOriginalPreferred, ErrImagePixelLimit, fmt.Errorf("image dimensions exceed display limit"))
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegExifOrientation(prefix.Bytes())
	}
	if err := acquireImageDecodeSlot(ctx); err != nil {
		return nil, err
	}
	defer releaseImageDecodeSlot()
	source, _, err := image.Decode(io.MultiReader(bytes.NewReader(prefix.Bytes()), body))
	if err != nil {
		return nil, err
	}
	source = applyExifOrientation(source, orientation)
	display := scaleToFit(source, displayLongEdge)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, display, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func sniffImageFormat(prefix []byte) string {
	switch {
	case len(prefix) >= 3 && prefix[0] == 0xff && prefix[1] == 0xd8 && prefix[2] == 0xff:
		return "jpeg"
	case len(prefix) >= 8 && bytes.Equal(prefix[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "png"
	case len(prefix) >= 6 && (bytes.Equal(prefix[:6], []byte("GIF87a")) || bytes.Equal(prefix[:6], []byte("GIF89a"))):
		return "gif"
	case len(prefix) >= 12 && bytes.Equal(prefix[:4], []byte("RIFF")) && bytes.Equal(prefix[8:12], []byte("WEBP")):
		return "webp"
	default:
		return ""
	}
}

func acquireImageDecodeSlot(ctx context.Context) error {
	select {
	case imageDecodeSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseImageDecodeSlot() {
	<-imageDecodeSlots
}

// validateGIFStructure checks frame dimensions and compressed data boundaries
// before gif.DecodeAll allocates a pixel buffer for every frame. The standard
// decoder then performs the actual LZW and palette validation.
func validateGIFStructure(file io.ReadSeeker) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	header := make([]byte, 13)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if sniffImageFormat(header) != "gif" {
		return fmt.Errorf("invalid GIF header")
	}
	canvasWidth := int(binary.LittleEndian.Uint16(header[6:8]))
	canvasHeight := int(binary.LittleEndian.Uint16(header[8:10]))
	if canvasWidth <= 0 || canvasHeight <= 0 || int64(canvasWidth) > MaxImagePixels/int64(canvasHeight) {
		return ErrImagePixelLimit
	}
	packed := header[10]
	if packed&0x80 != 0 {
		if err := skipGIFBytes(reader, int64(3*(1<<((packed&0x07)+1)))); err != nil {
			return err
		}
	}
	var totalPixels int64
	frames := 0
	for {
		marker, err := reader.ReadByte()
		if err != nil {
			return err
		}
		switch marker {
		case 0x3b: // trailer
			if frames == 0 {
				return fmt.Errorf("GIF has no frames")
			}
			return nil
		case 0x21: // extension block
			if _, err := reader.ReadByte(); err != nil {
				return err
			}
			if err := skipGIFSubBlocks(reader); err != nil {
				return err
			}
		case 0x2c: // image descriptor
			descriptor := make([]byte, 9)
			if _, err := io.ReadFull(reader, descriptor); err != nil {
				return err
			}
			left := int(binary.LittleEndian.Uint16(descriptor[0:2]))
			top := int(binary.LittleEndian.Uint16(descriptor[2:4]))
			width := int(binary.LittleEndian.Uint16(descriptor[4:6]))
			height := int(binary.LittleEndian.Uint16(descriptor[6:8]))
			if width <= 0 || height <= 0 || left+width > canvasWidth || top+height > canvasHeight {
				return fmt.Errorf("GIF frame bounds are invalid")
			}
			pixels := int64(width) * int64(height)
			if pixels > MaxImagePixels-totalPixels {
				return ErrImagePixelLimit
			}
			totalPixels += pixels
			frames++
			if frames > 256 {
				return fmt.Errorf("GIF has too many frames")
			}
			if descriptor[8]&0x80 != 0 {
				if err := skipGIFBytes(reader, int64(3*(1<<((descriptor[8]&0x07)+1)))); err != nil {
					return err
				}
			}
			if _, err := reader.ReadByte(); err != nil { // LZW minimum code size
				return err
			}
			if err := skipGIFSubBlocks(reader); err != nil {
				return err
			}
		default:
			return fmt.Errorf("GIF has invalid block marker %#x", marker)
		}
	}
}

func skipGIFBytes(reader *bufio.Reader, count int64) error {
	_, err := io.CopyN(io.Discard, reader, count)
	return err
}

func skipGIFSubBlocks(reader *bufio.Reader) error {
	for {
		size, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if size == 0 {
			return nil
		}
		if err := skipGIFBytes(reader, int64(size)); err != nil {
			return err
		}
	}
}

func buildImagePreview(ctx context.Context, objects storage.ObjectStorage, objectPath, ownerID, mediaID string, createdAt time.Time) imageDetails {
	body, err := objects.Open(ctx, objectPath)
	if err != nil {
		return imageDetails{}
	}
	prefix := bytes.NewBuffer(nil)
	config, format, err := image.DecodeConfig(io.TeeReader(io.LimitReader(body, maxImageHeaderBytes), prefix))
	body.Close()
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return imageDetails{}
	}
	if _, _, ok := imageFormatInfo(format); !ok {
		return imageDetails{}
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegExifOrientation(prefix.Bytes())
	}
	details := imageDetails{}
	details.width, details.height = orientedDimensions(config.Width, config.Height, orientation)
	if int64(config.Width) > MaxImagePixels/int64(config.Height) {
		return details
	}

	encodedBytes, err := func() ([]byte, error) {
		body, err := objects.Open(ctx, objectPath)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		if err := acquireImageDecodeSlot(ctx); err != nil {
			return nil, err
		}
		defer releaseImageDecodeSlot()
		source, _, err := image.Decode(body)
		if err != nil {
			return nil, err
		}
		source = applyExifOrientation(source, orientation)
		preview := scaleToFit(source, previewLongEdge)
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, preview, &jpeg.Options{Quality: 82}); err != nil {
			return nil, err
		}
		return encoded.Bytes(), nil
	}()
	if err != nil {
		return details
	}
	previewPath := "/previews/" + remoteOwnerSegment(ownerID) + "/" + createdAt.UTC().Format("2006/01") + "/" + mediaID + ".jpg"
	if err := objects.Put(ctx, previewPath, bytes.NewReader(encodedBytes), int64(len(encodedBytes))); err != nil {
		return details
	}
	details.previewPath = previewPath
	return details
}

func scaleToFit(source image.Image, maxEdge int) image.Image {
	if source == nil || maxEdge <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	scale := math.Min(1, float64(maxEdge)/float64(max(width, height)))
	targetWidth := max(1, int(math.Round(float64(width)*scale)))
	targetHeight := max(1, int(math.Round(float64(height)*scale)))
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	// JPEG has no alpha channel. Start from white and use a high quality
	// resampler with Over so transparent PNG pixels remain white in JPEG.
	draw.Draw(target, target.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Over, nil)
	return target
}

func applyExifOrientation(source image.Image, orientation int) image.Image {
	if source == nil || orientation <= 1 || orientation > 8 {
		return source
	}
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	targetWidth, targetHeight := w, h
	if orientation >= 5 && orientation <= 8 {
		targetWidth, targetHeight = h, w
	}
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		for x := 0; x < targetWidth; x++ {
			sx, sy := x, y
			switch orientation {
			case 2:
				sx = w - 1 - x
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sy = h - 1 - y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			target.Set(x, y, source.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	return target
}
