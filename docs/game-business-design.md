# 自动代练详细设计

> 版本 2026-10-23。只设计自动代练。会员、钱包、订单、支付、退款复用 Dujiao-Next。
> 功能 JSON 格式见 `docs/boost-feature-schema.md`；可运行的业务规则参考见 `prototype/`。
> 下文"源码事实"来自对基线 `upstream-base`（`d2e44618`）的静态阅读（未编译运行），改代码后行号会漂移，动手前重新确认。

## 1. 业务流程

```text
管理员                          用户                                   Worker
───────────────────────────────────────────────────────────────────────────────
新建游戏（草稿）
添加区服
新建代练功能（JSON）→ 校验 → v1（草稿）
后台预览 → 上架
游戏上线 ──────────────────→ 看到游戏和功能
                               购买积分包（Dujiao 订单 + 支付）
                               支付成功 → 积分到账（幂等）
                               添加游戏账号 → PENDING ─────────────→ 领取绑定
                                            ← WAIT_INPUT（要验证码）←┘
                               提交验证码 → PENDING ────────────────→ 再次领取 → 成功
                               选功能 → 自动生成的表单（关卡/模式/次数）
                               实时报价（服务端同一套计算）
                               提交 → 冻结积分 + 任务 PENDING ─────→ 领取（租约 90s）
                                                                     心跳 / 进度 / 事件
                               查看步骤进度、日志 ←──────────────────┘
                               （可取消：执行中需 Worker 确认）       完成 / 失败 / 确认取消
                               结算：消耗已完成部分，其余退回
```

## 2. 数据模型

### 2.1 游戏与功能

```text
boost_game
  id, code UNIQUE, name, short_name, color, sort, status(draft/active/hidden), version, created_at, updated_at

boost_game_server
  id, game_id, code, name, platform(all/android/ios/pc), status(active/disabled)
  UNIQUE(game_id, code)

boost_feature
  id, game_id, code, status(draft/active/disabled), sort, version
  definition_json        -- 当前版本，已经过 normalize（补全默认值）
  created_at, updated_at
  UNIQUE(game_id, code)

boost_feature_version
  feature_id, version, definition_json, admin_id, created_at
  PRIMARY KEY(feature_id, version)
```

### 2.2 积分

```text
boost_point_pack                   -- 每个积分包对应一个 Dujiao 商品 / SKU
  id, product_id, sku_id, name, price, points, bonus, valid_days NULL, active

boost_point_account
  user_id PK, available bigint ≥ 0, frozen bigint ≥ 0, version

boost_point_lot                    -- 积分批次，支持到期、按来源撤回
  id, user_id, source_type(order/grant/promo), source_ref, amount, remaining, expires_at NULL
  status(active/exhausted/expired/revoked), suggest_refund NULL, created_at
  UNIQUE(source_type, source_ref)

boost_point_ledger
  id, user_id, type(purchase/grant/hold/release/consume/expire/revoke/admin_adjust)
  delta_available, delta_frozen, available_after, frozen_after
  lot_allocations_json, biz_key UNIQUE, task_id NULL, order_id NULL, operator_admin_id NULL, remark, created_at
```

业务号格式：`order:order_item:{id}`、`grant:admin:{admin}:{request}`、`hold:task:{id}`、`consume:task:{id}`、`release:task:{id}`、`expire:lot:{id}`、`revoke:order_item:{id}`。

### 2.3 绑定与任务

