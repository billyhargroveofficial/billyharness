//go:build darwin

package clipboard

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
)

func init() {
	RegisterPlatform(readImageMacOS)
}

// readImageMacOS reads an image from the macOS clipboard.
//
// Approach: use osascript to write the clipboard picture to a temp PNG file,
// then read it back. This avoids CGo entirely.
//
// Fallback: if osascript fails (e.g. no image in clipboard), try `pngpaste`
// if installed (common via Homebrew).
func readImageMacOS() (image.Image, error) {
	img, err := readViaOsascript()
	if err == nil {
		return img, nil
	}
	// If osascript fails and pngpaste is available, try that
	img, err2 := readViaPngpaste()
	if err2 == nil {
		return img, nil
	}
	return nil, fmt.Errorf("clipboard: %w (osascript: %v, pngpaste: %v)", ErrNoImage, err, err2)
}

// readViaOsascript uses osascript to export clipboard image to a temp file.
func readViaOsascript() (image.Image, error) {
	tmpFile, err := os.CreateTemp("", "billy-clipboard-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// AppleScript: write clipboard picture to temp PNG file
	script := fmt.Sprintf(
		`set imgFile to (POSIX file "%s")`+"\n"+
			`set imgData to the clipboard as picture`+"\n"+
			`try`+"\n"+
			`	set fileRef to open for access imgFile with write permission`+"\n"+
			`	write imgData to fileRef`+"\n"+
			`	close access fileRef`+"\n"+
			`	return "ok"`+"\n"+
			`on error errMsg`+"\n"+
			`	try`+"\n"+
			`		close access imgFile`+"\n"+
			`	end try`+"\n"+
			`	return "error: " & errMsg`+"\n"+
			`end try`,
		strings.ReplaceAll(tmpPath, `\`, `\\`))

	cmd := exec.Command("osascript", "-e", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("osascript failed: %w, stderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	result := strings.TrimSpace(string(out))
	if strings.HasPrefix(result, "error:") {
		msg := strings.TrimSpace(strings.TrimPrefix(result, "error:"))
		if strings.Contains(strings.ToLower(msg), "not allowed") ||
			strings.Contains(strings.ToLower(msg), "no image") ||
			strings.Contains(strings.ToLower(msg), "can't get") {
			return nil, ErrNoImage
		}
		return nil, fmt.Errorf("osascript: %s", msg)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read clipboard image: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrNoImage
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode clipboard image: %w", err)
	}
	return img, nil
}

// readViaPngpaste tries the `pngpaste` CLI utility as a fallback.
func readViaPngpaste() (image.Image, error) {
	if _, err := exec.LookPath("pngpaste"); err != nil {
		return nil, fmt.Errorf("pngpaste not installed: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "billy-clipboard-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("pngpaste", tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pngpaste failed: %w, stderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read pngpaste output: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrNoImage
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode pngpaste image: %w", err)
	}
	return img, nil
}

// Ensure image/png is registered at compile time.
var _ = png.Encoder{}
