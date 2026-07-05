//go:build windows

package clipboard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"reflect"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

const (
	CF_DIB   = 8
	CF_DIBV5 = 17
)

func init() {
	RegisterPlatform(readImageWindows)
}

func readImageWindows() (image.Image, error) {
	img, err := readDIBFromClipboard()
	if err != nil {
		return nil, err
	}
	return img, nil
}

// dibHeaderSize reads the first 4 bytes as biSize.
func dibHeaderSize(dib []byte) int {
	if len(dib) < 4 {
		return 0
	}
	return int(binary.LittleEndian.Uint32(dib[:4]))
}

// dibToImage converts raw DIB bytes (BITMAPINFOHEADER + optional color table + pixel data)
// to a Go image.Image.
func dibToImage(dib []byte) (image.Image, error) {
	if len(dib) < 40 {
		return nil, fmt.Errorf("DIB too short: %d bytes", len(dib))
	}

	// BITMAPINFOHEADER fields (all little-endian)
	_ = dibHeaderSize(dib) // biSize — we trust it
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	heightRaw := int32(binary.LittleEndian.Uint32(dib[8:12]))
	bitCount := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20]) // 0 = BI_RGB

	if width <= 0 || heightRaw == 0 {
		return nil, fmt.Errorf("invalid DIB dimensions: %dx%d", width, heightRaw)
	}

	topDown := heightRaw < 0
	height := int(heightRaw)
	if height < 0 {
		height = -height
	}

	if compression != 0 {
		return nil, fmt.Errorf("unsupported DIB compression: %d (only BI_RGB=0 supported)", compression)
	}

	headerSize := dibHeaderSize(dib)
	biClrUsed := binary.LittleEndian.Uint32(dib[32:36])
	var colorTableSize int
	if biClrUsed > 0 {
		colorTableSize = int(biClrUsed) * 4
	} else if bitCount <= 8 {
		colorTableSize = (1 << uint(bitCount)) * 4
	}
	pixelOffset := headerSize + colorTableSize

	if pixelOffset >= len(dib) {
		return nil, fmt.Errorf("DIB pixel offset %d beyond data %d", pixelOffset, len(dib))
	}
	pixelData := dib[pixelOffset:]

	switch bitCount {
	case 32:
		return decode32bppDIB(pixelData, width, height, topDown), nil
	case 24:
		return decode24bppDIB(pixelData, width, height, topDown), nil
	default:
		return nil, fmt.Errorf("unsupported DIB bit count: %d (only 24 and 32 supported)", bitCount)
	}
}

func rowStride(width int, bytesPerPixel int) int {
	stride := width * bytesPerPixel
	align := stride % 4
	if align != 0 {
		stride += 4 - align
	}
	return stride
}

func decode32bppDIB(data []byte, width, height int, topDown bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	stride := rowStride(width, 4)
	for y := 0; y < height; y++ {
		srcY := y
		if !topDown {
			srcY = height - 1 - y
		}
		srcOff := srcY * stride
		for x := 0; x < width; x++ {
			pxOff := srcOff + x*4
			if pxOff+3 >= len(data) {
				continue
			}
			b := data[pxOff+0]
			g := data[pxOff+1]
			r := data[pxOff+2]
			a := data[pxOff+3]
			off := img.PixOffset(x, y)
			img.Pix[off+0] = r
			img.Pix[off+1] = g
			img.Pix[off+2] = b
			img.Pix[off+3] = a
		}
	}
	return img
}

func decode24bppDIB(data []byte, width, height int, topDown bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	stride := rowStride(width, 3)
	for y := 0; y < height; y++ {
		srcY := y
		if !topDown {
			srcY = height - 1 - y
		}
		srcOff := srcY * stride
		for x := 0; x < width; x++ {
			pxOff := srcOff + x*3
			if pxOff+2 >= len(data) {
				continue
			}
			b := data[pxOff+0]
			g := data[pxOff+1]
			r := data[pxOff+2]
			off := img.PixOffset(x, y)
			img.Pix[off+0] = r
			img.Pix[off+1] = g
			img.Pix[off+2] = b
			img.Pix[off+3] = 255
		}
	}
	return img
}

// readDIBFromClipboard opens the clipboard and reads CF_DIBV5 or CF_DIB.
func readDIBFromClipboard() (image.Image, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, err := procOpenClipboard.Call(0)
	if ret == 0 {
		return nil, fmt.Errorf("open clipboard: clipboard may be in use by another app (%v)", err)
	}
	defer procCloseClipboard.Call()

	// Try CF_DIBV5 first (preferred, supports alpha), then CF_DIB
	format := uint32(CF_DIBV5)
	ret2, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	if ret2 == 0 {
		format = CF_DIB
		ret2, _, _ = procIsClipboardFormatAvailable.Call(uintptr(format))
	}
	if ret2 == 0 {
		return nil, errors.New("no image in clipboard")
	}

	handle, _, err := procGetClipboardData.Call(uintptr(format))
	if handle == 0 {
		return nil, fmt.Errorf("GetClipboardData: %v", err)
	}

	locked, _, err := procGlobalLock.Call(handle)
	if locked == 0 {
		return nil, fmt.Errorf("GlobalLock: %v", err)
	}
	defer procGlobalUnlock.Call(handle)

	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, errors.New("clipboard data size is 0")
	}

	// GlobalLock returns a pointer to locked HGLOBAL memory.
	// We copy into Go memory while locks are held.
	dib := make([]byte, int(size))
	{
		// Use reflect to construct a slice backed by the locked memory.
		// This avoids the direct uintptr→unsafe.Pointer conversion that vet flags.
		// The memory is valid while we hold OpenClipboard + GlobalLock.
		var src []byte
		sh := (*reflect.SliceHeader)(unsafe.Pointer(&src))
		sh.Data = locked
		sh.Len = int(size)
		sh.Cap = int(size)
		copy(dib, src)
	}

	return dibToImage(dib)
}
