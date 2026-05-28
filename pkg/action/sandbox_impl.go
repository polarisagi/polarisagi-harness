package action

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// SandboxProvider 是沙箱执行抽象接口，允许对 Wasm/InProcess/Container 分别实现。
// 架构文档: docs/arch/M07-Tool-Action-Layer.md §4.2
type SandboxProvider interface {
	// Run 执行工具并返回结果。spec 描述执行约束。
	Run(ctx context.Context, spec SandboxSpec) (*protocol.ToolResult, error)
}

// SandboxSpec 描述一次沙箱执行的完整规格。
type SandboxSpec struct {
	ToolName     string
	Input        []byte
	SandboxTier  protocol.SandboxTier
	Capability   protocol.CapabilityLevel
	SideEffects  []protocol.SideEffect
	WasmPath     string   // SandboxTier=Wasm 时必填
	WasmBytes    []byte   // 直接传入的 Wasm 字节码 (主要用于测试或直接下发)
	AllowedPaths []string // 文件系统白名单
	CPUQuotaMs   int      // 0 = 默认 5000ms
	IOBudget     int64    // 0 = 默认 8MB
	MaxCalls     int      // 0 = 默认 10000
}

// ─── Tier 1: InProcessSandbox ────────────────────────────────────────────────

// InProcessSandbox 在调用方 goroutine 内直接执行内置工具函数。
// 适用于: protocol.ToolBuiltin + protocol.CapReadOnly
// 安全约束: 无文件写、无网络——由 PolicyGate 在调用前验证，此处不再重复校验。
type InProcessSandbox struct {
	mu       sync.RWMutex
	registry map[string]InProcessFn
	// taintMap 存储每个工具的输出污点等级。
	// 内置工具保持 TaintNone（零值），MCP/外部工具通过 RegisterWithTaint 写入。
	taintMap map[string]protocol.TaintLevel
}

// InProcessFn 内置工具执行函数签名。
type InProcessFn func(ctx context.Context, input []byte) ([]byte, error)

func NewInProcessSandbox() *InProcessSandbox {
	return &InProcessSandbox{
		registry: make(map[string]InProcessFn),
		taintMap: make(map[string]protocol.TaintLevel),
	}
}

// Register 注册工具函数（并发安全，支持运行时动态注册 MCP 工具）。
// 内置工具使用此方法，输出污点为 TaintNone。
func (s *InProcessSandbox) Register(toolName string, fn InProcessFn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry[toolName] = fn
}

// RegisterWithTaint 注册工具函数并指定输出污点等级。
// MCP/外部工具调用此方法：白名单 → TaintMedium，其余 → TaintHigh。
func (s *InProcessSandbox) RegisterWithTaint(toolName string, fn InProcessFn, taint protocol.TaintLevel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry[toolName] = fn
	s.taintMap[toolName] = taint
}

// Unregister 取消注册工具（MCP Server 断开时调用）。
func (s *InProcessSandbox) Unregister(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registry, toolName)
	delete(s.taintMap, toolName)
}

func (s *InProcessSandbox) Run(ctx context.Context, spec SandboxSpec) (*protocol.ToolResult, error) {
	s.mu.RLock()
	fn, ok := s.registry[spec.ToolName]
	taint := s.taintMap[spec.ToolName] // TaintNone(0) for builtins
	s.mu.RUnlock()
	if !ok {
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("inprocess_sandbox: unknown tool %q", spec.ToolName))
	}

	quotaMs := spec.CPUQuotaMs
	if quotaMs == 0 {
		quotaMs = 5000
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(quotaMs)*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := fn(execCtx, spec.Input)
	if err != nil {
		return &protocol.ToolResult{
			Success:    false,
			Error:      err.Error(),
			LatencyMs:  time.Since(start).Milliseconds(),
			TaintLevel: taint,
		}, nil
	}
	return &protocol.ToolResult{
		Success:    true,
		Output:     out,
		LatencyMs:  time.Since(start).Milliseconds(),
		TaintLevel: taint,
	}, nil
}

