//go:build darwin

package action

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

type darwinScreenshot struct{}

func (d *darwinScreenshot) Capture() ([]byte, error) {
	tmpPath := filepath.Join(os.TempDir(), "polaris_screenshot.png")
	defer os.Remove(tmpPath)
	cmd := exec.Command("screencapture", "-x", "-t", "png", tmpPath)
	if err := cmd.Run(); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("screencapture failed (requires macOS): %v", err), err)
	}
	return os.ReadFile(tmpPath)
}

type darwinMouse struct{}

func (d *darwinMouse) Move(x, y int) error {
	// relies on cliclick
	return exec.Command("cliclick", fmt.Sprintf("m:%d,%d", x, y)).Run()
}

func (d *darwinMouse) Click(x, y int) error {
	return exec.Command("cliclick", fmt.Sprintf("c:%d,%d", x, y)).Run()
}

func (d *darwinMouse) Scroll(dx, dy int) error {
	// simple stub for scroll
	return exec.Command("cliclick", "du:1").Run()
}

func (d *darwinMouse) Drag(x1, y1, x2, y2 int) error {
	return exec.Command("cliclick", fmt.Sprintf("dd:%d,%d", x1, y1), fmt.Sprintf("du:%d,%d", x2, y2)).Run()
}

type darwinKeyboard struct{}

func (k *darwinKeyboard) Type(text string) error {
	script := fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, text)
	return exec.Command("osascript", "-e", script).Run()
}

func (k *darwinKeyboard) KeyCombo(keys ...string) error {
	// Very simplified KeyCombo assuming last key is the target and others are modifiers
	if len(keys) == 0 {
		return nil
	}
	// Stub fallback to cliclick
	return exec.Command("cliclick", fmt.Sprintf("kp:%s", keys[len(keys)-1])).Run()
}

func (k *darwinKeyboard) KeyPress(key string) error {
	return exec.Command("cliclick", fmt.Sprintf("kp:%s", key)).Run()
}

func getPlatformProviders() (ScreenshotProvider, MouseController, KeyboardController) {
	return &darwinScreenshot{}, &darwinMouse{}, &darwinKeyboard{}
}
