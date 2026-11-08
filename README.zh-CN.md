<div align="center">

<img src="docs/media/logo.svg" alt="Seelegent" width="344">

**一个主导 agent 负责规划和审阅，运行在你自己账号上的编程 CLI 负责写代码。**

[安装](#安装) · [快速上手](#快速上手) · [使用指南](docs/usage.md) · [文档](docs/README.md) ·
[参与贡献](CONTRIBUTING.md)

[English](README.md) · [Tiếng Việt](README.vi.md) · **简体中文** · [Español](README.es.md) ·
[한국어](README.ko.md) · [日本語](README.ja.md) · [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#运行要求)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<p align="center">
  <a href="docs/media/hero.mp4"><img src="docs/media/hero.webp" width="900"
    alt="主导 agent 通过 Seelegent 委派任务；仪表盘显示 worker 的步骤、关卡和 diff"></a>
</p>

Seelegent 让一个主导编程 agent（lead）把工作交给在你自己账号上运行的 worker 编程 CLI。你只和 lead 对话（Claude
Code、Codex、Antigravity、OpenCode 或其他 MCP 宿主）；lead 设计工作、撰写任务说明（brief），通过 MCP 委派出去；
Seelegent 在剩余配额最多的账号上，以一次普通的非交互 CLI 调用（`agy`、`claude` 或 `codex`）执行它，检查结果，再把报告
交回给 lead 审阅。它适合手里有多个订阅或 API 密钥、希望由一个 agent 负责规划和审阅、其他 agent 负责写代码的人。

Seelegent 不是代理，也不会把多个账号聚合到同一个入口后面。每次运行都是在你自己拥有的账号上、在该账号独立的
profile 中进行的普通 CLI 调用，每个账号仍须遵守对应服务商的条款。

## 功能

- **从你正在用的 lead 直接委派。** lead 通过 MCP 调用 `delegate`、`wait`、`followup` 和 `review`，Seelegent 把每一轮
  作为一次普通的 worker CLI 调用来运行，并交回报告。→ [MCP 工具](docs/mcp.md)
- **先看到需要你处理的事。** **监控** 页面把有问题待答、缺少登录、关卡未通过或有文件面临风险的任务排在最前面，
  每个都带有说明该怎么做的横幅。→ [任务卡住时](docs/getting-started.md#7-when-something-is-stuck)
- **每一步都有记录。** 每个任务有终端、树、图、文件和 Git 五种视图：对话、每次工具调用、轮次和尝试，以及可在行上
  留审阅备注的 diff。→ [任务引擎](docs/task-engine.md)
- **多个账号，一个账号池。** 独立登录、本机登录或 API 密钥按优先级、运行中任务数和剩余配额挑选；账号配额用尽时，
  任务会带着交接信息转到另一个账号。→ [配置](docs/configuration.md)
- **关卡把不合格的工作退回去。** 每次报告后，守护进程运行测试、lint 和构建，未通过的关卡会作为自动修复轮次退回
  worker；**证据** 页面跟踪哪些 Spec Kit 需求已被证明。→ [关卡、通道与证据](docs/gates.md)
- **你未提交的改动不会丢。** Git 保护会拒绝可能丢掉改动的 git 命令，每轮前后的快照让你能恢复本轮可能弄丢的文件。
  → [安全](docs/safety.md)
- **一个团队，而不只是 worker。** 有名字、有级别的成员可以给出第二意见、在艰难决定前开会，还能把同一份 brief 作为
  2 到 5 个变体的竞赛，在各自的 worktree 中运行。→ [团队](docs/team.md)
- **从 issue 到草稿 pull request，或按计划执行。** 把 GitHub issue 分拣为拥有独立 worktree 和分支的工单，在扫描机密后
  创建草稿 pull request，并按计划运行已保存的委派。→ [GitHub](docs/github.md) · [自动化](docs/automations.md)
- **按你的机器调整。** 关卡并发数、内存阈值和测试并行度都根据本机的内存和 CPU 计算；你可以在 **设置 › 资源**
  中单独设定任意一项，或恢复为自动。→ [资源](docs/configuration.md#resources)
- **一条命令完成设置，另一条命令完成检查。** `seelegent setup` 把 MCP 服务器和 lead skill 注册到九种 lead 宿主，
  `seelegent doctor` 检查安装情况并说明如何修复失败项。→ [设置](docs/setup.md) · [doctor](docs/cli.md#doctor)

### 实际效果

点击每张预览图可观看一段短视频。

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/delegate.mp4"><img src="docs/media/delegate.webp" alt="lead 委派任务并收到 worker 的报告"></a><br>
      <b>委派</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/needs-you.mp4"><img src="docs/media/needs-you.webp" alt="监控页面列出需要你处理的任务，每个都带横幅"></a><br>
      <b>需要你处理</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/task-detail.mp4"><img src="docs/media/task-detail.webp" alt="任务的终端、树、图、文件和 Git 视图"></a><br>
      <b>任务详情</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/accounts.mp4"><img src="docs/media/accounts.webp" alt="账号页面的配额窗口和登录"></a><br>
      <b>账号与配额</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="未通过的关卡作为修复轮次退回 worker；证据页面"></a><br>
      <b>关卡与证据</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="Git 保护拒绝一条危险的 git 命令；恢复有风险的文件"></a><br>
      <b>Git 保护与恢复</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="成员、咨询、会议和竞赛"></a><br>
      <b>团队</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/github-automations.mp4"><img src="docs/media/github-automations.webp" alt="issue 分拣为工单和草稿 pull request；自动化计划"></a><br>
      <b>GitHub 与自动化</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/resources.mp4"><img src="docs/media/resources.webp" alt="设置 › 资源：本机的自动值、设置与使用自动"></a><br>
      <b>资源</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/setup-doctor.mp4"><img src="docs/media/setup-doctor.webp" alt="seelegent setup 注册 lead 宿主；seelegent doctor 检查安装"></a><br>
      <b>设置与 doctor</b>
    </td>
  </tr>
</table>

## 运行要求

- **操作系统：** Windows 10 或 11、Linux 或 macOS，x64 或 ARM64。Windows 是测试最充分的平台。
- **Git。** 快照、Git 视图、竞赛和 GitHub 工单都需要它。
- **至少一个 worker CLI**：Antigravity CLI（`agy`，Gemini 模型，默认后端）、Claude Code（`claude`）或 Codex
  （`codex`）。`claude` 和 `codex` 后端默认关闭，需在设置中开启。
- **一个支持 MCP 的 lead 宿主**：Claude Code、Claude Desktop、Codex CLI、OpenCode、Antigravity CLI 或 Antigravity
  应用、Gemini CLI、Cursor 或 Windsurf。不支持 MCP 的宿主可以改用 `seelegent` 命令。
- 可选：已登录的 GitHub CLI（`gh`），用于 GitHub 相关功能。

## 安装

**Windows**（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

**Linux 和 macOS：**

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

安装脚本会下载最新版本，用该版本的 `checksums.txt` 校验 SHA-256，把 `seelegent` 放入 PATH（Windows 上是
`%LOCALAPPDATA%\Programs\seelegent\bin`，其他系统是 `~/.local/bin`），如果发现旧版本的数据就导入，然后为本机的
lead 宿主运行 `seelegent setup`。完成后请打开新的终端并重启 lead 宿主。`--version`、`--install-dir`、`--no-setup`
（sh）或 `-Version`、`-InstallDir`、`-NoSetup`（PowerShell）等选项见 [docs/release.md](docs/release.md#installing)。
从源码构建请见 [CONTRIBUTING.md](CONTRIBUTING.md#development-setup)。

## 快速上手

1. **检查安装：** `seelegent doctor`。全新安装出现 `warn` 是正常的；`fail` 会附带修复方法。
2. **连接 lead 宿主：** 安装脚本已经运行过 `seelegent setup`，它会把 `seelegent` MCP 服务器添加到找到的每个 lead
   宿主，并安装 lead skill。安装新宿主后请再运行一次（`--dry-run` 先预览改动，`--remove` 撤销），然后重启宿主。
3. **添加账号：** `seelegent ui` 会在需要时启动守护进程，并打开 http://127.0.0.1:47821/。在 **账号** 页面选择
   **添加账号**，选好后端和类型，再点 **登录**：运行的是 worker CLI 自己的登录流程，在浏览器或终端窗口中完成，
   Seelegent 永远看不到你的密码。
4. **委派工作。** 在项目文件夹中对 lead 说类似这样的话：*“用 Seelegent 给注册表单加上输入校验，然后审阅 diff。”*
   lead 会调用 `delegate`，等待报告，阅读 diff，运行测试，然后发送 `followup` 或用 `review` 结束任务。在终端中：
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`。

[使用指南](docs/usage.md)（英文）按任务讲解日常用法，[docs/getting-started.md](docs/getting-started.md)（英文）
详细讲解第一次运行的每一步。

## 工作原理

```mermaid
flowchart LR
    U([你]) --> L[主导 agent]
    L -->|MCP: delegate, wait, followup, review| M[seelegent mcp]
    M -->|127.0.0.1 上的 HTTP| D[Seelegent 守护进程]
    D -->|每轮运行一次| A[agy]
    D --> C[claude]
    D --> X[codex]
    U -->|浏览器| B[仪表盘]
    B --- D
```

- **单个二进制文件。** `seelegent` 同时是守护进程、命令行、lead 宿主启动的 MCP 服务器（`seelegent mcp`）和网页仪表盘。
- **守护进程**只监听 `127.0.0.1:47821`。它选择账号、运行 worker、记录每一步、执行检查，并且重启后不会中断：下一个
  守护进程会重新接管仍在运行的 worker。
- **Worker** 就是你已经在用的 CLI，每个账号一个独立的 profile 文件夹。账号在任务中途用完配额时，会休息到配额窗口
  重置，任务则带着交接信息转到另一个账号。
- **仪表盘**（http://127.0.0.1:47821/）包括监控、任务、Issue、会议、竞赛、自动化、证据、账号、用量、角色和成员页面，
  以及可搜索的设置对话框。它提供 26 种配色主题、七种界面语言、命令面板（`Ctrl+K`，macOS 上为 `Cmd+K`）和键盘
  快捷键，内嵌在二进制文件中，不从互联网加载任何内容。
- **命令行。** 除 `profiles` 外，每个 MCP 工具都有对应命令（worker 角色在 **角色** 页面和通过 `profiles` 工具编辑），
  所有命令都支持 `--json`：见 `seelegent --help` 和 [docs/cli.md](docs/cli.md)。

### 一个任务会经历什么

**账号。** 三种后端（`agy`、`claude`、`codex`）和三种账号：带独立登录的隔离 profile、本机已有的登录（仅 `agy`），
或某个提供方的 API 密钥（仅 `claude` 和 `codex`：Anthropic、DeepSeek、Moonshot Kimi、Z.ai GLM、OpenRouter、OpenAI、
Groq、本地 Ollama，或自定义端点）。账号池读取每个账号的配额窗口，优先选择优先级更高、运行中任务更少、剩余配额更多
的账号。模型档位 `fast`、`standard`、`strong` 和 `max` 按后端映射到实际模型。

**任务。** 一个任务就是一段对话：先 `delegate`，再按需多次 `followup`，最后用 `review` 给出 `accepted` 或
`rejected`。任务可以并行运行，可以互相等待（`after`），每份 brief 都附带项目记忆和审阅历史，可以按某个 worker 角色
运行（规则、输出风格、技能、默认档位），也可以只传 Spec Kit 的功能文件夹和任务编号，而不必传整份 spec。停滞、
陷入循环或提出问题的任务会提前唤醒 lead；需要你处理的任务（需要登录、没有可用账号）会排在仪表盘最前面。你还可以在
控制台中 **接管** 任务，之后再交还给 worker。

**关卡、通道与证据。** worker 每次报告后，守护进程都会运行任务的关卡：lead 给的命令、spec 中的 `Gate:` 行或在仓库中
检测到的测试、lint、构建命令，以及项目准则、语法检查和卫生检查。关卡未通过时，会作为自动修复轮次退回给 worker
（默认 2 次），之后才唤醒 lead。通道列出任务可以修改的文件，让并行的 worker 互不踩踏。可选的二次审查会在关卡通过
后以只读方式再审一遍。

**团队、GitHub 与自动化。** 成员是有名字的 worker，带有后端、模型或档位、角色和级别（`worker`、`senior`、`peer`）；
在咨询中，同级成员的反对意见在 lead 答复之前会阻止接受。事件钩子会在任务、账号和守护进程的事件发生时运行你自己的
shell 命令。已连接仓库的 issue 会变成工单；工作被接受后，Seelegent 提交、推送工单分支、创建草稿 pull request，并跟踪
其 CI 检查。已登录 `gh` 时使用 `gh`，否则使用你在仪表盘中保存的 token，再否则使用带比较链接的手动模式。自动化按计划
运行（每小时、每天、工作日、每周或 cron，可指定时区），可以带预检命令和配额规则。

### 供 lead 使用的 MCP 工具

| 工具 | 作用 |
|---|---|
| `delegate` | 启动任务（或竞赛）并等待 worker 的报告。 |
| `wait` | 等待一个或多个任务；`any: true` 在第一个完成时返回。 |
| `followup` | 给任务发送下一条指示或修改要求。 |
| `review` | 以 `accepted` 或 `rejected` 结束任务，并附备注。 |
| `cancel` | 停止运行中或排队中的任务。 |
| `task` | 任务的报告及 worker 最近的步骤。 |
| `status` | 账号与配额、活动任务、后端。 |
| `memory` | 按项目保存、会写入每份 brief 的笔记。 |
| `profiles` | Worker 角色：规则、输出风格、技能、默认档位。 |
| `proof` | 需求矩阵、回归运行、通道占用、统计。 |
| `members` | 部门成员。 |
| `consult` | 由资深或同级成员对已完成任务给出第二意见。 |
| `notes` | 任务 diff 上的审阅备注，作为一次追加要求发送。 |
| `meeting` | 决策前由成员参加的结构化会议。 |
| `contest` | 跟进竞赛，选出胜者。 |
| `issues` | 把 GitHub issue 变成工单、worktree 和草稿 pull request。 |
| `automations` | 按计划执行的委派。 |

`seelegent setup` 还会安装 lead skill，教 lead 掌握这个循环；`seelegent skill` 可以打印它。详情见
[docs/mcp.md](docs/mcp.md)。

## 安全

worker 工作的文件夹里，往往有你和其他 worker 尚未提交的改动。Seelegent 分层保护这些改动；任何一层都不会单独判定
任务失败，而是告诉 lead 和仪表盘发生了什么。见 [docs/safety.md](docs/safety.md)。

- **Git 保护。** worker 无法运行可能丢掉未提交改动或改写历史的 git 命令：`reset`、`restore`、`stash`、`checkout`
  （用 `checkout -b` 创建分支除外）、`switch`、`clean`、`rebase`、`merge`、`pull`、`push`、`commit -a` 和
  `--amend` 等等，藏在 `bash -c`、PowerShell、管道传给 shell 的命令或 git 别名里也一样。lead 可以用 `allow_git`
  为一轮解除保护。
- **快照。** 每轮前后，Seelegent 都会记录仓库中未提交的文件，而不触碰你的 index、stash 或 ref。本轮可能弄丢的文件
  会显示为 *有风险*，可以在仪表盘中恢复，或使用 `seelegent restore` 命令。
- **轮后检查。** 换行符或 BOM 的变化、多余文件、写到项目之外的内容，以及与其他任务的重叠，都会在任务上报告。
- **worker 做不到的事。** worker 无法调用 Seelegent 本身（不能创建、修改或审阅任务），不能开启自动化，也不能保存
  GitHub token。后两件事只有你能在仪表盘中完成。
- **推送之前。** Seelegent 只推送它自己创建的一个提交，从不推送 worker 的提交，并且会先扫描要加入的每个文件：
  `.env` 文件、密钥和证书、凭据文件、内容中的 token、`nul` 之类的 Windows 设备名以及超过 5 MB 的文件都会让推送被
  拒绝，且无法绕过。它从不强制推送、合并或关闭 issue。
- **自动化默认关闭。** 每个自动化创建时都是关闭的。只有在你于仪表盘中确认了它具体要做什么之后才会运行；对运行内容
  或时间的任何改动都会让它再次关闭。

这些是防护，而不是沙箱：worker CLI 以你的用户身份运行。请审阅它们改动的内容。报告安全问题请见
[SECURITY.md](SECURITY.md)。

## 数据与隐私

- 一切都在你的机器上运行。守护进程只监听 `127.0.0.1`。Seelegent 本身不发送任何遥测；各 worker CLI 遵循它们自己的
  设置。
- 数据位于 `~/.seelegent`（设置 `SEELEGENT_HOME` 可以改位置）：`config.json`（设置）、`seelegent.db`（任务、账号、
  用量、团队、工单、自动化）、`profiles/`（每个账号一个隔离 profile，含其 API 密钥）、`github/`（token）、
  `snapshots/`、`contests/`（保存的竞赛 diff）、`worktrees/`（仅用于竞赛和工单）、`run/` 和 `logs/`。见
  [docs/configuration.md](docs/configuration.md)。
- Seelegent 从不索要密码。每个 worker CLI 用自己的流程登录，并把 token 保存在账号的 profile 中。API 密钥和 GitHub
  token 各自存放在单独的文件里：在 Windows 上用 DPAPI 加密，在其他系统上是仅所有者可读（0600）的明文；仪表盘只显示
  最后四个字符，日志会遮盖所有机密。
- 会离开本机的内容：worker CLI 与各自的服务商通信（这就是工作本身）；配额通过 CLI 读取（对于 Claude 登录账号，则用
  该账号自己的登录信息访问 Anthropic 的用量接口）；API 密钥账号调用你配置的提供方；GitHub 只会因你连接的仓库而被
  访问（同步、每 10 分钟轮询一次 issue 和 pull request，可用 `githubPollMinutes: 0` 关闭，以及你或 lead 要求的推送、
  pull request 和评论），以及在你打开 **Issue** 页面时检查你的 GitHub 登录；此外还有安装脚本和 `seelegent update`。

## 更新与卸载

`seelegent update` 告诉你是否有新版本；`seelegent update --apply` 就地安装（用 `checksums.txt` 校验、重启守护进程、
刷新 lead 宿主），`seelegent update --rollback` 可回退一次。数据库从不向后迁移，所以比你的数据更旧的版本会被拒绝。
见 [docs/release.md](docs/release.md#updating)。

卸载步骤：

1. `seelegent stop`，然后 `seelegent setup --remove`（移除 MCP 条目、lead skill 和钩子）。
2. 删除二进制文件及其旁边保留的 `seelegent.exe.previous`（`seelegent.previous`）：Windows 上在
   `%LOCALAPPDATA%\Programs\seelegent\bin\`，其他系统是 `~/.local/bin/seelegent*`。再把该文件夹从 PATH 中移除
   （Windows 上是用户的 `Path`，其他系统是安装脚本加到 shell 配置文件中的 `export PATH=...` 那一行）。
3. 可选：删除 `~/.seelegent`。其中有你的账号 profile（及其登录信息）、密钥、任务和历史。

### 从旧版本迁移

旧的 Node 版本把数据放在同一个文件夹。首次安装时，安装脚本会运行 `seelegent migrate`，导入设置、提供方、账号（含
profile 和 API 密钥）、项目记忆和审阅历史、角色、成员、钩子、会议记录和自动化（所有自动化都处于关闭状态，需要重新
确认）。任务、审阅备注、竞赛、证据记录、用量历史和 GitHub 文件夹不会导入：请重新连接仓库并重新保存 token。旧文件夹
永远不会被修改。在 Windows 上，如果安装文件夹中有旧的启动器，它会保留为 `seelegent-node`。安装前请先停止旧的守护
进程，并可运行 `seelegent migrate --dry-run` 预览将导入的内容。见 [docs/setup.md](docs/setup.md#seelegent-migrate)。

## 故障排查

- `seelegent doctor` 检查数据文件夹、设置、数据库、守护进程、git、每个已启用的 worker CLI、账号、PATH，以及空闲内存
  是否高于资源阈值，并说明如何修复失败项。除了在数据文件夹中做一次写入测试（`.doctor-write-test`，随后删除）之外，
  它不会改动任何东西。
- `seelegent status` 显示守护进程、账号及配额，以及需要你处理的任务；`seelegent restart` 重启守护进程。日志位于
  `~/.seelegent/logs/daemon.log`。
- `seelegent setup --dry-run` 显示各 lead 宿主上已注册的内容。运行 setup 或更新后请重启宿主。
- “Seelegent is not available inside a Seelegent worker.” 表示某个 worker 试图调用 Seelegent，这是有意拒绝的。
- 处于 **需要你处理** 的任务会在横幅上说明原因：回答它的问题、登录一个账号、恢复有风险的文件，或在关卡未通过时
  追加要求。

## 文档

文档为英文。

- [使用指南](docs/usage.md)：按任务讲解日常用法。
- [入门](docs/getting-started.md)：第一次运行，逐步说明。
- [文档索引](docs/README.md)：设置、配置、安全、GitHub、自动化、面向 lead 的参考（MCP 工具、关卡、团队）、命令行、
  HTTP API、任务引擎以及贡献者指南。

## 参与贡献与安全

欢迎贡献：[CONTRIBUTING.md](CONTRIBUTING.md) 介绍开发环境、pull request 必须通过的检查以及提交规范。所有参与者都须
遵守 [行为准则](CODE_OF_CONDUCT.md)。安全问题请按 [SECURITY.md](SECURITY.md) 的说明私下报告，不要开公开 issue。

## 许可证

Seelegent 以源码可用（source-available）的方式，按
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE) 发布。

- 你可以出于竞争性使用以外的任何目的使用、复制、修改和分享它，包括在公司内部使用、用于非商业的教育和研究，以及在
  你为使用它的人提供的专业服务中使用。
- 竞争性使用是指在商业产品或服务中把 Seelegent 提供给他人，而该产品或服务替代了它（或许可方借助它提供的其他产品或
  服务），或者提供相同或实质相似的功能。
- 每个版本发布两年后，该版本也同时以 Apache License 2.0 授权。

确切条款以 [LICENSE](LICENSE) 文件为准。随 Seelegent 分发的第三方作品列在
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 中。
