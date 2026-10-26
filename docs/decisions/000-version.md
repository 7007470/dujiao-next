# 000 固定上游版本

日期：2026-10-25　状态：**已确认**（原版测试基线 2026-10-26 全部通过）

## 基线

| 项 | 值 |
|---|---|
| 开发仓库 | `https://github.com/7007470/dujiao-next`（`dujiao-next/dujiao-next` 的 fork，带完整历史，1082 个提交） |
| 基线标签 | `upstream-base` |
| 基线 commit | `d2e44618de03068cbbe874a23a6d7c6a249a79ec`（2026-09-16 15:16 +0800，"Merge pull request #309 from SzeMeng76/fix/skip-upstream-inventory-alerts"） |
| 基线 tree | `d4e7b76d74ec3f6ba55f6ad3c3df63acd5188cac`：和之前静态阅读用的快照 `dujiao-next-main/` 逐字节一致，所以 `docs/game-business-design.md` §6 的核对结果直接适用 |
| 官方 release tag | 基线 commit 上没有 tag（fork 没带 tag）；需要时可在本机 `git fetch https://github.com/dujiao-next/dujiao-next.git --tags` 后用 `git describe --tags upstream-base` 查 |
| `version.Version` | 源码里写的 `v1.0.0` 是占位值，release 构建通过 ldflags 注入 tag（`.goreleaser.yaml:36`） |

## 工具链（来自源码）

| 项 | 值 | 来源 |
|---|---|---|
| Go | 1.26.5 | `go.mod`；CI 用 `go-version-file: go.mod` |
| Node / pnpm | 24.11.1 / 10.34.3 | `.github/workflows/ci.yml`、`frontend/*/package.json` |
| 数据库 | SQLite（默认）/ PostgreSQL | `config.yml.example` |
| Redis | 可选（asynq），示例配置里默认开启 | README、`config.yml.example` |

## 原版测试基线（2026-10-26，用户本机）

在 `upstream-base` 上按上游 CI 的顺序跑检查，结果全部通过：

| 检查 | 结果 |
|---|---|
| `go mod download` | PASS |
| `gofmt -l`（git 跟踪的 .go 文件） | PASS（没有未格式化的文件） |
| `go vet ./...` | PASS |
| `go test -count=1 ./...` | PASS：187 个包通过，170 个包没有测试，0 失败 |
| `go build ./cmd/server` | PASS |

环境：macOS 26.5.1 arm64、Go 1.26.5、Node 26.0.0（上游 CI 用 24.11.1）、Redis 8.6.3；本机没有 pnpm 和 PostgreSQL。完整日志：`docs/test-results/s0-baseline-go.txt`、`s0-toolchain.txt`。

所以之后 `boost/dev` 上任何 vet / test 失败都是我们引入的。前端（pnpm）和 PostgreSQL 的基线等用到时再补：S4 并发测试需要 PostgreSQL，S7 需要 pnpm。

## 决定

1. `main` 保持原版、跟随 fork 的同步，不直接开发；基线打标签 `upstream-base`；代练开发在 `boost/dev`（和从它拉出的功能分支）进行。`AGENTS.md`、`docs/`、`prototype/` 放在仓库根目录，不和上游目录冲突。
2. 所有对原版代码的改动都能用 `git diff upstream-base boost/dev -- cmd internal frontend` 审查，核心代码改动标注 `// BOOST-EXT:`。
3. 仓库是官方仓库的 fork，历史相连，升级上游可以直接 merge 新的官方 commit；每次升级都新开一条决策记录，不在本文件里改版本号。
4. 推送由用户在本机执行（开发环境不能访问 GitHub）。