// Execute 满足 tool.SandboxExecutor 接口（简化版，无 SandboxSpec 包装），
// 允许 InProcessSandbox 直接作为 InMemoryToolRegistry 的执行后端。
func (s *InProcessSandbox) Execute(ctx context.Context, toolName string, input []byte) ([]byte, error) {
	s.mu.RLock()
	fn, ok := s.registry[toolName]
	s.mu.RUnlock()
	if !ok {
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("inprocess_sandbox: unknown tool %q", toolName))
	}
	return fn(ctx, input)
}

// ─── Tier 2: WasmSandbox ─────────────────────────────────────────────────────

// WasmSandbox 通过 wazero 执行 Wasm 二进制。
// MVP: wazero 实体由 Rust 编译生成，目前以 stub 验证集成路径。
// 适用于: protocol.ToolMCP / ToolA2A / ToolLLMGenerated
// 架构文档: docs/arch/M07-Tool-Action-Layer.md §4.3
type WasmSandbox struct {
	runtime *WazeroRuntime
}

func NewWasmSandbox(ctx context.Context) *WasmSandbox {
	return &WasmSandbox{
		runtime: NewWazeroRuntime(ctx),
	}
}

func (s *WasmSandbox) PreWarmCache(skillID string, wasmBytes []byte) error {
	return s.runtime.PreWarmCache(skillID, wasmBytes)
}

func (s *WasmSandbox) Run(ctx context.Context, spec SandboxSpec) (*protocol.ToolResult, error) {
	quotaMs := spec.CPUQuotaMs
	if quotaMs == 0 {
		quotaMs = 5000
	}
	ioBudget := spec.IOBudget
	if ioBudget == 0 {
		ioBudget = 8 * 1024 * 1024 // 8MB
	}
	maxCalls := spec.MaxCalls
	if maxCalls == 0 {
		maxCalls = 10000
	}

	config := &ExecuteConfig{
		Capability:     int(spec.Capability),
		SandboxTier:    int(spec.SandboxTier),
		CPUQuotaMs:     quotaMs,
		WallClockLimit: quotaMs * 3,
		IOBudgetBytes:  ioBudget,
		MaxHostCall:    maxCalls,
		AllowedPaths:   spec.AllowedPaths,
		WasmBytes:      spec.WasmBytes,
	}

	return s.runtime.RunWasm(ctx, spec.ToolName, spec.Input, config, protocol.TaintNone)
}

// ─── Tier 3: ContainerSandbox ────────────────────────────────────────────────

// ContainerSandbox 通过 OS 子进程（未来集成 gVisor/Docker）执行特权工具。
// 当前 MVP 实现：通过 exec.Command 在限制环境中执行二进制。
// 适用于: protocol.CapPrivileged + protocol.SideProcessSpawn
// 约束: Tier 0 (8GB Linux) + 非 Linux 回退至 WasmSandbox
type ContainerSandbox struct {
	binPath string // 沙箱执行器二进制路径（如 /usr/local/bin/polaris-sandbox）
}

func NewContainerSandbox(binPath string) *ContainerSandbox {
	return &ContainerSandbox{binPath: binPath}
}