```text
boost_user_binding
  id, user_id, game_id, server_id, display_name, login_masked
  login_fingerprint      -- sha256(game_id + 小写登录名)；同一用户未失败/未取消的绑定内唯一
  credential_cipher NULL, key_version   -- 解绑后置空
  status, attempt, max_attempts, available_at, last_error_code, user_message, session_ref_cipher
  worker_id, lease_token, lease_expires_at, verified_at, deleted_at, created_at, updated_at

boost_input                        -- WAIT_INPUT / WAIT_USER 向用户索取的输入
  id, target(binding/task), target_id, kind(sms_code/email_code/captcha/confirm), prompt
  value_cipher NULL      -- 下发给 Worker 后立即清空
  status(waiting/submitted/consumed/expired), expires_at, created_at

boost_task
  id, task_no UNIQUE, user_id, binding_id, game_id, feature_id
  feature_version, snapshot_json       -- 名称、capability、refund_policy、timeout、折扣率
  params_json                          -- compile 后的规范化参数
  plan_json                            -- steps / fixed / fixed_points / raw_total / total / rate
  summary, units, units_done
  points_held, points_consumed, points_released
  status, cancel_requested, progress_percent, progress_text, result_json, error_code
  attempt, max_attempts, available_at, deadline_at
  worker_id, lease_token, lease_expires_at, started_at, finished_at
  idempotency_key, UNIQUE(user_id, idempotency_key)
  created_at, updated_at
  INDEX(status, game_id, available_at)

boost_task_event
  id, task_id, seq, type(state/log/input_request), message(已脱敏), data_json, created_at
  UNIQUE(task_id, seq)
```

### 2.4 Worker

```text
worker_node
  id, name, api_key UNIQUE, secret_cipher, key_version
  capabilities_json      -- [{"game":"starfall","features":["bind","main_stage"]}]
  max_concurrency, status(active/disabled), last_seen_at, last_ip, version

worker_lease
  token PK, kind(task/bind), target_id, worker_id, active, expires_at, closed_at, close_reason

worker_request             -- request_id 去重
  worker_id, request_id, response_json, created_at
  PRIMARY KEY(worker_id, request_id)
```

Nonce 防重放存 Redis（`SETNX`，120 秒）。

## 3. 关键规则

### 3.1 报价与创建任务

1. 读取功能当前定义，`compile(definition, params, {discountRate})`。
2. 有字段错误 → `PARAMS_INVALID`，不冻结。
3. 检查绑定属于本人且 `SUCCESS`、功能和绑定是同一游戏、未超 `max_active_per_user`。
4. 同一事务：插入任务（保存版本号和计划快照）→ 按批次 FIFO 冻结积分 → 写 `hold` 流水。
5. 幂等键重复返回原任务。

### 3.2 结算

```text
已完成部分 = pointsForUnits(plan, units_done)      -- 见 boost-feature-schema.md §5
成功 / 取消：消耗 = 已完成部分
失败：按 refund_policy 和错误码分类
退回 = 冻结 − 消耗；按冻结时批次的逆序退回；批次已过期则退回即失效（写 expire 流水）
```

### 3.3 错误码（服务端权威表）

| code | 可重试 | 分类 | 附加动作 |
|---|---|---|---|
| `NETWORK` / `GAME_MAINTENANCE` / `WORKER_CRASH` / `LEASE_EXPIRED` | 是 | 系统 | |
| `LOGIN_FAILED` / `ACCOUNT_BANNED` / `PASSWORD_CHANGED` | 否 | 系统 | 绑定置 `FAIL` |
| `INPUT_TIMEOUT` | 否 | 系统 | |
| `STAGE_LOCKED` / `NOT_UNLOCKED` / `INSUFFICIENT_STAMINA` / `USER_KICKED` / `TIMEOUT` | 否 | 用户 | |

可重试错误在 `attempt < max_attempts`（默认 2）且未超时时回到 `PENDING`，退避 `30s × 2^(attempt-1)`。

### 3.4 回收（每 30 秒）

- 租约过期：未超次数 → `PENDING`；否则 `FAIL(LEASE_EXPIRED)`。
- 输入过期（默认 5 分钟）：任务 `FAIL(INPUT_TIMEOUT)` 全额退；绑定 `FAIL`。
- 超过 `deadline_at`：`FAIL(TIMEOUT)`。

积分到期每小时扫描一次。

## 4. Worker 接口

前缀 `/api/v1/worker`，所有请求带：

