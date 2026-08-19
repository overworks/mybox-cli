# mybox-cli

네이버 [MYBOX](https://mybox.naver.com)를 명령줄에서 다루는 도구입니다.
Go로 작성된 단일 정적 바이너리이며 런타임 의존성이 없습니다.

*[English](README.md)*

명령줄 도구 자체의 메시지는 영어입니다. 이 문서만 한국어입니다.

```console
$ mybox ls /문서/2026
1월/
2월/
회의록.pdf

$ mybox up ./보고서.pdf /업무자료
$ mybox search files --category document --since 2026-01-01
$ mybox df
```

## 설치

```console
$ go install github.com/overworks/mybox-cli/cmd/mybox@latest
```

소스에서 빌드하려면 Go 1.26 이상이 필요합니다.

```console
$ git clone https://github.com/overworks/mybox-cli.git
$ cd mybox-cli && make build
```

## 시작하기

MYBOX 웹 > **설정 > 계정 및 개인 액세스 토큰 관리**에서 토큰을 발급하세요.
토큰은 발급 시 **한 번만** 노출되니 바로 복사해야 합니다.

```console
$ mybox auth login
Personal access token: (입력은 표시되지 않습니다)
signed in · 5.0 GiB of 30.0 GiB used (16.7%)
```

토큰은 `~/.config/mybox/config.json`에 소유자만 읽을 수 있는 권한(0600)으로
저장됩니다. CI 등에서는 환경 변수를 쓸 수 있습니다.

```console
$ export MYBOX_TOKEN=mbx_pat_xxxxxxxx
$ mybox df
```

## 경로와 ID

MYBOX API에는 **경로를 ID로 바꾸는 기능이 없습니다.** 목록 API는 상위 폴더 ID만
알려줄 뿐 경로를 주지 않습니다. 그래서 `mybox`는 `/문서/2026`을 만나면 루트를
조회해 `문서`를 찾고, 그 안을 조회해 `2026`을 찾는 식으로 한 단계씩 내려갑니다.

각 단계가 API 호출 한 번이고 대부분의 API는 분당 60회 제한이 있으므로, 해석
결과는 계정별로 로컬에 캐시됩니다(기본 24시간). 이미 ID를 알고 있다면 `id:`
접두사로 해석을 건너뛸 수 있습니다.

```console
$ mybox ls /문서/2026          # 루트부터 해석 (캐시됨)
$ mybox stat id:hV3sQ9pLzR2m   # 해석 없이 바로 조회
$ mybox cache info             # 캐시 상태
$ mybox cache clear            # 웹에서 파일을 옮겼다면 비우기
```

## 명령

### 조회

| 명령 | 설명 |
|---|---|
| `mybox df` | 용량, 파일 종류별 개수, 최대 업로드 크기 |
| `mybox ls [경로]` | 폴더 내용. `-l` 상세, `-a` 숨김 포함, `--sort` |
| `mybox stat 경로` | 파일·폴더 속성 |
| `mybox search files [검색어]` | 파일 검색. `--category`, `--in`, `--since`, `--until` |
| `mybox search folders [검색어]` | 폴더 검색. `--path`, `--in` |

### 파일 조작

| 명령 | 설명 |
|---|---|
| `mybox up 로컬파일... [대상폴더]` | 업로드. `--overwrite`, `--resume` |
| `mybox down 경로...` | 다운로드. `-o 디렉터리`, `-o -`(표준 출력) |
| `mybox mkdir [-p] 경로` | 폴더 생성 |
| `mybox cp 원본 대상` | MYBOX 내부 복사. `--name`, `--overwrite` |
| `mybox mv 원본 대상` | 이동(필요하면 이름 변경도 함께) |
| `mybox rename 경로 새이름` | 이름 변경. ID는 유지됨 |
| `mybox rm 경로...` | 휴지통으로 이동 |
| `mybox star` / `unstar` | 즐겨찾기 추가·해제 |

로컬 ↔ MYBOX 전송은 `up`/`down`이고, `cp`는 MYBOX 내부 복사 전용입니다.

### 휴지통

| 명령 | 설명 |
|---|---|
| `mybox trash ls` | 목록 |
| `mybox trash restore 대상` | 원래 위치로 복원 |
| `mybox trash rm 대상...` | 개별 영구 삭제 |
| `mybox trash empty` | 비우기 |
| `mybox trash autodelete [일수]` | 자동 삭제 주기 조회·설정 (0, 5, 15, 30, 50) |

휴지통 항목에는 경로가 없으므로 `trash ls`가 보여주는 `id:` 또는 이름으로
지정합니다. 이름이 중복되면 임의로 고르지 않고 중단합니다.

### 전역 옵션

| 옵션 | 설명 |
|---|---|
| `--json` | 결과를 JSON으로 출력 |
| `--quiet`, `-q` | 부가 메시지 숨김 |
| `--verbose`, `-v` | HTTP 요청 기록 (토큰은 마스킹) |
| `--token`, `--profile` | 인증 지정 |
| `--no-cache` | 경로 캐시 사용 안 함 |
| `--rate` | 호출 한도 재정의 ([아래](#호출-한도)) |
| `--timeout` | 전체 제한 시간 |

## 스크립팅

부가 메시지는 stderr로 나가므로 stdout은 항상 결과만 담습니다.

```console
$ mybox --json ls /문서 | jq -r '.[] | select(.type=="file") | .name'
$ mybox --json df | jq '.usedBytes / .quotaBytes * 100'
```

종료 코드로 실패 원인을 구분할 수 있습니다.

| 코드 | 의미 |
|---|---|
| 0 | 성공 |
| 1 | 일반 오류 |
| 2 | 사용법 오류 |
| 3 | 인증 실패 (401, 403) |
| 4 | 대상 없음 (404) |
| 5 | 호출 한도 초과 (429) |
| 6 | 저장 공간 부족 (507) |
| 130 | 중단됨 (Ctrl-C) |

## 호출 한도

MYBOX는 요금제에 따라 한도가 다른데, **어떤 요금제인지 알아낼 방법을 API가 주지
않습니다.** 그래서 문서상 가장 낮은 값에 맞춰 호출을 조절합니다.

| 그룹 | 기본값(분당) | 해당 API |
|---|---|---|
| `default` | 60 | 목록, 조회, 생성, 복사, 이동, 이름 변경, 즐겨찾기, URL 발급 |
| `search` | 10 | 파일·폴더 검색 |
| `delete` | 60 | 휴지통 이동, 영구 삭제 |
| `restore` | 180 | 휴지통 복원 |

180GB 이상 요금제는 API 1개당 240회/분(검색은 30회/분)이므로 그대로 두면
불필요하게 느립니다. `--rate`로 올리세요. 맨 앞의 숫자가 모든 그룹의 기준값이고
`그룹=숫자`가 개별로 덮어씁니다.

```console
$ mybox --rate 240,search=30 ls /사진   # 180GB 이상 요금제
$ mybox --rate 0 ls /사진               # 조절 끄기 (429는 서버가 알려줌)
```

매번 붙이기 번거로우면 프로파일에 저장하세요.

```json
{
  "defaultProfile": "default",
  "profiles": {
    "default": {
      "token": "mbx_pat_...",
      "limits": { "default": 240, "search": 30, "delete": 240, "restore": 240 }
    }
  }
}
```

적용된 값은 `mybox auth status`로 확인할 수 있습니다. 한도를 실제보다 높게 잡으면
서버가 429로 거부하는데, 그때는 `Retry-After`를 존중해 백오프하며 재시도합니다.

## 프로파일

계정이 여러 개라면 프로파일로 나눠 관리합니다.

```console
$ mybox --profile work auth login --set-default
$ mybox --profile personal auth login
$ mybox auth list
$ MYBOX_PROFILE=work mybox df
```

## 알려진 제약

- **암호 폴더와 공유받은 폴더**는 Open API가 지원하지 않습니다(PC웹·모바일 앱 전용).
- **다운로드는 일 단위 한도**가 있습니다(요금제에 따라 500~50,000회/일).
- 토큰 유효기간은 **최대 180일**입니다. 만료 전에 새로 발급해야 합니다.
- **삭제해도 `stat id:...`로는 계속 읽힙니다.** 휴지통으로 옮기면 상위 폴더만
  바뀔 뿐 ID는 살아 있습니다. 경로로 조회하면 정상적으로 "찾을 수 없음"이 납니다.
- **`--resume`과 `--overwrite`는 함께 쓸 수 없습니다.** 덮어쓰기를 요청하면
  MYBOX가 파일을 처음부터 다시 쓰겠다는 뜻으로 받아들여 이어올리기 지점이 0으로
  보고됩니다. 함께 주면 `--overwrite`를 무시합니다.

업로드 전송 형식은 네이버가 문서화하지 않았지만 실제 서비스에서 확인되어
[docs/api-reference.md](docs/api-reference.md#storage-transfer-protocol)에
정리해 두었고, `mybox`가 그 형식을 기본으로 씁니다. 평소에는 신경 쓸 일이
없지만, 형식이 바뀌어 업로드가 400/404로 거부되면 다시 실측할 수 있습니다.

```console
$ mybox debug upload-probe /tmp/probe.txt --dest /임시 --cleanup
$ mybox up ./파일 /대상 --strategy post-raw
```

## 환경 변수

| 변수 | 용도 |
|---|---|
| `MYBOX_TOKEN` | 개인 액세스 토큰 |
| `MYBOX_PROFILE` | 사용할 프로파일 |
| `MYBOX_CONFIG_HOME` | 설정 디렉터리 (기본 `~/.config/mybox`) |
| `MYBOX_CACHE_HOME` | 캐시 디렉터리 (기본 `~/.cache/mybox`) |
| `MYBOX_API_BASE` | API 주소 재정의 |
| `MYBOX_UPLOAD_STRATEGY` | 업로드 전송 형식 |

## 자동완성

```console
$ mybox completion zsh > "${fpath[1]}/_mybox"
$ mybox completion bash | sudo tee /etc/bash_completion.d/mybox
$ mybox completion fish > ~/.config/fish/completions/mybox.fish
```

## 문서

- [docs/api-reference.md](docs/api-reference.md) — 엔드포인트 20개와, 네이버가
  문서화하지 않아 실측으로 확인한 스토리지 전송 프로토콜 (영문).
  네이버 원문은 <https://developers.mybox.naver.com/> 입니다.

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 보세요(영문). 빌드·테스트 방법, 실계정
테스트 규칙, 커밋 관례가 정리되어 있습니다.

## 라이선스

MIT
