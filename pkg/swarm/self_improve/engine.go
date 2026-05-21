package self_improve

import (
	"context"
	"time"
)

// EvolutionLevel defines the scope of automated change. Higher = more gates, deeper rollback chain.
type EvolutionLevel int

const (
	L0ConfigAdjust       EvolutionLevel = iota // routing weights, timeout thresholds — auto
	L1PromptHeuristic                          // system prompt, routing criteria — auto
	L2SkillGeneration                          // Logic Collapse → new skill — semi-auto
	L3StrategyModify                           // agent behavior policy, LoRA adapter — approval req'd
	L4SourceArchitecture                       // system source code — multi-sig
)

// EvolutionGate verifies that a change at the given level passes all required checks.
type EvolutionGate interface {
	Approve(ctx context.Context, level EvolutionLevel, change *Change) error
}

type Change struct {
	Level       EvolutionLevel `json:"level"`
	Description string         `json:"description"`
	Patch       []byte         `json:"patch,omitempty"`
	Trajectory  []byte         `json:"trajectory,omitempty"`
	Signature   string         `json:"signature,omitempty"`
}

// FailureClass distinguishes uncontrollable infrastructure failures from logic errors.
// Values must match protocol.FailureClass: "logic", "controllable", "uncontrollable".
type FailureClass string

const (
	FailureLogic          FailureClass = "logic"          // incorrect reasoning, bad plan, skill error
	FailureControllable   FailureClass = "controllable"   // timeout, resource exhausted
	FailureUncontrollable FailureClass = "uncontrollable" // network offline, provider down, quota
)

// MEMF (Fallacy Memory Pool) stores failed trajectories for pruning.
type MEMF interface {
	Record(ctx context.Context, trajectory *FailureTrajectory) error
	Query(ctx context.Context, embedding []float64, threshold float64) ([]FailureTrajectory, error)
}

type FailureTrajectory struct {
	ID           string       `json:"id"`
	TaskType     string       `json:"task_type"`
	Embedding    []float64    `json:"embedding"`
	Error        string       `json:"error"`
	FailureClass FailureClass `json:"failure_class"`
	NodeQuality  float64      `json:"node_quality_score"`
}

// AutoCurriculum finds edge tasks during idle periods (Voyager style).
type AutoCurriculum interface {
	FindEdgeTask(ctx context.Context) (*Task, error)
	Execute(ctx context.Context, task *Task) (*TaskResult, error)
}

type Task struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	Embedding   []float64 `json:"embedding,omitempty"`
	Difficulty  float64   `json:"difficulty"`
}

type TaskResult struct {
	TaskID       string       `json:"task_id"`
	Success      bool         `json:"success"`
	FailureClass FailureClass `json:"failure_class,omitempty"`
	Output       []byte       `json:"output,omitempty"`
}

// IsUncontrollable returns true if the failure was due to infrastructure faults
// (network, provider, quota). Such failures must NOT update skill success-rate
// EWMA or trigger topology rollback.
func (r *TaskResult) IsUncontrollable() bool {
	return !r.Success && r.FailureClass == FailureUncontrollable
}

// =============================================================================
// Engine — M9 Self-Improvement Engine 主入口
// 架构文档: docs/arch/M09-Self-Improvement-Engine.md §2
//
// 三环架构:
//   内环（实时/小时）: 订阅任务完成事件 → ReflexionEngine → MEMF + HeuristicsMemory
//   中环（日/周）:    2min ticker → AutoCurriculumGenerator
//   外环（周/月）:    订阅版本变更 → ProgressiveRollout 门控推进
// =============================================================================

// TaskCompleteEvent 任务完成事件（由 Blackboard 事件总线推送）。
type TaskCompleteEvent struct {
	TaskID   string
	TaskType string
	Success  bool
	Failure  FailureClass
	Output   []byte
}

// VersionChangeEvent 版本变更事件（触发外环 Rollout 检查）。
type VersionChangeEvent struct {
	CandidateVersion string
	Stats            RolloutStats
}

// RolloutStats 外环统计数据（与 pkg/swarm 包的 RolloutStats 保持对齐）。
type RolloutStats struct {
	ErrorRate            float64
	BaselineErrorRate    float64
	P95Latency           float64
	BaselineP95Latency   float64
	SafetyViolations     int
	SurpriseIndexDegrade bool
}

// EngineConfig Engine 配置。
type EngineConfig struct {
	// InnerLoopInterval 内环轮询间隔（订阅模式时忽略）
	InnerLoopInterval time.Duration
	// MidLoopInterval 中环课程生成轮询间隔（默认 2min）
	MidLoopInterval time.Duration
	// MaxConcurrentReflections 并发反思上限（防止在高负载时积压）
	MaxConcurrentReflections int
}

// DefaultEngineConfig 返回默认配置。
func DefaultEngineConfig() *EngineConfig {
	return &EngineConfig{
		InnerLoopInterval:        0, // 事件驱动，不使用 ticker
		MidLoopInterval:          2 * time.Minute,
		MaxConcurrentReflections: 3,
	}
}

