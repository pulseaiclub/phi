<p align="center">
  <img src="assets/pixel-text-PHI.png" alt="phi" width="220" style="image-rendering: pixelated; image-rendering: crisp-edges;">
</p>

<p align="center">
  <a href="https://pulseaiclub.github.io/"><img alt="文档" src="https://img.shields.io/badge/docs-58A6FF?style=flat&colorA=222222&colorB=58A6FF" /></a>
  <a href="https://discord.gg/UnyHB3tvRk"><img alt="Discord" src="https://img.shields.io/badge/discord-community-5865F2?style=flat-square&logo=discord&logoColor=white" /></a>
  <a href="README.md"><img alt="English" src="https://img.shields.io/badge/English-58A6FF?style=flat&colorA=222222&colorB=58A6FF" /></a>
  <a href="https://github.com/pulseaiclub/phi/blob/main/LICENSE"><img src="https://img.shields.io/github/license/pulseaiclub/phi?style=flat&colorA=222222&colorB=58A6FF" alt="License"></a>
  <a href="https://github.com/pulseaiclub/phi/actions"><img src="https://img.shields.io/github/actions/workflow/status/pulseaiclub/phi/ci.yml?style=flat&colorA=222222&colorB=3FB950" alt="CI"></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat&colorA=222222&logo=go&logoColor=white" alt="Go"></a>
  <a href="https://github.com/pulseaiclub/phi/releases"><img src="https://img.shields.io/github/v/release/pulseaiclub/phi?style=flat&colorA=222222&colorB=8957E5" alt="Release"></a>
</p>

- 15 MB · ~31 ms · sub-agent · 锚点编辑 · 权限门控 · 渐进式 MCP · PXB 插件 · 全屏审阅 & 代码选中 · OpenAI / Anthropic / Gemini

![phi 欢迎界面](assets/phi.png)

![phi TUI](assets/image.png)

![phi diff 审阅](assets/diff.png)

