<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/media/logo-dark.svg">
  <img src="docs/media/logo-light.svg" alt="Seelegent" width="372">
</picture>

**Một agent lead lên kế hoạch và review. Các CLI lập trình chạy trên chính tài khoản của bạn viết code.**

[Cài đặt](#cài-đặt)&nbsp;· [Bắt đầu nhanh](#bắt-đầu-nhanh)&nbsp;· [Hướng dẫn sử dụng](docs/usage.md)&nbsp;· [Wiki](https://github.com/NovaSeele/Seelegent/wiki)&nbsp;· [Tài liệu](docs/README.md)&nbsp;·
[Đóng góp](CONTRIBUTING.md)

[English](README.md)&nbsp;· **Tiếng Việt**&nbsp;· [简⁠体⁠中⁠文](README.zh-CN.md)&nbsp;· [Español](README.es.md)&nbsp;·
[한⁠국⁠어](README.ko.md)&nbsp;· [日⁠本⁠語](README.ja.md)&nbsp;· [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#yêu-cầu)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<!-- Hero: at publish time, replace this block with the uploaded user-attachments video URL on its own line. -->
<p align="center">
  <a href="docs/media/hero.mp4"><img src="docs/media/hero.webp" width="900"
    alt="Agent lead giao một tác vụ qua Seelegent; dashboard hiện các bước của worker, cổng kiểm tra và diff"></a>
</p>

Seelegent cho một agent lead giao việc cho các worker là những CLI lập trình chạy trên chính tài khoản của bạn. Bạn
trò chuyện với lead (Claude Code, Codex, Antigravity, OpenCode hoặc một host MCP khác); lead thiết kế công việc, viết
brief và giao qua MCP; Seelegent chạy việc đó bằng một lần gọi CLI không tương tác bình thường (`agy`, `claude` hoặc
`codex`) trên tài khoản còn nhiều hạn mức (quota) nhất, kiểm tra kết quả rồi trả báo cáo về để lead review. Seelegent
dành cho người có nhiều gói thuê bao hoặc khóa API và muốn một agent lo lên kế hoạch và review, còn các agent khác
viết code.

Seelegent không phải proxy và không gộp các tài khoản sau một đầu mối chung. Mỗi lượt chạy là một lần gọi CLI bình
thường trên một tài khoản bạn sở hữu, trong profile cô lập của riêng nó, và điều khoản của từng nhà cung cấp vẫn áp
dụng cho từng tài khoản.

## Tính năng

- **Giao việc từ chính lead bạn đang dùng.** Lead gọi `delegate`, `wait`, `followup` và `review` qua MCP, còn
  Seelegent chạy mỗi lượt bằng một lần gọi CLI worker bình thường rồi trả báo cáo về. → [Công cụ MCP](docs/mcp.md)
- **Thấy ngay việc cần bạn.** Trang **Theo dõi** đưa lên đầu các tác vụ đang có câu hỏi, thiếu đăng nhập, không đạt
  cổng kiểm tra hoặc có tệp có nguy cơ mất, mỗi tác vụ kèm một banner nói rõ cần làm gì. →
  [Khi tác vụ bị kẹt](docs/getting-started.md#7-when-something-is-stuck)
- **Mọi bước đều được ghi lại.** Mỗi tác vụ có các chế độ xem Terminal, Cây, Đồ thị, Tệp và Git: cuộc hội thoại, từng
  lần gọi công cụ, các lượt và lần thử, cùng diff với ghi chú review trên từng dòng. →
  [Bộ máy tác vụ](docs/task-engine.md)
- **Nhiều tài khoản, một nhóm chung.** Đăng nhập riêng, đăng nhập của máy này hoặc khóa API được chọn theo độ ưu tiên,
  số tác vụ đang chạy và hạn mức còn lại; tác vụ có tài khoản hết hạn mức sẽ chuyển sang tài khoản khác kèm bản bàn
  giao. → [Cấu hình](docs/configuration.md)
- **Cổng kiểm tra trả việc chưa đạt về.** Sau mỗi báo cáo, daemon chạy test, lint và build, trả cổng không đạt về
  worker thành một lượt tự sửa, và trang **Bằng chứng** theo dõi yêu cầu nào của Spec Kit đã được chứng minh. →
  [Cổng kiểm tra, làn và bằng chứng](docs/gates.md)
- **Thay đổi chưa commit của bạn được giữ nguyên.** Bảo vệ Git từ chối các lệnh git có thể xóa công việc, và bản chụp
  trước, sau mỗi lượt cho phép khôi phục các tệp mà lượt đó có thể đã làm mất. → [An toàn](docs/safety.md)
- **Một đội ngũ, không chỉ là worker.** Các thành viên có tên và cấp bậc cho ý kiến thứ hai, họp trước những quyết định
  khó, và chạy một brief thành cuộc thi 2 đến 5 phiên bản trong các worktree riêng. → [Làm việc nhóm](docs/team.md)
- **Từ issue tới pull request nháp, hoặc theo lịch.** Phân loại issue GitHub thành ticket có worktree và nhánh riêng,
  mở pull request nháp sau khi quét bí mật khi bạn hoặc lead yêu cầu, và chạy các lần giao việc đã lưu theo lịch. →
  [GitHub](docs/github.md) · [Tự động](docs/automations.md)

### Xem thực tế

Mỗi ảnh xem trước dẫn tới một video ngắn; [hướng dẫn sử dụng](docs/usage.md) có thêm nhiều video khác.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/delegate.mp4"><img src="docs/media/delegate.webp" alt="Lead giao một tác vụ và nhận lại báo cáo của worker"></a><br>
      <b>Giao việc</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/needs-you.mp4"><img src="docs/media/needs-you.webp" alt="Trang Theo dõi liệt kê các tác vụ cần bạn, mỗi tác vụ kèm banner"></a><br>
      <b>Cần bạn</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/task-detail.mp4"><img src="docs/media/task-detail.webp" alt="Các chế độ xem Terminal, Cây, Đồ thị, Tệp và Git của một tác vụ"></a><br>
      <b>Chi tiết tác vụ</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="Cổng kiểm tra không đạt quay về worker thành lượt tự sửa; trang Bằng chứng"></a><br>
      <b>Cổng kiểm tra và bằng chứng</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="Bảo vệ Git từ chối một lệnh git nguy hiểm; các tệp có nguy cơ mất được khôi phục"></a><br>
      <b>Bảo vệ Git và khôi phục</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="Thành viên, hỏi ý kiến, cuộc họp và cuộc thi"></a><br>
      <b>Làm việc nhóm</b>
    </td>
  </tr>
</table>

## Yêu cầu

- **Hệ điều hành:** Windows 10 hoặc 11, Linux hoặc macOS, chip x64 hoặc ARM64. Windows là nền tảng được kiểm thử
  nhiều nhất.
- **Git.** Seelegent cần git cho bản chụp (snapshot), chế độ xem Git, cuộc thi và ticket GitHub.
- **Ít nhất một CLI worker** đã cài: Antigravity CLI (`agy`, model Gemini, backend mặc định), Claude Code (`claude`)
  hoặc Codex (`codex`). Backend `claude` và `codex` tắt sẵn cho tới khi bạn bật trong Cài đặt.
- **Một host cho lead** có MCP: Claude Code, Claude Desktop, Codex CLI, OpenCode, Antigravity CLI hoặc ứng dụng
  Antigravity, Gemini CLI, Cursor hoặc Windsurf. Host không có MCP có thể dùng lệnh `seelegent` thay thế.
- Không bắt buộc: GitHub CLI (`gh`) đã đăng nhập, cho các tính năng GitHub.

## Cài đặt

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

**Linux và macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

Trình cài đặt tải bản phát hành mới nhất, đối chiếu SHA-256 với `checksums.txt` của bản phát hành, đặt `seelegent`
vào PATH (`%LOCALAPPDATA%\Programs\seelegent\bin` trên Windows, `~/.local/bin` trên hệ khác), nhập dữ liệu của phiên
bản cũ nếu có, rồi chạy `seelegent setup` cho các host lead trên máy. Sau đó hãy mở terminal mới và khởi động lại
các host lead. Các tùy chọn như `--version`, `--install-dir` và `--no-setup` (sh) hay `-Version`, `-InstallDir` và
`-NoSetup` (PowerShell) có trong [docs/release.md](docs/release.md#installing). Muốn tự build từ mã nguồn, xem
[CONTRIBUTING.md](CONTRIBUTING.md#development-setup).

## Bắt đầu nhanh

1. **Kiểm tra bản cài:** `seelegent doctor` kiểm tra bản cài và chỉ cách sửa những gì hỏng. Mục `warn` là bình
   thường với bản cài mới; mục `fail` luôn kèm cách sửa.
2. **Kết nối host lead:** trình cài đặt đã chạy `seelegent setup`, lệnh này đăng ký MCP server `seelegent` với mọi
   host lead tìm thấy, trong số chín host nó biết, và cài skill của lead cho Claude Code, Codex CLI, OpenCode và
   Antigravity CLI. Chạy lại sau khi cài thêm host mới (`--dry-run`
   cho xem trước thay đổi, `--remove` gỡ bỏ), rồi khởi động lại host. → [Thiết lập](docs/setup.md)
3. **Thêm tài khoản:** `seelegent ui` khởi động daemon nếu cần và mở http://127.0.0.1:47821/. Ở trang **Tài khoản**,
   chọn **Thêm tài khoản**, rồi chọn backend và loại tài khoản. Với tài khoản có đăng nhập riêng, luồng đăng nhập của
   chính CLI worker bắt đầu ngay, trong trình duyệt hoặc một cửa sổ terminal (nút **Đăng nhập** trên thẻ tài khoản để
   thử lại), và Seelegent không bao giờ thấy mật khẩu của bạn.
4. **Giao việc.** Trong một thư mục dự án, nhờ lead kiểu như *"Dùng Seelegent để thêm kiểm tra dữ liệu nhập cho form
   đăng ký, rồi review diff."* Lead gọi `delegate`, chờ báo cáo, đọc diff, chạy test, rồi gửi `followup` hoặc đóng
   tác vụ bằng `review`. Từ terminal:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`.

[Hướng dẫn sử dụng](docs/usage.md) (tiếng Anh) đi qua công việc hằng ngày theo từng việc, còn
[docs/getting-started.md](docs/getting-started.md) (tiếng Anh) hướng dẫn chi tiết lần chạy đầu tiên.

## Cấu hình

Cài đặt nằm trong hộp thoại **Cài đặt** của dashboard và trong `~/.seelegent/config.json`. Số cổng kiểm tra chạy
cùng lúc, các ngưỡng bộ nhớ và mức song song khi test mặc định được tính theo bộ nhớ và CPU của máy này; bạn có thể
đặt riêng từng giá trị, hoặc quay về tự động, trong **Cài đặt › Tài nguyên**. →
[Cấu hình](docs/configuration.md) · [Tài nguyên](docs/configuration.md#resources)

## Cách hoạt động

```mermaid
flowchart LR
    U([Bạn]) --> L[Agent lead]
    L -->|MCP: delegate, wait, followup, review| M[seelegent mcp]
    M -->|HTTP trên 127.0.0.1| D[Daemon Seelegent]
    D -->|mỗi lượt một lần chạy| A[agy]
    D --> C[claude]
    D --> X[codex]
    U -->|trình duyệt| B[Dashboard]
    B --- D
```

- **Một tệp duy nhất.** `seelegent` vừa là daemon, vừa là dòng lệnh, vừa là MCP server mà host của lead khởi động
  (`seelegent mcp`), vừa là dashboard web.
- **Daemon** chỉ lắng nghe trên `127.0.0.1:47821`. Nó chọn tài khoản, chạy worker, ghi lại từng bước, chạy các bước
  kiểm tra và không bỏ dở việc khi khởi động lại: daemon mới nhận lại các worker đang chạy.
- **Worker** là chính các CLI bạn đang dùng, mỗi tài khoản một thư mục profile riêng. Khi một tài khoản hết hạn mức
  giữa chừng, nó được nghỉ tới khi khung hạn mức đặt lại, còn tác vụ chuyển sang tài khoản khác kèm bản bàn giao.
- **Dashboard** (http://127.0.0.1:47821/) được nhúng sẵn trong tệp chạy và không tải gì từ internet. Nó có bảy
  ngôn ngữ và theo ngôn ngữ của trình duyệt, trừ khi bạn chọn một ngôn ngữ trong **Cài đặt › Ngôn ngữ**. Mỗi công cụ
  MCP, trừ `profiles`, đều có lệnh `seelegent` tương ứng ([docs/cli.md](docs/cli.md)).

[Hướng dẫn sử dụng](docs/usage.md#what-a-task-goes-through) (tiếng Anh) giải thích một tác vụ đi qua những gì và liệt
kê đủ 17 công cụ MCP.

## An toàn

Bảo vệ Git từ chối các lệnh git có thể xóa thay đổi chưa commit, bản chụp trước và sau mỗi lượt cho phép khôi phục
những gì lượt đó có thể đã làm mất, và Seelegent quét mọi tệp trước khi push nhánh của một ticket. Đây là các lớp bảo
vệ, không phải sandbox: CLI worker chạy dưới tài khoản người dùng của bạn, nên hãy review những gì chúng đã sửa. Xem
[docs/safety.md](docs/safety.md), và [SECURITY.md](SECURITY.md) để báo một vấn đề bảo mật.

## Dữ liệu và quyền riêng tư

Mọi thứ chạy trên máy bạn: daemon chỉ lắng nghe trên `127.0.0.1`, Seelegent không gửi telemetry và không bao giờ hỏi
mật khẩu. Dữ liệu nằm ở `~/.seelegent` (`SEELEGENT_HOME` đổi chỗ được). Những gì được lưu ở đó và những gì rời khỏi
máy: [hướng dẫn sử dụng](docs/usage.md#data-and-privacy).

## Cập nhật và gỡ cài đặt

`seelegent update --apply` cài đè bản mới tại chỗ và `seelegent update --rollback` quay về bản trước một lần.
`seelegent setup --remove` gỡ phần đăng ký với các host trước khi bạn xóa tệp chạy. Đầy đủ các bước, cùng cách chuyển
từ phiên bản Node cũ: [hướng dẫn sử dụng](docs/usage.md#update-and-uninstall).

## Xử lý sự cố

Hãy chạy `seelegent doctor` trước, rồi `seelegent status`; log của daemon là `~/.seelegent/logs/daemon.log`. Một
tác vụ ở mục **Cần bạn** ghi trên banner việc cần làm. Thêm: [hướng dẫn sử dụng](docs/usage.md#doctor-and-troubleshooting).

## Tài liệu

Tài liệu viết bằng tiếng Anh.

- [Hướng dẫn sử dụng](docs/usage.md): công việc hằng ngày, theo từng việc.
- [Bắt đầu](docs/getting-started.md): lần chạy đầu tiên, từng bước một.
- [Mục lục tài liệu](docs/README.md): thiết lập, cấu hình, an toàn, GitHub, tự động, tài liệu cho lead (công cụ MCP,
  cổng kiểm tra, làm việc nhóm), dòng lệnh, HTTP API, bộ máy tác vụ và hướng dẫn cho người đóng góp.

## Đóng góp và bảo mật

Mọi đóng góp đều được chào đón: [CONTRIBUTING.md](CONTRIBUTING.md) mô tả cách dựng môi trường phát triển, các bước
kiểm tra mà một pull request phải qua và quy ước commit. Mọi người tham gia đều tuân theo
[Quy tắc ứng xử](CODE_OF_CONDUCT.md). Hãy báo vấn đề bảo mật một cách riêng tư như [SECURITY.md](SECURITY.md) mô tả,
không mở issue công khai.

## Giấy phép

Seelegent công khai mã nguồn (source-available) theo giấy phép
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE).

- Bạn được dùng, sao chép, sửa và chia sẻ cho mọi mục đích không phải sử dụng cạnh tranh, kể cả trong công ty, cho
  giáo dục và nghiên cứu phi thương mại, và trong dịch vụ chuyên môn bạn cung cấp cho người dùng nó.
- Sử dụng cạnh tranh là đưa Seelegent tới người khác trong một sản phẩm hay dịch vụ thương mại thay thế cho nó (hoặc
  cho một sản phẩm, dịch vụ khác mà bên cấp phép cung cấp cùng nó), hoặc có chức năng giống hay gần giống nó.
- Hai năm sau khi mỗi phiên bản được phát hành, phiên bản đó cũng được cấp phép theo Apache License 2.0.

Tệp [LICENSE](LICENSE) có điều khoản chính xác. Các thành phần bên thứ ba đi kèm Seelegent được liệt kê trong
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
