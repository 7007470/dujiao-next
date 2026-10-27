# Dujiao-Next 自动代练开发计划

> 用于新的独立仓库。先开发、先测试，暂不迁移旧用户、订单、余额或账号。
> 规则见 `AGENTS.md`；设计见 `docs/game-business-design.md`；功能 JSON 见 `docs/boost-feature-schema.md`。

## 1. 当前进度（新会话从这里开始）

| 项 | 状态 |
|---|---|
| 范围 | 只做自动代练；账号交易暂缓（见 `docs/decisions/001-scope-boost-only.md`） |
| 业务规则 | 已在 `prototype/` 用 JS 实现并验证：47 个自动化测试全部通过，用户已确认"基本符合要求" |
| 功能 JSON 规范 | `docs/boost-feature-schema.md`，实现 `prototype/src/catalog.js` |
| Go 实现 | S0–S3 完成，另补了游戏图标和用户端浏览 / 报价页面；S4 积分起未开始 |
| 仓库 | `https://github.com/7007470/dujiao-next`（官方仓库的 fork，带完整历史）。`main` 是原版；基线标签 `upstream-base` = `d2e44618`（2026-09-16），和之前读的快照逐字节一致；开发分支 `boost/dev` |
| S0 进度（2026-10-26） | 已完成：版本固定、分支和标签已推送、文档导入、§6 改动点核对、原版 Go 基线**全部通过**（187 个包 ok，0 失败）。还差：启动测试实例、登录、模拟支付（可以和后续阶段并行） |
| S1 进度（2026-10-26） | **完成**：15 张 `boost_` / `worker_` 表、迁移入口、约束测试（见 `docs/decisions/002-s1-schema.md`）。gofmt / vet / build 通过；全量 `go test` 189 个包 ok（基线 187 + 新增 2），0 失败；变异检查确认约束测试能抓到索引失效 |
| 环境 | 开发助手用 `.tools/`（外层目录）里的 Linux Go 1.26.5 和离线模块缓存自己编译测试；`git push` 仍由用户执行。新增依赖时要用户在 Mac 上重新 `go mod download` 到 `.tools/gomod` |
| S2 进度（2026-10-26） | **完成**：`internal/modules/boost/domain/featuredef`，与原型逐字节一致（316 条校验 + 402 条编译对照用例，见 `docs/decisions/003-featuredef-go.md`） |
| 开发方式（用户 2026-10-26 选定） | 每块后端做完就同时做对应的后台 / 用户页面，每一步都能在浏览器里验收；不再等到 S7 统一做页面 |
| S3 进度（2026-10-27） | **完成**：后台接口 `/api/v1/admin/boost/*`（游戏 / 区服 / 功能 / 版本 / 校验 / 预览），`operations` 角色种子，写操作记入权限审计日志；后台菜单"代练管理"，含游戏列表、区服管理、功能 JSON 编辑器（实时校验、错误定位、6 个示例、版本历史、下单表单实时预览）。全量 `go test` 191 包 ok；`vue-tsc` 通过；真实二进制 + SQLite 冒烟通过。浏览器里未验证（沙箱没有浏览器） |
| 用户反馈（2026-10-27） | 已补：① 游戏图标（`icon_url`，后台用媒体库上传）；② 用户端页面：导航"代练"→ `/boost` 游戏列表、`/boost/:code` 选功能 + 自动表单 + 服务端实时报价（登录用户按会员等级打折）。下单按钮暂提示"即将开放"，等 S4/S5 |
| 下一步唯一建议 | 用户浏览器验收图标和用户端页面；然后 S4 积分（积分包商品、支付到账、账本、到期），S5 绑定账号 + 下单把用户端串起来 |

原型验证命令：

```bash
node --test prototype/test/catalog.test.js prototype/test/engine.test.js prototype/test/ui-smoke.test.js
```

## 2. 阶段总览

| 阶段 | 交付物 | 难度 | 进入条件 |
|---|---|---:|---|
| S0 | 固定版本、Git 仓库、测试实例、版本决策记录 | 低 | 无 |
| S1 | 代练模块骨架、迁移、架构测试通过 | 中 | S0 |
| S2 | 功能 JSON 规范的 Go 实现（校验 / 编译 / 结算） | 中 | S1 |
| S3 | 后台：游戏、区服、功能（版本、上下架、校验接口） | 中 | S2 |
| S4 | 积分：积分包商品接入支付、账本、到期、发放、退款撤回 | 高 | S1 |
| S5 | 绑定 + 任务创建 / 取消 / 结算 | 高 | S2、S3、S4 |
| S6 | Worker 接口：签名、领取、租约、回收 | 最高 | S5 |
| S7 | 用户端和管理端页面（Vue 3） | 中高 | S3–S6 |
| S8 | 全链路验收：并发、幂等、安全、重启 | 高 | S7 |
| S9 | 真实数据迁移设计（只设计，不执行） | 高 | S8 冻结通过 |

S2 和 S4 可以并行。每个阶段都要有测试结果和决策记录。

## 3. S0：固定版本和测试环境

- 新建独立 Git 仓库；记录 upstream、release、commit、Go、Node、数据库和 Redis 版本。
- 建立 `docs/api/`、`docs/test-results/`；`docs/decisions/` 已存在。
- 使用独立测试数据库、Redis、支付配置、测试用户和测试 Worker。
- 重新核对 `docs/game-business-design.md` §6 列出的源码位置和行号。

验收：

