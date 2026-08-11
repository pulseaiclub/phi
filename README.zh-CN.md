**[English](README.md) | [中文](README.zh-CN.md)**

# phi

一个用 Go 编写的最小化终端编码代理框架（harness）——Pi 的姊妹项目。
内置子代理、基于哈希行的精确编辑（hashline edits）与权限门控；兼容任意
OpenAI 兼容接口或 Anthropic 模型，无厂商锁定。

<p align="center">
  <a href="https://github.com/pulseaiclub/phi/blob/main/LICENSE"><img src="https://img.shields.io/github/license/pulseaiclub/phi?style=flat&colorA=222222&colorB=58A6FF" alt="License"></a>
  <a href="https://github.com/pulseaiclub/phi/actions"><img src="https://img.shields.io/github/actions/workflow/status/pulseaiclub/phi/ci.yml?style=flat&colorA=222222&colorB=3FB950" alt="CI"></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat&colorA=222222&logo=go&logoColor=white" alt="Go"></a>
  <a href="https://github.com/pulseaiclub/phi/releases"><img src="https://img.shields.io/github/v/release/pulseaiclub/phi?style=flat&colorA=222222&colorB=8957E5" alt="Release"></a>
</p>

![phi 欢迎界面](assets/phi.png)

![phi TUI](assets/image.png)

