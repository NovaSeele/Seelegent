# Seelegent

[English](README.md) · [Tiếng Việt](README.vi.md) · [简体中文](README.zh-CN.md) · [Español](README.es.md) ·
**한국어** · [日本語](README.ja.md) · [Русский](README.ru.md)

Seelegent는 하나의 리드 코딩 에이전트가 여러분 자신의 계정에서 돌아가는 워커 코딩 CLI에 작업을 맡길 수 있게 해 줍니다.
여러분은 리드(Claude Code, Codex, Antigravity, OpenCode 또는 다른 MCP 호스트)와만 대화합니다. 리드가 작업을 설계하고
브리프를 써서 MCP로 위임하면, Seelegent는 남은 할당량이 가장 많은 계정에서 이를 평범한 비대화형 CLI 호출(`agy`,
`claude`, `codex`)로 실행하고, 결과를 검사한 뒤 리뷰할 수 있도록 보고서를 리드에게 돌려줍니다. 구독이나 API 키가
여러 개 있고, 한 에이전트는 계획과 리뷰를, 나머지는 코드 작성을 맡기고 싶은 사람을 위한 도구입니다.

Seelegent는 프록시가 아니며, 여러 계정을 하나의 엔드포인트 뒤로 묶지도 않습니다. 모든 실행은 여러분이 소유한 계정의
격리된 프로필에서 이루어지는 일반 CLI 호출이며, 각 계정에는 해당 제공자의 약관이 그대로 적용됩니다.

## 동작 방식

```mermaid
flowchart LR
    U([사용자]) --> L[리드 에이전트]
    L -->|MCP: delegate, wait, followup, review| M[seelegent mcp]
    M -->|127.0.0.1의 HTTP| D[Seelegent 데몬]
    D -->|턴마다 한 번 실행| A[agy]
    D --> C[claude]
    D --> X[codex]
    U -->|브라우저| B[대시보드]
    B --- D
```

- **바이너리 하나.** `seelegent` 하나가 데몬, 명령줄, 리드 호스트가 실행하는 MCP 서버(`seelegent mcp`), 웹 대시보드를
  모두 맡습니다.
- **데몬**은 `127.0.0.1:47821`에서만 수신합니다. 계정을 고르고, 워커를 실행하고, 모든 단계를 기록하고, 검사를
  실행합니다. 재시작해도 작업이 끊기지 않으며, 실행 중인 워커는 다음 데몬이 이어받습니다.
- **워커**는 여러분이 이미 쓰는 CLI 그대로이며, 계정마다 자기 프로필 폴더가 있습니다. 작업 도중 계정의 할당량이
  바닥나면 그 계정은 할당량 창이 재설정될 때까지 쉬고, 작업은 인수인계 정보와 함께 다른 계정으로 옮겨 갑니다.
