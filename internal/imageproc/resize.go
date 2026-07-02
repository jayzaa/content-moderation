// Package imageproc provides helpers to keep uploaded images within the
// limits required by the Alibaba Cloud Content Moderation (Green/CIP)
// ImageModeration API:
//
//   - file size must not exceed 20 MB
//   - width/height must not exceed 30,000 px
//   - total pixel count must not exceed 250,000,000
//
// See: https://help.aliyun.com/en/document_detail/467829.html
package imageproc

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
)

// Moderation API limits.
const (
	MaxFileSizeBytes = 20 * 1024 * 1024 // 20 MB
	MaxDimensionPx   = 30000
	MaxTotalPixels   = 250_000_000
)

// NeedsResize reports whether an image with the given properties would
// violate the moderation API's limits.
func NeedsResize(fileSizeBytes int64, width, height int) bool {
	if fileSizeBytes > MaxFileSizeBytes {
		return true
	}
	if width > MaxDimensionPx || height > MaxDimensionPx {
		return true
	}
	if int64(width)*int64(height) > MaxTotalPixels {
		return true
	}
	return false
}

// EnsureWithinLimits decodes the image data and, if it exceeds any
// moderation limit (file size, max dimension, or total pixel count),
// resizes it down (preserving aspect ratio) and re-encodes it.
//
// It returns the (possibly unchanged) image bytes, the content type used
// for encoding, and whether a resize was performed.
func EnsureWithinLimits(data []byte) (out []byte, contentType string, resized bool, err error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", false, fmt.Errorf("imageproc: decode: %w", err)
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	if !NeedsResize(int64(len(data)), width, height) {
		return data, mimeForFormat(format), false, nil
	}

	newWidth, newHeight := targetDimensions(width, height)

	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	encoded, err := encode(dst, format)
	if err != nil {
		return nil, "", false, fmt.Errorf("imageproc: encode: %w", err)
	}

	// If still too large by file size after resizing dimensions (e.g. very
	// detailed PNG), fall back to progressively smaller scales.
	for len(encoded) > MaxFileSizeBytes && (newWidth > 1 || newHeight > 1) {
		newWidth = maxInt(1, newWidth*3/4)
		newHeight = maxInt(1, newHeight*3/4)
		dst = image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		encoded, err = encode(dst, format)
		if err != nil {
			return nil, "", false, fmt.Errorf("imageproc: re-encode: %w", err)
		}
	}

	return encoded, mimeForFormat(format), true, nil
}

// targetDimensions scales width/height down so both the per-dimension and
// total-pixel-count limits are satisfied, preserving aspect ratio.
func targetDimensions(width, height int) (int, int) {
	scale := 1.0

	if width > MaxDimensionPx {
		s := float64(MaxDimensionPx) / float64(width)
		if s < scale {
			scale = s
		}
	}
	if height > MaxDimensionPx {
		s := float64(MaxDimensionPx) / float64(height)
		if s < scale {
			scale = s
		}
	}

	totalPixels := int64(width) * int64(height)
	if totalPixels > MaxTotalPixels {
		s := sqrt(float64(MaxTotalPixels) / float64(totalPixels))
		if s < scale {
			scale = s
		}
	}

	newWidth := maxInt(1, int(float64(width)*scale))
	newHeight := maxInt(1, int(float64(height)*scale))
	return newWidth, newHeight
}

func encode(img image.Image, format string) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default: // jpeg and anything else we can't preserve losslessly
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mimeForFormat(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method; adequate precision for scale-factor use.
	z := x
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

// DecodeConfigSize returns width/height without fully decoding pixel data.
// Useful for a fast pre-check before reading the whole file into memory.
func DecodeConfigSize(r io.Reader) (width, height int, format string, err error) {
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0, "", fmt.Errorf("imageproc: decode config: %w", err)
	}
	return cfg.Width, cfg.Height, format, nil
}
