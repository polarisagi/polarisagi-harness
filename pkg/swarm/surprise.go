package swarm

import "sync"

// SurpriseIndex — System 1/2 路由信号计算。
// 权威实现位于 M9（因依赖 MEMF），M3 仅暴露 Prometheus Gauge。
// 架构文档: docs/arch/09-Self-Improvement-Engine-深度选型.md §2.0

type SurpriseIndex struct {
	EmbeddingSurprise    float64
	ToolSequenceSurprise float64
	MEMFMatchSurprise    float64
}

// Compute 计算 SurpriseIndex = 0.4*embedding + 0.35*toolSeq + 0.25*MEMF.
func (si *SurpriseIndex) Compute() float64 {
	return 0.4*si.EmbeddingSurprise +
		0.35*si.ToolSequenceSurprise +
		0.25*si.MEMFMatchSurprise
}

// Route 根据 SurpriseIndex 选择执行路径。
func Route(si float64) int {
	switch {
	case si < 0.3:
		return 1
	case si < 0.6:
		return 15
	default:
		return 2
	}
}

// SurpriseCalculator 异步计算器 (BoundedWorkQueue + LoadShedder)
type SurpriseCalculator struct {
	queue        chan *CalcRequest
	memfPool     *FallacyMemoryPool
	rollingAvg   float64 // 滑动平均 SurpriseIndex
	rollingCount int64
	mu           sync.Mutex // 保护 rollingAvg/rollingCount
}

type CalcRequest struct {
	TaskID   string
	TaskType string
	Keywords []string // Embedding 的替代
	ToolSeq  []string // 工具序列
	ResultCh chan float64
}

func NewSurpriseCalculator(memf *FallacyMemoryPool) *SurpriseCalculator {
	c := &SurpriseCalculator{
		queue:    make(chan *CalcRequest, 256), // cap=256
		memfPool: memf,
	}
	// 启动固定 4 个 worker
	for i := 0; i < 4; i++ {
		go c.workerLoop()
	}
	return c
}

// Submit 提交计算任务。如果队列满，执行丢弃降载（LoadShedding）。
func (c *SurpriseCalculator) Submit(req *CalcRequest) bool {
	select {
	case c.queue <- req:
		return true
	default:
		// Queue full -> LoadShedder
		// 这里简单直接丢弃，让上层回退 safe default
		return false
	}
}

// CurrentSurprise 返回滑动平均 SurpriseIndex，实现 SurpriseReader 接口。
// 无历史数据时返回默认值 0.5。
func (c *SurpriseCalculator) CurrentSurprise() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rollingCount == 0 {
		return 0.5
	}
	return c.rollingAvg
}

func (c *SurpriseCalculator) workerLoop() {
	for req := range c.queue {
		embSurprise := 0.2
		if len(req.Keywords) == 0 {
			embSurprise = 0.8
		}

		toolSurprise := 0.1
		for _, t := range req.ToolSeq {
			if t == "bash" || t == "computer_use" {
				toolSurprise += 0.3
			}
		}
		if toolSurprise > 1.0 {
			toolSurprise = 1.0
		}

		memfSurprise := 0.1
		if c.memfPool != nil && c.memfPool.db != nil {
			var maxQuality float64
			err := c.memfPool.db.QueryRow(`
				SELECT MAX(node_quality_score) 
				FROM fallacy_records 
				WHERE task_type = ? 
			`, req.TaskType).Scan(&maxQuality)
			if err == nil && maxQuality > 0 {
				memfSurprise += maxQuality * 0.5
			}
		}
		if memfSurprise > 1.0 {
			memfSurprise = 1.0
		}

		idx := &SurpriseIndex{
			EmbeddingSurprise:    embSurprise,
			ToolSequenceSurprise: toolSurprise,
			MEMFMatchSurprise:    memfSurprise,
		}
		result := idx.Compute()

		// 维护滑动平均（EWMA α=0.2），mutex 保护并发读写
		c.mu.Lock()
		if c.rollingCount == 0 {
			c.rollingAvg = result
		} else {
			c.rollingAvg = 0.8*c.rollingAvg + 0.2*result
		}
		c.rollingCount++
		c.mu.Unlock()

		select {
		case req.ResultCh <- result:
		default:
		}
	}
}
