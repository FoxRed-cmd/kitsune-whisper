// THROWAWAY prototype Go client (Windows first): toggle -> capture mic ->
// resample to 16 kHz mono -> POST /transcribe -> clipboard + Ctrl+V.
//
// This is NOT production code. It exists to answer one question: does the
// end-to-end loop feel right, and where exactly does the client break?
//
// Run:  go run .        (needs CGO_ENABLED=1 and a C compiler: see build.ps1)
// Hotkey: Ctrl+Shift+Space toggles recording.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"golang.design/x/clipboard"
	"golang.design/x/hotkey"
	"golang.org/x/sys/windows"
)

const (
	targetRate     = 16000
	targetChannels = 1
)

var serverURL = envOr("KITSUNE_SERVER", "http://127.0.0.1:8080")

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	if err := clipboard.Init(); err != nil {
		log.Fatalf("clipboard init failed: %v", err)
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(msg string) {
		log.Printf("malgo: %s", msg)
	})
	if err != nil {
		log.Fatalf("malgo context: %v", err)
	}
	defer func() { _ = ctx.Uninit(); ctx.Free() }()

	rec := &recorder{ctx: ctx}

	hk := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModShift}, hotkey.KeySpace)
	if err := hk.Register(); err != nil {
		log.Fatalf("hotkey register failed (Ctrl+Shift+Space taken?): %v", err)
	}
	log.Printf("hotkey registered: %v", hk)
	log.Printf("server: %s", serverURL)
	fmt.Println("Ready. Press Ctrl+Shift+Space to start/stop recording. (Ctrl+C to quit)")

	// Debounce: RegisterHotKey repeats while held; only the first keydown of a
	// physical press should toggle.
	pressed := false
	for {
		select {
		case <-hk.Keydown():
			if pressed {
				continue
			}
			pressed = true
			if rec.active {
				rec.stopAndTranscribe()
			} else {
				rec.start()
			}
		case <-hk.Keyup():
			pressed = false
		}
	}
}

type recorder struct {
	ctx    *malgo.AllocatedContext
	mu     sync.Mutex // guards device/active
	device *malgo.Device
	active bool
	pcmMu  sync.Mutex // guards pcm (written from audio callback)
	pcm    []byte
}

func (r *recorder) start() {
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = targetChannels
	cfg.SampleRate = targetRate
	cfg.Alsa.NoMMap = 1

	dev, err := malgo.InitDevice(r.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(_, in []byte, _ uint32) {
			r.pcmMu.Lock()
			r.pcm = append(r.pcm, in...)
			r.pcmMu.Unlock()
		},
	})
	if err != nil {
		log.Printf("capture init failed: %v", err)
		return
	}
	if err := dev.Start(); err != nil {
		log.Printf("capture start failed: %v", err)
		dev.Uninit()
		return
	}

	r.mu.Lock()
	r.device = dev
	r.active = true
	r.mu.Unlock()
	r.pcmMu.Lock()
	r.pcm = r.pcm[:0]
	r.pcmMu.Unlock()

	log.Printf("REC started: requested %d Hz -> device %d Hz (internal %d Hz), fmt %v, ch %d",
		targetRate, dev.SampleRate(), dev.CaptureInternalSampleRate(), dev.CaptureFormat(), dev.CaptureChannels())
	fmt.Println(">> recording... press the hotkey again to stop")
}

func (r *recorder) stopAndTranscribe() {
	r.mu.Lock()
	dev := r.device
	started := time.Now()
	r.device = nil
	r.active = false
	r.mu.Unlock()
	if dev == nil {
		return
	}
	dev.Uninit()

	r.pcmMu.Lock()
	pcm := append([]byte(nil), r.pcm...)
	r.pcmMu.Unlock()

	secs := float64(len(pcm)) / float64(targetRate*2*targetChannels)
	log.Printf("REC stopped: %d samples (%.2fs) in %dms wall", len(pcm)/2, secs, ms(time.Since(started)))

	if len(pcm) == 0 {
		log.Printf("no audio captured; nothing to transcribe")
		return
	}

	clip := filepath.Join(os.TempDir(), "kitsune-last-clip.wav")
	if err := os.WriteFile(clip, encodeWAV(pcm), 0o600); err != nil {
		log.Printf("save clip: %v", err)
	} else {
		log.Printf("clip saved: %s", clip)
	}

	text, err := transcribe(pcm)
	if err != nil {
		log.Printf("transcribe failed: %v", err)
		return
	}
	paste(text)
}

func transcribe(pcm []byte) (string, error) {
	wav := encodeWAV(pcm)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("audio", "clip.wav")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(wav); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	t0 := time.Now()
	resp, err := http.Post(serverURL+"/transcribe", w.FormDataContentType(), &body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var env struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		return "", fmt.Errorf("server %d: %s", resp.StatusCode, env.Error.Message)
	}

	var out struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Elapsed  float64 `json:"elapsed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	wall := ms(time.Since(t0))
	log.Printf("POST wall=%dms server=%.0fms audio=%.2fs over=%.2fx lang=%s text=%q",
		wall, out.Elapsed*1000, out.Duration, out.Elapsed/out.Duration, out.Language, out.Text)
	return out.Text, nil
}

func paste(text string) {
	if text == "" {
		log.Printf("empty transcript; clipboard untouched")
		return
	}
	if _, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text)); err != nil {
		log.Printf("clipboard write failed: %v", err)
		return
	}
	time.Sleep(80 * time.Millisecond)
	if err := sendCtrlV(); err != nil {
		log.Printf("paste failed: %v", err)
		return
	}
	// Clipboard restore is impossible to do safely on Windows (research #2):
	// the OS never tells us when the paste was consumed. Leave the transcript
	// on the clipboard and say so.
	log.Printf("pasted. clipboard now holds the transcript (restore skipped: impossible on Windows)")
}

func ms(d time.Duration) int64 { return d.Milliseconds() }

func encodeWAV(pcm []byte) []byte {
	var b bytes.Buffer
	dataLen := uint32(len(pcm))
	byteRate := uint32(targetRate * targetChannels * 2)
	blockAlign := uint16(targetChannels * 2)
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36)+dataLen)
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&b, binary.LittleEndian, uint16(targetChannels))
	binary.Write(&b, binary.LittleEndian, uint32(targetRate))
	binary.Write(&b, binary.LittleEndian, byteRate)
	binary.Write(&b, binary.LittleEndian, blockAlign)
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, dataLen)
	b.Write(pcm)
	return b.Bytes()
}

// --- Windows Ctrl+V via SendInput (cgo-free) ---

const (
	inputKeyboard  = 1
	keyeventfKeyup = 0x0002
	vkControl      = 0x11
	vkV            = 0x56
)

type keybdInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// 40 bytes on amd64: type(4)+pad(4)+union(32).
type input struct {
	typ uint32
	_   uint32
	ki  keybdInput
	_   [8]byte
}

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

func sendCtrlV() error {
	inputs := []input{
		{typ: inputKeyboard, ki: keybdInput{vk: vkControl}},
		{typ: inputKeyboard, ki: keybdInput{vk: vkV}},
		{typ: inputKeyboard, ki: keybdInput{vk: vkV, flags: keyeventfKeyup}},
		{typ: inputKeyboard, ki: keybdInput{vk: vkControl, flags: keyeventfKeyup}},
	}
	n, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if n != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput sent %d/%d: %v", n, len(inputs), err)
	}
	return nil
}
