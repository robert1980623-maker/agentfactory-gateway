# AgentFactory Gateway — 开发指南

> **目标读者**: 开发 Go Gateway 的 Agent 或工程师
> **最后更新**: 2026-05-26

---

## 1. 项目结构

```
agentfactory-gateway/
├── main.go                     # 入口 — 配置加载、SQLite 初始化、Crash Recovery、信号处理
├── env_loader.go               # .env 文件加载器
├── go.mod
│
├── config/
│   ├── config.go               # 配置结构体 + 验证
│   └── config_test.go
│
├── protocol/
│   ├── protocol.go             # 协议定义: TaskRequest, SlackEvent, EventPayload
│   └── protocol_test.go
│
├── state/
│   ├── interface.go            # StateManager 接口定义
│   ├── state_manager.go        # JSON 实现 (旧, 保留用于测试)
│   ├── state_manager_test.go
│   ├── sqlite_store.go         # SQLite 实现 (当前生产使用)
│   └── sqlite_store_test.go
│
├── worker/
│   ├── stream_worker.go        # JSONL StreamWorker — 启动 Python/Cline, 解析事件
│   ├── stream_worker_test.go
│   ├── python_worker.go        # 非流式 Python Worker (fallback)
│   ├── python_worker_test.go
│   ├── cline_adapter.go        # Cline CLI 输出适配器
│   ├── cline_adapter_test.go
│   ├── throttler.go            # MessageThrottler — 1s 防抖
│   └── throttler_test.go
│
├── gateway/
│   ├── slack.go                # Slack Socket Mode 适配器 (主入口)
│   ├── renderer.go             # Block Kit UI 渲染
│   ├── renderer_test.go
│   ├── task_queue.go           # 任务队列 — FIFO + 并发控制
│   ├── task_queue_test.go
│   ├── queued_task.go          # 任务状态机
│   ├── recovery.go             # Crash Recovery
│   ├── recovery_integration_test.go
│   ├── dispatch_test.go        # Dispatch 模式测试套件
│   ├── metrics.go              # 指标追踪
│   └── interaction_handler.go  # HITL 交互处理
│
├── tests/
│   ├── test_helper.go          # Mock helpers (MockSlackClient, MockStatusChecker)
│   └── integration_test.go     # 集成测试 (10 cases)
│
└── .env                        # 环境变量 (已 gitignore)
```

---

## 2. 启动流程

```
main()
  │
  ├─ LoadConfig()              // 加载配置 + .env
  ├─ NewSQLiteStore()          // 初始化 SQLite 状态存储
  ├─ RecoverActiveTasks()      // Crash Recovery: 扫描 running 任务
  ├─ NewTaskQueue()            // 初始化任务队列 (global=5, per-channel=1)
  ├─ NewSlackGateway()         // 初始化 Slack 适配器
  ├─ signal.Notify()           // 注册 SIGTERM/SIGINT 信号
  └─ slackSocketMode.Start()   // 启动 Socket Mode 连接
```

### 信号处理

```
SIGTERM/SIGINT
  │
  ├─ Phase 1: 等待 Worker 排空 (10s timeout)
  │   └─ TaskQueue.Stop() → 停止接收新任务, 等待运行中任务完成
  ├─ Phase 2: 关闭 SQLite 连接
  │   └─ SQLiteStore.Close()
  └─ 进程退出
```

---

## 3. 核心模块

### 3.1 Slack Gateway (`gateway/slack.go`)

**职责**: Slack Socket Mode 事件处理

**关键函数**:

| 函数 | 用途 |
|------|------|
| `handleMentionStream()` | 处理 @bot 提及, 路由到 StreamWorker |
| `handleBlockAction()` | 处理 Block Kit 交互按钮 (HITL) |
| `postMessage()` | 发送新消息 |
| `updateMessage()` | 更新现有消息 (进度更新) |
| `postError()` | 发送错误消息 |

**Dispatch 路由**:

```go
taskText := event.Text
dispatch := isDispatchTask(taskText)  // 检测 /dispatch 前缀
if dispatch {
    taskText = stripDispatchPrefix(taskText)
}

req := protocol.TaskRequest{
    Task:     taskText,
    Dispatch: dispatch,
}

if req.Dispatch {
    execErr = g.streamWorker.ExecuteDispatch(req, cb)
} else {
    execErr = g.streamWorker.Execute(req, cb)
}
```

