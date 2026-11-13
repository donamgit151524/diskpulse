//go:build windows

package tray

import (
	"image/color"
	"testing"
	"unsafe"
)

func TestNotifyIconDataSizeAndIconBuild(t *testing.T) {
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Fatalf("sizeof(notifyIconData) = %d, want 976 on amd64 Windows", got)
	}

	h := buildStatusIcon(color.NRGBA{R: 14, G: 165, B: 233, A: 255})
	if h == 0 {
		t.Fatalf("buildStatusIcon returned 0")
	}
	_, _, _ = procDestroyIcon.Call(h)
}