phi 刻意保持小巧：一个模型循环、一组工具、一个 TUI，以及让助手输出可读的
Markdown 渲染。你可以通过 [Skills（技能）](#skills技能) 扩展它，并用一个 YAML
文件完成全部配置。

- [快速开始](#快速开始)
- [资源占用](#资源占用)
- [配置](#配置)
- [交互模式](#交互模式)
- [命令](#命令)
- [会话](#会话)
- [无头模式](#无头模式)
- [Skills（技能）](#skills技能)
- [权限](#权限)
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

首次启动时，phi 会自动创建 `~/.phi/{bin,skills,session}`。搜索工具
（`fd`、`rg`）缺失时会在后台下载到 `~/.phi/bin`。

TUI 给模型提供四个核心工具——`read`、`write`、`edit` 和 `bash`——外加 `grep`、`glob`、`list`、`fetch`。模型用这些工具来完成你的请求。

## 资源占用

phi 的目标是运行便宜、也便于动手改造。以下数据来自剥离的发布构建
（`CGO_ENABLED=0`，`-ldflags="-s -w"`），除注明外均在 macOS arm64 上测得。

| 指标 | phi |
| --- | ---: |
| 发布二进制 | **约 12 MB** |
| 空闲 RSS（1 个会话） | **约 21 MB** |
| 10 个空闲会话（RSS 总量） | **约 196 MB**（每个约 20 MB） |
| 首帧时间 | **约 40 ms**（27–65 ms） |
| 冷 `go build`（空 `GOCACHE`） | **约 5.5 s** |
| 热重建 | **约 0.7 s** |
| Go 源码（不含测试） | **约 22k 行** / 107 个文件 |
| Go 包数量 | **32** |
| 直接模块依赖 | **6**（共 15 个模块） |
| 链接运行时 | 仅系统库（无 Node / Electron / Python） |

## 配置

phi 读取 `~/.phi/config.yaml`（标准 YAML）。环境变量可覆盖配置，用于一次性运行。
`phi config` 会在浏览器中打开一个 HTML 编辑器来编辑同一个文件。

![phi config](assets/config.png)

```yaml
# ~/.phi/config.yaml
models:
  - name: gpt-4o            # 模型名；"claude-*" 走 Anthropic API
    api_key: sk-...         # 或设置 PHI_API_KEY
    base_url: https://api.openai.com/v1   # 默认；PHI_BASE_URL 可覆盖
    context_window: 128000  # 可选
    default: true           # 启动时使用的模型；缺省时第一项生效
  - name: claude-sonnet-4-20250514   # 额外模型；运行时可切换
    api_key: sk-ant-...
    base_url: https://api.anthropic.com
    context_window: 200000

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
  fetch:
    default: allow
    allowed_hosts:
      - "github.com"
```

环境变量覆盖：

| 变量 | 覆盖项 |
| ---------------- | ------------------ |
| `PHI_API_KEY` | `models[].api_key`（默认模型） |
| `PHI_MODEL` | `models[].name`（默认模型） |
| `PHI_BASE_URL` | `models[].base_url`（默认模型） |
| `PHI_SKILL_PATH` | `skill_path` |

提供商路由：base URL 包含 `anthropic` 或模型名以 `claude` 开头时使用
Anthropic Messages API；其余走 OpenAI 兼容的 `/chat/completions` 路径。

### 工作区布局

```
~/.phi/
├── config.yaml   # 全局配置
├── bin/          # 下载的搜索工具（fd、ripgrep）
├── skills/       # SKILL.md 技能目录
├── jobs/         # 子代理任务产物（meta、logs、result.md）
└── session/      # 持久化会话，每个工作目录一个目录
    └── <encoded-cwd>/
```

## 交互模式

`phi`（或 `phi tui`）启动 TUI：上方是对话记录，底部是编辑器，底部状态栏显示
当前活动。有新版发布时，状态栏会提示类似 `0.2.0 available · phi update`。

助手输出按 Markdown（CommonMark/GFM）渲染：标题、强调、删除线、链接、引用、
列表、任务复选框和表格都会按当前主题着色；围栏代码块带边框和按语言的高亮。
结构标记（`#`、`` ` ``、`*`）会被去除。

编辑器支持：

- `@` —— 模糊文件选择器（输入 `@` 后开始输入路径）
- `/` —— 斜杠命令选择器（`/sessions`、`/resume`）
- `!command` —— 在本地运行 shell 命令，并把输出流式写入对话记录
  （见 [命令](#命令)）
- `Ctrl+K` —— 命令面板：设置 → 模型 / 主题 / 权限 / 代理、技能

### 键盘快捷键

| 按键 | 作用 |
| -------------- | ------------------------------- |
| `Ctrl+C` | 退出 phi |
| `Esc` | 取消正在运行的代理 / 关闭选择器 |
| `Ctrl+K` | 开关命令面板 |
| `Ctrl+Shift+C` | 复制选中的对话文本 |

主题：`Dark`、`Darcula`、`Pink` 和 `Terminal`（默认），可在面板的
设置 → 主题中切换。

## 命令

| 命令 | 说明 |
| ------------------ | --------------------------------------------- |
| `phi` / `phi tui` | 启动交互式 TUI |
| `phi run -p "…"` | 以无头模式运行一个代理循环（见下文） |
| `phi update` | 下载并安装最新的 GitHub 发布版本 |
| `phi update --check` | 只查询最新版本，不安装 |
| `phi sessions list` | 列出当前目录的持久化会话 |
| `/sessions` | 列出当前目录的会话（TUI 内） |
| `/resume <id>` | 按 id 或唯一前缀恢复会话（TUI 内） |
| `!command` | 在本地运行 shell 命令，把输出流式写入对话记录；`Esc` 取消 |

在 TUI 中，`!command` 通过 `bash -c` 在本地运行——在代理循环之外。它不计入
代理忙碌状态，运行中的命令可以用 `Esc` 取消，且不影响正在进行的代理回合。

## 会话

会话会按工作目录自动持久化到 `~/.phi/session/<encoded-cwd>/`，以 JSONL 轨迹
记录。

- `phi sessions list` —— 列出当前目录的会话 id、修改时间和预览
- TUI 内 `/sessions` —— 同上，在应用内查看
- `/resume <id>` —— 继续一个会话（id 或唯一前缀）
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
| `--max-rounds N` | 限制工具轮数（默认 64） |
| `--timeout DURATION` | 限制 Agent 运行总时长（例如 `10m`，默认不限制） |
| `--session ID` | 按 id 或唯一前缀恢复已持久化的会话 |
| `--continue-last` | 恢复当前目录最新的持久化会话 |
| `--session-dir DIR` | 覆盖会话存储目录 |

退出码：`0` 成功 · `1` 运行时/LLM 错误 · `2` 达到最大轮数 ·
`3` 配置/用法错误。

无头模式下，权限 `ask` 的决策会被拒绝（没有审批界面），因此无需额外参数
即可获得 `readonly` 级别的安全性。

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

按工具的规则：`bash.default` / `bash.allow` / `bash.deny`（精确命令前缀匹配）
和 `fetch.default` / `fetch.allowed_hosts`。全局键：`workspace_only_writes`
（默认 true）、`ask_timeout_sec` 和 `dangerously_allow_all`（默认 false）。

在 TUI 中，审批对话框会替换编辑器，提供批准、带反馈地拒绝、或对本次会话 /
所有会话全部允许等选项。面板的 设置 → 权限 条目可切换会话级绕过。

## 子代理

子代理工具（`agent_spawn`、`agent_task` 等）**默认开启**。如果想保持工具精简，可在 `~/.phi/config.yaml` 中禁用：

```yaml
agents:
  enabled: false
```

也可以在当前会话中通过面板切换：设置 → 代理。禁用后这些工具不会注册，模型无法派发任务给子代理。

子代理本身使用一种 **role**（`explore` 默认 | `review` | `worker`）：

| Role | 工具 | 用途 |
|------|--------|---------|
| `explore` | 只读（+ 白名单 bash） | 搜索 / 梳理结构 |
| `review` | 只读（+ 白名单 bash） | 差异 / 检查；不编辑 |
| `worker` | 除嵌套外全部工具 | 已规划的独立编辑 |

默认保持 explore（只读）。只有在父代理已有具体计划时，才优先使用 worker。

## 工具

模型可调用的内置工具（见 `internal/tools/`）：

| 工具 | 用途 |
| -------------- | -------------------------------------------- |
| `bash` | 在工作目录运行 shell 命令 |
| `read` | 读取文件 |
| `write` | 写入文件（受权限门控） |
| `edit` | 精准编辑文件的某一段 |
| `grep` | 跨文件正则搜索 |
| `glob` | 文件模式匹配 |
| `list` | 目录列表 |
| `fetch` | HTTP 抓取（按权限做主机限制） |
| `agent_spawn` | 启动一个隔离的子代理任务（异步） |
| `agent_task` | 派发并等待一个子代理的总结 |
| `agent_wait` | 等待任务；只返回简短总结 |
| `agent_list` | 列出任务 |
| `agent_log` | 查看任务的日志尾部 |
| `agent_cancel` | 取消运行中的任务 |

子代理的完整记录存放在 `~/.phi/jobs/<id>/`，子代理的上下文**不会**注入父代理上下文——只有 wait/task 的总结会注入。

快速搜索工具（`fd`、`ripgrep`）在首次启动缺失时，会下载到 `~/.phi/bin`。

源码结构图见 [项目结构](doc/project-layout.md)。

开发环境搭建、代码风格与提交规范见 [CONTRIBUTING.md](CONTRIBUTING.md)。