### 3.2 StreamWorker (`worker/stream_worker.go`)

**职责**: 启动 Python/Cline 子进程, 解析 JSONL 输出, 回调事件

**关键方法**:

| 方法 | 用途 |
|------|------|
| `Execute()` | 根据 task_type 选择 Python 或 Cline |
| `ExecuteDispatch()` | Dispatch 模式 — 设置 dispatch 标志, 发送 start 事件 |
| `executePython()` | 启动 Python worker, 逐行解析 JSONL |
| `executeCline()` | 启动 Cline CLI, 适配文本输出为事件 |

**事件流**:

```
Go StreamWorker                    Python worker.py
     │                                   │
     │  JSON TaskRequest (stdin)         │
     ├──────────────────────────────────>│
     │                                   │
     │  JSONL Events (stdout)            │
     │<──────────────────────────────────┤
     │  {"type":"start", ...}            │
     │  {"type":"progress", ...}         │
     │  {"type":"done", ...}             │
     │  {"type":"error", ...}            │
     │                                   │
     ▼                                   ▼
  回调 cb(event)                     执行完成
```

### 3.3 TaskQueue (`gateway/task_queue.go`)

**职责**: 任务排队 + 并发控制

**限制**:
- 全局最大并发: 5
- 每通道最大并发: 1 (同一 Slack 频道串行)
- 队列策略: FIFO

**状态机**:

```
queued → dispatching → running → done
                         │
                         └→ error
```

### 3.4 StateManager (`state/`)

**接口**: `state/interface.go`

```go
type StateManager interface {
    Set(record TaskRecord) error
    Get(taskID string) (*TaskRecord, error)
    ListActive() ([]TaskRecord, error)
    HasActiveTask(channelID string) bool
    GetByChannel(channelID string) (*TaskRecord, error)
    Close() error
}
```

**当前实现**: `SQLiteStore` (`state/sqlite_store.go`)

### 3.5 Crash Recovery (`gateway/recovery.go`)

**流程**:

```
RecoverActiveTasks()
  │
  ├─ ListActive() → 获取所有 running/paused 任务
  │
  ├─ 对每个任务:
  │   ├─ CheckStatus(task_id) → 查询 Python worker 状态
  │   ├─ 如果已完成 → 更新 Slack 卡片 + SQLite 状态
  │   ├─ 如果失败 → 标记 error + 通知用户
  │   └─ 如果仍在运行 → 保持 running (Worker 还活着)
  │
  └─ 恢复完成, Gateway 正常启动
```

### 3.6 Block Kit 渲染 (`gateway/renderer.go`)

**函数**:

| 函数 | 用途 |
|------|------|
| `BuildTaskCard()` | 根据事件类型构建完整卡片 |
| `buildDispatchBlocks()` | Dispatch 模式的 Agent 网格 |
| `buildAgentGrid()` | 多 Agent 状态展示 |
| `buildAgentSummary()` | Agent 结果摘要 |
| `BuildQueuedCard()` | 排队状态卡片 |
| `buildHeader()` | 卡片头部 (任务类型 + 状态) |
| `buildProgressBar()` | 进度条 `[████░░] 50%` |
| `buildFooter()` | 底部元数据 (模型、Token、耗时) |
| `buildHITLButtons()` | HITL 审核按钮 |

---

## 4. 通信协议

### 4.1 Go → Python (JSON Request)

```json
{
  "task": "build the API",
  "task_type": "internal",
  "dispatch": true,
  "context": {
    "task_id": "t-001",
    "channel_id": "C123",
    "dispatch_mode": true
  }
}
```

### 4.2 Python → Go (JSONL Events)

```jsonl
{"type": "start", "payload": {"task_id": "t-001", "task_type": "dispatch", "total_agents": 3}}
{"type": "progress", "payload": {"task_id": "t-001", "agents": [{"agent_id": "step_0", "role": "developer", "progress": 0.5, "status": "running"}]}}
{"type": "done", "payload": {"task_id": "t-001", "output": "[developer] API built successfully"}}
```

### 4.3 事件类型完整定义

