//go:build windows

package clipboard

import (
	"encoding/binary"
	"testing"
)

func TestDIBToImage_Bitfields32(t *testing.T) {
	dib := make([]byte, 56+4)
	binary.LittleEndian.PutUint32(dib[0:4], 56)
	binary.LittleEndian.PutUint32(dib[4:8], 1)
	binary.LittleEndian.PutUint32(dib[8:12], 0xffffffff)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[16:20], biBitfields)
	binary.LittleEndian.PutUint32(dib[40:44], 0x00ff0000)
	binary.LittleEndian.PutUint32(dib[44:48], 0x0000ff00)
	binary.LittleEndian.PutUint32(dib[48:52], 0x000000ff)
	binary.LittleEndian.PutUint32(dib[52:56], 0xff000000)
	binary.LittleEndian.PutUint32(dib[56:60], 0x80402010)

	img, err := dibToImage(dib)
	if err != nil {
		t.Fatalf("dibToImage bitfields: %v", err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if r>>8 != 0x40 || g>>8 != 0x20 || b>>8 != 0x10 || a>>8 != 0x80 {
		t.Fatalf("pixel = %#02x %#02x %#02x %#02x", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestDIBToImage_32bppAllZeroAlphaIsOpaque(t *testing.T) {
	dib := make([]byte, 40+4)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], 1)
	binary.LittleEndian.PutUint32(dib[8:12], 0xffffffff)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[16:20], biRGB)
	dib[40] = 0x10
	dib[41] = 0x20
	dib[42] = 0x40
	dib[43] = 0x00

	img, err := dibToImage(dib)
	if err != nil {
		t.Fatalf("dibToImage 32bpp BI_RGB: %v", err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if r>>8 != 0x40 || g>>8 != 0x20 || b>>8 != 0x10 || a>>8 != 0xff {
		t.Fatalf("pixel = %#02x %#02x %#02x %#02x", r>>8, g>>8, b>>8, a>>8)
	}
}
