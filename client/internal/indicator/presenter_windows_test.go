//go:build windows

package indicator

import (
	"image"
	"testing"
	"unsafe"
)

func TestCopyBGRATopDownSwapsRedAndBlue(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// Premultiplied RGBA: a half-transparent red and an opaque blue.
	frame.Pix[0], frame.Pix[1], frame.Pix[2], frame.Pix[3] = 128, 0, 0, 128
	frame.Pix[4], frame.Pix[5], frame.Pix[6], frame.Pix[7] = 0, 0, 255, 255
	// Second row: opaque white and fully transparent.
	frame.Pix[8], frame.Pix[9], frame.Pix[10], frame.Pix[11] = 255, 255, 255, 255
	frame.Pix[12], frame.Pix[13], frame.Pix[14], frame.Pix[15] = 0, 0, 0, 0

	dst := make([]byte, 2*2*4)
	copyBGRATopDown(dst, frame)

	want := []byte{
		0, 0, 128, 128, // red
		255, 0, 0, 255, // blue
		255, 255, 255, 255, // white
		0, 0, 0, 0, // transparent
	}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("byte %d = %d, want %d (dst = %v)", i, dst[i], want[i], dst)
		}
	}
}

func TestCopyBGRATopDownHonoursStrideAndBounds(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// Only the top-right pixel is opaque: it must land in the second slot of
	// the first DIB row.
	frame.Pix[4], frame.Pix[5], frame.Pix[6], frame.Pix[7] = 10, 20, 30, 255

	dst := make([]byte, 2*2*4)
	copyBGRATopDown(dst, frame)

	if got := dst[4:8]; got[0] != 30 || got[1] != 20 || got[2] != 10 || got[3] != 255 {
		t.Fatalf("top-right pixel = %v, want [30 20 10 255]", got)
	}
	if got := dst[0:4]; got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 0 {
		t.Fatalf("top-left pixel = %v, want transparent", got)
	}
}

func TestClickOnlyInsideStopRegion(t *testing.T) {
	p := &windowsPresenter{
		clicks: make(chan struct{}, 1),
		input:  StopRegion(1),
	}
	stop := StopRegion(1).Min.Add(image.Pt(1, 1))

	p.click(int32(stop.X), int32(stop.Y))
	select {
	case <-p.clicks:
	default:
		t.Fatal("click inside the stop region was not forwarded")
	}

	p.click(1, 1)
	select {
	case <-p.clicks:
		t.Fatal("click outside the stop region was forwarded")
	default:
	}

	p.input = image.Rectangle{}
	p.click(int32(stop.X), int32(stop.Y))
	select {
	case <-p.clicks:
		t.Fatal("click was forwarded with no input region")
	default:
	}
}

func TestWin32StructLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout assertions are for windows/amd64")
	}
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"WNDCLASSEXW", unsafe.Sizeof(winWindowClassEx{}), 80},
		{"MONITORINFO", unsafe.Sizeof(winMonitorInfo{}), 40},
		{"MSG", unsafe.Sizeof(windowsMessage{}), 48},
		{"BITMAPINFOHEADER", unsafe.Sizeof(winBitmapInfoHeader{}), 40},
		{"BLENDFUNCTION", unsafe.Sizeof(winBlendFunction{}), 4},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s size = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