```text
X-Worker-Key:        api_key
X-Worker-Timestamp:  unix 秒（偏差 > 60s 拒绝）
X-Worker-Nonce:      随机串（120s 内不可重复）
X-Worker-Signature:  hex(HMAC-SHA256(secret, "{METHOD}\n{PATH}\n{TS}\n{md5(body)}"))
```

签名算法与 `internal/upstream/signer.go` 一致，头名不同。

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| POST | `/pull` | `{kinds:["task","bind"], max}` | `{items:[{kind, id, lease_token, lease_expires_at, attempt, game, credential, session_ref, input, task_no, feature, feature_version, params, steps, units, units_done}]}` |
| POST | `/leases/{token}/heartbeat` | `{units_done, progress_percent?, progress_text?}` | `{lease_expires_at, cancel_requested}` |
| POST | `/leases/{token}/events` | `{seq, type, message}` | `{ok}`（seq 去重） |
| POST | `/leases/{token}/need-input` | `{kind, prompt}` | `{input_id, status, expires_at}`（释放租约） |
| POST | `/leases/{token}/finish` | `{request_id, units_done, result?, session_ref?}` | `{status}` |
| POST | `/leases/{token}/fail` | `{request_id, error_code, units_done, message?}` | `{status, points_consumed, points_released}` |
| POST | `/leases/{token}/cancel-ack` | `{request_id, units_done}` | `{status, points_consumed, points_released}` |

领取规则：按 `created_at` 先进先出；能力匹配（游戏 code + 功能 capability，绑定用 `bind`）；同一绑定账号已有执行中任务时跳过；受 `max_concurrency` 限制。PostgreSQL 用 `FOR UPDATE SKIP LOCKED`，其它数据库用"按 id 条件更新 + 检查影响行数"。

`credential` 只在 `pull` 响应里出现一次，日志中间件不记录该响应体。

## 5. 用户端和管理端接口

```text
用户端（登录）
GET  /api/v1/boost/games                              上线的游戏（含区服）
GET  /api/v1/boost/games/{code}/features              上架功能（含当前定义）
POST /api/v1/boost/quote                              {feature_id, params} → compile 结果
POST /api/v1/boost/tasks                              {binding_id, feature_id, params, idempotency_key}
GET  /api/v1/boost/tasks  /tasks/{task_no}            列表 / 详情（步骤进度、最近事件）
POST /api/v1/boost/tasks/{task_no}/cancel
POST /api/v1/boost/tasks/{task_no}/input              {value}
GET  /api/v1/boost/bindings   POST /bindings   DELETE /bindings/{id}
POST /api/v1/boost/bindings/{id}/input                {value}
POST /api/v1/boost/bindings/{id}/confirm
GET  /api/v1/boost/points   /points/ledger   /points/lots
积分包购买走 Dujiao 原生商品下单和支付

管理端（JWT + RBAC，权限 boost:game:write / boost:feature:write / boost:points:write / boost:read）
CRUD /api/v1/admin/boost/games   /games/{id}/servers
POST /api/v1/admin/boost/games/{id}/status
POST /api/v1/admin/boost/features                     {game_id, definition}（新建）
PUT  /api/v1/admin/boost/features/{id}                {definition, version}（新版本）
POST /api/v1/admin/boost/features/{id}/status   /reorder
GET  /api/v1/admin/boost/features/{id}/versions
POST /api/v1/admin/boost/features/validate            {definition} → 校验结果（编辑器实时用）
GET  /api/v1/admin/boost/tasks   POST /tasks/{no}/cancel   POST /tasks/{no}/retry
GET  /api/v1/admin/boost/bindings
CRUD /api/v1/admin/boost/point-packs
POST /api/v1/admin/boost/points/grant   /points/deduct
POST /api/v1/admin/boost/orders/{order_no}/revoke-points
CRUD /api/v1/admin/worker/nodes   POST /nodes/{id}/rotate-secret
```

## 6. 接入 Dujiao-Next 的改动点