```go
const (
    EventTypeStart    SlackEventType = "start"
    EventTypeProgress SlackEventType = "progress"
    EventTypeDone     SlackEventType = "done"
    EventTypeError    SlackEventType = "error"
    EventTypeToolCall SlackEventType = "tool_call"
    EventTypePaused   SlackEventType = "paused"
)
```

---

## 5. 测试规范

### 5.1 运行测试

```bash
cd ~/projects/agentfactory-gateway
GOPROXY=https://goproxy.cn,direct go test ./...         # 全量
GOPROXY=https://goproxy.cn,direct go test ./gateway -v   # 仅 gateway
GOPROXY=https://goproxy.cn,direct go test ./worker -v    # 仅 worker
GOPROXY=https://goproxy.cn,direct go test ./state -v     # 仅 state
GOPROXY=https://goproxy.cn,direct go test ./... -run Dispatch  # 仅 dispatch
```

### 5.2 Mock Helpers

`tests/test_helper.go` 提供：

- `MockSlackClient` — 模拟 Slack API 调用
- `MockStatusChecker` — 模拟任务状态查询
- `NewTestStateManager()` — 创建隔离的测试用 StateManager

### 5.3 测试分层

| 层级 | 位置 | 范围 |
|------|------|------|
| Unit | `*_test.go` (同级目录) | 单个函数/结构体 |
| Integration | `tests/integration_test.go` | 模块间交互 |
| E2E | `tests/recovery_integration_test.go` | 完整恢复流程 |

---

## 6. 关键设计决策

### 6.1 SQLite 选择 `modernc.org/sqlite`

- **纯 Go 实现**, 无 CGO 依赖
- 交叉编译简单 (macOS → Linux 无需特殊配置)
- 兼容 Go 1.22+

### 6.2 StateManager 接口抽象

```
StateManager (interface)
    ├── JSONStateManager (旧, 测试用)
    └── SQLiteStore (当前生产)
```

Gateway 层只依赖接口，切换实现无需修改业务代码。

### 6.3 Go ↔ Python 通信

- **JSON over stdio**: Go 通过 stdin 发送 JSON 请求, Python 通过 stdout 输出 JSONL 事件
- **无网络依赖**: 进程间直接通信
- **stderr 独立**: Python 日志走 stderr, 不影响 JSONL 解析

### 6.4 消息防抖

`MessageThrottler` 将 progress 事件限制为 **1 秒/次**, 避免 Slack API 限流。

### 6.5 Dispatch 并发策略

- Go 启动**一个** Python 子进程
- Python 内部用 `ThreadPoolExecutor` 并行执行多个 Agent 步骤
- Go 侧不管理单个 Agent 的并发

---

## 7. 常见陷阱

| 陷阱 | 后果 | 解决方案 |
|------|------|----------|
| worker.py 路径错误 | `No such file or directory` | 确保 `Script` 字段指向正确路径 |
| SQLite 锁竞争 | `database is locked` | 使用 WAL mode, 写操作加锁 |
| stderr 未 drain | 子进程阻塞 | `go func() { io.Copy(os.Stderr, stderr) }()` 提前启动 |
| Throttler 未 Flush | 最后一个 progress 丢失 | `defer throttler.Flush()` |
| TaskQueue 状态不一致 | 任务卡在 running | 使用状态机 + SQLite 持久化 |
| .env 文件提交到 Git | GitHub secret 扫描拦截 | 已加入 .gitignore, 历史用 filter-branch 清理 |

---

## 8. 环境变量

### .env 文件

```env
SLACK_BOT_TOKEN=xoxb-...
SLACK_APP_TOKEN=xapp-...
```

### 加载顺序

1. 系统环境变量 (最高优先级)
2. `.env` 文件
3. `config.yaml` 默认值

---

## 9. 构建与部署

### 本地构建

```bash
cd ~/projects/agentfactory-gateway
GOPROXY=https://goproxy.cn,direct go build -o gateway ./main.go
```

### 交叉编译 (macOS → Linux)

```bash
GOOS=linux GOARCH=amd64 GOPROXY=https://goproxy.cn,direct go build -o gateway-linux ./main.go
```

### 运行

```bash
./gateway
# 或
go run .
```

---

## 10. Git 提交规范

遵循 Conventional Commits：

```
feat(gateway): add dispatch mode support
fix(state): handle SQLite lock contention
test(worker): add JSONL stream parsing tests
docs: update architecture diagram
```