你可以通过 [Skills（技能）](#skills技能)、[Extensions（扩展）](#extensions扩展)
和 [MCP](#mcp) 扩展它——不必做成插件框架。

- [文档](https://pulseaiclub.github.io/docs/getting-started/)
- [快速开始](#快速开始)
- [资源占用](#资源占用)
- [配置](#配置)
- [交互模式](#交互模式)
- [Diff 审阅](#diff-审阅)
- [代码查看器](#代码查看器)
- [命令](#命令)
- [会话](#会话)
- [无头模式](#无头模式)
- [Skills（技能）](#skills技能)
- [权限](#权限)
- [Extensions（扩展）](#extensions扩展)
- [MCP](#mcp)
- [子代理](#子代理)
- [工具](#工具)
- [项目结构](doc/project-layout.md)

## 快速开始

安装最新发布版本（macOS / Linux）：

```sh
curl -fsSL https://raw.githubusercontent.com/pulseaiclub/phi/main/scripts/install.sh | bash
```

Windows（PowerShell 5.1+）：

```powershell
irm https://raw.githubusercontent.com/pulseaiclub/phi/main/scripts/install.ps1 | iex
```

首次启动需要配置模型。使用下面这个命令打开配置编辑器（会创建 `~/.phi` 目录结构并写入 `~/.phi/config.yaml`）：

```sh
phi config
```

也可以设置环境变量做一次性运行：

```sh
export PHI_MODEL=gpt-4o
export PHI_API_KEY=sk-...
```

然后启动 TUI：

```sh
phi
```

或者从源码构建（Go 1.26.3+，见 `go.mod`）：

```sh
make build          # 生成 ./phi
make install        # 构建并安装到 $GOBIN
```

首次启动时，phi 会自动创建 `~/.phi/{bin,skills,hooks,session}`。搜索工具
（`fd`、`rg`）缺失时会在后台下载到 `~/.phi/bin`。

TUI 给模型提供四个核心工具——`read`、`write`、`edit` 和 `bash`——外加 `grep`、`find`、`ls`。模型用这些工具来完成你的请求。外部 HTTP 抓取在配置 MCP 后可用。

## 资源占用

精简只是底线——phi 还要启动即开、负载下仍省内存。phi 数字来自剥离的发布构建
（`CGO_ENABLED=0`，`-ldflags="-s -w"`），在 macOS arm64 上测得。其他 harness
用已公开的 Linux PSS / 交互式 PTY 数据。

### 首帧时间

<p align="center">
  <img src="assets/perf-first-frame.png" alt="首帧时间：phi 0.031s，对比其他终端 harness" width="900">
</p>

### 空闲内存 · 1 个会话

<p align="center">
  <img src="assets/perf-ram-1.png" alt="空闲内存（1 个会话）：phi 21.2 MB，对比其他终端 harness" width="900">
</p>

### 空闲内存 · 10 个会话

<p align="center">
  <img src="assets/perf-ram-10.png" alt="空闲内存（10 个会话）：phi 221 MB，对比其他终端 harness" width="900">
</p>

| 指标 | phi |
| --- | ---: |
| 发布二进制 | **约 15 MB** |
| 空闲 RSS（1 个会话） | **约 21 MB** |
| 10 个空闲会话（RSS 总量） | **约 221 MB** |
| 首帧时间 | **约 31 ms**（26–49 ms） |
| 冷 `go build`（空 `GOCACHE`） | **约 5.5 s** |
| 热重建 | **约 0.7 s** |
| Go 源码（不含测试） | **约 22k 行** / 107 个文件 |
| Go 包数量 | **32** |
| 直接模块依赖 | **6**（共 15 个模块） |
| 链接运行时 | 仅系统库（无 Node / Electron / Python） |

## 配置

phi 读取 `~/.phi/config.yaml`（标准 YAML）。环境变量可覆盖配置，用于一次性运行。
`phi config` 会在终端里打开全屏编辑器来编辑同一个文件；不保存就不落盘，保存前
的旧文件会留作 `config.yaml.bak`。

```
phi config 按键
  ↑↓ ←→      移动 / 切换取值            a   新增模型
  ⏎          编辑，或打开选择列表       d   删除当前行
  esc        收起列表 / 退出            f   从服务商拉取模型列表
  ^s         保存                       s   保存     q  退出
```

留空表示「未设置」，也就是交给加载器填默认值；占位符显示的就是那个值。

```yaml
# ~/.phi/config.yaml
models:
  - name: gpt-4o
    api: OpenAI             # OpenAI | OpenAIResponses | Anthropic | Gemini（空则走 OpenAI 兼容）
    api_key: sk-...         # 或设置 PHI_API_KEY
    base_url: https://api.openai.com/v1   # 默认；PHI_BASE_URL 可覆盖
    context_window: 128000  # 可选
    default: true           # 启动时使用的模型；缺省时第一项生效
  - name: claude-sonnet-4-20250514
    api: Anthropic          # 必填 — 不再按名字/URL 猜测
    api_key: sk-ant-...
    base_url: https://api.anthropic.com
    context_window: 200000
  - name: deepseek-flash    # 内置 preset：自动补齐 base_url / context / thinking
    api_key: sk-...
  - name: gemini-2.5-flash  # 内置 preset（api: Gemini）
    api_key: ...
    think_level: high       # 可选：off | minimal | low | medium | high | …

skill_path: ~/.phi/skills # SKILL.md 文件的加载目录

agents:
  enabled: true           # 默认；设为 false 可禁用 agent_* 子代理工具

permissions:
  mode: interactive       # interactive | readonly | autopilot | headless-strict
  bash:
    default: ask          # ask | allow | deny
    allow:
      - "go test ./..."
    deny:
      - "rm -rf *"
```

内置 preset 与思考参数上线格式见 [doc/models.md](doc/models.md)。

环境变量覆盖：

| 变量 | 覆盖项 |
| ---------------- | ------------------ |
| `PHI_API_KEY` | `models[].api_key`（默认模型） |
| `PHI_MODEL` | `models[].name`（默认模型） |
| `PHI_BASE_URL` | `models[].base_url`（默认模型） |
| `PHI_SKILL_PATH` | `skill_path` |
| `PHI_THINK_LEVEL` | `models[].think_level`（默认模型；`off` 关闭思考） |
| `PHI_OPTIMIZER` | 优化器功能的主开关（设为 `0`、`false`、`off`、`no` 可禁用） |
| `TYPESAFE_API_KEY` | 使用优化器功能所需的 TypeSafe API 密钥 |

**安全提示：** 开启优化器后，命令历史与上下文会发送到外部 TypeSafe 服务进行评判和排序。避免在可能包含敏感信息（API 密钥、令牌、凭证）的环境中使用优化器。
提供商路由看显式 `api` 字段（`OpenAI` / `Anthropic` / `Gemini`）。详见 [支持的模型](doc/models.md)。

### 工作区布局

```
~/.phi/
├── config.yaml   # 全局配置
├── bin/          # 下载的搜索工具（fd、ripgrep）
├── skills/       # SKILL.md 技能目录
├── extensions/   # PXB 二进制 + phi.yaml
├── jobs/         # 子代理任务产物（meta、logs、result.md）
└── session/      # 持久化会话，每个工作目录一个目录
    └── <encoded-cwd>/
```

## 交互模式

`phi`（或 `phi tui`）启动 TUI：上方是对话记录，底部是编辑器，底部状态栏显示
当前活动。有新版发布时，状态栏会提示类似 `0.2.0 available · phi update`。

助手输出按 Markdown（CommonMark/GFM）渲染：标题、强调、删除线、链接、引用、
列表、任务复选框和表格都会按当前主题着色；围栏代码块上方有淡色语言标注，并按语言高亮。
结构标记（`#`、`` ` ``、`*`）会被去除。

编辑器支持：

- `@` —— 模糊文件选择器（输入 `@` 后开始输入路径）
- `/` —— 斜杠命令选择器（`/sessions`、`/branch`、`/new`、`/diff`、`/code`）
- `?` —— 快捷键帮助选择器（列出 `/`、`!`、`@` 和按键绑定；`Esc` 关闭）
- `!command` —— 在本地运行 shell 命令，并把输出流式写入对话记录
  （见 [命令](#命令)）
- `Ctrl+K` —— 命令面板：设置 → 模型 / 主题 / 权限 / 代理、技能、hooks

### 键盘快捷键

| 按键 | 作用 |
| -------------- | ------------------------------- |
| `Ctrl+C` | 退出 phi |
| `Esc` | 取消正在运行的代理 / 关闭选择器 |
| `Ctrl+K` | 开关命令面板 |
| `Ctrl+A` | 光标跳到行首 |
| `Ctrl+E` | 光标跳到行尾 |
| `Ctrl+U` | 清空输入框（含图片和技能） |
| `Ctrl+Shift+C` | 复制选中的对话文本 |

主题：`Dark`（默认）、`Darcula`、`Pink` 和 `Terminal`，可在面板的
设置 → 主题中切换。

## Diff 审阅

`/diff` 是 TUI 里的全屏 git 审阅——看改动、写行级批注，再把批注交给代理，
全程不用离开终端。

| 命令 | 打开内容 |
| --- | --- |
| `/diff` | 工作区（`git diff`） |
| `/diff staged` | 暂存区 |
| `/diff HEAD` | 最近一次提交（`git show`） |

斜杠选择器里回车会把 `/diff` 连同一个空格填进输入框；再提交才打开。
审阅层内：`j`/`k` 移动，`s` 左右对照，`i` 添加/编辑批注，`x` 删除，`a` 发给代理，
`?` 帮助，`q` / `Esc` 关闭。批注只存在内存里，切 diff 就丢，不会落盘。

## 代码查看器

`/code <path>[:line]` 打开全屏源码查看器：语法高亮、支持 CJK 宽度的光标
（`j`/`k`、`h`/`l`、`gg`/`G`），`v` 选中行后按 `a`，把选中的行作为
`path:12-18` 引用交给聊天输入框，模型直接可读。查看器自己读文件，并遵守
与工具门相同的敏感路径清单——`~/.ssh`、`.env` 等敏感路径、二进制文件和
超过 8 MiB 的文件都会拒绝并以 toast 提示。

`Esc` 关闭。阅读时状态栏显示路径、光标和行数；`v` 激活时显示已选中行数。

## 切换分支

`/branch` 打开当前目录的分支选择器，只有两列：分支名和它最近的一次提交。
`●` 标记 HEAD 所在的分支。顺序是：你在哪、你从哪来（git reflog）、其余本地
分支、远程跟踪分支。

回车对选中行执行 `git switch`（不占用 UI 线程，慢仓库不会卡住输入框）。
选中远程行等同于检出跟踪它的本地分支——也就是 `git switch feat` 对
`origin/feat` 做的事。

`/branch <name>` 跳过选择器：切到该分支；如果没有任何分支叫这个名字，就从
HEAD 新建一个。敲名字本身就是「新建分支」的全部流程，比填表单快。

未提交的改动不会被丢弃：git 会拒绝，原文提示以 toast 显示。代理正在回复或执行
命令时禁止切换；merge、rebase、cherry-pick、revert、bisect 未完成时同样禁止。

## 命令

| 命令 | 说明 |
| ------------------ | --------------------------------------------- |
| `phi` / `phi tui` | 启动交互式 TUI |
| `phi run -p "…"` | 以无头模式运行一个代理循环（见下文） |
| `phi update` | 下载并安装最新的 GitHub 发布版本 |
| `phi update --check` | 只查询最新版本，不安装 |
| `phi sessions list` | 列出当前目录的持久化会话 |
| `/sessions` | 列出当前目录的会话（TUI 内） |
| `/branch` | 切换工作分支 — 见 [切换分支](#切换分支) |
| `/new` | 开启一个全新的空会话（TUI 内） |
| `/diff` | 全屏 git 审阅 — 见 [Diff 审阅](#diff-审阅) |
| `/code` | 全屏源码查看器 — 见 [代码查看器](#代码查看器) |
| `!command` | 在本地运行 shell 命令，把输出流式写入对话记录；`Esc` 取消 |

在 TUI 中，`!command` 通过 `bash -c` 在本地运行——在代理循环之外。它不计入
代理忙碌状态，运行中的命令可以用 `Esc` 取消，且不影响正在进行的代理回合。

## 会话

会话会按工作目录自动持久化到 `~/.phi/session/<encoded-cwd>/`，以 JSONL 轨迹
记录。

- `phi sessions list` —— 列出当前目录的会话 id、修改时间和预览
- TUI 内 `/sessions` —— 同上，在应用内查看
- `/new` —— 开启全新会话（新 id、空对话记录）
- `phi run --session <id>` / `phi run --continue-last` —— 无头模式恢复会话

## 无头模式

```sh
phi run -p "fix the failing test in internal/tools"
```

不启动 TUI，运行一个代理循环。人类可读的日志输出到 stderr；加上 `--jsonl` 后，机器可读事件输出到 stdout，每行一个 JSON 对象。

参数：

| 参数 | 说明 |
| -------------------- | ---------------------------------------------- |
| `-p, --prompt STRING` | 要运行的提示词（必填） |
| `--jsonl` | 向 stdout 输出 JSONL 事件 |
| `--yolo` | 本次运行跳过所有权限检查（仅用于 benchmark / CI） |
| `--max-rounds N` | 限制工具轮数（默认 64） |
| `--timeout DURATION` | 限制 Agent 运行总时长（例如 `10m`，默认不限制） |
| `--session ID` | 按 id 或唯一前缀恢复已持久化的会话 |
| `--continue-last` | 恢复当前目录最新的持久化会话 |
| `--session-dir DIR` | 覆盖会话存储目录 |
| `--tools LIST` | 仅启用逗号分隔的指定内置工具 |

`--tools` 接受 `read,ls,grep` 之类的内置工具名称。已配置的 MCP 和 agent
工具仍会照常追加；该参数只限制内置工具集。

退出码：`0` 成功 · `1` 运行时/LLM 错误 · `2` 达到最大轮数 ·
`3` 配置/用法错误。

交互式 TUI 在工具轮数耗尽时会询问 Continue / Stop。
无头 `phi run` 没有确认界面，因此直接以退出码 2 结束。

无头模式下，权限 `ask` 的决策会被拒绝（没有审批界面），因此无需额外参数
即可获得 `readonly` 级别的安全性。跑 benchmark 需要任意 shell（`pytest`、
`npm test` 等）时，对该次运行加 `--yolo` 即可跳过权限门控。

## Skills（技能）

技能是包含 `SKILL.md` 文件的目录，文件带 YAML frontmatter 和 Markdown 正文。
它们从 `~/.phi/skills/`（或 `skill_path` / `PHI_SKILL_PATH`）加载，注入到代理
上下文中，让你能给模型提供可复用的流程：

```markdown
---
name: My Skill
 description: What this skill does
license: MIT
compatibility: claude, openai
---
Instructions the agent should follow when this skill is relevant.
```

在 TUI 中，可以从面板添加技能（技能 → 列表），然后在勾选所需技能后发送
消息。

## 权限

工具执行受权限策略门控，因此代理默认只读，遇到破坏性操作会先询问。在
`~/.phi/config.yaml` 的 `permissions:` 下配置。

模式：

| 模式 | 行为 |
| ------------------ | --------------------------------------------------- |
| `interactive` | 默认。`ask` 决策在 TUI 中弹出询问。 |
| `readonly` | 拒绝写入 / bash；只读工具仍可用。 |
| `autopilot` | 把 `ask` 折叠为 allow，无人值守运行。 |
| `headless-strict` | 把 `ask` 折叠为 deny（`phi run` 使用）。 |

按工具的规则：`bash.default` / `bash.allow` / `bash.deny`（精确命令前缀匹配）。
全局键：`workspace_only_writes`
（默认 true）、`ask_timeout_sec` 和 `dangerously_allow_all`（默认 false）。

在 TUI 中，审批对话框会替换编辑器，提供批准、带反馈地拒绝、或对本次会话 /
所有会话全部允许等选项。面板的 设置 → 权限 条目可切换会话级绕过。

## Extensions（扩展）

扩展是讲 **PXB** 二进制协议的原生进程（作者 SDK：Go `github.com/pulseaiclub/phi/ext/go/phi` 和 Rust [`ext/rust`](ext/rust) `phi-ext`）：订阅工具/会话事件、注册 LLM 工具、添加斜杠命令。

```bash
go get github.com/pulseaiclub/phi/ext/go@v0.21.0
```

```go
package main

import (
	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("hello", "0.1.0")
	m.OnToolCall(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
		// return &ext.ToolCallResult{Block: true, Reason: "..."}
		return nil
	})
	_ = m.Run()
}
```

放到 `~/.phi/extensions/<name>/`，附带 `phi.yaml` 指向二进制。
TUI：`Ctrl+K` → **extensions**。禁用：`PHI_EXTENSIONS=off`。完整指南见 [doc/extensions.md](doc/extensions.md)。

Codec 吞吐（Apple Silicon，release，单线程）：

| 实现 | Hello encode+decode | Frame write+read（内存） | Allocs |
|---|---|---|---|
| Rust PXB (`phi-ext`) | ~0.12 µs | ~0.06 µs | — |
| Go PXB (`ext/go/pxb`) | ~0.11 µs | ~0.05 µs | 3 / op |
| Go JSON lines | ~1.2 µs | — | 15 / op |

相对 JSON lines 约 10× 来自协议本身（定长头 + tagged fields），不是语言——同套
codec 工作下 Rust / Go 在噪声内。扩展真实延迟仍由进程 spawn 和 pipe RTT 主导。
复测：在 [`ext/rust`](ext/rust) 里跑 `cargo run --release --example bench`。

## MCP

**配 100 个 MCP 服务器，开场 schema 仍接近 0 token。**

多数 MCP Host 会在你提问前把全部 `tools/list` schema 塞进上下文——光浏览器类
工具就能烧掉 5 万+ token。phi 不这么干。

Agent 只拿到三个元工具；系统提示里会列出已配置的 **server 名**（不含 schema）：

| 工具 | 作用 |
| --- | --- |
| `mcp_list` | 列某个 server 上的工具**名**（紧凑文本） |
| `mcp_inspect` | 按需拉单个工具的精简参数说明 |
| `mcp_call` | 执行 `server` + `tool` + `args` |

流程：从提示词里的 server 名出发 → `mcp_list(server=…)` → `mcp_inspect` → `mcp_call`。子进程**懒启动**。调用仍走 PreHooks → Gate / Ask → Run → PostHooks。

```sh
phi mcp add browsermcp -- npx @browsermcp/mcp@latest
phi mcp doctor
# 在 TUI 里直接让模型用已配置的 server（不必先猜有没有 MCP）
```

配置：`~/.phi/mcp.json`（项目 `<cwd>/.phi/mcp.json` 可覆盖同名）。
`PHI_MCP=off` 关闭。首版支持 stdio 与 HTTP。

完整文档：[doc/mcp.md](doc/mcp.md)。

## 子代理

子代理工具（`agent_spawn`、`agent_wait` 等）**默认开启**。如果想保持工具精简，可在 `~/.phi/config.yaml` 中禁用：

```yaml
agents:
  enabled: false
```

也可以在当前会话中通过面板切换：设置 → 代理。禁用后这些工具不会注册，模型无法派发任务给子代理。

子代理本身使用一种 **role**（`explore` 默认 | `review` | `worker`）：

| Role | 工具 | 用途 |
|------|--------|---------|
| `explore` | 无 write/edit；bash 除硬拒绝外可用 | 多跳侦察 / 梳理结构 |
| `review` | 与 explore 相同 | 差异 / 检查；只出报告 |
| `worker` | 除嵌套外全部工具；bash 除硬拒绝外可用 | 范围明确的独立改动 |

默认保持 explore（不可编辑）。任务是在隔离上下文里落地一块改动时，再用 worker。

## 工具

模型可调用的内置工具（见 `internal/tools/`）：

| 工具 | 用途 |
| -------------- | -------------------------------------------- |
| `bash` | 在工作目录运行 shell 命令 |
| `read` | 读取文件 |
| `write` | 写入文件（受权限门控） |
| `edit` | 精准编辑文件的某一段 |
| `grep` | 跨文件正则搜索 |
| `find` | 文件模式匹配（fd） |
| `ls` | 目录列表 |
| `agent_spawn` | 启动一个隔离的子代理任务（异步） |
| `agent_wait` | 等待任务；只返回简短总结 |
| `agent_cancel` | 取消运行中的任务 |

子代理的完整记录存放在 `~/.phi/jobs/<id>/`，子代理的上下文**不会**注入父代理上下文——只有 wait/task 的总结会注入。

快速搜索工具（`fd`、`ripgrep`）在首次启动缺失时，会下载到 `~/.phi/bin`。

源码结构图见 [项目结构](doc/project-layout.md)。

开发环境搭建、代码风格与提交规范见 [CONTRIBUTING.md](CONTRIBUTING.md)。
