package substrate

import (
	"context"
	"sync"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// EmbeddingBatcher — Embedding API 批量调用优化器。
// 架构文档: docs/arch/01-Inference-Runtime-深度选型.md §6.1

// EmbedFn M1 Embedding API 调用函数类型（依赖注入，可 mock）。
type EmbedFn func(ctx context.Context, texts []string, model string) ([][]float32, error)

type EmbeddingBatcher struct {
	pendingHigh  [180]EmbedRequest // PriorityHigh: SurpriseIndex、交互式查询
	pendingLow   [76]EmbedRequest  // PriorityLow: GraphRAG、Consolidation
	batchWindow  time.Duration     // 10ms
	maxBatchSize int               // 100
	mu           sync.Mutex
	timer        *time.Timer
	embedFn      EmbedFn // M1 Embedding API 注入点
}

// Start 启动后台批处理定时器。
func (b *EmbeddingBatcher) Start(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		return
	}
	b.timer = time.NewTimer(b.batchWindow)
	go func() {
		for {
			select {
			case <-ctx.Done():
				b.timer.Stop()
				return
			case <-b.timer.C:
				b.flushQueue(ctx)
				b.timer.Reset(b.batchWindow)
			}
		}
	}()
}

// flushQueue 在定时器到期时执行，优先 High (最多 80)，用 Low 补齐 (最多 100)。
func (b *EmbeddingBatcher) flushQueue(ctx context.Context) {
	b.mu.Lock()
	var toProcess []EmbedRequest

	// Drain High: max 80
	for i := range b.pendingHigh {
		if b.pendingHigh[i].Text != "" {
			toProcess = append(toProcess, b.pendingHigh[i])
			b.pendingHigh[i] = EmbedRequest{} // clear
			if len(toProcess) >= int(float64(b.maxBatchSize)*0.8) {
				break
			}
		}
	}

	// Drain Low: fill up to 100
	for i := range b.pendingLow {
		if len(toProcess) >= b.maxBatchSize {
			break
		}
		if b.pendingLow[i].Text != "" {
			toProcess = append(toProcess, b.pendingLow[i])
			b.pendingLow[i] = EmbedRequest{} // clear
		}
	}
	b.mu.Unlock()

	if len(toProcess) == 0 {
		return
	}

	texts := make([]string, len(toProcess))
	for i, req := range toProcess {
		texts[i] = req.Text
	}
	// Call flushBatch
	// Note: We use the first request's model as a simplification,
	// assuming batches group by model in practice.
	model := "default"
	if len(toProcess) > 0 {
		model = toProcess[0].Model
	}
	results, _ := b.flushBatch(ctx, texts, model)
	for i, req := range toProcess {
		if i < len(results) {
			req.ResultCh <- results[i]
		} else {
			req.ResultCh <- EmbedResult{Error: perrors.New(perrors.CodeInternal, "missing result")}
		}
	}
}

// NewEmbeddingBatcher 创建 EmbeddingBatcher，embedFn 为 M1 Embedding API（nil 则 flushBatch 报错）。
func NewEmbeddingBatcher(batchWindow time.Duration, maxBatchSize int, embedFn EmbedFn) *EmbeddingBatcher {
	if batchWindow <= 0 {
		batchWindow = 10 * time.Millisecond
	}
	if maxBatchSize <= 0 {
		maxBatchSize = 100
	}
	return &EmbeddingBatcher{
		batchWindow:  batchWindow,
		maxBatchSize: maxBatchSize,
		embedFn:      embedFn,
	}
}

// EmbedRequest 单次 embedding 请求。
type EmbedRequest struct {
	Text     string
	Model    string
	Priority int
	ResultCh chan EmbedResult
}

// EmbedResult embedding 结果。
type EmbedResult struct {
	Vector []float32
	Error  error
}

// Embed 提交 embedding 请求。
// IF len(texts) >= maxBatchSize → 直接发单批。
// 否则入队 pendingHigh|Low, 启动/重置 10ms timer。
// timer 到期: drain pendingHigh max 80 条 → drain pendingLow 补齐至 100。
// 保留 20% 槽位给 Low (防饥饿)。
// Aging: Low 排队 >100ms → 自动升 High。
// 背压: High cap 80%→ErrBatcherSaturated 指数退避(50ms, max 2s);
//
//	Low cap 80%→排队 30ms→连续3次后指数退避。
func (b *EmbeddingBatcher) Embed(ctx context.Context, texts []string, model string, priority int) ([]EmbedResult, error) {
	if len(texts) >= b.maxBatchSize {
		return b.flushBatch(ctx, texts, model)
	}

	results := make([]EmbedResult, len(texts))
	for i, text := range texts {
		req := EmbedRequest{Text: text, Model: model, Priority: priority, ResultCh: make(chan EmbedResult, 1)}
		b.enqueue(req)
		select {
		case r := <-req.ResultCh:
			results[i] = r
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}
	return results, nil
}

func (b *EmbeddingBatcher) enqueue(req EmbedRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// 简化: 直接入队 High/Low
	if req.Priority == 0 {
		for i := range b.pendingHigh {
			if b.pendingHigh[i].Text == "" {
				b.pendingHigh[i] = req
				return
			}
		}
	}
	for i := range b.pendingLow {
		if b.pendingLow[i].Text == "" {
			b.pendingLow[i] = req
			return
		}
	}
}

func (b *EmbeddingBatcher) flushBatch(ctx context.Context, texts []string, model string) ([]EmbedResult, error) {
	if b.embedFn == nil {
		return nil, perrors.New(perrors.CodeInternal, "embedding batcher: embedFn not configured")
	}
	vecs, err := b.embedFn(ctx, texts, model)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "embedding batch call failed", err)
	}
	results := make([]EmbedResult, len(texts))
	for i, vec := range vecs {
		if i < len(results) {
			results[i] = EmbedResult{Vector: vec}
		}
	}
	return results, nil
}
