// Command winicon writes the Windows icon resource used by the Client
// executable. The artwork stays in the SVG brand asset; this command rasterises
// it into the sizes Explorer and the taskbar ask for, and wraps it in an .ico
// container so the build does not need a checked-in binary asset.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/brand"
)

// iconSizes are the pixel sizes Windows picks from: the 16–24 px variants are
// drawn in the taskbar, the small window title, and Explorer's list views; the
// larger ones are used by the desktop and the installer.
var iconSizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

func main() {
	out := flag.String("out", "app.ico", "path of the .ico file to write")
	flag.Parse()

	data, err := buildIcon(iconSizes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "winicon: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "winicon: %v\n", err)
		os.Exit(1)
	}
}

// buildIcon encodes every requested size as one .ico file. Sizes up to 128 px
// use the uncompressed DIB form, which every Windows version understands; the
// 256 px frame is PNG, the one size where the format is universally supported.
func buildIcon(sizes []int) ([]byte, error) {
	frames := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		frame, err := frame(size)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}

	var out bytes.Buffer
	// ICONDIR: reserved, type (1 = icon), image count.
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(frames)))

	offset := 6 + 16*len(frames)
	for i, frame := range frames {
		size := sizes[i]
		out.WriteByte(byte(size % 256)) // 0 means 256
		out.WriteByte(byte(size % 256))
		out.WriteByte(0)                                        // palette entries
		out.WriteByte(0)                                        // reserved
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))  // colour planes
		_ = binary.Write(&out, binary.LittleEndian, uint16(32)) // bits per pixel
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(frame)))
		_ = binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(frame)
	}
	for _, frame := range frames {
		out.Write(frame)
	}
	return out.Bytes(), nil
}

// frame renders one icon size and returns its ICO image payload.
func frame(size int) ([]byte, error) {
	encoded := brand.AppPNG(size)
	if len(encoded) == 0 {
		return nil, fmt.Errorf("render %d px icon", size)
	}
	img, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode %d px icon: %w", size, err)
	}
	if size >= 256 {
		return encoded, nil
	}
	return dib(img), nil
}

// dib encodes an image as an ICO DIB payload: a BITMAPINFOHEADER, a bottom-up
// 32-bit BGRA colour bitmap, and the 1-bit AND mask that marks transparent
// pixels for Windows versions that ignore the alpha channel.
func dib(img image.Image) []byte {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint32(40)) // header size
	_ = binary.Write(&out, binary.LittleEndian, int32(width))
	_ = binary.Write(&out, binary.LittleEndian, int32(height*2)) // colour + mask
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))       // planes
	_ = binary.Write(&out, binary.LittleEndian, uint16(32))      // bits per pixel
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))       // BI_RGB
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))       // image size
	_ = binary.Write(&out, binary.LittleEndian, int32(0))        // x pixels per metre
	_ = binary.Write(&out, binary.LittleEndian, int32(0))        // y pixels per metre
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))       // colours used
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))       // important colours

	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			out.Write([]byte{byte(b >> 8), byte(g >> 8), byte(r >> 8), byte(a >> 8)})
		}
	}

	maskRow := ((width + 31) / 32) * 4
	for y := height - 1; y >= 0; y-- {
		row := make([]byte, maskRow)
		for x := 0; x < width; x++ {
			if _, _, _, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA(); a == 0 {
				row[x/8] |= 0x80 >> uint(x%8)
			}
		}
		out.Write(row)
	}
	return out.Bytes()
}
