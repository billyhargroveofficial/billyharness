// Package clipboard provides cross-platform clipboard image paste support.
//
// Supported platforms:
//   - Windows: Win32 API via syscall (pure Go) — converts DIB → PNG
//   - macOS:   osascript + pngpaste fallback (no CGo)
//   - other:   unconditional stub (returns ErrNoImage)
package clipboard

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"strings"
)

// ErrNoImage is returned when the clipboard contains no image data.
var ErrNoImage = errors.New("no image in clipboard")

// ReadImage reads an image from the system clipboard.
// On success it returns the image encoded as PNG bytes and a suggested file name.
// On failure it returns ErrNoImage or a platform-specific error.
func ReadImage() ([]byte, string, error) {
	img, err := readImage()
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", fmt.Errorf("encode clipboard image to png: %w", err)
	}
	return buf.Bytes(), suggestedName(), nil
}

// readImage is the platform-specific dispatch.
var readImage func() (image.Image, error)

// RegisterPlatform hooks in a platform implementation (called from init in
// platform-specific files).
func RegisterPlatform(fn func() (image.Image, error)) {
	readImage = fn
}

func suggestedName() string {
	return fmt.Sprintf("clipboard-%s.png", strings.ReplaceAll(runtime.GOOS, "/", "-"))
}

// EncodePNG is exposed for tests.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