- **대시보드**(http://127.0.0.1:47821/)는 처리할 일, 실행 중인 작업, 각 작업의 단계와 diff, 계정과 할당량, 그리고
  아래의 모든 기능을 보여 줍니다.

## 요구 사항

- **OS:** Windows 10 또는 11, Linux, macOS(x64 또는 ARM64). 가장 많이 테스트된 플랫폼은 Windows입니다.
- **Git.** 스냅샷, Git 보기, 대회, GitHub 티켓에 필요합니다.
- **워커 CLI 하나 이상**: Antigravity CLI(`agy`, Gemini 모델, 기본 백엔드), Claude Code(`claude`) 또는
  Codex(`codex`). `claude`와 `codex` 백엔드는 설정에서 켜기 전까지 꺼져 있습니다.
- **MCP를 지원하는 리드 호스트**: Claude Code, Claude Desktop, Codex CLI, OpenCode, Antigravity CLI 또는 앱,
  Gemini CLI, Cursor, Windsurf. MCP가 없는 호스트는 대신 `seelegent` 명령을 쓸 수 있습니다.
- 선택: GitHub 기능을 쓰려면 로그인한 GitHub CLI(`gh`).

## 설치

Windows(PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

Linux와 macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

설치 프로그램은 최신 릴리스를 내려받아 릴리스의 `checksums.txt`로 SHA-256을 확인하고, `seelegent`를 PATH에
넣고(Windows는 `%LOCALAPPDATA%\Programs\seelegent\bin`, 그 밖에는 `~/.local/bin`), 이전 버전의 데이터가 있으면
가져온 다음, 이 컴퓨터의 리드 호스트에 대해 `seelegent setup`을 실행합니다. 끝나면 새 터미널을 열고 리드 호스트를
다시 시작하세요. `--version`, `--install-dir`, `--no-setup`(sh)이나 `-Version`, `-InstallDir`, `-NoSetup`(PowerShell)
같은 옵션은 [docs/release.md](docs/release.md#installing)에 있습니다.

## 첫걸음

1. **설치 확인:** `seelegent doctor`.
2. **대시보드 열기:** `seelegent ui`는 필요하면 데몬을 시작하고 http://127.0.0.1:47821/을 엽니다.
3. **계정** 페이지에서 **계정 추가**: 백엔드와 종류(별도 로그인, 이 컴퓨터의 로그인, 또는 API 제공자를 통한 API 키)를
   고릅니다. 로그인은 워커 CLI 자체의 로그인 절차를 브라우저나 터미널 창에서 실행합니다. Seelegent는 비밀번호를 절대
   보지 않습니다. `claude`나 `codex`를 쓰려면 먼저 **설정 › 백엔드**에서 해당 백엔드를 켜고 티어별 모델을 지정하세요.
4. **리드 호스트 연결**(설치 프로그램이 이미 실행함): `seelegent setup`은 찾은 모든 리드 호스트에 `seelegent` MCP
   서버를 추가하고, 스킬을 읽는 호스트에는 리드 스킬을 설치합니다. `--host claude,codex`로 호스트를 고르고,
   `--dry-run`으로 변경 내용을 먼저 보고, `--remove`로 되돌립니다. 그다음 호스트를 다시 시작합니다.
5. **위임하기.** 프로젝트 폴더에서 리드에게 *"Seelegent로 회원 가입 폼에 입력 검증을 추가하고 diff를 리뷰해 줘."*
   같이 요청합니다. 리드는 `delegate`를 호출해 보고서를 기다리고, diff를 읽고 테스트를 실행한 뒤 `followup`을
   보내거나 `review`로 작업을 닫습니다. 터미널에서는:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`.

각 단계는 [docs/getting-started.md](docs/getting-started.md)(영어)에서 자세히 설명합니다.

## 기능

**워커와 계정.** 백엔드 세 가지(`agy`, `claude`, `codex`)와 계정 세 종류: 별도 로그인을 가진 격리 프로필, 이 컴퓨터에
이미 있는 로그인(`agy`만), 제공자의 API 키(`claude`와 `codex`만: Anthropic, DeepSeek, Moonshot Kimi, Z.ai GLM,
OpenRouter, OpenAI, Groq, 로컬 Ollama, 또는 사용자 지정 엔드포인트). 계정 풀은 각 계정의 할당량 창을 읽고,
우선순위가 높고 실행 중인 작업이 적고 남은 할당량이 많은 계정을 먼저 씁니다. 모델 티어 `fast`, `standard`,
`strong`, `max`는 백엔드마다 실제 모델에 연결됩니다. [설정](docs/configuration.md)을 보세요.

**작업.** 작업은 하나의 대화입니다. `delegate` 다음 필요한 만큼 `followup`을 보내고, 마지막에 `review`로
`accepted` 또는 `rejected`를 줍니다. 작업은 병렬로 실행되고, 서로를 기다릴 수 있으며(`after`), 모든 브리프에
프로젝트 메모리와 리뷰 기록이 들어갑니다. 워커 역할(규칙, 출력 스타일, 스킬, 기본 티어)로 실행하거나, spec 전체
대신 Spec Kit 기능 폴더와 작업 ID를 넘길 수도 있습니다. 멈추거나, 같은 일을 반복하거나, 질문하는 작업은 리드를 일찍
깨우고, 여러분이 처리해야 하는 작업(로그인 필요, 남은 계정 없음)은 대시보드 맨 위에 올라옵니다. 작업을 콘솔에서
**직접 이어받기** 했다가 워커에게 돌려줄 수도 있습니다. [작업 엔진](docs/task-engine.md)을 보세요.

**게이트, 레인, 증거.** 워커가 보고할 때마다 데몬은 작업의 게이트를 실행합니다. 리드의 명령, spec의 `Gate:` 줄 또는
저장소에서 찾아낸 테스트·lint·빌드 명령, 프로젝트 원칙, 구문 검사, 위생 검사입니다. 게이트가 **실패**하면 리드를
깨우기 전에 자동 수정 턴으로 워커에게 돌려보냅니다(기본 2회). 레인은 작업이 바꿀 수 있는 파일을 정해, 병렬 워커가
서로의 파일을 건드리지 않게 합니다. 선택적으로, 게이트를 통과한 뒤 읽기 전용 2차 검토를 돌릴 수 있습니다. **증거**
페이지는 Spec Kit 요구 사항 중 무엇이 증명되었는지 보여 주고 회귀 실행을 돌립니다. [게이트](docs/gates.md)를 보세요.

**팀.** 멤버는 이름이 있는 워커로, 백엔드, 모델 또는 티어, 역할, 등급(작업자, 시니어, 동료: `worker`, `senior`,
`peer`)을 가집니다. 끝난 작업에 대해 시니어나 동료에게 두 번째 의견을 구하거나(동료의 이의는 리드가 답할 때까지
승인을 막습니다), diff 줄에 리뷰 노트를 남겨 한 번의 추가 요청으로 보내거나, 어려운 결정 전에 멤버 최대 여섯 명과
회의를 열거나(동료는 공식적으로 반대를 기록할 수 있습니다), 같은 브리프를 별도 worktree에서 2~5개 변형으로 겨루는
대회를 열고 우승자를 적용할 수 있습니다. 이벤트 훅은 작업, 계정, 데몬 이벤트(작업의 시작·종료·차단, 계정의 할당량
소진이나 로그인 필요 등)가 있을 때 여러분의 셸 명령을 실행합니다. [팀](docs/team.md)을 보세요.

**GitHub.** 저장소를 연결하고, 열린 이슈를 받은 함으로 동기화하고(로컬 이슈도 추가 가능), 각각을 티켓으로
분류합니다. 티켓은 자기 worktree와 브랜치를 가지며 하나 이상의 작업으로 처리됩니다. 작업이 승인되면 여러분이나 리드가
Seelegent에게 커밋, 티켓 브랜치 푸시, 초안 pull request 열기를 요청합니다. 그 뒤 Seelegent는 CI 검사를 따라가며 실패한
검사를 고치는 작업을 시작할 수도 있습니다. `gh`에 로그인되어 있으면 `gh`를, 아니면 대시보드에 저장한 토큰을, 그것도
없으면 비교 링크를 쓰는 수동 모드를 사용합니다. [GitHub](docs/github.md)를 보세요.

**자동화.** 저장한 위임을 일정(매시, 매일, 평일, 매주 또는 cron, 시간대 지정 가능)에 따라 실행합니다. 사전 검사
명령과 할당량 규칙을 붙일 수 있어, 할당량이 적으면 실행을 건너뛰고, 다 쓰지 못한 채 곧 재설정될 주간 할당량을 쓰는
추가 실행도 고를 수 있습니다. [자동화](docs/automations.md)를 보세요.

**대시보드.** 모니터링, 작업, 이슈, 회의, 대회, 자동화, 증거, 계정, 사용량, 역할, 멤버 페이지와 검색할 수 있는 설정
대화 상자가 있습니다. 작업마다 터미널, 트리, 그래프, 파일, Git 보기가 있습니다. 색 테마 26가지, 화면 언어 일곱 가지,
명령 팔레트(`Ctrl+K`, macOS에서는 `Cmd+K`), 키보드 단축키를 제공합니다. 대시보드는 바이너리에 내장되어 있으며
인터넷에서 아무것도 불러오지 않습니다.

**명령줄.** `profiles`를 뺀 모든 MCP 도구에 해당 명령이 있고(워커 역할은 **역할** 페이지와 `profiles` 도구로
편집합니다), 모든 명령이 `--json`을 받습니다. `seelegent --help`와 [docs/cli.md](docs/cli.md)를 보세요.

### 리드를 위한 MCP 도구

| 도구 | 하는 일 |
|---|---|
| `delegate` | 작업(또는 대회)을 시작하고 워커의 보고서를 기다립니다. |
| `wait` | 하나 이상의 작업을 기다립니다. `any: true`면 첫 작업이 끝날 때 돌아옵니다. |
| `followup` | 작업에 다음 지시나 수정 요청을 보냅니다. |
| `review` | 작업을 `accepted` 또는 `rejected`로 닫고 메모를 남깁니다. |
| `cancel` | 실행 중이거나 대기 중인 작업을 멈춥니다. |
| `task` | 작업의 보고서와 워커의 최근 단계. |
| `status` | 계정과 할당량, 활성 작업, 백엔드. |
| `memory` | 모든 브리프에 들어가는 프로젝트별 메모. |
| `profiles` | 워커 역할: 규칙, 출력 스타일, 스킬, 기본 티어. |
| `proof` | 요구 사항 매트릭스, 회귀 실행, 레인 점유, 통계. |
| `members` | 부서 멤버. |
| `consult` | 끝난 작업에 대한 시니어나 동료의 두 번째 의견. |
| `notes` | 작업 diff의 리뷰 노트. 한 번의 추가 요청으로 보냅니다. |
| `meeting` | 결정 전에 여는 멤버들의 구조화된 회의. |
| `contest` | 대회를 지켜보고 우승자를 고릅니다. |
| `issues` | GitHub 이슈를 티켓, worktree, 초안 pull request로 바꿉니다. |
| `automations` | 일정에 따라 실행되는 위임. |

`seelegent setup`은 리드 스킬도 설치해 리드에게 이 작업 순서를 알려 줍니다. `seelegent skill`로 내용을 볼 수
있습니다. 자세한 내용: [docs/mcp.md](docs/mcp.md).

## 안전

워커가 일하는 폴더에는 여러분과 다른 워커가 아직 커밋하지 않은 변경이 있습니다. Seelegent는 이를 여러 겹으로
보호합니다. 어느 겹도 그것만으로 작업을 실패시키지 않으며, 무슨 일이 있었는지 리드와 대시보드에 알려 줍니다.
[docs/safety.md](docs/safety.md)를 보세요.

- **Git 보호.** 워커는 커밋하지 않은 변경을 날리거나 기록을 다시 쓸 수 있는 git 명령을 실행할 수 없습니다:
  `reset`, `restore`, `stash`, `checkout`(브랜치를 만드는 `checkout -b`는 제외), `switch`, `clean`, `rebase`,
  `merge`, `pull`, `push`, `commit -a`와 `--amend` 등. `bash -c`, PowerShell, 셸로 넘기는 파이프, git 별칭 안에
  있어도 마찬가지입니다. 리드는 `allow_git`으로 한 턴 동안 보호를 풀 수 있습니다.
- **스냅샷.** 매 턴 전후로 Seelegent는 저장소의 커밋되지 않은 파일을 기록하며, 여러분의 index, stash, ref는 건드리지
  않습니다. 그 턴에서 잃어버렸을 수 있는 파일은 *위험한 파일*로 표시되고, 대시보드의 **복원** 버튼이나
  `seelegent restore`로 되돌릴 수 있습니다.
- **턴 후 검사.** 줄 바꿈이나 BOM 변경, 불필요한 파일, 프로젝트 밖에 쓴 내용, 다른 작업과 겹치는 파일을 작업에
  보고합니다.
- **워커가 할 수 없는 일.** 워커는 Seelegent 자체를 호출할 수 없고(작업을 만들거나 바꾸거나 리뷰할 수 없음),
  자동화를 켤 수도, GitHub 토큰을 저장할 수도 없습니다. 뒤의 두 가지는 여러분만 대시보드에서 할 수 있습니다.
- **푸시하기 전에.** Seelegent는 자기가 만든 커밋 하나만 푸시하고, 워커의 커밋은 절대 푸시하지 않으며, 그것도 추가할
  모든 파일을 검사한 다음에만 푸시합니다. `.env` 파일, 키와 인증서, 자격 증명 파일, 파일 내용 속 토큰, `nul` 같은
  Windows 장치 이름, 5 MB가 넘는 파일이 있으면 푸시는 거부되며 우회할 방법이 없습니다. 강제 푸시, 병합, 이슈 닫기는
  하지 않습니다.
- **자동화는 꺼진 채로 시작.** 모든 자동화는 꺼진 상태로 만들어집니다. 무엇을 실행하는지 여러분이 대시보드에서
  확인한 뒤에야 실행되며, 실행 내용이나 시간을 바꾸면 다시 꺼집니다.

이것들은 보호 장치일 뿐 샌드박스가 아닙니다. 워커 CLI는 여러분의 사용자 권한으로 실행됩니다. 바뀐 내용은 꼭
리뷰하세요.

## 데이터와 개인 정보

- 모든 것이 여러분의 컴퓨터에서 실행됩니다. 데몬은 `127.0.0.1`에서만 수신합니다. Seelegent 자체는 원격 측정을
  보내지 않으며, 워커 CLI는 각자의 설정을 따릅니다.
- 데이터는 `~/.seelegent`에 있습니다(`SEELEGENT_HOME`으로 위치 변경 가능): `config.json`(설정), `seelegent.db`(작업,
  계정, 사용량, 팀, 티켓, 자동화), `profiles/`(계정마다 격리된 프로필, API 키도 여기에), `github/`(토큰),
  `snapshots/`, `contests/`(저장된 대회 diff), `worktrees/`(대회와 티켓용만), `run/`, `logs/`.
  [docs/configuration.md](docs/configuration.md)를 보세요.
- Seelegent는 비밀번호를 묻지 않습니다. 각 워커 CLI는 자기 방식으로 로그인하고 토큰을 계정 프로필에 보관합니다.
  API 키와 GitHub 토큰은 각각 별도 파일에 저장되며, Windows에서는 DPAPI로 암호화되고, 그 밖에서는 소유자만 읽을 수
  있는(0600) 평문입니다. 대시보드에는 마지막 네 글자만 보이고, 로그에서는 비밀 값이 가려집니다.
- 컴퓨터 밖으로 나가는 것: 워커 CLI와 각 제공자의 통신(이것이 작업 자체입니다), CLI를 통한 할당량 읽기(Claude
  로그인 계정은 그 계정 자신의 로그인으로 Anthropic 사용량 엔드포인트를 읽습니다), API 키 계정이 설정한 제공자를
  호출하는 것, 그리고 GitHub 호출입니다. GitHub는 연결한 저장소에 대해서만(동기화, 10분마다 이슈와 pull request
  폴링(`githubPollMinutes: 0`으로 끄기), 여러분이나 리드가 요청한 푸시, pull request, 댓글), **이슈** 페이지를 열 때
  GitHub 로그인 확인, 그리고 설치 프로그램과 `seelegent update`에서만 호출됩니다.

## 업데이트와 제거

`seelegent update`는 새 릴리스가 있는지 알려 줍니다. `seelegent update --apply`는 제자리에서 설치하고(`checksums.txt`로
검증, 데몬 재시작, 리드 호스트 갱신), `seelegent update --rollback`은 한 번 이전으로 되돌립니다. 데이터베이스는
절대 거꾸로 마이그레이션되지 않으므로, 데이터보다 오래된 릴리스는 거부됩니다.
[docs/release.md](docs/release.md#updating)를 보세요.

제거 방법:

1. `seelegent stop` 후 `seelegent setup --remove`(MCP 항목, 리드 스킬, 훅 제거).
2. 바이너리와 그 옆에 남아 있는 `seelegent.exe.previous` (`seelegent.previous`)를 지웁니다. Windows는
   `%LOCALAPPDATA%\Programs\seelegent\bin\`, 그 밖에는 `~/.local/bin/seelegent*`입니다. 그 폴더를 PATH에서 뺍니다
   (Windows는 사용자 `Path`, 그 밖에는 설치 프로그램이 셸 프로필에 추가한 `export PATH=...` 줄).
3. 원하면 `~/.seelegent`를 지웁니다. 계정 프로필(과 그 로그인), 키, 작업, 기록이 들어 있습니다.

## 이전 버전에서 옮겨 오기

이전 Node 버전도 같은 폴더에 데이터를 두었습니다. 처음 설치할 때 설치 프로그램이 `seelegent migrate`를 실행해
설정, 제공자, 계정(프로필과 API 키 포함), 프로젝트 메모리와 리뷰 기록, 역할, 멤버, 훅, 회의 기록, 자동화(모두 꺼진
상태로, 다시 확인 필요)를 가져옵니다. 작업, 리뷰 노트, 대회, 증거 기록, 사용량 기록, GitHub 폴더는 가져오지 않습니다.
저장소를 다시 연결하고 토큰을 다시 저장하세요. 이전 폴더는 절대 바뀌지 않습니다. Windows에서는 설치 폴더에 이전
실행기가 있으면 `seelegent-node`로 남습니다. 설치 전에 이전 데몬을 멈추고, `seelegent migrate --dry-run`으로
가져올 내용을 미리 확인하세요. [docs/setup.md](docs/setup.md#seelegent-migrate)를 보세요.

## 문제 해결

- `seelegent doctor`는 데이터 폴더, 설정, 데이터베이스, 데몬, git, 켜진 각 워커 CLI, 계정, PATH를 검사하고 실패한
  항목을 고치는 법을 알려 줍니다. 데이터 폴더에서의 쓰기 테스트(`.doctor-write-test`, 바로 삭제) 말고는 아무것도
  바꾸지 않습니다.
- `seelegent status`는 데몬, 계정과 할당량, 처리할 작업을 보여 줍니다. `seelegent restart`는 데몬을 다시 시작합니다.
  로그는 `~/.seelegent/logs/daemon.log`입니다.
- `seelegent setup --dry-run`은 각 리드 호스트에 무엇이 등록되어 있는지 보여 줍니다. setup이나 업데이트 뒤에는
  호스트를 다시 시작하세요.
- "Seelegent is not available inside a Seelegent worker."는 워커가 Seelegent를 호출하려 했다는 뜻이며, 의도적으로
  거부한 것입니다.
- **처리할 일**에 있는 작업은 배너에 이유를 보여 줍니다. 질문에 답하거나, 계정에 로그인하거나, 위험한 파일을
  복원하거나, 게이트가 실패했다면 추가 요청을 보내세요.

## 문서

모든 문서(영어)는 [docs/README.md](docs/README.md)에 정리되어 있습니다: 사용자를 위한 시작 가이드, 리드를 위한
참고 문서, 명령줄, HTTP API, 기여자 가이드.

## 라이선스

Seelegent는 소스 공개(source-available) 소프트웨어이며
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE)로 배포됩니다.

- 회사 내부를 포함해 어떤 목적으로든 사용, 수정, 공유할 수 있고, 이를 이용해 자신의 제품을 만들어 팔 수도 있습니다.
- Seelegent 자체나 그 수정본을, 이와 경쟁하는 상용 제품이나 서비스로 다른 사람에게 제공할 수는 없습니다.
- 각 버전은 출시 2년 뒤 Apache License 2.0으로도 사용할 수 있게 됩니다.

정확한 조건은 [LICENSE](LICENSE) 파일에 있습니다. Seelegent와 함께 배포되는 제3자 저작물은
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)에 나와 있습니다.
