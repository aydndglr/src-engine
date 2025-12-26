//go:build windows

package video

import (
	"image"
	"src-engine-v2/internal/platform/win32"
)

// Capturer Interface
// Stream servisi bu arayüzü bekliyor.
type Capturer interface {
	Start() error
	Capture() (*image.RGBA, error)
	Size() (int, int)
	Close()
}

// NewCapturer:
// Artık burada C kodu yok.
// İşi "internal/platform/win32" paketine devrediyoruz.
func NewCapturer(displayIndex int) Capturer {
	return win32.NewDxgiCapturer(displayIndex)
}