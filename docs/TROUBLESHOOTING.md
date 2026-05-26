# AgentFactory — 故障排查手册

> **目标读者**: 运维或调试 AgentFactory 的 Agent 或工程师
> **最后更新**: 2026-05-26

---

## 1. Gateway 无法启动

### 症状

```
$ go run .
panic: ...
```

### 排查步骤

| 步骤 | 命令 | 预期 |
|------|------|------|
| 检查 Go 版本 | `go version` | ≥ 1.22 |
| 检查依赖 | `go mod tidy` | 无错误 |
| 检查 .env | `cat .env` | SLACK_BOT_TOKEN 和 SLACK_APP_TOKEN 存在 |
| 检查 SQLite | `ls gateway_state.db` | 文件存在或可创建 |
| 检查端口 | `lsof -i :PORT` | 无冲突 |

### 常见原因

- **Token 无效**: Slack API 返回 `invalid_auth`
- **端口占用**: 另一个进程占用了端口
- **权限不足**: 无法创建 SQLite 文件或日志目录

---

## 2. Slack 机器人无响应

### 症状

用户在 Slack @bot 机器人, 但没有任何回复

### 排查步骤

| 步骤 | 操作 | 检查点 |
|------|------|--------|
| 1 | 检查 Gateway 日志 | 看到 `App mention (stream)` 日志? |
| 2 | 检查 Socket Mode 连接 | 日志中有 `connected to Slack`? |
| 3 | 检查 TaskQueue | 日志中有 `Channel already has an active task`? |
| 4 | 检查 Python Worker | 日志中有 `worker stderr` 错误? |
| 5 | 检查 LM Studio | `curl http://127.0.0.1:1234/v1/models` 返回模型? |

### 快速诊断

在 `handleMentionStream` 入口添加调试日志：

```go
log.Printf("[EVENT] Received mention: user=%s text=%s channel=%s", event.User, event.Text, event.Channel)
```

---

## 3. worker.py 找不到

### 症状

```
worker stderr: can't open file 'worker.py': [Errno 2] No such file or directory
```

### 解决方案

1. 确认 `worker.py` 在 AgentFactory Core 根目录:
   ```bash
   ls ~/projects/agentfactory/worker.py
   ```

2. 检查 Gateway 的 `Script` 路径配置:
   ```go
   // stream_worker.go
   Script: "worker.py",  // 相对路径, 需要正确的工作目录
   ```

3. 使用绝对路径:
   ```go
   Script: "/Users/rowang/projects/agentfactory/worker.py",
   ```

---

## 4. Dispatch 模式不工作

### 症状

输入 `/dispatch ...` 后, 行为与普通模式相同

### 排查步骤

| 步骤 | 检查 |
|------|------|
| 前缀检测 | `isDispatchTask("/dispatch test")` 返回 true? |
| Dispatch 标志 | `req.Dispatch` 在 Go 中为 true? |
| Python 路由 | `_process_request()` 中 `is_dispatch` 为 true? |
| LM Studio | Planner 模型是否可用? |

### 调试命令

```bash
# 测试 Python dispatch 直接调用
echo '{"task_id":"test","text":"test","dispatch":true}' | python worker.py
```

---

## 5. SQLite 数据库锁定

### 症状

```
database is locked
```

### 解决方案

1. **启用 WAL mode** (已默认):
   ```go
   db.Exec("PRAGMA journal_mode=WAL")
   ```

2. **减少写入频率**:
   - Progress 事件使用 `progressCache.shouldPersist()` 限流 (10s)
   - 避免每次 progress 都写数据库

3. **检查未关闭的连接**:
   ```bash
   lsof gateway_state.db
   ```

4. **重启 Gateway**:
   ```bash
   # 优雅关闭
   kill -TERM <pid>
   # 重新启动
   go run .
   ```

---

## 6. Crash Recovery 不工作

### 症状

Gateway 重启后, 之前运行中的任务丢失

### 排查步骤

| 步骤 | 检查 |
|------|------|
| SQLite 数据 | `sqlite3 gateway_state.db "SELECT * FROM tasks WHERE status='running'"` |
| Recovery 调用 | 日志中有 `Recovering active tasks`? |
| ListActive 返回 | 返回了 running 任务? |
| CheckStatus | 对每个任务调用了 CheckStatus? |
| Slack 更新 | 卡片状态是否正确更新? |

### 调试

```bash
# 查看当前活跃任务
sqlite3 gateway_state.db ".mode column" ".headers on" "SELECT * FROM tasks WHERE status IN ('running','paused') LIMIT 10;"
```

---

## 7. 测试失败

### Python 测试

| 问题 | 解决方案 |
|------|----------|
| `ModuleNotFoundError: agentfactory` | `pip install -e .` |
| 测试超时 (30s) | 检查是否调用了真实 LLM |
| FTS5 不可用 | 检查 SQLite 编译选项 |

### Go 测试

| 问题 | 解决方案 |
|------|----------|
| 网络超时 | `GOPROXY=https://goproxy.cn,direct` |
| SQLite 测试失败 | 使用 `tmp_path` 或 `t.TempDir()` |
| Secret scanning 拦截 | 不要提交 .env 文件, 用 filter-branch 清理历史 |

---

## 8. LM Studio 问题

### 症状

Supervisor 返回空响应或错误

### 排查

```bash
# 检查 LM Studio 是否运行
curl http://127.0.0.1:1234/v1/models

# 预期输出:
{"data":[{"id":"qwen/qwen3.6-35b-a3b",...}]}

# 检查模型名称 (必须带组织前缀)
# ❌ qwen3.6-35b-a3b
# ✅ qwen/qwen3.6-35b-a3b
```

### 配置修正

`config.yaml` 或 `worker.py` 中：
```python
DEFAULT_BASE_URL = "http://127.0.0.1:1234/v1"
DEFAULT_MODEL = "qwen/qwen3.6-35b-a3b"  # 带组织前缀
```

---

## 9. 日志位置

| 组件 | 日志位置 |
|------|----------|
| Gateway | stdout/stderr (终端) |
| Python Worker | stderr (被 Gateway 捕获) |
| LM Studio | LM Studio 应用内日志 |
| SQLite | `gateway_state.db` (数据, 非日志) |

---

## 10. 紧急操作

### 强制停止 Gateway

```bash
# 优雅关闭 (推荐)
kill -TERM <pid>

# 强制终止 (不推荐, 会触发 Crash Recovery)
kill -9 <pid>
```

### 重置状态

```bash
# 停止 Gateway
kill -TERM <pid>

# 删除状态文件
rm gateway_state.db

# 重启
go run .
```

### 查看队列状态

```bash
# Gateway 日志中搜索:
grep "TaskQueue\|queued\|running" /path/to/logs
```
