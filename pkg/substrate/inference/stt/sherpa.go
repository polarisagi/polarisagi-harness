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
	loadOnce sync.Once
	loadErr  error
	loaded   bool
)

// LoadLibrary 动态加载 sherpa-onnx C API (零 CGO)
func LoadLibrary(libPath string) error {
	loadOnce.Do(func() {
		lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}

		purego.RegisterLibFunc(&CreateOfflineRecognizer, lib, "CreateOfflineRecognizer")
		purego.RegisterLibFunc(&DestroyOfflineRecognizer, lib, "DestroyOfflineRecognizer")
		purego.RegisterLibFunc(&CreateOfflineStream, lib, "CreateOfflineStream")
		purego.RegisterLibFunc(&DestroyOfflineStream, lib, "DestroyOfflineStream")
		purego.RegisterLibFunc(&AcceptWaveformOffline, lib, "AcceptWaveformOffline")
		purego.RegisterLibFunc(&DecodeOfflineStream, lib, "DecodeOfflineStream")
		purego.RegisterLibFunc(&GetOfflineStreamResult, lib, "GetOfflineStreamResult")
		purego.RegisterLibFunc(&DestroyOfflineRecognizerResult, lib, "DestroyOfflineRecognizerResult")

		loaded = true
	})
	return loadErr
}

// Engine 包装了 STT 引擎实例
type Engine struct {
	recognizer *SherpaOnnxOfflineRecognizer
	mu         sync.Mutex
}

// NewEngine 初始化引擎。由于缺少完整的 C Struct ABI 定义，此处先进行 Stub。
func NewEngine(modelDir string) (*Engine, error) {
	if !loaded {
		return nil, errors.New("sherpa-onnx library not loaded. Please install libsherpa-onnx-c-api")
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
