# 01 Go 编码规范

> 本项目的 Go 代码模式约定。不涉及通用 Go 语言知识（如 defer 语义），只涉及本项目独有的风格和结构要求。

## F1 文件结构

每个 `.go` 文件按固定顺序排列（从已有文件实证）：

```
1. // Package doc（只有 doc.go 才写包级注释，其余文件不重复）
2. package xxx
3. import (标准库 → 第三方 → internal/)
4. const (常量块)
5. type (类型定义)
6. type 结构体定义
7. func NewXxx (构造函数)
8. func (接收者) 公开方法
9. func (接收者) 私有方法 (按被调用的顺序排列)
10. // 辅助纯函数、类型转换函数
```

参考 `pkg/swarm/orchestrator.go`、`pkg/substrate/policy/factuality_guard.go`。

例外：`xxx_test.go` 中测试函数按被测对象分组。`doc.go` 只包含包级 markdown 注释。

## F2 接口定义

接口在消费方定义（consumer-side），注解式注明生产者和消费方：

```go
// @consumer: M5(Memory System - 四层记忆物理存储)
// @producer: pkg/substrate/storage/(SQLite / SurrealDB-Core 引擎适配器)
// @arch: docs/arch/M02-Storage-Fabric.md §1.1
type Store interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
}
```

- `@consumer`：调用该接口的模块
- `@producer`：实现该接口的模块  
- `@arch`：关联的架构文档位置

消费方定义在 `internal/protocol/interfaces.go`。实现方不暴露出接口定义，只暴露结构体和构造函数。

## F3 错误处理

- 唯一错误类型：`internal/errors/errors.go` 的 `PolarisError`
- 禁止裸 `error`、`fmt.Errorf`、`errors.New` 在业务代码中
- 错误传播：`return perrors.Wrap(CodeInternal, "用户可读的描述", err)`
- 代码常量：`CodeInternal`, `CodeNotFound`, `CodePermission`, `CodeUnavailable`
- 降级路径：`if err != nil { log.Warn("降级描述"); fallback() }`，不 panic

参考 `pkg/edge/scheduler/scheduler.go:46` 的 `perrors.Wrap` 用法。

## F4 并发模式

场景对照表：

| 场景 | 模式 | 示例 |
|------|------|------|
| 保护结构体字段 | `sync.Mutex` 嵌入结构体 | `Orchestrator.mu` |
| 多读少写 | `sync.RWMutex` | — |
| 阻塞等待 | `sync.Cond` + 独立 goroutine 监听 ctx | `ResourceGovernor.cond` |
| 事件驱动 | channel, select, goroutine | `Engine.Run` → chan `TaskCompleteEvent` |
| 任务并发上限 | buffered chan 作为 semaphore | `self_improve/engine.go` `sem chan struct{}` |
| 定时触发 | `time.NewTicker` + `select` | `midTicker := time.NewTicker(2min)` |

信号量用法模板：
```go
sem := make(chan struct{}, maxConcurrent)
select {
case sem <- struct{}{}:
    go func() {
        defer func() { <-sem }()
        // 实际工作
    }()
default:
    // 信号量满，降级（非阻塞丢弃）
}
```

## F5 构造函数模式

- `NewXxx(cfg, deps...) *Xxx`，所有依赖必须是构造参数
- 不允许 `SetXxx` 后置注入——`InjectLLMProvider` 作为 Tier1+ 可选注入的特例被允许
- 不允许 `init()` 函数在 `pkg/` 下

## F6 导入顺序

三块，空行分隔：

```go
import (
    "context"
    "time"

    "github.com/xxx/yyy"

    "github.com/mrlaoliai/polaris-harness/internal/protocol"
    perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)
```

## F7 可测试性

- 接口消费方不关心实现细节 → 测试中可 mock
- 测试文件与被测文件同级同包
- 表驱动测试（subscription-based test table）
- 私有函数通过公开接口间接测试，不对私有函数直接写 tests——除非逻辑复杂需要直接单元测试
