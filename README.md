# webagent

一个用 Go 写的终端 AI agent（TUI + CLI 双模式），核心是通过 MCP 协议把"执行能力"交给独立进程，工具调用前有用户确认机制。

## 特性

- **TUI 界面**（bubbletea）：流式输出、工具调用实时显示、安全审批弹窗（y/n）
- **CLI 模式**：老式逐行输入，`-cli` 切换
- **MCP 驱动**：所有执行类工具（read_file / list_dir / write_file / exec）由 `mcpserver` 子进程提供
- **安全确认**：exec 和"项目根之外的写文件"调用前弹窗确认，根内（默认 `output/`）免确认
- **Skill 能力**：`skills/` 目录的 markdown 技能，模型按需 `load_skill` 加载
- **滚动摘要 + 持久化**：长对话自动压缩旧消息；对话历史与摘要落盘，重启可恢复
- **流式输出 + MCP 断线自动重连**：模型逐字输出；server 崩溃自动重启并重新握手

## 架构

```
              ┌─────────────────────────────┐
              │  webagent (TUI / CLI)       │
              │  agent.Run: 循环 + 分叉      │
              │  - Confirm 安全审批           │
              │  - load_skill (本地)         │
              └──────────┬──────────────────┘
                         │ JSON-RPC over stdio
              ┌──────────▼──────────────────┐
              │  mcpserver.exe (MCP server)  │
              │  read_file / list_dir        │
              │  write_file / exec           │
              └─────────────────────────────┘
```

- `internal/llm`：模型协议（OpenAI 兼容）+ 流式 + 重试
- `internal/agent`：任务循环、滚动摘要、安全审批
- `internal/mcp`：手写 MCP 客户端（JSON-RPC/stdio/自动重连）+ tool 适配器
- `internal/skill`：技能加载与 `load_skill` 工具
- `cmd/mcpserver`：官方 mcp-go SDK 写的工具服务端
- `cmd/webagent`：TUI（bubbletea）与 CLI 入口

## 快速开始

```powershell
# 1. 配置模型（环境变量优先）
[Environment]::SetEnvironmentVariable("WEBAGENT_API_KEY", "sk-...", "User")
[Environment]::SetEnvironmentVariable("WEBAGENT_BASE_URL", "https://tokenrhythm.studio/v1", "User")

# 2. 构建 MCP server（工具全部来自它，必须先构建）
go build -o mcpserver.exe ./cmd/mcpserver

# 3. 运行（默认 TUI；加 -cli 用命令行）
go run ./cmd/webagent
```

## 配置（config.yaml）

| 项 | 说明 |
|---|---|
| `model.*` | base_url / model / temperature / max_tokens / timeout，可用环境变量覆盖 |
| `agent.max_steps` | 单轮回合步数上限 |
| `agent.max_history` | 滚动摘要阈值（对话超 N 条压缩旧消息），0 关闭 |
| `mcp.servers` | MCP server 列表（command + args），工具经它提供 |

## 安全模型

- **exec**：每次调用前询问用户（y/n）
- **write_file**：目标在项目根内（含 `output/`）直接写；绝对路径在根外时询问用户
- 读取类工具不拦截（读不破坏数据）
- MCP server 进程崩溃自动重启并重新握手（agent 透明）

## 测试

```powershell
go test ./...
```

36 个测试，覆盖：协议解析、重试语义、流式、重连、滚动摘要、安全审批、路径解析、MCP 往返。