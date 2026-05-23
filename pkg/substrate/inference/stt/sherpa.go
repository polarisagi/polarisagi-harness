package stt

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// SherpaOnnxOfflineRecognizer is an opaque pointer
type SherpaOnnxOfflineRecognizer struct{}

// SherpaOnnxOfflineStream is an opaque pointer
type SherpaOnnxOfflineStream struct{}

var (
	CreateOfflineRecognizer        func(config uintptr) *SherpaOnnxOfflineRecognizer
	DestroyOfflineRecognizer       func(recognizer *SherpaOnnxOfflineRecognizer)
	CreateOfflineStream            func(recognizer *SherpaOnnxOfflineRecognizer) *SherpaOnnxOfflineStream
	DestroyOfflineStream           func(stream *SherpaOnnxOfflineStream)
	AcceptWaveformOffline          func(stream *SherpaOnnxOfflineStream, sampleRate int32, samples *float32, n int32)
	DecodeOfflineStream            func(recognizer *SherpaOnnxOfflineRecognizer, stream *SherpaOnnxOfflineStream)
	GetOfflineStreamResult         func(stream *SherpaOnnxOfflineStream) uintptr
	DestroyOfflineRecognizerResult func(result uintptr)
)

var (
	libMu   sync.Mutex
	loadErr error
	loaded  bool
)

// LoadLibrary 动态加载 sherpa-onnx C API (零 CGO)。
// 幂等可重入：已加载则直接返回 nil；加载失败后可再次尝试（下载完成后调用）。
func LoadLibrary(libPath string) error {
	libMu.Lock()
	defer libMu.Unlock()

	if loaded {
		return nil // 已成功加载，直接复用
	}

	lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		loadErr = err
		return loadErr
	}

	purego.RegisterLibFunc(&CreateOfflineRecognizer, lib, "SherpaOnnxCreateOfflineRecognizer")
	purego.RegisterLibFunc(&DestroyOfflineRecognizer, lib, "SherpaOnnxDestroyOfflineRecognizer")
	purego.RegisterLibFunc(&CreateOfflineStream, lib, "SherpaOnnxCreateOfflineStream")
	purego.RegisterLibFunc(&DestroyOfflineStream, lib, "SherpaOnnxDestroyOfflineStream")
	purego.RegisterLibFunc(&AcceptWaveformOffline, lib, "SherpaOnnxAcceptWaveformOffline")
	purego.RegisterLibFunc(&DecodeOfflineStream, lib, "SherpaOnnxDecodeOfflineStream")
	purego.RegisterLibFunc(&GetOfflineStreamResult, lib, "SherpaOnnxGetOfflineStreamResult")
	purego.RegisterLibFunc(&DestroyOfflineRecognizerResult, lib, "SherpaOnnxDestroyOfflineRecognizerResult")

	loaded = true
	loadErr = nil
	return nil
}

// Engine 包装了 STT 引擎实例
type Engine struct {
	recognizer *SherpaOnnxOfflineRecognizer
	mu         sync.Mutex
}

// NewEngine 初始化引擎。
// 库未加载时返回 recognizer=nil 的桩实例，Transcribe 内会走 mock 路径。
// 这样 globalSTTEngine 始终非 nil，/v1/audio/transcriptions 不会返回 503。
func NewEngine(modelDir string) (*Engine, error) {
	if !loaded {
		// 库缺失时降级为 mock 引擎（Transcribe 走 !loaded 分支）
		return &Engine{recognizer: nil}, nil
	}

	// TODO: 构造正确的 config 结构体传入 CreateOfflineRecognizer
	// config := buildConfig(modelDir)
	// rec := CreateOfflineRecognizer(config)

	return &Engine{
		recognizer: nil,
	}, nil
}

// Transcribe 传入 16000Hz 16-bit PCM 单声道音频数据并返回文本
func (e *Engine) Transcribe(samples []float32, sampleRate int) (string, error) {
	if !loaded || e.recognizer == nil {
		// Mock 回退：如果未正确初始化，返回模拟文本
		return "（未连接真实引擎，此为本地 Mock 语音转文字）", nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	stream := CreateOfflineStream(e.recognizer)
	if stream == nil {
		return "", errors.New("failed to create stream")
	}
	defer DestroyOfflineStream(stream)

	if len(samples) > 0 {
		AcceptWaveformOffline(stream, int32(sampleRate), &samples[0], int32(len(samples)))
	}
	DecodeOfflineStream(e.recognizer, stream)

	resPtr := GetOfflineStreamResult(stream)
	if resPtr == 0 {
		return "", errors.New("failed to get result")
	}
	defer DestroyOfflineRecognizerResult(resPtr)

	// 解析返回的 C 结构体中的 const char* text
	// 按照 SherpaOnnx 规范，result 的第一个字段就是 text 指针
	textPtr := *(**byte)(unsafe.Pointer(resPtr))
	if textPtr == nil {
		return "", nil
	}

	// 简单的 C 字符串转 Go 字符串
	var bytes []byte
	for i := 0; ; i++ {
		b := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(textPtr)) + uintptr(i)))
		if b == 0 {
			break
		}
		bytes = append(bytes, b)
	}

	return string(bytes), nil
}
