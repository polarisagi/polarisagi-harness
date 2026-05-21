package action

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chromedp/chromedp"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// BrowserUseTool 提供无头浏览器控制能力。
type BrowserUseTool struct{}

func NewBrowserUseTool() *BrowserUseTool {
	return &BrowserUseTool{}
}

type browserUseArgs struct {
	Action   string `json:"action"` // "navigate", "click", "type", "screenshot"
	URL      string `json:"url,omitempty"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
}

type browserUseResult struct {
	Base64Image string `json:"base64_image,omitempty"`
	Status      string `json:"status"`
}

func (b *BrowserUseTool) Execute(ctx context.Context, input []byte) ([]byte, error) {
	var args browserUseArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "browser_use: invalid args", err)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()

	// Timeout to prevent hanging
	ctxTimeout, cancelTimeout := context.WithTimeout(taskCtx, 30*time.Second)
	defer cancelTimeout()

	res := browserUseResult{Status: "success"}

	switch args.Action {
	case "navigate":
		if args.URL == "" {
			return nil, perrors.New(perrors.CodeInternal, "browser_use: navigate requires url")
		}
		if err := chromedp.Run(ctxTimeout, chromedp.Navigate(args.URL)); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "browser_use: navigate failed", err)
		}
	case "click":
		if args.Selector == "" {
			return nil, perrors.New(perrors.CodeInternal, "browser_use: click requires selector")
		}
		if err := chromedp.Run(ctxTimeout, chromedp.Click(args.Selector, chromedp.NodeVisible)); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "browser_use: click failed", err)
		}
	case "type":
		if args.Selector == "" || args.Text == "" {
			return nil, perrors.New(perrors.CodeInternal, "browser_use: type requires selector and text")
		}
		if err := chromedp.Run(ctxTimeout, chromedp.SendKeys(args.Selector, args.Text, chromedp.NodeVisible)); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "browser_use: type failed", err)
		}
	case "screenshot":
		var buf []byte
		if err := chromedp.Run(ctxTimeout, chromedp.CaptureScreenshot(&buf)); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "browser_use: screenshot failed", err)
		}
		// res.Base64Image = base64.StdEncoding.EncodeToString(buf)
		res.Status = "screenshot captured"
	default:
		return nil, perrors.New(perrors.CodeInternal, "browser_use: unsupported action: "+args.Action)
	}

	return json.Marshal(res)
}