源码事实：Dujiao-Next 没有事件总线，支付成功后 `payment/application/payment_service_callback_dispatch.go` 的 `enqueueOrderPaidAsync` 直接调用各下游服务（返利、会员升级、自动发货、`procurementSvc.CreateForOrder`）。上游采购（`procurement`）链路是最接近的模板。

2026-10-23 已对快照（tree `d4e7b76…`，见 `docs/decisions/000-version.md`）逐条核对，行号以该快照为准。`modules/` 指 `internal/modules/`。

| # | 位置（已核对） | 现状 | 改动 |
|---|---|---|---|
| C1 | `internal/constants/constants.go:24-30` | 只有 `auto` / `manual` / `upstream` | 新增 `FulfillmentTypeBoostPoints = "boost_points"` |
| C2 | `modules/catalog/product/domain/purchase_policy.go:40-53` `NormalizeFulfillmentType` | switch，未知类型返回 `""` | 接受新类型。注意 `modules/notification/application/format/order_format.go:105` 有同名函数，需同步 |
| C3 | `modules/order/application/order_service_validate.go:191-196` | if 链（不调用 C2），三种之外返回 `ErrFulfillmentInvalid`；198/203 行是 manual / upstream 库存检查，258 行写快照 | 放行新类型，不走库存检查 |
| C4 | `modules/payment/application/payment_service_rules.go:126-140` `shouldMarkFulfilling` | `""` / manual / upstream 返回 true | 新类型返回 true |
| C5 | `modules/payment/application/payment_service_callback_dispatch.go:22-112` `enqueueOrderPaidAsync` | 顺序：返利 → 状态邮件 → 通知 → bot → 会员升级 → **队列未启用则 return** → 子订单循环（人工待发货 / 自动发货）→ `enqueueProcurementAsync`（≈115 行，忽略 `ErrExists`）→ 下游回调 | 在 `enqueueProcurementAsync` 旁调用 `boostSvc.HandleOrderPaid`；注入仿 `payment_service.go:76` `ProcurementCreator` 接口和 `:95` `SetProcurementService`。`internal/architecture/payment_service_structure_test.go:16` 列出了 setter，需同步 |
| C6 | `internal/app/container/services_integration.go:116-127` 构造，`services_wiring.go:8` 注入，字段在 `container.go:184` | | 构造并注入代练服务 |
| C7 | `internal/bootstrap/database/migrations/registry.go:45-95` 单个 `db.AutoMigrate(...)`，最后一项 `:95` | | 追加代练模型 |
| C8 | `internal/app/httpserver/routes_admin.go:87-97`（`authorized` / `paymentProtected`）、`routes_storefront.go:116-117`（`user`）；具体路径在各模块 `transport/http/routes.go` | 还有 `routes_channel.go`、`routes_upstream.go` | 注册用户端、管理端；Worker 路由新建文件 |
| C9 | `internal/authz/bootstrap.go:14` `BuiltinRoleSeeds()`；测试 `internal/app/httpserver/rbac_coverage_test.go:32` | 测试静态解析 `routes_admin*.go` 和 `modules/**/routes.go` 里**字面量路径**，每条都须被某个种子匹配 | 新管理路由写字面量路径并加种子 |
| C10 | `internal/app/jobs/consumer/consumer_core.go:35-55` `mux.HandleFunc`；任务名 `internal/queue/tasks.go` + `internal/constants`；定时 `internal/app/jobs/service.go:51` `registerPeriodicTasks`，`scheduler.Register("@every 1m", ...)` | | 注册 `boost:grant_points`、`boost:reap`（`@every 30s`）、`boost:expire_points`（`@every 1h`） |
| C11 | `modules/order/application/refund/wallet.go:33` `AdminRefundToWallet`、`refund/service.go:253` `AdminManualRefund` | **只有管理端退款，没有用户端退款入口**；另外管理端通用状态接口 `PATCH /orders/:id` → `modules/order/application/order_service_child.go:159` `UpdateOrderStatus` 可以把订单直接改成 `refunded` / `partially_refunded`（不动钱） | 两个退款函数在事务内 `GetByIDForUpdate` 之后拒绝"剩余积分未撤回"的积分包订单；`UpdateOrderStatus` 对积分包订单的退款目标状态做同样检查 |

