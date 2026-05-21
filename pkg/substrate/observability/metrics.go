package observability

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var (
	GlobalTokenBurnRate = NewTokenBurnRate()
	GlobalSurpriseIndex = NewSurpriseIndex()
)

// TokenBurnRate tracks token consumption rate for circuit breaking.
// 架构文档: docs/arch/M03-Observability-深度选型.md §3
type TokenBurnRate struct {
	cumulativeTokens atomic.Int64
	lastTick         time.Time
	lastTokens       int64

	ema5s  float64
	ema30s float64

	baselineP95 float64
	callCount   atomic.Int64

	mu sync.RWMutex
}

func NewTokenBurnRate() *TokenBurnRate {
	return &TokenBurnRate{
		lastTick:    time.Now(),
		baselineP95: 200.0, // 冷启动保护值
	}
}

func (tbr *TokenBurnRate) Add(tokens int64) {
	tbr.cumulativeTokens.Add(tokens)
	tbr.callCount.Add(1)
}

// Tick updates the EMA rates. Must be called periodically (e.g., every 1s).
func (tbr *TokenBurnRate) Tick() {
	tbr.mu.Lock()
	defer tbr.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tbr.lastTick).Seconds()
	if elapsed <= 0 {
		return
	}

	currentTokens := tbr.cumulativeTokens.Load()
	deltaTokens := currentTokens - tbr.lastTokens
	instantRate := float64(deltaTokens) / elapsed

	// α=0.33 for ~5s window
	tbr.ema5s = (0.33 * instantRate) + (1-0.33)*tbr.ema5s
	// α=0.06 for ~30s window
	tbr.ema30s = (0.06 * instantRate) + (1-0.06)*tbr.ema30s

	tbr.lastTokens = currentTokens
	tbr.lastTick = now
}

type ThrottleStage int

const (
	ThrottleNormal ThrottleStage = 0
	ThrottleStage1 ThrottleStage = 1 // THROTTLE
	ThrottleStage2 ThrottleStage = 2 // HARD STOP
	ThrottleStage3 ThrottleStage = 3 // FULLSTOP
)

func (tbr *TokenBurnRate) CheckThrottle() ThrottleStage {
	tbr.mu.RLock()
	defer tbr.mu.RUnlock()

	// 学习型基线 (MVP: 暂时使用静态保守上限)
	limit := math.Max(tbr.baselineP95, 200.0)

	switch {
	case tbr.ema30s > limit*10.0:
		return ThrottleStage3
	case tbr.ema30s > limit*3.0:
		return ThrottleStage2
	case tbr.ema5s > limit*2.0:
		return ThrottleStage1
	default:
		return ThrottleNormal
	}
}

// SurpriseIndex measures trajectory deviation from historical successes.
// 基础版实现 (两组件: embedding + tool sequence).
// 架构文档: docs/arch/M03-Observability-深度选型.md §4.0
type SurpriseIndex struct {
	defaultValue float64
	staleness    time.Time
	mu           sync.RWMutex
}

func NewSurpriseIndex() *SurpriseIndex {
	return &SurpriseIndex{
		defaultValue: 0.5,
		staleness:    time.Now(),
	}
}

// ComputeBasic calculates the basic Phase 0.1 surprise index.
func (si *SurpriseIndex) ComputeBasic(ctx context.Context, embedding []float64, toolSeq []string) float64 {
	// MVP: return default fallback 0.5 (System 1.5) as the embedding and distance metrics
	// are not yet populated locally.
	si.mu.Lock()
	si.staleness = time.Now()
	si.mu.Unlock()
	return si.defaultValue
}

func (si *SurpriseIndex) IsStale() bool {
	si.mu.RLock()
	defer si.mu.RUnlock()
	// Staleness > 120s -> true
	return time.Since(si.staleness).Seconds() > 120
}

// DecisionLog records a single routing decision for offline analysis.
type DecisionLog struct {
	Timestamp     time.Time `json:"timestamp"`
	Route         string    `json:"route"`
	SurpriseIndex float64   `json:"surprise_index"`
	Provider      string    `json:"provider"`
	Reason        string    `json:"reason"`
}

type DecisionLogStore interface {
	Append(ctx context.Context, log DecisionLog) error
}

type DecisionLogger struct {
	mu    sync.Mutex
	store DecisionLogStore
}

// NewDecisionLogger 创建新的决策日志记录器。
func NewDecisionLogger(store DecisionLogStore) *DecisionLogger {
	return &DecisionLogger{
		store: store,
	}
}

// Log 记录一条决策日志。
func (dl *DecisionLogger) Log(ctx context.Context, log DecisionLog) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if dl.store == nil {
		return nil
	}
	return dl.store.Append(ctx, log)
}

// ============================================================================
// polaris_surrealdb_index_size_mb — Prometheus Gauge
// 覆盖 [Storage-SurrealDB-Core] HNSW + BM25 + 图索引总内存占用。
// 架构文档: docs/arch/M02-Storage-Fabric.md §3
// ============================================================================

var PolarisSurrealDBIndexSizeMB atomic.Int64

// ReportSurrealDBIndexSize 设置当前 [Storage-SurrealDB-Core] 索引的内存占用（MB）。
// 由 SurrealDB-Core FFI 的定期监控 goroutine 调用。
func ReportSurrealDBIndexSize(sizeMB int64) {
	PolarisSurrealDBIndexSizeMB.Store(sizeMB)
}

// MetricsHandler 返回 Prometheus 文本格式的 /metrics HTTP Handler。
// 当前暴露的指标:
//
//	polaris_surrealdb_index_size_mb — SurrealDB-Core 索引内存占用（Gauge）
//
// 所有 gauge 不带 label（MVP 简化版；Tier 1+ 升级为 promhttp.Handler + 标准 OTel 维度）
func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		ls := PolarisSurrealDBIndexSizeMB.Load()
		fmt.Fprintf(w, "# HELP polaris_surrealdb_index_size_mb SurrealDB-Core index memory usage\n")
		fmt.Fprintf(w, "# TYPE polaris_surrealdb_index_size_mb gauge\n")
		fmt.Fprintf(w, "polaris_surrealdb_index_size_mb %d\n", ls)
	})
}
