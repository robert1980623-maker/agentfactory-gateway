# AgentFactory Gateway — 架构文档

> **目标读者**: 需要理解 Gateway 架构设计的 Agent 或工程师
> **最后更新**: 2026-05-26

---

## 1. 设计目标

1. **薄代理**: Gateway 只做协议转换和事件路由, 不做业务逻辑
2. **高可用**: Crash Recovery + Graceful Shutdown
3. **可扩展**: 插件化 Adapter 模式 (Slack/飞书/CLI)
4. **可观测**: Metrics + 结构化日志 + 状态持久化

---

## 2. 整体架构

```
┌──────────────────────────────────────────────────────────────────┐
│                        External Platforms                        │
│   Slack (Socket Mode)    │    Feishu (未来)    │    CLI (未来)   │
└────────────────┬─────────┴──────────┬──────────┴────────┬────────┘
                 │                    │                   │
                 ▼                    ▼                   ▼
┌──────────────────────────────────────────────────────────────────┐
│                     Gateway Layer (Go)                           │
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │ SlackAdapter│  │ Future Adpt │  │ Future Adapter          │  │
│  │             │  │             │  │                         │  │
│  │ - Socket    │  │ - HTTP      │  │ - stdin/stdout          │  │
│  │   Mode      │  │ - Webhook   │  │ - interactive           │  │
│  │             │  │             │  │                         │  │
│  └──────┬──────┘  └──────┬──────┘  └────────────┬────────────┘  │
│         │                │                      │               │
│         └────────────────┼──────────────────────┘               │
│                          ▼                                      │
│              ┌───────────────────────┐                          │
│              │    Message Router     │                          │
│              │                       │                          │
│              │ - mention detection   │                          │
│              │ - dispatch detection  │                          │
│              │ - HITL action routing │                          │
│              └───────────┬───────────┘                          │
│                          │                                      │
│              ┌───────────▼───────────┐                          │
│              │     TaskQueue         │                          │
│              │                       │                          │
│              │ - global cap: 5       │                          │
│              │ - per-channel: 1      │                          │
│              │ - FIFO + auto-dequeue │                          │
│              └───────────┬───────────┘                          │
│                          │                                      │
│              ┌───────────▼───────────┐                          │
│              │    StreamWorker       │                          │
│              │                       │                          │
│              │ - executePython()     │                          │
│              │ - executeCline()      │                          │
│              │ - ExecuteDispatch()   │                          │
│              │ - JSONL parser        │                          │
│              │ - MessageThrottler    │                          │
│              └───────────┬───────────┘                          │
│                          │                                      │
│              ┌───────────┴───────────┐                          │
│              │   Block Kit Renderer  │                          │
│              │                       │                          │
│              │ - BuildTaskCard()     │                          │
│              │ - progress bars       │                          │
│              │ - agent grid          │                          │
│              │ - HITL buttons        │                          │
│              └───────────┬───────────┘                          │
│                          │                                      │
│              ┌───────────▼───────────┐                          │
│              │    StateManager       │                          │
│              │   (SQLite)            │                          │
│              │                       │                          │
│              │ - task persistence    │                          │
│              │ - crash recovery      │                          │
│              └───────────────────────┘                          │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
                          │ stdio JSON
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│                    AgentFactory Core (Python)                     │
│                                                                   │
│  worker.py ──→ Supervisor ──→ Workers (DeepAgent / CLI)          │
│                                                                   │
└──────────────────────────────────────────────────────────────────┘
```

---

## 3. 模块职责

### 3.1 适配器层 (Adapters)

**职责**: 将平台特定协议转换为 Gateway 内部事件

**当前实现**:
- `SlackAdapter`: Socket Mode 连接, 事件解析, Block Kit 消息

**未来扩展**:
- `FeishuAdapter`: HTTP Webhook 连接
- `CLIAdapter`: 交互式终端输入

**接口约定**:
```
平台事件 → Gateway 内部格式 → 路由处理 → 平台消息
```

### 3.2 消息路由 (Message Router)

**职责**: 根据事件类型分发到不同处理器

**路由规则**:
- App Mention → `handleMentionStream()`
- Block Action → `handleBlockAction()` (HITL)
- 其他 → 忽略

### 3.3 任务队列 (TaskQueue)

**设计**:
```
                    ┌─────────────┐
    New Task ──────>│   Queue     │
                    │  (FIFO)     │
                    └──────┬──────┘
                           │
              ┌────────────▼────────────┐
              │   Concurrency Guard     │
              │                         │
              │  global ≤ 5?  ── N ──→ wait
              │       │ Y               │
              │  channel ≤ 1? ── N ──→ wait
              │       │ Y               │
              │       ▼                 │
              │   Execute Task          │
              └─────────────────────────┘
```

**关键特性**:
- 队列任务自动出队 (回调机制)
- 通道级别序列化 (避免同一频道消息混乱)
- 全局并发限制 (保护 Python Worker 资源)

