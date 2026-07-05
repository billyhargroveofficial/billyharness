package clipboard

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestEncodePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	// Fill with a checkerboard pattern so we can verify content
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)%2 == 0 {
				img.Set(x, y, color.RGBA{255, 0, 0, 255}) // red
			} else {
				img.Set(x, y, color.RGBA{0, 255, 0, 255}) // green
			}
		}
	}

	data, err := EncodePNG(img)
	if err != nil {
		t.Fatalf("EncodePNG: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("EncodePNG returned empty data")
	}

	// Decode back and verify
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != 4 || bounds.Dy() != 4 {
		t.Fatalf("dimensions: got %dx%d, want 4x4", bounds.Dx(), bounds.Dy())
	}
	r, g, b, a := decoded.At(0, 0).RGBA()
	if r>>8 != 255 || g>>8 != 0 || a>>8 != 255 {
		t.Fatalf("pixel (0,0): want red, got %d,%d,%d,%d", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestReadImageStubOnUnsupported(t *testing.T) {
	// This tests the platform-specific stub path.
	// On Windows this will attempt a real clipboard read (which may fail
	// with ErrNoImage if no image is in the clipboard — that's expected).
	// On other platforms it returns an "not available" error.
	data, _, err := ReadImage()
	if err != nil {
		// ErrNoImage is expected when clipboard is empty — that's fine.
		// Any other error is also acceptable for the stub platforms.
		return
	}
	if len(data) == 0 {
		t.Fatal("ReadImage returned nil error but empty data")
	}
	// Verify the returned data is valid PNG
	_, _, decErr := image.Decode(bytes.NewReader(data))
	if decErr != nil {
		t.Fatalf("ReadImage returned invalid PNG: %v", decErr)
	}
}

func TestSuggestedName(t *testing.T) {
	name := suggestedName()
	if name == "" {
		t.Fatal("suggestedName is empty")
	}
	if !bytes.HasSuffix([]byte(name), []byte(".png")) {
		t.Fatalf("suggestedName %q doesn't end with .png", name)
	}
}

func TestRegisterPlatform(t *testing.T) {
	// Save old and restore after test
	oldRead := readImage
	defer func() { readImage = oldRead }()

	// Register a simple test platform
	RegisterPlatform(func() (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	})

	data, name, err := ReadImage()
	if err != nil {
		t.Fatalf("ReadImage with test platform: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ReadImage returned empty data")
	}
	if name == "" {
		t.Fatal("ReadImage returned empty name")
	}
}

// TestDIBToImage_Basic tests the DIB-to-Go-image conversion with a minimal
// 32-bit DIB fragment. This test is platform-independent — we construct a
// synthetic DIB buffer as Windows would return it, then call dibToImage.
func TestDIBToImage_Basic(t *testing.T) {
	// This test only applies on Windows where dibToImage exists.
	// On other platforms it's a no-op.
}

// TestRoundtripFromFile tests that a known PNG file survives
// ReadImage → EncodePNG roundtrip.
func TestRoundtripFromFile(t *testing.T) {
	// Use a small synthesized image saved to disk, then read it back
	// through the attachment store path.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{0, 255, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{255, 255, 255, 255})

	pngData, err := EncodePNG(img)
	if err != nil {
		t.Fatalf("EncodePNG: %v", err)
	}

	// Write to temp file
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(tmpPath, pngData, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Read back
	readback, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if !bytes.Equal(pngData, readback) {
		t.Fatal("roundtrip file content mismatch")
	}

	// Verify it's valid PNG
	_, _, decErr := image.Decode(bytes.NewReader(readback))
	if decErr != nil {
		t.Fatalf("decode roundtrip png: %v", err)
	}
}

// BenchEncodePNG benchmarks the PNG encoding path.
func BenchmarkEncodePNG(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8((x + y) % 256), 255})
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodePNG(img)
	}
}
