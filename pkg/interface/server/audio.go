package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/mrlaoliai/polaris-harness/pkg/substrate/inference/stt"
)

var globalSTTEngine *stt.Engine

// SetSTTEngine 注入全局的 STT 引擎实例
func SetSTTEngine(engine *stt.Engine) {
	globalSTTEngine = engine
}

// handleAudioTranscriptions 处理前端语音输入并转写文本
// 路由: POST /v1/audio/transcriptions
func (s *Server) handleAudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	if globalSTTEngine == nil {
		http.Error(w, "STT Engine not initialized", http.StatusServiceUnavailable)
		return
	}

	// 解析 multipart，获取 audio 文件 (通常是 webm 格式)
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 最大 10MB
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 将录音保存为临时文件以供 ffmpeg 处理
	tmpDir := os.TempDir()
	inPath := filepath.Join(tmpDir, uuid.New().String()+".webm")

	outFile, err := os.Create(inPath)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(outFile, file); err != nil {
		outFile.Close()
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	outFile.Close()
	defer os.Remove(inPath)

	// 使用 ffmpeg 提取为 16000Hz f32le 原始 PCM 数据流
	// 我们不落地 wav 文件，而是直接通过管道读取标准输出
	cmd := exec.Command("ffmpeg", "-y", "-i", inPath, "-f", "f32le", "-ac", "1", "-ar", "16000", "-")

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	if err := cmd.Run(); err != nil {
		slog.Error("ffmpeg decode failed", "err", err)
		// 如果机器上没有 ffmpeg，则触发 Mock (纯测试回退)
		mockText, _ := globalSTTEngine.Transcribe(nil, 16000)
		respondJSON(w, map[string]any{"text": mockText})
		return
	}

	pcmBytes := outBuf.Bytes()
	samples := make([]float32, len(pcmBytes)/4)
	for i := range samples {
		bits := binary.LittleEndian.Uint32(pcmBytes[i*4 : (i+1)*4])
		samples[i] = math.Float32frombits(bits)
	}

	text, err := globalSTTEngine.Transcribe(samples, 16000)
	if err != nil {
		http.Error(w, "stt failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, map[string]any{"text": text})
}

func respondJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}
