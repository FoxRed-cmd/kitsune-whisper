package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

// TestBuildIconWritesAnEntryPerSize guards the .ico container: Windows reads
// the directory before it looks at any image, and a wrong offset or width byte
// makes Explorer fall back to a generic icon.
func TestBuildIconWritesAnEntryPerSize(t *testing.T) {
	sizes := []int{16, 32, 256}
	data, err := buildIcon(sizes)
	if err != nil {
		t.Fatalf("buildIcon: %v", err)
	}
	if reserved := binary.LittleEndian.Uint16(data[0:2]); reserved != 0 {
		t.Fatalf("reserved = %d", reserved)
	}
	if kind := binary.LittleEndian.Uint16(data[2:4]); kind != 1 {
		t.Fatalf("type = %d, want 1 (icon)", kind)
	}
	if count := binary.LittleEndian.Uint16(data[4:6]); int(count) != len(sizes) {
		t.Fatalf("count = %d, want %d", count, len(sizes))
	}

	for i, size := range sizes {
		entry := 6 + 16*i
		width := int(data[entry])
		if size == 256 {
			if width != 0 {
				t.Fatalf("size %d: width byte = %d, want 0", size, width)
			}
		} else if width != size {
			t.Fatalf("width byte = %d, want %d", width, size)
		}
		length := int(binary.LittleEndian.Uint32(data[entry+8 : entry+12]))
		offset := int(binary.LittleEndian.Uint32(data[entry+12 : entry+16]))
		if offset < 6+16*len(sizes) || offset+length > len(data) {
			t.Fatalf("size %d: frame [%d,%d) is outside the file", size, offset, offset+length)
		}
		frame := data[offset : offset+length]
		if size == 256 {
			if _, err := png.Decode(bytes.NewReader(frame)); err != nil {
				t.Fatalf("size %d: PNG frame: %v", size, err)
			}
			continue
		}
		if header := binary.LittleEndian.Uint32(frame[0:4]); header != 40 {
			t.Fatalf("size %d: DIB header = %d, want 40", size, header)
		}
	}
}
