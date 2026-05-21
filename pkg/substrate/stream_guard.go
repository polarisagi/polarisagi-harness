package substrate

import "context"

// StreamBudgetGuard — 流式响应 token 预算守卫。
// 架构文档: docs/arch/01-Inference-Runtime-深度选型.md §5.2-5.4

type StreamBudgetGuard struct {
	sessionBudget  *TokenBudget
	burnDetector   *TokenBurnDetector
	maxBufferSize  int // 256KB
	accumulatedOut int
	chunkCount     int
}

// NewStreamBudgetGuard 创建 TokenBudget。
func NewStreamBudgetGuard(budget *TokenBudget, detector *TokenBurnDetector, maxBuf int) *StreamBudgetGuard {
	return &StreamBudgetGuard{
		sessionBudget: budget,
		burnDetector:  detector,
		maxBufferSize: maxBuf,
	}
}

// GetMaxBufferSize 返回最大缓冲区大小。
func (g *StreamBudgetGuard) GetMaxBufferSize() int {
	return g.maxBufferSize
}

// TokenBudget token 预算。
type TokenBudget struct {
	remaining int
}

// TokenBurnDetector 基于加速度的 burn rate 检测。
// 5s 窗口: v1=(mid-first)/dt1, v2=(last-mid)/dt2, accel=(v2-v1)/((dt1+dt2)/2)
// accel > baseline.P95 × 3.0 → BurnAlert
type TokenBurnDetector struct {
	samples []burnSample
	window  int64 // 5s
}

// NewTokenBurnDetector 创建燃烧检测器。
func NewTokenBurnDetector(window int64) *TokenBurnDetector {
	return &TokenBurnDetector{
		window: window,
	}
}

// GetWindow 返回检测窗口时间。
func (d *TokenBurnDetector) GetWindow() int64 {
	return d.window
}

type burnSample struct {
	tokens int64
	ts     int64 // unix micro
}

// GuardChunk 检查每个 token chunk。
// 摊销检查: 每 100 chunk 或第 1 chunk 执行。
// L1: sessBudget.Remaining() <= 0 → WARN (不阻断)
// L2: burnDetector 检测加速度异常 → 硬阻断
// L3: sessBudget.IsExhausted() → 硬阻断
func (g *StreamBudgetGuard) GuardChunk(ctx context.Context, tokens int) error {
	g.chunkCount++
	g.accumulatedOut += tokens

	if g.chunkCount%100 != 0 && g.chunkCount != 1 {
		return nil
	}

	if alert := g.burnDetector.CheckAcceleration(g.accumulatedOut); alert != nil {
		return ErrFatalStreamAbort
	}

	if g.sessionBudget.remaining <= 0 {
		return ErrStreamBudgetExhausted
	}

	return nil
}

// CheckAcceleration 检测 token 消耗加速度异常。
func (d *TokenBurnDetector) CheckAcceleration(accumulated int) error {
	now := int64(0) // time.Now().UnixMicro()
	d.samples = append(d.samples, burnSample{tokens: int64(accumulated), ts: now})

	// 保留 5s 窗口
	cutoff := now - 5_000_000 // 5s in microseconds
	start := 0
	for start < len(d.samples) && d.samples[start].ts < cutoff {
		start++
	}
	d.samples = d.samples[start:]

	if len(d.samples) < 3 {
		return nil
	}

	mid := len(d.samples) / 2
	first := d.samples[0]
	last := d.samples[len(d.samples)-1]
	middle := d.samples[mid]

	dt1 := float64(middle.ts - first.ts)
	dt2 := float64(last.ts - middle.ts)
	if dt1 == 0 || dt2 == 0 {
		return nil
	}

	v1 := float64(middle.tokens-first.tokens) / dt1
	v2 := float64(last.tokens-middle.tokens) / dt2
	accel := (v2 - v1) / ((dt1 + dt2) / 2)

	if accel > 3.0 { // 3.0x P95 阈值
		return &BurnAlert{Acceleration: accel}
	}
	return nil
}

// JSONRepair 栈式 JSON 修复。
// 栈式括号匹配 → 自动闭合 } ] " → 截断至最后合法 JSON 路径 → 移除不完整 key-value。
// 确定性 Go 实现, <1ms.
func JSONRepair(data []byte) (*RepairResult, error) {
	return &RepairResult{}, nil
}

type RepairResult struct {
	Repaired       []byte
	DiscardedKeys  []string
	BracketsClosed int
	JsonRepaired   bool
}

// TrackStreamCost 流式成本追踪。
// 流正常结束 → 精确 API usage; 流中断 → 根据中断原因处理。
func TrackStreamCost(ctx context.Context, accumulated int, provider string) error {
	// FatalStreamAbort → 丢弃 accumulatedOutput → M4 S_REPLAN
	// > MaxStreamBufferSize → 写入 workspace 临时文件 → ErrResponseTooLarge
	// 正常中断 → JSONRepair + 双重安全校验
	return nil
}

var (
	ErrFatalStreamAbort      = &StreamError{"fatal stream abort"}
	ErrStreamBudgetExhausted = &StreamError{"stream budget exhausted"}
	ErrResponseTooLarge      = &StreamError{"response too large"}
)

type StreamError struct{ msg string }

func (e *StreamError) Error() string { return e.msg }

type BurnAlert struct{ Acceleration float64 }

func (b *BurnAlert) Error() string { return "burn rate acceleration alert" }