// Reflector 内环反思接口（由 pkg/swarm.ReflexionEngine 实现，通过接口解耦）。
type Reflector interface {
	Reflect(ctx context.Context, taskID, taskType string, result *TaskResult, trajectory []Step) (*Reflection, error)
}

// Reflection 反思结果（镜像 swarm.Reflection 以避免循环引用）。
type Reflection struct {
	TaskID             string `json:"task_id"`
	Cause              string `json:"cause"`
	Counterfactual     string `json:"counterfactual"`
	GeneratedHeuristic string `json:"generated_heuristic"`
	MEMFRecordID       string `json:"memf_record_id,omitempty"`
	CreatedAt          int64  `json:"created_at"`
}

// Step 任务轨迹步骤（镜像 swarm.Step）。
type Step struct {
	Index     int    `json:"index"`
	Action    string `json:"action"`
	Reasoning string `json:"reasoning"`
	Result    string `json:"result"`
	Success   bool   `json:"success"`
}

// CurriculumGenerator 中环课程生成接口。
type CurriculumGenerator interface {
	Generate(ctx context.Context, surpriseIndex float64) error
}

// RolloutAdvancer 外环门控推进接口。
type RolloutAdvancer interface {
	AdvanceGate(ctx context.Context, version string, stats RolloutStats) error
}

// Engine M9 Self-Improvement Engine 主结构。
// 所有依赖通过构造器注入，无全局状态。
type Engine struct {
	cfg        *EngineConfig
	reflector  Reflector
	curriculum CurriculumGenerator
	rollout    RolloutAdvancer

	// 事件通道（由外部订阅者推入，Engine 消费）
	taskEvents    <-chan TaskCompleteEvent
	versionEvents <-chan VersionChangeEvent

	// 反思并发信号量（控制 goroutine 数量）
	sem chan struct{}

	// Tier1+：从 M3 Metrics 读取实时 SurpriseIndex 的函数（nil 时用 0.5 占位）
	surpriseIndexFn func() float64
}

// SetSurpriseIndexProvider 注入 SurpriseIndex 读取函数（Tier1+ 从 M3 Metrics 读取）。
// fn 应读取 observability.GlobalSurpriseIndex 或类似实现。
func (e *Engine) SetSurpriseIndexProvider(fn func() float64) { e.surpriseIndexFn = fn }

func (e *Engine) currentSurpriseIndex() float64 {
	if e.surpriseIndexFn != nil {
		return e.surpriseIndexFn()
	}
	return 0.5
}

// NewEngine 创建 Engine 实例，所有依赖必须非 nil（fail-fast）。
func NewEngine(
	cfg *EngineConfig,
	reflector Reflector,
	curriculum CurriculumGenerator,
	rollout RolloutAdvancer,
	taskEvents <-chan TaskCompleteEvent,
	versionEvents <-chan VersionChangeEvent,
) *Engine {
	if cfg == nil {
		cfg = DefaultEngineConfig()
	}
	maxConcurrent := cfg.MaxConcurrentReflections
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	return &Engine{
		cfg:           cfg,
		reflector:     reflector,
		curriculum:    curriculum,
		rollout:       rollout,
		taskEvents:    taskEvents,
		versionEvents: versionEvents,
		sem:           make(chan struct{}, maxConcurrent),
	}
}

// Run 启动三环主循环，阻塞直到 ctx 取消。
// 内环：消费 taskEvents 频道，并发执行 Reflect（受信号量限制）
// 中环：2min ticker 触发 AutoCurriculumGenerator
// 外环：消费 versionEvents 频道，触发 Rollout AdvanceGate
func (e *Engine) Run(ctx context.Context) error {
	midTicker := time.NewTicker(e.cfg.MidLoopInterval)
	defer midTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		// 内环：任务完成事件 → Reflexion
		case ev, ok := <-e.taskEvents:
			if !ok {
				return nil
			}
			if !ev.Success {
				// 异步执行，受信号量并发限制
				select {
				case e.sem <- struct{}{}:
					go func(event TaskCompleteEvent) {
						defer func() { <-e.sem }()
						result := &TaskResult{
							TaskID:       event.TaskID,
							Success:      event.Success,
							FailureClass: event.Failure,
							Output:       event.Output,
						}
						_, _ = e.reflector.Reflect(ctx, event.TaskID, event.TaskType, result, nil)
					}(ev)
				default:
					// 信号量满，丢弃（后台任务以尽力而为原则运行）
				}
			}

		// 中环：定时触发 AutoCurriculum
		case <-midTicker.C:
			if e.curriculum != nil {
				go func() {
					_ = e.curriculum.Generate(ctx, e.currentSurpriseIndex())
				}()
			}

		// 外环：版本变更 → Rollout 门控推进
		case ev, ok := <-e.versionEvents:
			if !ok {
				return nil
			}
			if e.rollout != nil {
				go func(event VersionChangeEvent) {
					_ = e.rollout.AdvanceGate(ctx, event.CandidateVersion, event.Stats)
				}(ev)
			}
		}
	}
}