其它核对结果：`fulfillments.order_id` 唯一索引在 `modules/fulfillment/domain/fulfillment.go:27`，可作到账幂等。`internal/workflows/giftcardredeem` **只有 `infrastructure/gormuow/runner.go`，没有 application 层**（用例端口放在 giftcard 模块的 contract 里），`boostpoints` 照此结构。分层测试还引用了 `workflows/checkout/infrastructure/gormuow/unit_of_work.go`，但快照中不存在。

所有改动标注 `// BOOST-EXT:`，升级时按此表重放。到账写 `fulfillments`（`order_id` 唯一，可作幂等），payload 只写"已到账 N 积分"。

新代码位置（以 `internal/architecture/dependencies_test.go` 的分层规则为准：只有 `infrastructure/gormstore` 可以导入 GORM，`application` 不能导入 gin / asynq）：

```text
internal/modules/boost/          domain / application / contract / infrastructure / transport
internal/modules/workerapi/      Worker 签名认证和路由
internal/workflows/boostpoints/  积分到账（订单 + 积分账本同事务，参照 workflows/giftcardredeem）
```

## 7. 页面

**用户端：** 发起代练（游戏页签 → 功能列表 → 选择账号 → 按 JSON 生成的表单 + 右侧实时报价）；我的任务（步骤进度、日志、验证码输入、取消）；我的游戏账号（添加、验证码、确认、解绑）；积分（余额、30 天内到期、积分包、批次、流水）。

**管理端：** 游戏与代练功能（游戏列表、区服、功能列表、JSON 编辑器 + 校验 + 用户端实时预览 + 执行端步骤预览 + 版本历史、模板新建）；代练任务；积分与订单；执行端（含"已上架功能无执行端可处理"告警）；审计；一致性校验。

字段错误只在用户改动过该字段或点击提交后显示；后台预览直接显示全部错误。

## 8. 必测场景

`prototype/test/` 的 47 个场景是基准，Go 版本都要有对应测试，另外补数据库层并发：

- 功能定义：全部校验规则、编译、隐藏字段、模式定价、禁用关卡、上限、折扣单调性；
- 后台：游戏上线条件、code 不可改、权限、非法 JSON 不留半成品、版本冲突、旧任务按旧快照结算；
- 积分：并发冻结不超额、发放幂等、FIFO、到期、退回到已过期批次、积分包重复回调只到账一次、退款前撤回；
- 任务：参数错误不冻结、并发上限、幂等、部分完成按步骤价格结算、系统失败全退、取消两种路径、完成量截断；
- Worker：并发领取只有一个拿到、租约过期 `LEASE_LOST`、多次失联失败、重复完成只结算一次、同绑定串行、能力匹配、签名 / 时间戳 / 重放 / 篡改 / 停用 / 轮换；
- 绑定：验证码、游戏内确认、超时、解绑销毁凭据；
- 安全：日志和任务事件脱敏；
- 重启：状态恢复后旧租约可继续完成；
- 随机压力：随机操作后全部不变量成立；
- **Go 补充：** PostgreSQL 下 20 个并发请求冻结同一账户、两个进程并发领取同一任务。

## 9. 风险

- **合规：** 代练通常违反游戏厂商用户协议，部分地区有监管要求，上线前需自行评估（不是法律意见）。
- **GPL-3.0：** 修改版对外分发要遵守 GPL 义务。
- **升级冲突：** §6 的核心改动需要随 Dujiao-Next 升级重放。
- **执行端：** 每个功能的 `capability` 需要真实 Worker 程序支持；后台只能配置界面和价格，不能凭空产生执行能力。
- **未验证：** 以上源码事实是静态阅读，已确认对应基线 `upstream-base`；但还没有编译运行，行为要以 S0 基线测试和实际运行为准。
