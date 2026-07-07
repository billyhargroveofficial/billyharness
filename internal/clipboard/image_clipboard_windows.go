//go:build windows

package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math/bits"
	"reflect"
	"runtime"
	"strings"
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
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

const (
	CF_DIB   = 8
	CF_DIBV5 = 17

	biRGB            = 0
	biBitfields      = 3
	biAlphaBitfields = 6
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
	headerSize := dibHeaderSize(dib)
	if headerSize < 40 || headerSize > len(dib) {
		return nil, fmt.Errorf("invalid DIB header size: %d", headerSize)
	}
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	heightRaw := int32(binary.LittleEndian.Uint32(dib[8:12]))
	bitCount := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])

	if width <= 0 || heightRaw == 0 {
		return nil, fmt.Errorf("invalid DIB dimensions: %dx%d", width, heightRaw)
	}

	topDown := heightRaw < 0
	height := int(heightRaw)
	if height < 0 {
		height = -height
	}

	if compression != biRGB && compression != biBitfields && compression != biAlphaBitfields {
		return nil, fmt.Errorf("unsupported DIB compression: %d (supported: BI_RGB, BI_BITFIELDS)", compression)
	}

	masks, maskBytes, hasMasks, err := dibColorMasks(dib, headerSize, bitCount, compression)
	if err != nil {
		return nil, err
	}

	biClrUsed := binary.LittleEndian.Uint32(dib[32:36])
	var colorTableSize int
	if biClrUsed > 0 {
		colorTableSize = int(biClrUsed) * 4
	} else if bitCount <= 8 {
		colorTableSize = (1 << uint(bitCount)) * 4
	}
	pixelOffset := headerSize + maskBytes + colorTableSize

	if pixelOffset >= len(dib) {
		return nil, fmt.Errorf("DIB pixel offset %d beyond data %d", pixelOffset, len(dib))
	}
	pixelData := dib[pixelOffset:]

	switch bitCount {
	case 32:
		if hasMasks {
			return decode32bppBitfieldDIB(pixelData, width, height, topDown, masks), nil
		}
		return decode32bppDIB(pixelData, width, height, topDown), nil
	case 24:
		if compression != biRGB {
			return nil, fmt.Errorf("unsupported 24-bit DIB compression: %d", compression)
		}
		return decode24bppDIB(pixelData, width, height, topDown), nil
	default:
		return nil, fmt.Errorf("unsupported DIB bit count: %d (only 24 and 32 supported)", bitCount)
	}
}

type dibMasks struct {
	red   uint32
	green uint32
	blue  uint32
	alpha uint32
}

func dibColorMasks(dib []byte, headerSize, bitCount int, compression uint32) (dibMasks, int, bool, error) {
	if compression == biRGB {
		return dibMasks{}, 0, false, nil
	}
	if bitCount != 32 {
		return dibMasks{}, 0, false, fmt.Errorf("unsupported DIB bitfields for %d-bit image", bitCount)
	}
	if headerSize >= 52 {
		masks := dibMasks{
			red:   binary.LittleEndian.Uint32(dib[40:44]),
			green: binary.LittleEndian.Uint32(dib[44:48]),
			blue:  binary.LittleEndian.Uint32(dib[48:52]),
		}
		if headerSize >= 56 {
			masks.alpha = binary.LittleEndian.Uint32(dib[52:56])
		}
		if masks.red == 0 || masks.green == 0 || masks.blue == 0 {
			return dibMasks{}, 0, false, nil
		}
		return masks, 0, true, nil
	}
	if headerSize == 40 {
		if len(dib) < headerSize+12 {
			return dibMasks{}, 0, false, fmt.Errorf("DIB bitfield masks are truncated")
		}
		masks := dibMasks{
			red:   binary.LittleEndian.Uint32(dib[40:44]),
			green: binary.LittleEndian.Uint32(dib[44:48]),
			blue:  binary.LittleEndian.Uint32(dib[48:52]),
		}
		maskBytes := 12
		if compression == biAlphaBitfields {
			if len(dib) < headerSize+16 {
				return dibMasks{}, 0, false, fmt.Errorf("DIB alpha bitfield mask is truncated")
			}
			masks.alpha = binary.LittleEndian.Uint32(dib[52:56])
			maskBytes = 16
		}
		if masks.red == 0 || masks.green == 0 || masks.blue == 0 {
			return dibMasks{}, 0, false, fmt.Errorf("DIB bitfield color masks are missing")
		}
		return masks, maskBytes, true, nil
	}
	return dibMasks{}, 0, false, fmt.Errorf("unsupported DIB header size %d for bitfields", headerSize)
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
	forceOpaque := dibAlphaAllZero(data, width, height, stride)
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
			if forceOpaque {
				a = 255
			}
			off := img.PixOffset(x, y)
			img.Pix[off+0] = r
			img.Pix[off+1] = g
			img.Pix[off+2] = b
			img.Pix[off+3] = a
		}
	}
	return img
}

