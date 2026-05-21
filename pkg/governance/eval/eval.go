package eval

import "context"

// Evaluator represents one level in the evaluation pyramid.
type EvaluatorLevel int

const (
	Level1Assert     EvaluatorLevel = iota // deterministic string/regex check
	Level2Schema                           // JSON schema validation
	Level3Trajectory                       // tool call sequence matching
	Level4LLMJudge                         // semantic quality assessment
	Level5Human                            // calibration only
)

// EvalCase is a single evaluation scenario.
type EvalCase struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Input       map[string]any `json:"input"`
	Expected    map[string]any `json:"expected"`
	Level       EvaluatorLevel `json:"level"`
	Severity    Severity       `json:"severity"`
	Tags        []string       `json:"tags,omitempty"`
}

type Severity string

const (
	SeverityP0 Severity = "P0" // block merge
	SeverityP1 Severity = "P1" // warn
	SeverityP2 Severity = "P2" // record only
)

type EvalResult struct {
	CaseID   string `json:"case_id"`
	Passed   bool   `json:"passed"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_ms"`
}

// Runner executes the evaluation suite.
type Runner interface {
	Run(ctx context.Context, cases []EvalCase) []EvalResult
}

// TrajectoryRecorder captures a full agent execution trace for replay.
type TrajectoryRecorder interface {
	Record(ctx context.Context, sessionID string) (*TrajectoryTrace, error)
}

// TrajectoryReplayer replays a recorded trace deterministically, zero LLM calls.
type TrajectoryReplayer interface {
	Replay(ctx context.Context, trace *TrajectoryTrace) (*EvalResult, error)
}

type TrajectoryTrace struct {
	SessionID  string             `json:"session_id"`
	LLMCalls   []LLMCallRecord    `json:"llm_calls"`
	ToolCalls  []ToolCallRecord   `json:"tool_calls"`
	StateTrans []StateTransRecord `json:"state_transitions"`
}

type LLMCallRecord struct {
	Request  map[string]any `json:"request"`
	Response map[string]any `json:"response"`
}

type ToolCallRecord struct {
	Name   string         `json:"name"`
	Input  map[string]any `json:"input"`
	Output map[string]any `json:"output"`
}

type StateTransRecord struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Event string `json:"event"`
}

// RegressionDetector compares current metrics against the 30-day rolling baseline.
type RegressionDetector struct{}

func (rd *RegressionDetector) Check(baseline, current *RunMetrics) *RegressionAlert {
	return nil
}

type RunMetrics struct {
	TaskSuccessRate float64 `json:"task_success_rate"`
	TokenBurnRate   float64 `json:"token_burn_rate"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`
}

type RegressionAlert struct {
	Metric    string  `json:"metric"`
	Baseline  float64 `json:"baseline"`
	Current   float64 `json:"current"`
	Threshold float64 `json:"threshold"`
}
