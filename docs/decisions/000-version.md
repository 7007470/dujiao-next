# 000 固定上游版本

日期：2026-10-25　状态：**已确认（版本）**；原版测试基线待本机补充

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

## 待本机补充：原版测试基线

在 `upstream-base` 上跑上游 CI 的同一组检查，结果存到 `docs/test-results/`：

```bash
git switch --detach upstream-base
mkdir -p ../s0 && { go version; node --version; pnpm --version; } > ../s0/toolchain.txt 2>&1
{ gofmt -l $(git ls-files '*.go'); go vet ./...; go test -count=1 ./...; go build -o /tmp/dujiao-next ./cmd/server; } > ../s0/baseline-go.txt 2>&1; echo "exit=$?" >> ../s0/baseline-go.txt
git switch boost/dev
mkdir -p docs/test-results && cp ../s0/*.txt docs/test-results/
```

先在 detached 的基线上跑，再切回 `boost/dev` 拷入结果，这样基线反映的是原版代码。原版自带的失败也照实记录，后面用来区分新引入的问题。

## 决定

1. `main` 保持原版、跟随 fork 的同步，不直接开发；基线打标签 `upstream-base`；代练开发在 `boost/dev`（和从它拉出的功能分支）进行。`AGENTS.md`、`docs/`、`prototype/` 放在仓库根目录，不和上游目录冲突。
2. 所有对原版代码的改动都能用 `git diff upstream-base boost/dev -- cmd internal frontend` 审查，核心代码改动标注 `// BOOST-EXT:`。
3. 仓库是官方仓库的 fork，历史相连，升级上游可以直接 merge 新的官方 commit；每次升级都新开一条决策记录，不在本文件里改版本号。
4. 推送由用户在本机执行（开发环境不能访问 GitHub）。
