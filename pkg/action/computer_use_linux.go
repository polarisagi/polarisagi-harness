//go:build linux

package action

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type linuxScreenshot struct{}

func (s *linuxScreenshot) Capture() ([]byte, error) {
	tmpPath := filepath.Join(os.TempDir(), "polaris_screenshot.png")
	defer os.Remove(tmpPath)
	// Requires scrot or imagemagick (xwd/convert), fallback to scrot
	cmd := exec.Command("scrot", tmpPath)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("scrot failed (requires X11 and scrot): %w", err)
	}
	return os.ReadFile(tmpPath)
}

type linuxMouse struct{}

func (m *linuxMouse) Move(x, y int) error {
	return exec.Command("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)).Run()
}

func (m *linuxMouse) Click(x, y int) error {
	if err := m.Move(x, y); err != nil {
		return err
	}
	return exec.Command("xdotool", "click", "1").Run()
}

func (m *linuxMouse) Scroll(dx, dy int) error {
	// xdotool click 4/5 for scroll up/down
	btn := "5"
	if dy < 0 {
		btn = "4"
	}
	return exec.Command("xdotool", "click", btn).Run()
}

func (m *linuxMouse) Drag(x1, y1, x2, y2 int) error {
	if err := m.Move(x1, y1); err != nil {
		return err
	}
	if err := exec.Command("xdotool", "mousedown", "1").Run(); err != nil {
		return err
	}
	if err := m.Move(x2, y2); err != nil {
		return err
	}
	return exec.Command("xdotool", "mouseup", "1").Run()
}

type linuxKeyboard struct{}

func (k *linuxKeyboard) Type(text string) error {
	return exec.Command("xdotool", "type", "--delay", "10", text).Run()
}

func (k *linuxKeyboard) KeyCombo(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	combo := ""
	for i, key := range keys {
		if i > 0 {
			combo += "+"
		}
		combo += key
	}
	return exec.Command("xdotool", "key", combo).Run()
}

func (k *linuxKeyboard) KeyPress(key string) error {
	return exec.Command("xdotool", "key", key).Run()
}

func getPlatformProviders() (ScreenshotProvider, MouseController, KeyboardController) {
	return &linuxScreenshot{}, &linuxMouse{}, &linuxKeyboard{}
}
