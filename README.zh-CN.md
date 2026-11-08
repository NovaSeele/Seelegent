<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/media/logo-dark.svg">
  <img src="docs/media/logo-light.svg" alt="Seelegent" width="372">
</picture>

**一个主导 agent 负责规划和审阅，运行在你自己账号上的编程 CLI 负责写代码。**

[安装](#安装)&nbsp;· [快速上手](#快速上手)&nbsp;· [使用指南](docs/usage.md)&nbsp;· [文档](docs/README.md)&nbsp;·
[参与贡献](CONTRIBUTING.md)

[English](README.md)&nbsp;· [Tiếng Việt](README.vi.md)&nbsp;· **简体中文**&nbsp;· [Español](README.es.md)&nbsp;·
[한국어](README.ko.md)&nbsp;· [日本語](README.ja.md)&nbsp;· [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#运行要求)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<!-- Hero: at publish time, replace this block with the uploaded user-attachments video URL on its own line. -->
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

### 实际效果

点击每张预览图可观看一段短视频；[使用指南](docs/usage.md) 里还有更多。

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
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="未通过的关卡作为修复轮次退回 worker；证据页面"></a><br>
      <b>关卡与证据</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="Git 保护拒绝一条危险的 git 命令；恢复有风险的文件"></a><br>
      <b>Git 保护与恢复</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="成员、咨询、会议和竞赛"></a><br>
      <b>团队</b>
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

1. **检查安装：** `seelegent doctor` 检查安装情况并说明如何修复失败项。全新安装出现 `warn` 是正常的；`fail` 会附带
   修复方法。
2. **连接 lead 宿主：** 安装脚本已经运行过 `seelegent setup`，它会把 `seelegent` MCP 服务器和 lead skill 注册到找到
   的每个 lead 宿主（它支持九种宿主）。安装新宿主后请再运行一次（`--dry-run` 先预览改动，`--remove` 撤销），然后
   重启宿主。→ [设置](docs/setup.md)
3. **添加账号：** `seelegent ui` 会在需要时启动守护进程，并打开 http://127.0.0.1:47821/。在 **账号** 页面选择
   **添加账号**，选好后端和类型，再点 **登录**：运行的是 worker CLI 自己的登录流程，在浏览器或终端窗口中完成，
   Seelegent 永远看不到你的密码。
4. **委派工作。** 在项目文件夹中对 lead 说类似这样的话：*“用 Seelegent 给注册表单加上输入校验，然后审阅 diff。”*
   lead 会调用 `delegate`，等待报告，阅读 diff，运行测试，然后发送 `followup` 或用 `review` 结束任务。在终端中：
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`。

[使用指南](docs/usage.md)（英文）按任务讲解日常用法，[docs/getting-started.md](docs/getting-started.md)（英文）
详细讲解第一次运行的每一步。

## 配置

设置位于仪表盘的 **设置** 对话框和 `~/.seelegent/config.json` 中。关卡并发数、内存阈值和测试并行度默认根据本机的
内存和 CPU 计算；你可以在 **设置 › 资源** 中单独设定任意一项，或恢复为自动。→
[配置](docs/configuration.md) · [资源](docs/configuration.md#resources)

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
- **仪表盘**（http://127.0.0.1:47821/）内嵌在二进制文件中，不从互联网加载任何内容。它提供七种语言，默认跟随浏览器的
  语言，除非你在 **设置 › 语言** 中另选一种。除 `profiles` 外，每个 MCP 工具都有对应的 `seelegent` 命令
  （[docs/cli.md](docs/cli.md)）。

[使用指南](docs/usage.md#what-a-task-goes-through)（英文）说明一个任务会经历什么，并列出全部 17 个 MCP 工具。

## 安全

Git 保护会拒绝可能丢掉未提交改动的 git 命令，每轮前后的快照让你能恢复本轮可能弄丢的内容，Seelegent 在推送工单分支
前还会扫描每个文件。这些是防护，而不是沙箱：worker CLI 以你的用户身份运行，请审阅它们改动的内容。见
[docs/safety.md](docs/safety.md)；报告安全问题请见 [SECURITY.md](SECURITY.md)。

## 数据与隐私

一切都在你的机器上运行：守护进程只监听 `127.0.0.1`，Seelegent 不发送遥测，也从不索要密码。数据位于 `~/.seelegent`
（可用 `SEELEGENT_HOME` 改位置）。其中保存了什么、哪些内容会离开本机，见 [使用指南](docs/usage.md#data-and-privacy)。

## 更新与卸载

`seelegent update --apply` 就地安装新版本，`seelegent update --rollback` 可回退一次。删除二进制文件之前，先用
`seelegent setup --remove` 撤销宿主注册。完整步骤以及从旧的 Node 版本迁移，见
[使用指南](docs/usage.md#update-and-uninstall)。

## 故障排查

先运行 `seelegent doctor`，再运行 `seelegent status`；守护进程日志位于 `~/.seelegent/logs/daemon.log`。处于
**需要你处理** 的任务会在横幅上说明该做什么。更多见 [使用指南](docs/usage.md#doctor-and-troubleshooting)。

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
