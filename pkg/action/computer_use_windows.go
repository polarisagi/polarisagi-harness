//go:build windows

package action

import (
	"errors"
)

type windowsScreenshot struct{}

func (s *windowsScreenshot) Capture() ([]byte, error) {
	return nil, errors.New("windows computer use not fully implemented yet")
}

type windowsMouse struct{}

func (m *windowsMouse) Move(x, y int) error {
	return errors.New("windows computer use not fully implemented yet")
}

func (m *windowsMouse) Click(x, y int) error {
	return errors.New("windows computer use not fully implemented yet")
}

func (m *windowsMouse) Scroll(dx, dy int) error {
	return errors.New("windows computer use not fully implemented yet")
}

func (m *windowsMouse) Drag(x1, y1, x2, y2 int) error {
	return errors.New("windows computer use not fully implemented yet")
}

type windowsKeyboard struct{}

func (k *windowsKeyboard) Type(text string) error {
	return errors.New("windows computer use not fully implemented yet")
}

func (k *windowsKeyboard) KeyCombo(keys ...string) error {
	return errors.New("windows computer use not fully implemented yet")
}

func (k *windowsKeyboard) KeyPress(key string) error {
	return errors.New("windows computer use not fully implemented yet")
}

func getPlatformProviders() (ScreenshotProvider, MouseController, KeyboardController) {
	return &windowsScreenshot{}, &windowsMouse{}, &windowsKeyboard{}
}
