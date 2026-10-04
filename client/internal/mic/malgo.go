package mic

import (
	"fmt"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// malgoBackend is the real Backend, wrapping miniaudio via malgo. Capture is
// requested at the device's native format (16-bit, native channels, native
// rate), so the Recorder owns downmix/resample.
type malgoBackend struct {
	ctx         *malgo.AllocatedContext
	deviceInfos []malgo.DeviceInfo
	// deviceIDs holds one C-allocated copy of a device ID per device, keyed by
	// the ID's hex string. DeviceID.Pointer() allocates in C because cgo forbids
	// handing C a Go pointer to a Go value that itself contains a Go pointer (the
	// config struct embeds the device ID pointer). Caching keeps the allocation
	// bounded, and miniaudio copies the ID during device init, so reuse across
	// Opens is safe.
	deviceIDs map[string]unsafe.Pointer
}

func newMalgoBackend(onLog func(string)) (*malgoBackend, error) {
	var logProc malgo.LogProc
	if onLog != nil {
		logProc = onLog
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, logProc)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	return &malgoBackend{ctx: ctx, deviceIDs: map[string]unsafe.Pointer{}}, nil
}

func (b *malgoBackend) Devices() ([]Device, error) {
	infos, err := b.ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("enumerate capture devices: %w", err)
	}
	b.deviceInfos = infos
	devices := make([]Device, len(infos))
	for i := range infos {
		devices[i] = Device{
			Index:     i,
			Name:      infos[i].Name(),
			IsDefault: infos[i].IsDefault != 0,
		}
	}
	return devices, nil
}

func (b *malgoBackend) Open(index int, onData func(pcm []byte)) (Stream, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 0 // device native
	cfg.SampleRate = 0       // device native
	cfg.Alsa.NoMMap = 1

	name := "system default"
	if index >= 0 {
		if len(b.deviceInfos) == 0 {
			if _, err := b.Devices(); err != nil {
				return nil, err
			}
		}
		if index >= len(b.deviceInfos) {
			return nil, fmt.Errorf("capture device index %d out of range", index)
		}
		info := b.deviceInfos[index]
		name = info.Name()
		key := info.ID.String()
		ptr, ok := b.deviceIDs[key]
		if !ok {
			ptr = info.ID.Pointer()
			b.deviceIDs[key] = ptr
		}
		cfg.Capture.DeviceID = ptr
	}

	dev, err := malgo.InitDevice(b.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(_, in []byte, _ uint32) { onData(in) },
	})
	if err != nil {
		return nil, fmt.Errorf("open capture device %q: %w", name, err)
	}
	return &malgoStream{device: dev}, nil
}

func (b *malgoBackend) Close() error {
	if b.ctx == nil {
		return nil
	}
	err := b.ctx.Uninit()
	b.ctx.Free()
	b.ctx = nil
	return err
}

type malgoStream struct {
	device *malgo.Device
}

func (s *malgoStream) SampleRate() int { return int(s.device.SampleRate()) }
func (s *malgoStream) Channels() int   { return int(s.device.CaptureChannels()) }

func (s *malgoStream) Start() error { return s.device.Start() }

// Close stops and uninitializes the device.
func (s *malgoStream) Close() error {
	s.device.Uninit()
	return nil
}