```text
[ ] 固定 commit 能启动，管理员和测试用户能登录
[x] 原版 go test ./... 基线结果已存档（2026-10-26，全部通过，docs/test-results/）
[ ] 测试订单可创建、模拟支付、关闭；重复支付回调不重复处理
[ ] 测试环境没有旧库连接串
[x] docs/decisions/000-version.md 已记录（upstream-base = d2e44618）
[x] §6 改动点逐条确认存在或记录差异（2026-10-23，按快照 tree d4e7b76…）
```

2026-10-25：克隆下来的 `main` HEAD tree 和快照完全一致，直接作为基线，没有差异需要处理。

源码和文档不一致时暂停，固定实际可运行版本。

## 4. S1：模块骨架

```text
internal/modules/boost/          domain / application / contract / infrastructure / transport
internal/modules/workerapi/      Worker 签名认证和路由
internal/workflows/boostpoints/  积分到账
```

验收：

```text
[x] 迁移可重复执行（模块 Migrate 和中央 AutoMigrate 各跑两次）
[x] internal/architecture 的分层测试通过
[x] 新管理路由被 authz 角色种子覆盖（S1 不加路由；rbac_coverage_test 通过）
[x] 不修改 Dujiao 原表（测试断言只新增 boost_ / worker_ 表）
```

## 5. S2：功能 JSON 规范

把 `prototype/src/catalog.js` 移植为 Go（`internal/modules/boost/domain/featuredef`）：`ValidateDefinition`、`Normalize`、`Compile`、`PointsForUnits`、`StepProgress`。

验收：

```text
[x] prototype/test/catalog.test.js 的 15 个场景都有 Go 测试且通过（全部收进 golden.json）
[x] seed.js 6 个示例定义对照：JS 和 Go 的完整输出逐字段一致（不只是 total / units / steps）
[ ] 前端不再自己算价格：报价接口直接返回 Compile 结果（S3 预览接口 / S5 用户报价接口）
```

前端报价的做法已定（见 `docs/decisions/003-featuredef-go.md`）：前端调用服务端报价接口，不在浏览器里重复实现规则。

## 6. S3：后台游戏与功能

验收：

```text
[x] 游戏上线前必须有启用区服和已上架功能（上线后也不能下架最后一个区服 / 功能）
[x] 游戏 / 功能 code 不可修改；重复 code 被拒绝
[x] 非法定义不能保存且不留半成品；保存生成新版本；内容不变不升版本；版本冲突被拒绝
[x] 版本历史可查看和载入
[x] 每次管理操作写审计（authz_audit_logs，action = boost.*）
[ ] 用户在浏览器里验收后台页面
```

## 7. S4：积分

- 积分包作为 Dujiao 商品（`fulfillment_type = boost_points`），走原生下单和支付。
- 支付成功 → `boostSvc.HandleOrderPaid` → 作业 `boost:grant_points` → 同事务写积分批次 + 账本 + `fulfillments` + 订单完成。
- 退款入口：积分包订单需先撤回剩余积分。

验收：

```text
[ ] 重复支付回调只到账一次
[ ] 余额支付、在线支付、超时取消三条路径正确
[ ] 并发冻结同一账户不超额（PostgreSQL 真并发）
[ ] FIFO、到期、退回到已过期批次
[ ] 发放 / 扣减幂等并写审计
[ ] 退款前未撤回被拒绝；撤回只撤剩余；建议金额按比例
```

## 8. S5：绑定与任务

验收：

```text
[ ] 绑定：验证码、游戏内确认、超时、解绑销毁凭据
[ ] 创建任务：参数错误不冻结；游戏不匹配、未验证、并发上限、积分不足被拒绝；幂等
[ ] 任务保存功能版本和计划快照；后台改价不影响已创建任务
[ ] 结算：部分完成按步骤价格、系统失败全退、两种取消路径、完成量截断
[ ] 消耗 + 退回 = 冻结
```

## 9. S6：Worker 接口

验收：

```text
[ ] 签名错误 / 时间戳偏差 / 重放 / 篡改请求体 / 停用节点 / 轮换后旧密钥 → 401
[ ] 两个进程并发领取同一任务只有一个成功
[ ] 租约过期后旧 token 写入 LEASE_LOST；多次失联最终 FAIL
[ ] 重复 finish / fail / cancel-ack 结果一致、只结算一次
[ ] 同一绑定账号串行；能力不匹配拉不到
[ ] credential 只在 pull 响应出现，日志不记录
```

## 10. S7：页面

以原型（`prototype/index.html`）为界面和交互参考，用 Dujiao 的 Vue 3 前端实现：

- 用户端：发起代练、我的任务、我的游戏账号、积分；
- 管理端：游戏与代练功能（JSON 编辑器 + 校验 + 实时预览 + 版本历史 + 模板）、代练任务、积分与订单、执行端、审计。

验收：

```text
[ ] 所有表单由功能 JSON 生成，没有为某个游戏写死的页面
[ ] 字段错误只在改动或提交后显示；后台预览显示全部错误
[ ] 后台预览和用户端渲染结果一致
```

## 11. S8：全链路验收和冻结

```text
[ ] prototype/test/engine.test.js 的 28 个场景都有 Go 测试
[ ] 随机压力测试后不变量全部成立
[ ] 重启后进行中的任务和租约正确恢复
[ ] 安全检查：权限、脱敏、凭据加密、密钥版本
[ ] 至少一个真实 Worker 跑通一个功能
```

## 12. S9：真实数据迁移（只定义入口，不执行）

S8 通过后另开任务设计。迁移冻结前禁止连接生产数据。
