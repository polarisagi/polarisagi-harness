package server

// sse_media.go — 多模态内容预处理
// 职责：在将图片/视频传递给大模型前做尺寸/大小管控，减少 token 用量和网络延迟。
// 仅使用 Go 标准库，无外部依赖。
//
// 设计约束（Tier-0）：
//   - 图片：最长边 > 1568px 时等比缩放（Anthropic Claude 有效感知上限）
//   - 图片：PNG/GIF → JPEG（quality=85），去除 alpha 通道节省传输体积
//   - 视频：> 20MB 拒绝内联（Gemini inlineData 上限），提示用户
//   - 音频：已经由 STT 转写为文本，不经过此路径

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif" // 注册 GIF 解码器
	"image/jpeg"
	_ "image/jpeg" // 注册 JPEG 解码器
	_ "image/png"  // 注册 PNG 解码器
	"strings"
)

const (
	// maxImageSide 图片最长边上限（像素）。
	// 取 Anthropic Claude 推荐值 1568px，为三家主流 provider 中最保守的限制。
	// 超过此值额外像素不提升视觉理解能力，但线性增加 token 用量。
	maxImageSide = 1568

	// maxVideoInlineBytes Gemini inlineData 视频大小上限（20MB）。
	// 超过此值需走 Gemini File API 上传后使用 URI，当前不支持，拒绝处理。
	maxVideoInlineBytes = 20 * 1024 * 1024

	// jpegQuality JPEG 重编码质量（0-100）。
	// 85 在视觉质量与文件大小之间取得平衡，视觉模型对微小失真不敏感。
	jpegQuality = 85
)

// resizeImageForLLM 对图片进行预处理后再发送给大模型：
//  1. 最长边超过 maxImageSide 时等比降采样（最近邻，对 LLM 输入足够）
//  2. 非 JPEG 格式（PNG/GIF/WebP 等）转为 JPEG 减少传输体积
//
// 若解码失败（如 WebP，标准库不支持），原样返回，由 provider 自行处理。
// 返回值：(处理后字节, 实际 mimeType, 是否被处理)
func resizeImageForLLM(data []byte, mimeType string) ([]byte, string, bool) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// 无法解码（如 WebP）→ 原样透传，适配器会 base64 发送
		return data, mimeType, false
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	needsResize := w > maxImageSide || h > maxImageSide
	// PNG/GIF 转 JPEG；JPEG 本身无需重编码，除非需要 resize
	needsConvert := format == "png" || format == "gif"

	if !needsResize && !needsConvert {
		// 已是 JPEG 且尺寸合规，直接返回
		return data, mimeType, false
	}

	if needsResize {
		// 等比缩放，保持宽高比
		var newW, newH int
		if w >= h {
			newW = maxImageSide
			newH = h * maxImageSide / w
		} else {
			newH = maxImageSide
			newW = w * maxImageSide / h
		}
		if newW < 1 {
			newW = 1
		}
		if newH < 1 {
			newH = 1
		}
		img = scaleNearest(img, newW, newH)
	}

	// 统一编码为 JPEG（去除 alpha 通道，使用白色背景合成）
	dst := flattenToRGB(img)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		// 编码失败，原样返回
		return data, mimeType, false
	}
	return buf.Bytes(), "image/jpeg", true
}

// scaleNearest 最近邻插值缩放。
// 对于"降采样送给 LLM"的场景，最近邻精度完全够用，且无需外部依赖。
func scaleNearest(src image.Image, newW, newH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			// 映射目标像素到源像素（整数最近邻）
			srcX := x*srcW/newW + srcBounds.Min.X
			srcY := y*srcH/newH + srcBounds.Min.Y
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

// flattenToRGB 将图片合成到白色背景的 RGBA 画布。
// 消除 PNG/GIF 的透明通道，JPEG 不支持 alpha，直接编码会丢失透明度信息。
func flattenToRGB(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)

	// 先用白色填充背景
	white := image.NewUniform(opaqueWhite{})
	draw.Draw(dst, bounds, white, image.Point{}, draw.Src)
	// 将源图像叠加（Over 混合，透明像素呈现白色底）
	draw.Draw(dst, bounds, src, bounds.Min, draw.Over)
	return dst
}

// opaqueWhite 纯白不透明色，用于 flattenToRGB 的背景填充。
type opaqueWhite struct{}

func (opaqueWhite) RGBA() (r, g, b, a uint32) { return 0xffff, 0xffff, 0xffff, 0xffff }

// isProcessableImage 判断 MIME 类型是否可被标准库处理。
// WebP 需要 golang.org/x/image/webp，当前未引入，跳过处理。
func isProcessableImage(mimeType string) bool {
	switch {
	case strings.HasPrefix(mimeType, "image/jpeg"),
		strings.HasPrefix(mimeType, "image/jpg"),
		strings.HasPrefix(mimeType, "image/png"),
		strings.HasPrefix(mimeType, "image/gif"):
		return true
	default:
		// image/webp, image/bmp 等标准库不支持解码
		return false
	}
}