func decode32bppBitfieldDIB(data []byte, width, height int, topDown bool, masks dibMasks) *image.RGBA {
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
			pixel := binary.LittleEndian.Uint32(data[pxOff : pxOff+4])
			a := uint8(255)
			if masks.alpha != 0 {
				a = scaleMaskedByte(pixel, masks.alpha)
			}
			off := img.PixOffset(x, y)
			img.Pix[off+0] = scaleMaskedByte(pixel, masks.red)
			img.Pix[off+1] = scaleMaskedByte(pixel, masks.green)
			img.Pix[off+2] = scaleMaskedByte(pixel, masks.blue)
			img.Pix[off+3] = a
		}
	}
	return img
}

func scaleMaskedByte(pixel, mask uint32) uint8 {
	if mask == 0 {
		return 0
	}
	shift := bits.TrailingZeros32(mask)
	width := bits.OnesCount32(mask)
	value := uint64((pixel & mask) >> uint(shift))
	maxValue := (uint64(1) << uint(width)) - 1
	return uint8((value*255 + maxValue/2) / maxValue)
}

func dibAlphaAllZero(data []byte, width, height, stride int) bool {
	seen := false
	for y := 0; y < height; y++ {
		srcOff := y * stride
		for x := 0; x < width; x++ {
			pxOff := srcOff + x*4
			if pxOff+3 >= len(data) {
				continue
			}
			seen = true
			if data[pxOff+3] != 0 {
				return false
			}
		}
	}
	return seen
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

// readDIBFromClipboard opens the clipboard and reads PNG, CF_DIBV5, or CF_DIB image data.
func readDIBFromClipboard() (image.Image, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, err := procOpenClipboard.Call(0)
	if ret == 0 {
		return nil, fmt.Errorf("open clipboard: clipboard may be in use by another app (%v)", err)
	}
	defer procCloseClipboard.Call()

	img, err := readImageFromOpenClipboard()
	if err != nil {
		return nil, err
	}
	return img, nil
}

func readImageFromOpenClipboard() (image.Image, error) {
	var failures []string

	if pngFormat := registeredClipboardFormat("PNG"); pngFormat != 0 && clipboardFormatAvailable(pngFormat) {
		data, err := clipboardFormatBytes(pngFormat)
		if err == nil {
			img, _, decodeErr := image.Decode(bytes.NewReader(data))
			if decodeErr == nil {
				return img, nil
			}
			err = decodeErr
		}
		failures = append(failures, fmt.Sprintf("PNG: %v", err))
	}

	for _, format := range []struct {
		id   uint32
		name string
	}{
		{CF_DIBV5, "CF_DIBV5"},
		{CF_DIB, "CF_DIB"},
	} {
		if !clipboardFormatAvailable(format.id) {
			continue
		}
		data, err := clipboardFormatBytes(format.id)
		if err == nil {
			img, decodeErr := dibToImage(data)
			if decodeErr == nil {
				return img, nil
			}
			err = decodeErr
		}
		failures = append(failures, fmt.Sprintf("%s: %v", format.name, err))
	}

	if len(failures) == 0 {
		return nil, ErrNoImage
	}
	return nil, fmt.Errorf("clipboard image formats failed: %s", strings.Join(failures, "; "))
}

func registeredClipboardFormat(name string) uint32 {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	format, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(ptr)))
	return uint32(format)
}

func clipboardFormatAvailable(format uint32) bool {
	ret, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return ret != 0
}

func clipboardFormatBytes(format uint32) ([]byte, error) {
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
	data := make([]byte, int(size))
	{
		// Use reflect to construct a slice backed by the locked memory.
		// This avoids the direct uintptr→unsafe.Pointer conversion that vet flags.
		// The memory is valid while we hold OpenClipboard + GlobalLock.
		var src []byte
		sh := (*reflect.SliceHeader)(unsafe.Pointer(&src))
		sh.Data = locked
		sh.Len = int(size)
		sh.Cap = int(size)
		copy(data, src)
	}

	return data, nil
}
