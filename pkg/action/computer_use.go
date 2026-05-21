package action

import (
	"context"
	"encoding/json"
	"fmt"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// ComputerUseTool 提供 GUI 自动化能力（点击/键盘/截图）。
// 架构文档: docs/arch/07-Tool-Action-Layer-深度选型.md §7.1
type ComputerUseTool struct {
	ScreenshotCap  ScreenshotProvider
	MouseAction    MouseController
	KeyboardAction KeyboardController
	DisplayWidth   int // 1280
	DisplayHeight  int // 800
}

// NewComputerUseTool 创建跨平台的 GUI 自动化工具实例。
func NewComputerUseTool() *ComputerUseTool {
	s, m, k := getPlatformProviders()
	return &ComputerUseTool{
		ScreenshotCap:  s,
		MouseAction:    m,
		KeyboardAction: k,
		DisplayWidth:   1280,
		DisplayHeight:  800,
	}
}

type computerUseArgs struct {
	Action     string `json:"action"` // "screenshot", "left_click", "right_click", "mouse_move", "type", "key"
	Coordinate []int  `json:"coordinate,omitempty"`
	Text       string `json:"text,omitempty"`
}

type computerUseResult struct {
	Base64Image string `json:"base64_image,omitempty"` // For screenshot
	Status      string `json:"status"`
}

// Execute 执行具体的 GUI 动作。
func (c *ComputerUseTool) Execute(ctx context.Context, input []byte) ([]byte, error) { //nolint:gocyclo
	var args computerUseArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: invalid args", err)
	}

	res := computerUseResult{Status: "success"}

	switch args.Action {
	case "screenshot":
		data, err := c.ScreenshotCap.Capture()
		if err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: capture failed", err)
		}
		// In a real scenario we'd base64 encode it, for now just returning success status
		// and the length to avoid massive JSON in logs if not needed.
		res.Base64Image = fmt.Sprintf("<image data length: %d>", len(data))
	case "left_click":
		if len(args.Coordinate) < 2 {
			return nil, perrors.New(perrors.CodeInternal, "computer_use: left_click requires [x, y]")
		}
		if err := c.MouseAction.Click(args.Coordinate[0], args.Coordinate[1]); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: left_click failed", err)
		}
	case "mouse_move":
		if len(args.Coordinate) < 2 {
			return nil, perrors.New(perrors.CodeInternal, "computer_use: mouse_move requires [x, y]")
		}
		if err := c.MouseAction.Move(args.Coordinate[0], args.Coordinate[1]); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: mouse_move failed", err)
		}
	case "type":
		if args.Text == "" {
			return nil, perrors.New(perrors.CodeInternal, "computer_use: type requires text")
		}
		if err := c.KeyboardAction.Type(args.Text); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: type failed", err)
		}
	case "key":
		if args.Text == "" {
			return nil, perrors.New(perrors.CodeInternal, "computer_use: key requires text")
		}
		if err := c.KeyboardAction.KeyPress(args.Text); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: key failed", err)
		}
	default:
		return nil, perrors.New(perrors.CodeInternal, "computer_use: unsupported action: "+args.Action)
	}

	return json.Marshal(res)
}

// ScreenshotProvider 捕获屏幕图像。
type ScreenshotProvider interface {
	Capture() ([]byte, error) // *image.RGBA 编码为 PNG
}

// MouseController 鼠标操作。
type MouseController interface {
	Move(x, y int) error
	Click(x, y int) error
	Scroll(dx, dy int) error
	Drag(x1, y1, x2, y2 int) error
}

// KeyboardController 键盘操作。
type KeyboardController interface {
	Type(text string) error
	KeyCombo(keys ...string) error
	KeyPress(key string) error
}
