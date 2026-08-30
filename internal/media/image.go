package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"time"

	"dorm-memorial/internal/storage"
)

const (
	maxPreviewPixels = 24_000_000
	previewLongEdge  = 960
	displayLongEdge  = 2048
)

type imageDetails struct {
	width       int
	height      int
	previewPath string
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
	// Validate dimensions before allocating pixels, retaining the header so that
	// reading it does not require a second remote download of the original.
	var prefix bytes.Buffer
	config, _, err := image.DecodeConfig(io.TeeReader(io.LimitReader(body, 8<<20), &prefix))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxPreviewPixels {
		return nil, fmt.Errorf("image dimensions exceed display limit")
	}
	source, _, err := image.Decode(io.MultiReader(bytes.NewReader(prefix.Bytes()), body))
	if err != nil {
		return nil, err
	}
	display := scaleToFit(source, displayLongEdge)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, display, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func buildImagePreview(ctx context.Context, objects storage.ObjectStorage, objectPath, ownerID, mediaID string, createdAt time.Time) imageDetails {
	body, err := objects.Open(ctx, objectPath)
	if err != nil {
		return imageDetails{}
	}
	config, _, err := image.DecodeConfig(io.LimitReader(body, 8<<20))
	body.Close()
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return imageDetails{}
	}
	details := imageDetails{width: config.Width, height: config.Height}
	if int64(config.Width)*int64(config.Height) > maxPreviewPixels {
		return details
	}

	body, err = objects.Open(ctx, objectPath)
	if err != nil {
		return details
	}
	source, _, err := image.Decode(body)
	body.Close()
	if err != nil {
		return details
	}
	preview := scaleToFit(source, previewLongEdge)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, preview, &jpeg.Options{Quality: 82}); err != nil {
		return details
	}
	previewPath := "/previews/" + remoteOwnerSegment(ownerID) + "/" + createdAt.UTC().Format("2006/01") + "/" + mediaID + ".jpg"
	if err := objects.Put(ctx, previewPath, bytes.NewReader(encoded.Bytes()), int64(encoded.Len())); err != nil {
		return details
	}
	details.previewPath = previewPath
	return details
}

func scaleToFit(source image.Image, maxEdge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := math.Min(1, float64(maxEdge)/float64(max(width, height)))
	targetWidth := max(1, int(math.Round(float64(width)*scale)))
	targetHeight := max(1, int(math.Round(float64(height)*scale)))
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := range targetHeight {
		sourceY := bounds.Min.Y + min(height-1, y*height/targetHeight)
		for x := range targetWidth {
			sourceX := bounds.Min.X + min(width-1, x*width/targetWidth)
			r, g, b, a := source.At(sourceX, sourceY).RGBA()
			// JPEG has no alpha channel; composite transparent pixels over white.
			target.SetRGBA(x, y, color.RGBA{
				R: uint8((r + 0xffff - a) >> 8),
				G: uint8((g + 0xffff - a) >> 8),
				B: uint8((b + 0xffff - a) >> 8),
				A: 0xff,
			})
		}
	}
	return target
}
