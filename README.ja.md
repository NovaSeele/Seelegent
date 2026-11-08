<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/media/logo-dark.svg">
  <img src="docs/media/logo-light.svg" alt="Seelegent" width="372">
</picture>

**計画とレビューは 1 つのリードエージェントが、コードはあなた自身のアカウントで動くコーディング CLI が書きます。**

[インストール](#インストール)&nbsp;· [クイックスタート](#クイックスタート)&nbsp;· [使い方ガイド](docs/usage.md)&nbsp;·
[ドキュメント](docs/README.md)&nbsp;· [コントリビュート](CONTRIBUTING.md)

[English](README.md)&nbsp;· [Tiếng Việt](README.vi.md)&nbsp;· [简体中文](README.zh-CN.md)&nbsp;· [Español](README.es.md)&nbsp;·
[한국어](README.ko.md)&nbsp;· **日本語**&nbsp;· [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#動作環境)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<!-- Hero: at publish time, replace this block with the uploaded user-attachments video URL on its own line. -->
<p align="center">
  <a href="docs/media/hero.mp4"><img src="docs/media/hero.webp" width="900"
    alt="リードエージェントが Seelegent でタスクを委任し、ダッシュボードにワーカーのステップ、ゲート、差分が表示される"></a>
</p>

Seelegent は、1 つのリードコーディングエージェントが、あなた自身のアカウントで動くワーカー (コーディング CLI) に
作業を任せるためのツールです。あなたはリード (Claude Code、Codex、Antigravity、OpenCode などの MCP ホスト) とだけ
話します。リードが作業を設計してブリーフを書き、MCP 経由で委任すると、Seelegent はクォータが最も多く残っている
アカウントで、それを通常の非対話型 CLI 呼び出し (`agy`、`claude`、`codex`) として実行し、結果をチェックして、
レビュー用のレポートをリードに返します。複数のサブスクリプションや API キーを持ち、計画とレビューは 1 つの
エージェントに、コードを書くのは他のエージェントに任せたい人のためのツールです。

Seelegent はプロキシではなく、複数のアカウントを 1 つのエンドポイントの裏にまとめることもしません。どの実行も、
あなたが所有するアカウントの独立したプロファイルで行われる通常の CLI 呼び出しであり、各アカウントには各提供元の
規約がそのまま適用されます。

## 機能

- **いつものリードからそのまま委任。** リードは MCP で `delegate`、`wait`、`followup`、`review` を呼び、Seelegent は
  各ターンを通常のワーカー CLI 呼び出し 1 回として実行してレポートを返します。→ [MCP ツール](docs/mcp.md)
- **対応が必要なものから先に。** **監視** ページは、質問がある、サインインが必要、ゲートに失敗した、ファイルが失われた
  おそれがあるタスクを先頭に出し、それぞれに何をすべきかを示すバナーを付けます。→
  [タスクが止まったとき](docs/getting-started.md#7-when-something-is-stuck)
- **すべてのステップを記録。** 各タスクにはターミナル、ツリー、グラフ、ファイル、Git のビューがあり、会話、すべての
  ツール呼び出し、ターンと試行、行ごとにレビューノートを付けられる差分を確認できます。→
  [タスクエンジン](docs/task-engine.md)
- **アカウントは複数、プールは 1 つ。** 専用のサインイン、このコンピューターのサインイン、API キーから、優先度、
  実行中のタスク数、残りのクォータでアカウントを選びます。クォータが尽きたアカウントのタスクは、引き継ぎ情報とともに
  別のアカウントへ移ります。→ [設定](docs/configuration.md)
- **ゲートが失敗した作業を差し戻す。** レポートのたびにデーモンがテスト、lint、ビルドを実行し、失敗したゲートは
  自動修正ターンとしてワーカーに戻します。**証拠** ページでは Spec Kit の要件のどれが証明済みかを追えます。→
  [ゲート、レーン、証拠](docs/gates.md)
- **未コミットの作業はそのまま残る。** Git ガードが作業を捨てうる git コマンドを拒否し、各ターン前後のスナップショット
  から、そのターンで失われたかもしれないファイルを復元できます。→ [安全性](docs/safety.md)
- **ワーカーだけでなくチームで。** 名前とランクを持つメンバーがセカンドオピニオンを出し、難しい判断の前に会議を開き、
  同じブリーフを別々の worktree で 2〜5 個のバリアントとして競わせます。→ [チーム](docs/team.md)
- **issue からドラフトの pull request まで、またはスケジュールで。** GitHub の issue を専用の worktree とブランチを
  持つチケットに振り分け、秘密情報のスキャン後にドラフトの pull request を作り、保存した委任をスケジュールで
  実行します。→ [GitHub](docs/github.md) · [自動化](docs/automations.md)

### 実際の動き

プレビューをクリックすると短い動画が開きます。ほかの動画は [使い方ガイド](docs/usage.md) にあります。

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/delegate.mp4"><img src="docs/media/delegate.webp" alt="リードがタスクを委任し、ワーカーのレポートを受け取る"></a><br>
      <b>委任</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/needs-you.mp4"><img src="docs/media/needs-you.webp" alt="監視ページに対応が必要なタスクがバナー付きで並ぶ"></a><br>
      <b>要対応</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/task-detail.mp4"><img src="docs/media/task-detail.webp" alt="タスクのターミナル、ツリー、グラフ、ファイル、Git のビュー"></a><br>
      <b>タスクの詳細</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="失敗したゲートが修正ターンとしてワーカーに戻る。証拠ページ"></a><br>
      <b>ゲートと証拠</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="Git ガードが危険な git コマンドを拒否し、リスクのあるファイルを復元する"></a><br>
      <b>Git ガードと復元</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="メンバー、相談、会議、コンテスト"></a><br>
      <b>チーム</b>
    </td>
  </tr>
</table>

## 動作環境

- **OS:** Windows 10 または 11、Linux、macOS (x64 または ARM64)。最もよくテストされているのは Windows です。
- **Git。** スナップショット、Git ビュー、コンテスト、GitHub のチケットに必要です。
- **ワーカー CLI を 1 つ以上**: Antigravity CLI (`agy`、Gemini モデル、既定のバックエンド)、Claude Code
  (`claude`)、Codex (`codex`)。`claude` と `codex` のバックエンドは、設定でオンにするまでオフです。
- **MCP 対応のリード用ホスト**: Claude Code、Claude Desktop、Codex CLI、OpenCode、Antigravity CLI またはアプリ、
  Gemini CLI、Cursor、Windsurf。MCP のないホストでは、代わりに `seelegent` コマンドを使えます。
- 任意: GitHub 機能を使うなら、サインイン済みの GitHub CLI (`gh`)。

## インストール

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

**Linux と macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

インストーラーは最新リリースをダウンロードし、リリースの `checksums.txt` で SHA-256 を検証し、`seelegent` を PATH に
置き (Windows は `%LOCALAPPDATA%\Programs\seelegent\bin`、それ以外は `~/.local/bin`)、旧バージョンのデータがあれば
取り込み、このマシンのリード用ホストに対して `seelegent setup` を実行します。終わったら新しいターミナルを開き、
リード用ホストを再起動してください。`--version`、`--install-dir`、`--no-setup` (sh) や `-Version`、`-InstallDir`、
`-NoSetup` (PowerShell) などのオプションは [docs/release.md](docs/release.md#installing) にあります。ソースから
ビルドする方法は [CONTRIBUTING.md](CONTRIBUTING.md#development-setup) を参照してください。

## クイックスタート

1. **インストールを確認:** `seelegent doctor` はインストールを点検し、失敗した項目の直し方を示します。新しい
   インストールでは `warn` が出ても問題ありません。`fail` には直し方が添えられます。
2. **リード用ホストを接続:** インストーラーが `seelegent setup` を実行済みです。これは対応する 9 種類のうち見つかった
   すべてのリード用ホストに、`seelegent` MCP サーバーとリード用スキルを登録します。新しいホストを入れたら再度実行し
   (`--dry-run` で変更を先に確認、`--remove` で元に戻す)、ホストを再起動してください。→
   [セットアップ](docs/setup.md)
3. **アカウントを追加:** `seelegent ui` は必要ならデーモンを起動し、http://127.0.0.1:47821/ を開きます。
   **アカウント** で **アカウントを追加** を選び、バックエンドと種類を決めてから **サインイン** します。ワーカー CLI
   自身のサインインがブラウザーかターミナルウィンドウで実行され、Seelegent があなたのパスワードを見ることは
   ありません。
4. **委任する。** プロジェクトフォルダーで、リードに *「Seelegent を使ってサインアップフォームに入力チェックを
   追加して、差分をレビューして」* のように頼みます。リードは `delegate` を呼び、レポートを待ち、差分を読み、
   テストを実行してから、`followup` を送るか `review` でタスクを閉じます。ターミナルからは:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`。

[使い方ガイド](docs/usage.md) (英語) は日々の作業をやりたいことごとに説明し、
[docs/getting-started.md](docs/getting-started.md) (英語) は最初の実行を詳しく説明します。

## 設定

設定はダッシュボードの **設定** ダイアログと `~/.seelegent/config.json` にあります。ゲートの同時実行数、メモリーの
しきい値、テストの並列度は、既定ではこのマシンのメモリーと CPU から計算されます。**設定 › リソース** でどの値も
自分で決めたり、自動に戻したりできます。→ [設定](docs/configuration.md) · [リソース](docs/configuration.md#resources)

## しくみ

```mermaid
flowchart LR
    U([あなた]) --> L[リードエージェント]
    L -->|MCP: delegate, wait, followup, review| M[seelegent mcp]
    M -->|127.0.0.1 上の HTTP| D[Seelegent デーモン]
    D -->|ターンごとに 1 回実行| A[agy]
    D --> C[claude]
    D --> X[codex]
    U -->|ブラウザー| B[ダッシュボード]
    B --- D
```

- **バイナリは 1 つだけ。** `seelegent` が、デーモン、コマンドライン、リードのホストが起動する MCP サーバー
  (`seelegent mcp`)、Web ダッシュボードのすべてを兼ねます。
- **デーモン**は `127.0.0.1:47821` だけで待ち受けます。アカウントを選び、ワーカーを実行し、すべてのステップを記録し、
  チェックを実行します。再起動しても作業は途切れず、実行中のワーカーは次のデーモンが引き継ぎます。
- **ワーカー**は、あなたがすでに使っている CLI そのもので、アカウントごとに専用のプロファイルフォルダーを持ちます。
  タスクの途中でアカウントのクォータが尽きると、そのアカウントはウィンドウがリセットされるまで休み、タスクは
  引き継ぎ情報とともに別のアカウントへ移ります。
- **ダッシュボード** (http://127.0.0.1:47821/) はバイナリに組み込まれていて、インターネットから何も読み込みません。
  7 つの言語に対応し、**設定 › 言語** で選ばない限りブラウザーの言語に従います。`profiles` 以外のどの MCP ツール
  にも `seelegent` コマンドがあります ([docs/cli.md](docs/cli.md))。

タスクがたどる流れと、17 個の MCP ツールの一覧は [使い方ガイド](docs/usage.md#what-a-task-goes-through) (英語) に
あります。

## 安全性

Git ガードは未コミットの作業を捨てうる git コマンドを拒否し、各ターン前後のスナップショットからそのターンで失われた
かもしれないものを復元でき、Seelegent はチケットブランチをプッシュする前にすべてのファイルをスキャンします。これらは
ガードであってサンドボックスではありません。ワーカー CLI はあなたのユーザーとして動くので、変更内容は必ずレビュー
してください。[docs/safety.md](docs/safety.md) と、セキュリティの問題の報告は [SECURITY.md](SECURITY.md) を参照
してください。

## データとプライバシー

すべてはあなたのマシン上で動きます。デーモンは `127.0.0.1` でのみ待ち受け、Seelegent はテレメトリを送らず、
パスワードを求めることもありません。データは `~/.seelegent` にあります (`SEELEGENT_HOME` で移動可能)。何が保存され、
何がマシンの外に出るかは [使い方ガイド](docs/usage.md#data-and-privacy) にあります。

## 更新とアンインストール

`seelegent update --apply` は新しいリリースをその場でインストールし、`seelegent update --rollback` で 1 回だけ前に
戻せます。バイナリを消す前に `seelegent setup --remove` でホストへの登録を取り消してください。すべての手順と、旧
Node 版からの移行は [使い方ガイド](docs/usage.md#update-and-uninstall) にあります。

## トラブルシューティング

まず `seelegent doctor`、次に `seelegent status` を実行してください。デーモンのログは
`~/.seelegent/logs/daemon.log` です。**要対応** にあるタスクは、何をすべきかをバナーに表示します。詳しくは
[使い方ガイド](docs/usage.md#doctor-and-troubleshooting)。

## ドキュメント

ドキュメントは英語です。

- [使い方ガイド](docs/usage.md): 日々の作業を、やりたいことごとに。
- [はじめに](docs/getting-started.md): 最初の実行を一歩ずつ。
- [ドキュメント一覧](docs/README.md): セットアップ、設定、安全性、GitHub、自動化、リード向けのリファレンス
  (MCP ツール、ゲート、チーム)、コマンドライン、HTTP API、タスクエンジン、コントリビューター向けガイド。

## コントリビュートとセキュリティ

コントリビュートを歓迎します。[CONTRIBUTING.md](CONTRIBUTING.md) に開発環境、pull request が通るべきチェック、
コミットの書き方があります。参加する人は誰でも [行動規範](CODE_OF_CONDUCT.md) に従います。セキュリティの問題は
公開の issue ではなく、[SECURITY.md](SECURITY.md) の手順で非公開に報告してください。

## ライセンス

Seelegent はソースアベイラブルなソフトウェアで、
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE) で提供されています。

- 競合する利用でない限り、どんな目的でも使用、複製、変更、共有できます。会社の中での利用、非商用の教育と研究、
  利用者に向けて提供する専門サービスでの利用も含まれます。
- 競合する利用とは、商用の製品やサービスの中で Seelegent を他者に提供することで、その製品やサービスが Seelegent
  (またはライセンサーがそれを使って提供する別の製品やサービス) の代わりになるもの、あるいは同じか実質的に似た機能を
  提供するものを指します。
- 各バージョンは、リリースから 2 年後に Apache License 2.0 でも利用できるようになります。

正確な条項は [LICENSE](LICENSE) ファイルにあります。Seelegent に同梱されているサードパーティの著作物は
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) に記載されています。