func (s *ContainerSandbox) Run(ctx context.Context, spec SandboxSpec) (*protocol.ToolResult, error) {
	quotaMs := spec.CPUQuotaMs
	if quotaMs == 0 {
		quotaMs = 30000
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(quotaMs)*time.Millisecond)
	defer cancel()

	// MVP: 直接调用系统沙箱二进制（未来替换为 gVisor Runtime）
	cmd := exec.CommandContext(execCtx, s.binPath, "--tool", spec.ToolName)
	cmd.Stdin = bytes2ReadCloser(spec.Input)
	// Linux: 注入命名空间隔离属性（CLONE_NEWPID|CLONE_NEWNS + Pdeathsig）
	if attrs := containerSandboxSysProcAttr(); attrs != nil {
		cmd.SysProcAttr = attrs
	}

	start := time.Now()
	out, err := cmd.Output()
	if err != nil {
		return &protocol.ToolResult{
			Success:   false,
			Error:     err.Error(),
			LatencyMs: time.Since(start).Milliseconds(),
		}, nil
	}
	return &protocol.ToolResult{
		Success:   true,
		Output:    out,
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

// ─── SandboxRouter ────────────────────────────────────────────────────────────

// SandboxRouter 根据 SandboxSpec.SandboxTier 路由至对应沙箱实现。
// 架构文档: docs/arch/M07-Tool-Action-Layer.md §4.2 三层矩阵
type SandboxRouter struct {
	inProcess *InProcessSandbox
	wasm      *WasmSandbox
	container *ContainerSandbox
	remote    *RemoteSandbox // L4：可选，Tier-0 OOM 逃生路径
	goos      string         // "darwin" | "linux" | "windows"
	hwTier    int            // 0 = Tier 0 (8GB) 主线
}

func NewSandboxRouter(inProcess *InProcessSandbox, wasm *WasmSandbox, container *ContainerSandbox, goos string, hwTier int) *SandboxRouter {
	return &SandboxRouter{
		inProcess: inProcess,
		wasm:      wasm,
		container: container,
		goos:      goos,
		hwTier:    hwTier,
	}
}

// WithRemote 注入 Remote Sandbox（可选）。返回自身，支持链式调用。
// 配置后，SandboxRemote 层级工具和 Tier-0 非 Linux 下 SandboxContainer 的降级均路由至此。
func (r *SandboxRouter) WithRemote(remote *RemoteSandbox) *SandboxRouter {
	r.remote = remote
	return r
}

// Route 根据工具属性选择最合适的沙箱，返回 SandboxProvider。
// 规则与 AssignSandboxTier 保持一致。
func (r *SandboxRouter) Route(tool protocol.Tool) SandboxProvider {
	tier := AssignSandboxTier(tool, r.hwTier, r.goos)
	switch tier {
	case protocol.SandboxRemote:
		if r.remote != nil {
			return r.remote
		}
		// 未配置远端，降级到 container/wasm/inProcess 链
		fallthrough
	case protocol.SandboxContainer:
		if r.container != nil {
			return r.container
		}
		// Tier-0 非 Linux 无 L3：优先走 Remote（逃生路径），再降 L2
		if r.remote != nil {
			return r.remote
		}
		if r.wasm != nil {
			return r.wasm
		}
		return r.inProcess
	case protocol.SandboxWasm:
		if r.wasm != nil {
			return r.wasm
		}
		// L2Sandbox 被 FeatureGate 禁用时降级到 in-process
		return r.inProcess
	default: // SandboxInProcess
		return r.inProcess
	}
}

// Execute 完整执行路径：Route → Run → ToolResult。
func (r *SandboxRouter) Execute(ctx context.Context, tool protocol.Tool, input []byte) (*protocol.ToolResult, error) {
	provider := r.Route(tool)
	spec := SandboxSpec{
		ToolName:    tool.Name,
		Input:       input,
		SandboxTier: tool.SandboxTier,
		Capability:  tool.Capability,
		SideEffects: tool.SideEffects,
		CPUQuotaMs:  int(tool.Timeout.Milliseconds()),
	}
	return provider.Run(ctx, spec)
}

// ─── 工具函数 ─────────────────────────────────────────────────────────────────

// bytes2ReadCloser 将 []byte 封装为 io.ReadCloser（供 ContainerSandbox stdin 使用）。
func bytes2ReadCloser(b []byte) *noopReadCloser {
	return &noopReadCloser{data: b, pos: 0}
}

type noopReadCloser struct {
	data []byte
	pos  int
}

func (r *noopReadCloser) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, perrors.New(perrors.CodeInternal, "EOF")
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *noopReadCloser) Close() error { return nil }