### 3.4 StreamWorker

**设计模式**: 进程管理 + 流式解析

```
┌─────────────────────────────────────────┐
│              StreamWorker               │
│                                         │
│  ┌───────────┐    ┌──────────────────┐  │
│  │  cmd.Start│───>│  stdout Pipe     │  │
│  │  (Python) │    │  bufio.Scanner   │  │
│  └───────────┘    └────────┬─────────┘  │
│                            │            │
│               ┌────────────▼─────────┐  │
│               │  JSONL Parser        │  │
│               │  json.Unmarshal      │  │
│               └────────────┬─────────┘  │
│                            │            │
│               ┌────────────▼─────────┐  │
│               │  MessageThrottler    │  │
│               │  (1s debounce)       │  │
│               └────────────┬─────────┘  │
│                            │            │
│               ┌────────────▼─────────┐  │
│               │  StreamCallback      │  │
│               │  → Renderer → Slack  │  │
│               └──────────────────────┘  │
└─────────────────────────────────────────┘
```

### 3.5 状态管理 (StateManager)

**接口**: `state/interface.go`

**实现**:
- `SQLiteStore`: 生产使用, 持久化存储
- `JSONStateManager`: 测试使用, 内存/文件存储

**Schema**:
```sql
CREATE TABLE IF NOT EXISTS tasks (
    task_id    TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    slack_ts   TEXT,
    user_id    TEXT,
    prompt     TEXT,
    status     TEXT NOT NULL,  -- queued|running|done|error|paused
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

---

## 4. 数据流

### 4.1 正常任务流程

```
Slack Mention
    │
    ▼
SlackAdapter.parseEvent()
    │
    ▼
MessageRouter.route()
    │
    ├─ isDispatchTask()? ── Yes ──→ ExecuteDispatch()
    │                                    │
    │                                    ▼
    │                               Python worker.py
    │                               (dispatch mode)
    │                                    │
    │                                    ▼
    │                               JSONL events
    │                                    │
    │                                    ▼
    │                               Renderer → Slack Update
    │
    └─ No ──→ Execute()
                   │
                   ▼
              Python worker.py
              (normal mode)
                   │
                   ▼
              JSONL events
                   │
                   ▼
              Renderer → Slack Update
```

### 4.2 HITL 流程

```
Python emits "paused" event
    │
    ▼
Gateway receives paused event
    │
    ▼
Renderer builds HITL buttons (Approve / Reject / Modify)
    │
    ▼
Slack displays interactive card
    │
    ▼
User clicks button → Block Action event
    │
    ▼
handleBlockAction()
    │
    ├─ "approve" → continue task
    ├─ "reject" → retry step
    └─ "modify" → accept user feedback + retry
```

### 4.3 Crash Recovery 流程

```
Gateway Start
    │
    ▼
SQLiteStore.ListActive() → status IN ('running', 'paused')
    │
    ├─ Empty → 正常启动
    │
    └─ Found tasks
           │
           ▼
       For each task:
           │
           ├─ CheckStatus(task_id)
           │      │
           │      ├─ "done" → Update Slack card + SQLite
           │      ├─ "error" → Mark error + notify user
           │      └─ "running" → Keep running (Worker alive)
           │
           ▼
       Recovery complete → Gateway ready
```

---

## 5. 依赖关系

```
main.go
  ├── config/
  │   └── config.go          # 配置加载
  ├── state/
  │   ├── interface.go       # 接口定义
  │   └── sqlite_store.go    # 实现
  ├── gateway/
  │   ├── slack.go           # 依赖: protocol, worker, state, renderer
  │   ├── renderer.go        # 依赖: protocol
  │   ├── task_queue.go      # 独立
  │   ├── recovery.go        # 依赖: state, worker
  │   └── interaction_handler.go  # 依赖: protocol, state
  ├── worker/
  │   ├── stream_worker.go   # 依赖: protocol
  │   ├── throttler.go       # 独立
  │   ├── python_worker.go   # 依赖: protocol
  │   └── cline_adapter.go   # 依赖: protocol
  └── protocol/
      └── protocol.go        # 无依赖
```

---

## 6. 关键设计模式

### 6.1 接口抽象

StateManager 通过接口隔离实现：
- Gateway 层不依赖具体实现
- 测试可用 JSON, 生产用 SQLite
- 未来可轻松替换为 PostgreSQL 等

### 6.2 流式处理

StreamWorker 使用 bufio.Scanner 逐行读取, 配合 goroutine drain stderr：
- 非阻塞 I/O
- 实时事件推送
- Throttler 防抖保护

### 6.3 回调模式

StreamCallback 函数作为参数传递：
- 解耦 Worker 和 UI 渲染
- 易于测试 (mock callback)
- 支持多消费者

### 6.4 状态机

QueuedTask 使用严格状态机：
- queued → dispatching → running → done/error
- 非法转换被拒绝
- 状态变更持久化到 SQLite
