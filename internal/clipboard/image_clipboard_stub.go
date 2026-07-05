//go:build !windows && !darwin

package clipboard

import (
	"fmt"
	"image"
	"runtime"
)

func init() {
	// Register a stub that always returns no-image.
	// Linux clipboard image support (xclip/wl-paste) is complex and rare;
	// we stub it out rather than pull in heavy X11/Wayland deps.
	RegisterPlatform(func() (image.Image, error) {
		return nil, fmt.Errorf("clipboard image paste not available on %s/%s", runtime.GOOS, runtime.GOARCH)
	})
}
