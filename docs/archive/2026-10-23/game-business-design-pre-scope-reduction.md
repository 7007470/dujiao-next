# 游戏账号批发 + 积分代练 详细设计

> 版本：v0.1（2026-10-20）
> 前置阅读：`AGENTS.md`、`docs/dujiao-next-customization-plan.md`
> 本文是对计划阶段 2–9 的细化，所有"源码事实"均已在 `dujiao-next-main/` 中核对过文件和行号。行号会随升级漂移，改代码前重新确认。

---

## 0. 结论先行

1. Dujiao-Next 没有插件机制或事件总线。支付成功后，`payment` 模块在 `enqueueOrderPaidAsync` 里**直接调用**各个下游服务（返利、会员升级、自动发货、上游采购）。所以"零核心改动"做不到，只能做到"少量、集中、可回滚的核心改动"。
2. 最佳模板是现有的 `procurement`（上游采购）链路：订单支付 → `CreateForOrder` → 外部执行 → 回调 → 写 `fulfillments` → 订单完成。游戏账号交付和积分到账都照这条链路接入。
3. 新增两种交付类型：`game_account`（游戏账号，含指定购买和批发）和 `boost_points`（积分充值包）。代练本身**不是订单**，而是消耗积分的任务。
4. 积分采用独立的"非现金积分账本"，只能通过购买积分包、订阅赠送或管理员发放获得，**不能提现、不能转回钱包余额**。这样它不算"第二个钱包"，同时满足"消耗积分使用不同功能"的需求。
5. Worker 认证复用仓库已有的 HMAC 签名格式（`internal/upstream/signer.go`），任务租约放在游戏模块自己的表中。

---

## 1. 源码事实（设计依据）

| 主题 | 事实 | 位置 |
|---|---|---|
| 版本 | 无 git；`version.Version = "v1.0.0"`（默认值，非真实 release）；Go 1.26.5；Gin + GORM + asynq；Vue 3 + Pinia + vue-i18n | `go.mod`、`internal/version/version.go:5` |
| 订单状态 | `pending_payment / paid / fulfilling / partially_delivered / partially_refunded / delivered / completed / canceled / refunded` | `internal/constants/constants.go:4-14` |
| 交付类型 | 只有 `auto / manual / upstream` | `constants.go:24-30`、`catalog/product/domain/purchase_policy.go NormalizeFulfillmentType` |
| 金额类型 | `money.Amount`，库内 `decimal(20,2)` | `order/domain/order.go` |
| 订单拆分 | 下单时每个商品项都会生成子订单（`childOrder`），库存在子订单事务内预留 | `order/application/order_service.go:562-640` |
| 卡密预留 | 事务内 `FOR UPDATE` 取可用行 → `UPDATE ... WHERE status='available'` 并校验 `RowsAffected` | `order_service.go:610-627`、`cardsecret/infrastructure/gormstore/store.go:189,398` |
| 取消释放 | `ReleaseByOrder(orderID)` + 释放手动库存 + 退回钱包抵扣 | `order/application/order_service_child.go:47-70,380-395` |
| 订单超时 | 创建后 `EnqueueTimeoutCancel(order.ID, 过期分钟)`；`order.payment_expire_minutes: 15` | `order_service.go:720`、`config.yml.example:162` |
| 支付成功置状态 | 含 `manual/upstream` 项 → 子订单置 `fulfilling`，否则 `paid` | `payment/application/payment_service_rules.go:126 shouldMarkFulfilling`、`payment_service_callback.go:443-486` |
| 支付后分发 | 返利 → 邮件 → 通知 → 会员升级 `OnOrderPaid` → 自动发货入队 → `procurementSvc.CreateForOrder` → 下游回调 | `payment/application/payment_service_callback_dispatch.go:22-100` |
| 循环依赖注入 | `PaymentService.SetProcurementService(...)` | `app/container/services_wiring.go:8` |
| 交付记录 | `fulfillments.order_id` 唯一；`payload` 为明文 text | `fulfillment/domain/fulfillment.go` |
| 上游交付写入 | 事务内 `FOR UPDATE` 查已有交付，存在即返回（幂等） | `procurement/infrastructure/gormstore/lifecycle.go:91` |
| 钱包幂等 | `wallet_transactions.reference` 唯一索引；`CreditInTransaction` 先按 reference 查重 | `wallet/domain/transaction.go`、`wallet/application/credit.go:15` |
| 跨模块事务模板 | 礼品卡兑换：`workflows/giftcardredeem/infrastructure/gormuow` 把礼品卡锁和钱包入账放进同一 GORM 事务 | `workflows/giftcardredeem/...runner.go` |
| 批发价 | 商品已有 `wholesale_prices` 阶梯（`min_quantity` → `unit_price`，可按 SKU） | `catalog/product/domain/wholesale_price.go` |
| 会员价 | `member_levels.discount_rate`（100=原价）+ 按商品/SKU 的 `member_level_prices` | `memberlevel/domain/level.go`、`memberlevel/application/service.go:130` |
| 买家表单 | 商品 `manual_form_schema`，支持 text/textarea/phone/email/number/select/radio/checkbox | `catalog/product/manualform/schema.go` |
| 积分 | 仓库内**不存在**积分概念 | 全仓 grep |
| 加密 | `crypto.Encrypt/Decrypt`（AES-256-GCM，hex 输出），密钥由 `DeriveKey(app.secret_key)` 派生 | `internal/crypto/aes.go` |
| 签名 | `Sign(secret, method, path, ts, body)` = HMAC-SHA256；头 `Dujiao-Next-Api-Key / -Timestamp / -Signature`；时间偏差 60s | `internal/upstream/signer.go` |
| 迁移 | 集中式 `AutoMigrate()` 模型列表 | `bootstrap/database/migrations/registry.go:43` |
| 定时任务 | asynq Scheduler，`registerPeriodicTasks` 注册 `@every` 任务 | `app/jobs/service.go:51` |
| 分层约束 | 只有 `infrastructure/gormstore` 可导入 GORM；application 不能导入 gin/asynq/infrastructure | `internal/architecture/dependencies_test.go:346 validateImport` |
| RBAC 覆盖 | 每条 admin 路由必须被 `authz.BuiltinRoleSeeds()` 中至少一个角色覆盖，否则测试失败 | `app/httpserver/rbac_coverage_test.go`、`authz/bootstrap.go:14` |
| 购物车 | 购物车只接受 `manual/auto` | `cart/application/service.go:138` |

**未能验证：** 沙箱里没有 Go 工具链，以上均为静态阅读，没有编译或运行。真实 release/commit 也无法确认（目录不是 git 仓库），这是阶段 0 的阻塞项。

---

## 2. 业务模型总览

```text
┌──────────── Dujiao-Next 原生 ────────────┐   ┌──────────── 游戏扩展 ────────────────┐
│ 用户 / 会员等级 / 钱包 / 商品 / SKU       │   │ gamecatalog  游戏、区服、角色字典      │
│ 订单 / 子订单 / 支付 / 退款 / 交付记录    │◄──┤ gameaccount  账号库存、预留、凭据、售后│
│ 批发价阶梯 / 分销商 / 后台 RBAC / 审计    │   │ boost        积分账本、功能、任务、计划│
└──────────────────────────────────────────┘   │ gamebind     用户游戏账号绑定          │
                                                │ workerapi    Worker 认证、租约、回报   │
                                                └──────────────────────────────────────┘
                                                              ▲ HTTPS + HMAC
                                                       ┌──────┴──────┐
                                                       │ 游戏 Worker  │（独立进程，不连商城库）
                                                       └─────────────┘
```

三条资金/资源流：

1. **账号售卖**：钱 → 订单 → 账号（`game_account`）。
2. **积分购买**：钱 → 订单 → 积分批次（`boost_points`）。
3. **代练消耗**：积分 → 冻结 → 任务 → 结算或释放。不产生订单，不碰钱包。

---

## 3. 游戏目录（gamecatalog）

### 3.1 表

```text
game
  id               bigint PK
  code             varchar(32)  UNIQUE        -- 例 "genshin"，Worker 能力匹配用
  name_json        json                        -- 多语言
  icon             varchar(255)
  status           varchar(16)  INDEX          -- active / hidden
  sort_order       int
  created_at / updated_at / deleted_at

game_server                                     -- 区服
  id, game_id INDEX, code varchar(32), name_json, platform varchar(16)  -- android/ios/pc/all
  UNIQUE(game_id, code)
  status, sort_order, timestamps

game_attr_def                                   -- 可筛选属性定义（角色、武器、等级等都走这里）
  id, game_id INDEX
  code         varchar(64)                      -- 例 "char_hutao"、"level"、"gem"
  category     varchar(32)                      -- character / weapon / stat / tag
  name_json    json
  value_type   varchar(16)                      -- count（拥有数量/命座）/ number（数值）/ bool / enum
  enum_options json NULL
  filterable   bool
  display_order int
  UNIQUE(game_id, code)
```

把原计划的 `game_role_category + game_role` 合并为 `game_attr_def`，原因：账号筛选条件不只角色，还有等级、货币、皮肤等；统一成"属性定义 + 账号属性值"一张索引表，查询写法只有一种。

### 3.2 规则

- `game.code` 创建后不可修改（Worker 能力和任务都引用它）。
- 删除游戏 = 置 `hidden`，有账号或任务引用时禁止物理删除。

---

## 4. 游戏账号与批发（gameaccount）

### 4.1 两种售卖方式

| 方式 | 适用 | 商品建模 | 数量 | 价格来源 |
|---|---|---|---|---|
| 指定账号 | 零售，买家挑具体号 | 每个游戏一个"指定账号"商品（`fulfillment_type=game_account`, `sale_mode=pick`） | 固定 1 | `game_account.price`（服务端读取） |
| 批发 | 批量拿货 | 一个"规格"一个 SKU（如"国服·40级·5金"），`sale_mode=bulk` | N（受 min/max 限制） | SKU 价 → 批发阶梯 `wholesale_prices` → 会员价 |

两种方式共用同一账号池。账号的 `pool_sku_id` 决定它是否进入某个批发规格；`pickable` 决定它是否在"指定账号"列表里出现。一个号可以同时属于两者，先被谁锁定就归谁。

### 4.2 表

```text
game_account
  id                 bigint PK
  game_id            INDEX
  server_id          INDEX
  platform           varchar(16)
  source_id          bigint NULL INDEX          -- 外部库存源（阶段 4）
  external_id        varchar(128) NULL
  UNIQUE(source_id, external_id)                -- 同步去重
  title              varchar(200)               -- 公开标题，如"满命胡桃+专武"
  public_attrs_json  json                       -- 公开展示的属性摘要（已脱敏）
  cover_images       json
  pick_product_id    bigint NULL INDEX          -- 指定账号商品
  pickable           bool INDEX
  price              decimal(20,2)              -- 指定购买价
  cost_price         decimal(20,2)              -- 成本，仅后台
  pool_product_id    bigint NULL
  pool_sku_id        bigint NULL INDEX          -- 批发规格
  status             varchar(16) INDEX          -- 见 4.3
  reserved_order_id  bigint NULL INDEX          -- 当前锁定的子订单
  reserved_at        timestamp NULL
  sold_order_id      bigint NULL INDEX
  sold_order_item_id bigint NULL
  sold_at            timestamp NULL
  warranty_until     timestamp NULL             -- 售后截止
  version            int                        -- 乐观锁，编辑/同步用
  created_at / updated_at / deleted_at
  INDEX(game_id, server_id, status, pickable, price)
  INDEX(pool_sku_id, status, id)                -- 批发取号

game_account_attr                               -- 可索引的属性值
  account_id   INDEX
  attr_id      INDEX
  int_value    bigint                           -- count / number / bool(0/1)
  text_value   varchar(64)                      -- enum
  PRIMARY KEY(account_id, attr_id)
  INDEX(attr_id, int_value)

game_account_credential                         -- 凭据，单独一张表，任何列表接口都不 JOIN
  account_id   PK
  key_id       varchar(16)                      -- 密钥版本，支持轮换
  cipher_text  text                             -- AES-256-GCM(JSON{login,password,email,email_password,security_answers,extra})
  fingerprint  char(64)                         -- sha256(登录名) 用于查重，不可逆
  UNIQUE(fingerprint)
  updated_at

game_account_event                              -- 账号状态流水（审计）
  id, account_id INDEX, from_status, to_status, reason varchar(32),
  order_id NULL, actor_type (system/admin/sync/worker), actor_id NULL, created_at

game_account_claim                              -- 售后工单
  id, account_id, order_id, order_item_id, user_id INDEX
  type          varchar(16)                     -- invalid_login / retrieved / mismatch
  status        varchar(16)                     -- open / approved_replace / approved_refund / rejected / closed
  evidence_json json, admin_note, replacement_account_id NULL, refund_record_id NULL
  idempotency_key varchar(64) UNIQUE
  timestamps
```

### 4.3 账号状态机

```text
DRAFT ──publish──► ON_SALE ──reserve──► LOCKED ──paid──► SOLD ──claim approved──► REVOKED
  ▲                  │  ▲                │
  │             off  │  └──release───────┘ (支付失败/超时/取消)
  │                  ▼
  └──────────── OFF_SALE ◄── sync 标记异常 ── SYNC_ERROR
```

| 状态 | 含义 | 可见于前台 | 允许的下一个状态 |
|---|---|---|---|
| `DRAFT` | 录入未上架 | 否 | ON_SALE, OFF_SALE |
| `ON_SALE` | 可售 | 是 | LOCKED, OFF_SALE, SYNC_ERROR |
| `LOCKED` | 被子订单预留 | 否 | SOLD, ON_SALE |
| `SOLD` | 已售出并交付 | 否 | REVOKED |
| `REVOKED` | 售后收回（换号/退款） | 否 | 终态 |
| `OFF_SALE` | 下架 | 否 | ON_SALE, DRAFT |
| `SYNC_ERROR` | 同步发现异常 | 否 | ON_SALE, OFF_SALE |

所有转换只能经过 `domain.Transition(from, to, reason)`，非法转换返回 `ErrInvalidTransition`。同步任务**只能**在 `ON_SALE/OFF_SALE/SYNC_ERROR/DRAFT` 之间转换，碰到 `LOCKED/SOLD/REVOKED` 一律跳过并计数。

### 4.4 下单与预留（核心改动 C1–C4）

沿用卡密的预留写法，保证单库 SQLite 和 PostgreSQL 都可用：

```go
// 指定账号：在订单事务内
affected := UPDATE game_account
  SET status='LOCKED', reserved_order_id=?, reserved_at=?, version=version+1
  WHERE id=? AND status='ON_SALE' AND pickable=true AND deleted_at IS NULL
if affected != 1 → ErrGameAccountUnavailable（整单回滚）

// 批发：在订单事务内
rows := SELECT id FROM game_account
  WHERE pool_sku_id=? AND status='ON_SALE' AND deleted_at IS NULL
  ORDER BY id LIMIT ? FOR UPDATE           -- PostgreSQL 下追加 SKIP LOCKED（由 gormstore 判断方言）
affected := UPDATE ... WHERE id IN rows AND status='ON_SALE'
if affected != quantity → ErrGameAccountInsufficient
```

指定账号的账号 ID 怎么传进订单：`CreateOrderItem` 目前只有 `ProductID/SKUID/Quantity/FulfillmentType`（`order_service.go:168`）。新增一个可选字段 `GameAccountID uint`，只对 `sale_mode=pick` 商品有效；预留关系写入游戏模块自己的表，不改 `order_items` 表结构：

```text
game_account_reservation
  id, order_id INDEX, order_item_id, account_id, mode (pick/bulk),
  status (active/consumed/released), created_at, updated_at
  UNIQUE(account_id, status)  WHERE status='active'   -- PostgreSQL 部分唯一索引；SQLite 用应用层 + 状态 CAS 兜底
```

价格：`validate` 阶段（`order_service_validate.go:191-258`）对 `game_account` 项调用 `GameAccountQuoter.Quote(productID, skuID, accountID, qty)`：

- pick：返回 `game_account.price` 作为 `OriginalUnitPrice`，前端传入的价格完全忽略。
- bulk：返回 SKU 价，后续沿用原有批发阶梯和会员价计算。
- 会员折扣：pick 模式只应用 `discount_rate`（全局折扣），不查 `member_level_prices`（账号没有固定商品价）。

快照：`order_items.sku_snapshot_json` 写入 `{ "game_account": { "id", "title", "server", "public_attrs" } }`。账号后续被编辑不影响订单。

### 4.5 支付后交付（核心改动 C5–C6）

```text
支付成功 (payment_service_callback.go)
  └─ shouldMarkFulfilling 把 game_account 视为需要履约 → 子订单 fulfilling
enqueueOrderPaidAsync
  └─ gameFulfillSvc.HandleOrderPaid(orderID)   （新增，和 procurement 并列）
       └─ asynq 任务 game:account_deliver {order_id}
            事务：
              1. FOR UPDATE fulfillments where order_id → 已存在则直接成功（幂等）
              2. 校验 reservation 全部 active 且账号 LOCKED 且 reserved_order_id=本单
              3. 账号 → SOLD，写 sold_*、warranty_until = now + product.warranty_hours
              4. reservation → consumed；写 game_account_event
              5. 写 fulfillments(type=game_account, status=delivered,
                   payload = "已交付 N 个账号，请在订单详情查看凭据"，
                   delivery_data = {account_ids:[...], count:N})
              6. 子订单 → completed；SyncParentStatus
            事务外：发邮件（不含明文凭据）、Telegram 通知
```

**为什么交付记录里不放明文：** `fulfillments.payload` 是明文 text，还会进邮件和导出，违反 AGENTS 5.3。凭据只通过 4.6 的专用接口解密查看。

异常：第 2 步校验失败（账号被管理员误改、同步覆盖等）→ 任务不重试，订单标记交付失败、通知管理员，走人工换号或退款。不会自动挑另一个号，避免违背"指定账号"的语义。批发模式可配置 `auto_substitute=true` 允许从同 SKU 补号。

### 4.6 凭据查看与导出

| 接口 | 谁 | 规则 |
|---|---|---|
| `GET /api/v1/game/orders/:order_no/accounts` | 买家 | 订单属于本人、状态 completed；返回解密凭据；每次访问写 `game_credential_access_log` |
| `GET /api/v1/game/orders/:order_no/accounts/export?format=csv\|txt` | 买家 | 批发用；单号限流 10 次/小时 |
| `GET /api/v1/admin/game/accounts/:id/credential` | 管理员 | 独立权限 `game:credential:read`，必填 `reason`，写审计 |

游客订单：游戏账号商品统一 `purchase_type=member`，不支持游客购买，减少凭据泄露面。

### 4.7 售后

- 质保期内买家提交 `claim`（带截图证据），管理员处理：
  - 换号：从同规格取一个 `ON_SALE` 账号直接 SOLD 给该订单项，原号 → `REVOKED`，追加交付记录行（`delivery_data.replacements`）。
  - 退款：调用原生 `AdminRefundToWallet`（`order/application/refund/wallet.go:33`），原号 → `REVOKED`。
- 超过 `warranty_until` 拒绝新工单。
- 一个订单项同时只能有一个 `open` 工单。

### 4.8 批发专属

- 复用商品 `wholesale_prices` 阶梯和 `min/max_purchase_quantity`，不新造价格表。
- 批发客户 = 会员等级里新增"批发商"等级（`discount_rate` 自定），由管理员 `SetUserLevel` 指定；或走现有分销商（reseller）体系。
- 库存展示：批发 SKU 的可售数 = `COUNT(*) WHERE pool_sku_id=? AND status='ON_SALE'`，接入 `stock_display_mode`。
- 大单（如 >200 个）交付时分批写入，单事务不超过 500 行。

---

## 5. 积分（boost 积分账本）

### 5.1 原则

- 积分是整数（`bigint`），单位"点"，没有小数。
- 获取：购买积分包（订单）、订阅赠送、管理员发放、活动赠送。
- 消耗：只能用于代练功能。
- **不能**提现、转账、兑换钱包余额。
- 每一笔变化都有唯一 `biz_key` 和流水；余额 = 未过期批次剩余之和。

### 5.2 表

```text
boost_point_account
  user_id        PK
  available      bigint NOT NULL DEFAULT 0     -- 可用
  frozen         bigint NOT NULL DEFAULT 0     -- 任务冻结中
  version        int
  updated_at
  CHECK(available >= 0 AND frozen >= 0)

boost_point_lot                                 -- 积分批次（支持过期、按来源退款）
  id, user_id INDEX
  source_type    varchar(16)                    -- order / grant / subscription / promo
  source_ref     varchar(64)                    -- 例 order_item:123
  amount         bigint
  remaining      bigint
  expires_at     timestamp NULL INDEX
  status         varchar(12)                    -- active / exhausted / expired / revoked
  created_at
  UNIQUE(source_type, source_ref)

boost_point_ledger
  id, user_id INDEX
  type           varchar(20)                    -- purchase / grant / hold / release / consume / expire / revoke / admin_adjust
  delta_available bigint                        -- 可用变化（可负）
  delta_frozen    bigint                        -- 冻结变化（可负）
  available_after bigint
  frozen_after    bigint
  lot_allocations json                          -- [{lot_id, amount}]，消耗/冻结时按 FIFO 分摊
  biz_key        varchar(96) UNIQUE             -- 幂等键
  task_id        bigint NULL INDEX
  order_id       bigint NULL INDEX
  operator_admin_id bigint NULL
  remark         varchar(255)
  created_at
```

### 5.3 操作（全部在一个事务内，先 `SELECT ... FOR UPDATE` 锁 `boost_point_account`）

| 操作 | biz_key 格式 | 效果 |
|---|---|---|
| Grant（到账） | `purchase:order_item:{id}` / `grant:{admin_id}:{uuid}` | 新建 lot；available += n |
| Hold（冻结） | `hold:task:{task_id}` | 按 lot 过期时间升序 FIFO 扣减 remaining；available -= n；frozen += n |
| Consume（结算） | `consume:task:{task_id}` | frozen -= 实际消耗 |
| Release（释放） | `release:task:{task_id}` | frozen -= 剩余；available += 剩余；按 hold 记录的 lot 分摊**原路退回**对应 lot（已过期的 lot 退回即过期） |
| Expire（过期） | `expire:lot:{lot_id}` | 定时任务；available -= remaining；lot → expired |
| Revoke（积分包退款） | `revoke:order_item:{id}` | 只能撤回该 lot 的 remaining；已用部分不退钱 |

biz_key 冲突 → 返回已有流水（与钱包 `CreditInTransaction` 的查重语义一致）。

### 5.4 积分包商品（核心改动 C7）

- 商品 `fulfillment_type=boost_points`，SKU 上用 `spec_values_json` 配置 `{ "points": 1000, "bonus": 100, "valid_days": 365 }`。
- 支付成功 → `boostSvc.HandleOrderPaid(orderID)` → asynq `boost:grant_points` → 事务内 Grant + 写 `fulfillments(type=boost_points, payload="已到账 1100 积分")` + 子订单 completed。
- 积分包退款：管理员在原生退款前，先调用 `POST /admin/boost/orders/:id/revoke-points`，按 lot.remaining 计算可退比例并给出建议金额；原生退款接口只认金额，所以这一步是管理端的强制前置检查（前端引导 + 后端在 revoke 成功前拒绝对该订单项退款需要核心改动 C8，可选）。

---

## 6. 代练功能与任务（boost）

### 6.1 功能定义

```text
boost_feature
  id, game_id INDEX
  code           varchar(64)                    -- 例 daily_quest / auto_dungeon / idle_farm
  UNIQUE(game_id, code)
  name_json, description_json, icon
  billing_mode   varchar(16)                    -- per_run（按次）/ per_minute（按时长）
  point_cost     bigint                         -- per_run：每次；per_minute：每分钟
  min_minutes    int NULL / max_minutes int NULL -- per_minute 时的下单范围
  params_schema  json                           -- 复用 manualform schema 格式，用户下单时填写（如副本层数、体力上限）
  worker_capability varchar(64)                 -- Worker 必须声明此能力才能领取
  timeout_minutes int                           -- 任务硬超时
  max_active_per_user int                       -- 同一用户同时进行中的上限
  max_active_per_binding int DEFAULT 1          -- 同一游戏账号同时只能跑 1 个任务（防止顶号）
  member_discount bool                          -- 是否按会员等级打折
  refund_policy  varchar(16)                    -- full_on_system_fail / prorate / none
  status, sort_order, timestamps

boost_feature_level_price                       -- 可选：按会员等级覆盖点数
  feature_id, member_level_id, point_cost
  PRIMARY KEY(feature_id, member_level_id)
```

示例：

| code | 名称 | 计费 | 点数 |
|---|---|---|---|
| `daily_quest` | 每日委托 | per_run | 20 |
| `auto_dungeon` | 自动刷本（N 次） | per_run × 次数参数 | 15/次 |
| `idle_farm` | 挂机刷材料 | per_minute | 1/分钟，30–480 分钟 |
| `event_clear` | 活动一条龙 | per_run | 120 |

### 6.2 游戏账号绑定（gamebind）

代练需要用户**自己的**游戏账号（与 4 章出售的账号无关；买到的账号也可以一键绑定）。

```text
game_binding
  id, user_id INDEX, game_id, server_id
  display_name      varchar(64)                 -- 用户自定义备注
  login_fingerprint char(64)                    -- sha256(game_id + 登录名)
  UNIQUE(user_id, login_fingerprint)
  credential_cipher text, key_id varchar(16)
  status            varchar(16)                 -- PENDING / PROCESSING / WAIT_INPUT / WAIT_USER / SUCCESS / FAIL / CANCELLED
  last_error_code   varchar(32), user_message_json json
  session_ref       varchar(128) NULL           -- Worker 返回的会话引用（加密），后续任务复用免登录
  verified_at       timestamp NULL
  attempt_count     int, max_attempts int DEFAULT 3
  timestamps

game_binding_input                              -- WAIT_INPUT 时向用户索取的数据
  id, binding_id INDEX (或 task_id)
  kind        varchar(16)                       -- sms_code / email_code / captcha_image / slider / confirm
  prompt_json json                              -- 给用户看的提示（图片为临时 URL）
  value_cipher text NULL                        -- 用户提交值，加密
  expires_at  timestamp
  status      varchar(12)                       -- waiting / submitted / expired / consumed
  timestamps
```

状态机（严格遵守 AGENTS 6.4 的状态集合）：

```text
PENDING ──pull──► PROCESSING ──需要验证码──► WAIT_INPUT ──用户提交──► PENDING
   ▲                  │                          │ 超时(默认5分钟)
   │                  │                          ▼
   │                  ├──需要用户在游戏内确认──► WAIT_USER ──用户点"已确认"──► PENDING
   │                  ├──成功──► SUCCESS
   │                  ├──可重试失败 & attempt<max──► PENDING（退避 30s×2^n）
   │                  └──不可重试/超次数──► FAIL
   └── 用户/管理员取消（非终态时）──► CANCELLED
```

绑定本身**不收积分**（避免用户因验证码超时被扣点），可通过 `boost_settings.bind_daily_limit` 限制每日次数防滥用。

### 6.3 代练任务

```text
boost_task
  id, task_no varchar(32) UNIQUE
  user_id INDEX, binding_id INDEX, game_id, feature_id
  feature_snapshot json                         -- 下单时的名称/计费/点数/超时，后续改功能不影响
  params_json      json                         -- 已按 params_schema 校验
  quantity         int                          -- per_run 次数 或 per_minute 分钟数
  points_held      bigint                       -- 冻结点数
  points_consumed  bigint DEFAULT 0
  status           varchar(16) INDEX            -- PENDING / PROCESSING / WAIT_INPUT / SUCCESS / FAIL / CANCELLED
  cancel_requested bool DEFAULT false
  progress_percent smallint, progress_text varchar(200), units_done int  -- units_done：已完成次数或分钟
  result_json      json NULL                    -- 结构化结果（获得物品等），不含凭据
  error_code       varchar(32), error_retryable bool
  attempt          int DEFAULT 0, max_attempts int DEFAULT 2
  worker_id        bigint NULL, lease_token char(32) NULL, lease_expires_at timestamp NULL INDEX
  started_at, finished_at, deadline_at INDEX
  schedule_id      bigint NULL INDEX
  idempotency_key  varchar(64)                  -- 用户端提交幂等
  UNIQUE(user_id, idempotency_key)
  timestamps
  INDEX(status, game_id, created_at)            -- pull 用

boost_task_event                                -- 进度/日志，分页查看
  id, task_id INDEX, seq int, type (progress/log/input_request/state), message varchar(500), data_json, created_at
  UNIQUE(task_id, seq)                          -- Worker 上报 seq 幂等
```

### 6.4 任务状态机与积分联动

```text
用户提交
  事务：校验功能/绑定 SUCCESS/并发上限 → 计算点数 → Hold → 插入 task(PENDING)
PENDING ──pull(租约)──► PROCESSING
PROCESSING ──需要验证码──► WAIT_INPUT ──提交──► PENDING（保留 units_done）
PROCESSING ──finish──► SUCCESS   ：Consume(实际) + Release(剩余)
PROCESSING ──fail(retryable 且 attempt<max)──► PENDING
PROCESSING ──fail(终态)──► FAIL  ：按 refund_policy 结算
PENDING / WAIT_INPUT ──用户取消──► CANCELLED ：全额 Release
PROCESSING ──用户取消──► cancel_requested=true，Worker 下次心跳收到 cancel → Worker 调 cancel 确认 → CANCELLED ：按已完成 units 结算
租约过期 ──reaper──► PENDING（attempt+1）或 FAIL（超次数）
超过 deadline_at ──reaper──► FAIL(error_code=TIMEOUT)
```

结算公式（全部整数运算，向上取整对用户不利，所以统一**向下取整**）：

```text
per_run:     consumed = point_cost × units_done
per_minute:  consumed = point_cost × units_done        （units_done=实际分钟，Worker 每分钟心跳累计）
FAIL + full_on_system_fail 且 error 属系统类：consumed = 0
FAIL + prorate：consumed = point_cost × units_done
FAIL + none：consumed = points_held
release = points_held - consumed   （必须 ≥ 0，Worker 上报 units_done 超过 quantity 时截断）
```

Worker 不能决定点数，只上报 `units_done` 和 `error_code`；点数由服务端按快照计算（对应 AGENTS 6.3"Worker 不能直接决定退款金额"）。

### 6.5 定时计划（每天自动跑日常）

```text
boost_schedule
  id, user_id, binding_id, feature_id, params_json, quantity
  cron_kind   varchar(16)                       -- daily（每天 HH:MM）/ weekly
  run_at      varchar(5)                        -- "04:30"，按 Asia/Shanghai
  weekdays    varchar(16) NULL
  next_run_at timestamp INDEX
  status      active / paused / stopped_insufficient_points
  last_task_id
```

`@every 1m` 扫描 `next_run_at <= now`，逐个调用"创建任务"用例（幂等键 `schedule:{id}:{yyyymmddHHMM}`）。积分不足 → 计划自动暂停并通知用户。

### 6.6 订阅（可选，第二期）

计划里的 `boost_plan / boost_subscription` 保留为第二期：订阅 = 一个周期性 Grant 积分 + 功能折扣的商品，到账走 5.4 同一条链路（`source_type=subscription`）。第一期只做"积分包 + 按次/按时消耗"，复杂度最低。

---

## 7. Worker 协议（workerapi）

### 7.1 认证

```text
worker_node
  id, name, api_key varchar(64) UNIQUE, secret_cipher text, key_id
  capabilities json        -- [{"game":"genshin","features":["daily_quest","bind"]}]
  max_concurrency int, status active/disabled
  last_seen_at, last_ip, version varchar(32)
```

每个请求带三个头（与 `internal/upstream/signer.go` 同算法、不同头名，避免和上游对接混淆）：

```text
X-Worker-Key:        {api_key}
X-Worker-Timestamp:  {unix 秒}            -- 偏差 > 60s 拒绝
X-Worker-Signature:  hex(HMAC-SHA256(secret, "{METHOD}\n{PATH}\n{TS}\n{md5(body)}"))
X-Worker-Nonce:      {uuid}               -- Redis SETNX 120s 防重放
```

不复用 `apicredential`：它是"用户申请的 API 凭证"，一个用户一条（`user_id uniqueIndex`），语义不符。

### 7.2 接口（前缀 `/api/v1/worker`，限流按 api_key）

| 方法 | 路径 | 请求 | 响应 | 幂等 |
|---|---|---|---|---|
| POST | `/pull` | `{kinds:["task","bind"], max:1}` | `[{kind, id, lease_token, lease_expires_at, game, feature, params, credential(解密后), session_ref, input(若有)}]` | 无，原子领取 |
| POST | `/leases/:token/heartbeat` | `{units_done, progress_percent, progress_text}` | `{lease_expires_at, cancel_requested}` | 天然幂等 |
| POST | `/leases/:token/events` | `{seq, type, message, data}` | `{ok}` | `UNIQUE(task_id, seq)` |
| POST | `/leases/:token/need-input` | `{kind, prompt}` | `{input_id}` | 同一租约只允许一个 waiting 输入 |
| POST | `/leases/:token/finish` | `{request_id, units_done, result, session_ref}` | `{status}` | `request_id` |
| POST | `/leases/:token/fail` | `{request_id, error_code, retryable, units_done, message}` | `{status}` | `request_id` |
| POST | `/leases/:token/cancel-ack` | `{request_id, units_done}` | `{status}` | `request_id` |

用 `lease_token` 而不是任务 ID 作为路径：旧 Worker 拿着过期 token 的任何写操作都直接 `409 LEASE_LOST`，不需要额外比较。

### 7.3 领取算法

```sql
-- PostgreSQL
UPDATE boost_task SET status='PROCESSING', worker_id=$w, lease_token=$t,
       lease_expires_at=now()+interval '90 seconds', attempt=attempt+1, started_at=COALESCE(started_at, now())
WHERE id = (
  SELECT id FROM boost_task
  WHERE status='PENDING' AND game_id = ANY($games) AND feature_code = ANY($features)
  ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING *;
-- SQLite：取候选 id 后 UPDATE ... WHERE id=? AND status='PENDING'，RowsAffected=0 则重试下一个
```

- 租约 90 秒，心跳间隔 30 秒。
- 同一 `binding_id` 有 PROCESSING 任务时，其他任务不可领取（防止顶号）。
- `credential` 只在 pull 响应里出现一次，日志中间件对 `/api/v1/worker/pull` 响应体不记录。

### 7.4 回收（reaper，`@every 30s`）

```text
lease_expires_at < now AND status IN (PROCESSING)
  → attempt < max_attempts ? PENDING : FAIL(LEASE_EXPIRED)，清空 lease_*
WAIT_INPUT 且输入 expires_at < now → FAIL(INPUT_TIMEOUT)，全额 Release
deadline_at < now 且非终态 → FAIL(TIMEOUT)
```

### 7.5 错误码（Worker 上报）

| code | retryable | 积分 |
|---|---|---|
| `NETWORK` / `GAME_MAINTENANCE` / `WORKER_CRASH` | 是 | 重试耗尽后全额释放 |
| `LOGIN_FAILED` / `ACCOUNT_BANNED` / `PASSWORD_CHANGED` | 否 | 全额释放，绑定 → FAIL |
| `INSUFFICIENT_STAMINA` / `NOT_UNLOCKED` | 否 | 按 units_done 结算 |
| `USER_KICKED`（用户自己上线顶号） | 否 | 按 units_done 结算 |

---

## 8. 对 Dujiao-Next 核心的改动清单

全部改动集中、可 grep，每处加注释 `// GAME-EXT:`，升级时按此清单逐条合并。

| # | 文件 | 改动 | 原因 |
|---|---|---|---|
| C1 | `internal/constants/constants.go` | 新增 `FulfillmentTypeGameAccount="game_account"`、`FulfillmentTypeBoostPoints="boost_points"` | 新交付类型 |
| C2 | `catalog/product/domain/purchase_policy.go NormalizeFulfillmentType` | 接受两个新类型 | 商品可保存 |
| C3 | `order/application/order_service.go:168 CreateOrderItem` | 新增 `GameAccountID uint` | 指定账号 |
| C4a | `order/application/order_service_validate.go:191-258` | 新类型放行；`game_account` 调 `GameAccountQuoter` 取价和快照 | 服务端定价 |
| C4b | `order/contract/store.go:75 Transaction` | 新增 `GameAccounts() gameaccountcontract.ReservationRepository` | 与 `CardSecrets()` 同模式，同事务预留 |
| C4c | `order_service.go:610` 附近 | `game_account` 项调用 `Reserve` | 预留 |
| C4d | `order_service_child.go:47-58, 387` | 取消/超时时 `GameAccounts().ReleaseByOrder` | 释放 |
| C5 | `payment/application/payment_service_rules.go:126 shouldMarkFulfilling` | 两个新类型返回 true | 支付后进入 fulfilling |
| C6 | `payment/application/payment_service_callback_dispatch.go:22` | 新增 `s.gameFulfillSvc.HandleOrderPaid(order.ID)`；接口 + `SetGameFulfillService` 注入 | 触发交付 |
| C7 | `app/container/services*.go`、`services_wiring.go` | 构造游戏服务并注入 | 装配 |
| C8 | `bootstrap/database/migrations/registry.go:43` | 追加游戏模块模型 | 建表 |
| C9 | `app/httpserver/routes_*.go` | 注册 `/game`、`/boost`、`/worker`、`/admin/game/*` | 路由 |
| C10 | `authz/bootstrap.go` | 新增角色种子 `game_operator`、`game_auditor` 并覆盖全部新 admin 路由 | 通过 RBAC 覆盖测试 |
| C11 | `app/jobs/service.go:51`、`jobs/consumer/consumer_core.go:41` | 注册 6 个任务（见 9 章） | 异步 |
| C12 | `cart/application/service.go:138` | **不改**：游戏商品不进购物车，直接购买 | 降低改动面 |
| C13 | `notification/application/format/order_format.go`、`dashboard/.../inventory.go` | 新类型显示名、库存统计 | 展示 |

新增模块全部放在 `internal/modules/game*` 和 `internal/modules/boost`、`internal/modules/workerapi`，跨模块事务放 `internal/workflows/gamefulfill`、`internal/workflows/boostpoints`（参照 `giftcardredeem`）。

---

## 9. 异步任务

| 任务名 | 触发 | 重试 | 说明 |
|---|---|---|---|
| `game:account_deliver` | 支付成功 | 3 次，指数退避 | 4.5 |
| `boost:grant_points` | 支付成功 | 5 次 | 5.4 |
| `boost:lease_reaper` | `@every 30s` | — | 7.4 |
| `boost:schedule_tick` | `@every 1m` | — | 6.5 |
| `boost:point_expire` | `@every 1h` | — | 过期积分批次 |
| `game:reservation_audit` | `@every 10m` | — | 找出 `LOCKED` 但订单已取消/超时的账号，自动释放并告警（兜底超时任务丢失） |

---

## 10. 对外接口清单

### 10.1 用户端（需登录，除标注 public）

```text
GET  /api/v1/public/games                               游戏列表
GET  /api/v1/public/games/:code/attrs                   可筛选属性
GET  /api/v1/public/games/:code/accounts                指定账号列表
       ?server=&platform=&price_min=&price_max=&attr[char_hutao]=gte:6&attr[level]=gte:55&sort=price_asc&page=
GET  /api/v1/public/game-accounts/:id                   账号详情（脱敏）
POST /api/v1/game/accounts/:id/buy                      { idempotency_key } → 调用原生 CreateOrder，返回 order_no 跳转支付
GET  /api/v1/game/orders/:order_no/accounts             查看凭据
GET  /api/v1/game/orders/:order_no/accounts/export
POST /api/v1/game/claims                                售后

GET  /api/v1/boost/points                               { available, frozen, expiring_soon:[{amount, expires_at}] }
GET  /api/v1/boost/points/ledger?page=
GET  /api/v1/public/boost/games/:code/features
POST /api/v1/boost/bindings                             { game, server, login, password, display_name }
GET  /api/v1/boost/bindings
POST /api/v1/boost/bindings/:id/input                   { input_id, value }
POST /api/v1/boost/bindings/:id/confirm                 WAIT_USER → PENDING
DELETE /api/v1/boost/bindings/:id                       有进行中任务时拒绝
POST /api/v1/boost/tasks/quote                          { binding_id, feature_id, quantity, params } → { points, available }
POST /api/v1/boost/tasks                                { ..., idempotency_key }
GET  /api/v1/boost/tasks?status=&page=
GET  /api/v1/boost/tasks/:task_no                       含最近 50 条事件
POST /api/v1/boost/tasks/:task_no/input
POST /api/v1/boost/tasks/:task_no/cancel
CRUD /api/v1/boost/schedules
```

批发走原生商品详情页和 `POST /api/v1/orders`（数量 + SKU），不另开接口。

### 10.2 管理端（`/api/v1/admin`，JWT + RBAC）

```text
游戏/区服/属性定义        CRUD /game/games, /game/servers, /game/attrs
账号                      GET /game/accounts（筛选）  POST /game/accounts  PUT /game/accounts/:id
                          POST /game/accounts/import（CSV，含凭据，先预览后确认）
                          POST /game/accounts/batch-status（上下架）
                          GET  /game/accounts/:id/credential?reason=   [game:credential:read]
                          GET  /game/accounts/:id/events
售后                      GET /game/claims  POST /game/claims/:id/resolve
积分                      GET /boost/points/accounts  POST /boost/points/grant  POST /boost/points/adjust
                          POST /boost/orders/:order_item_id/revoke-points
功能                      CRUD /boost/features  PUT /boost/features/:id/level-prices
绑定/任务                 GET /boost/bindings  GET /boost/tasks  POST /boost/tasks/:id/cancel  POST /boost/tasks/:id/retry
Worker                   CRUD /worker/nodes  POST /worker/nodes/:id/rotate-secret  GET /worker/nodes/:id/stats
```

### 10.3 错误码

统一前缀，HTTP 状态码 + 业务码：

```text
40901 GAME_ACCOUNT_UNAVAILABLE    账号已被锁定/售出
40902 GAME_ACCOUNT_INSUFFICIENT   批发库存不足
40903 POINTS_INSUFFICIENT
40904 BINDING_BUSY                该游戏账号有任务进行中
40905 TASK_LIMIT_REACHED
40906 LEASE_LOST                  Worker 租约失效
40907 INVALID_TRANSITION
42201 PARAMS_INVALID              params_schema 校验失败
41001 INPUT_EXPIRED
40301 CREDENTIAL_FORBIDDEN
```

---

## 11. 前台与后台页面

### 11.1 用户端（`frontend/user/src/views`，沿用现有 Vue 3 + vue-i18n + Pinia）

| 路由 | 页面 | 要点 |
|---|---|---|
| `/games` | 游戏入口 | 卡片：账号数量、代练功能数 |
| `/games/:code/accounts` | 账号市场 | 左侧筛选（区服、价格、属性多选+阈值）；URL 同步筛选条件；列表虚拟滚动 |
| `/game-accounts/:id` | 账号详情 | 图集、属性表、质保说明、"立即购买"（直接下单，不进购物车） |
| `/me/game-accounts` | 已购账号 | 订单维度列表；"查看凭据"二次确认；批发订单"导出" |
| `/me/points` | 我的积分 | 可用/冻结/即将过期；流水；"购买积分"跳积分包商品 |
| `/boost` | 代练中心 | 选游戏 → 选绑定账号 → 选功能 → 填参数 → 报价 → 提交 |
| `/me/bindings` | 我的游戏账号 | 绑定状态；WAIT_INPUT 弹出验证码输入框（轮询 3s，或 SSE 第二期） |
| `/me/boost/tasks` | 任务列表/详情 | 进度条、事件流、取消按钮、结果展示 |
| `/me/boost/schedules` | 自动计划 | 开关、时间、积分不足提示 |

在 `PersonalCenter.vue` 的面板导航中追加以上 `/me/*` 入口。

### 11.2 管理端（`frontend/admin/src`）

菜单分组"游戏业务"：游戏与属性、账号库存、账号导入、售后工单、积分管理、代练功能、绑定与任务、Worker 节点。账号列表默认不显示凭据列；凭据查看弹窗必须填写原因。

---

## 12. 安全

- 凭据加密密钥独立于 `app.secret_key`：新增配置 `game.credential_keys: { "k1": "<base64 32 bytes>" }` 和 `game.credential_active_key: k1`；表中记录 `key_id`，支持轮换脚本重加密。
- 所有 DTO 显式定义，禁止直接序列化 GORM 模型（`game_account_credential` 不出现在任何 `Preload` 中）。
- 日志：Worker pull 响应、绑定提交请求体、凭据接口响应全部排除；`zap` 字段过滤 `password/code/token/credential`。
- 买家查看凭据、管理员查看凭据都写访问日志（谁、何时、IP、原因）。
- 用户提交的验证码 5 分钟过期，消费后立即清空 `value_cipher`。
- Worker secret 只在创建/轮换时显示一次。

---

## 13. 测试清单（必须自动化）

### 13.1 账号

1. 100 并发购买同一指定账号，仅 1 单成功，其余 `40901`。
2. 批发 SKU 库存 50，并发 10 单各买 10，成功单数之和 ≤ 5，账号无重复。
3. 支付超时 → 账号回到 `ON_SALE`；`reservation_audit` 能修复超时任务丢失的情况。
4. 支付回调重复 10 次 → 交付记录 1 条，账号 SOLD 1 次。
5. 已售账号同步时保持 SOLD；同步计数 skipped+1。
6. 账号编辑后，历史订单快照不变。
7. 非订单所有者访问凭据接口 → 403；列表接口响应中不包含 `cipher_text` 字段（反射扫描 DTO）。
8. 会员 9 折、批发阶梯 10 件起价，服务端金额与预期一致；篡改前端价格无效。

### 13.2 积分

1. 并发 20 个任务各冻结 100，余额 1000 → 成功 10 个，余额不出现负数。
2. 同一 `biz_key` Grant 两次只到账一次。
3. Hold 跨 3 个批次 FIFO 分摊；Release 原路退回；退回到已过期批次时立即过期。
4. 积分包撤回只能撤 remaining。

### 13.3 任务与 Worker

1. 两个 Worker 并发 pull 同一任务只有一个拿到。
2. 租约过期后旧 Worker 的 finish 返回 `40906`，任务状态不变。
3. finish 重复 3 次（同 request_id）结果一致、积分只结算一次。
4. Worker 进程被杀 → 90 秒后任务回到 PENDING，attempt+1；超过次数 → FAIL 且积分全额释放。
5. 用户在 PROCESSING 取消 → 下一次心跳返回 `cancel_requested=true` → cancel-ack 后按 units_done 结算。
6. WAIT_INPUT 超时 → FAIL(INPUT_TIMEOUT)，积分全额释放。
7. 同一绑定账号两个任务，第二个在第一个结束前不可被领取。
8. Worker 上报 `units_done > quantity` → 截断，不多扣。
9. 签名错误 / 时间戳偏差 61s / nonce 重放 → 401。
10. 能力不匹配的 Worker 拉不到任务。

### 13.4 架构

- 新模块通过 `TestDependencyRules`。
- 新 admin 路由通过 `TestAllAdminRoutesCoveredByBuiltinRoles`。
- 新增一条架构测试：`internal/modules/boost/**` 和 `game*/**` 不得导入 `wallet/infrastructure`。

---

## 14. 实施顺序（与原计划阶段对应）

| 步骤 | 内容 | 对应阶段 | 产出验证 |
|---|---|---|---|
| S0 | 固定上游 release、建 git、跑通原版测试 | 0 | `go test ./...` 基线结果存档 |
| S1 | gamecatalog + gameaccount 表/后台 CRUD/导入/脱敏查询 | 1–2 | 13.1-7 |
| S2 | 核心改动 C1–C6 + 账号预留/交付/凭据查看 | 3 | 13.1-1~6、8 |
| S3 | 积分账本 + 积分包商品 | 5 | 13.2 |
| S4 | boost_feature + 任务创建/取消（先用"假 Worker"脚本） | 6 | 积分冻结结算 |
| S5 | workerapi + reaper + 真 Worker 对接 | 7 | 13.3 |
| S6 | gamebind + WAIT_INPUT | 8 | 绑定闭环 |
| S7 | 定时计划、售后、前端完善 | 9 | 全链路 |
| S8 | 外部库存同步 | 4 | 原计划验收 |

外部库存同步（阶段 4）放到最后：先用后台导入跑通全链路，同步只是另一种录入方式。

---

## 15. 待你确认的决策

每项都给了推荐默认值。没有回复前按推荐值推进，确认后再改。

| # | 问题 | 推荐 | 备选 |
|---|---|---|---|
| D1 | 积分是否独立于钱包 | 独立积分账本，不可提现 | 直接用钱包余额扣费（简单，但代练价格变成金额，失去"积分"概念） |
| D2 | 积分有效期 | 购买积分 365 天，赠送积分 30 天 | 永久有效 |
| D3 | 指定账号是否允许游客购买 | 不允许 | 允许（需凭订单密码查看凭据） |
| D4 | 批发交付凭据校验失败时 | 允许同 SKU 自动补号 | 全部人工处理 |
| D5 | 代练系统失败是否退积分 | 系统类失败全额退，用户类按已完成结算 | 一律按已完成结算 |
| D6 | 订阅套餐 | 第二期 | 第一期一起做 |
| D7 | 现有 Worker 协议 | 采用本文 7 章协议 | 若已有 Worker 协议，提供文档后做适配层 |
| D8 | 数据库 | 生产 PostgreSQL（需要 SKIP LOCKED 和部分唯一索引） | SQLite（并发性能和锁语义较弱，仅开发用） |

---

## 16. 风险

- **合规：** 游戏账号交易和代练通常违反游戏厂商用户协议，部分地区对代练有监管要求。上线前需要自行评估法律和平台风险（这不是法律意见）。
- **GPL-3.0：** 修改版对外分发需要遵守 GPL 义务，见 AGENTS 第 11 节。
- **升级冲突：** C1–C13 是核心改动，升级 Dujiao-Next 时必须逐条重放；改动处统一 `// GAME-EXT:` 标记便于检索。
- **未验证：** 本文基于静态阅读，沙箱无法编译运行；S0 完成前，所有行号和接口名都需要在固定版本上重新核对。

---

## 17. 进度记录

### 2026-10-22 本地原型

- 完成：`prototype/` 可运行原型（双击 `index.html`），用 JS 实现本文 §4–§7 的状态机、预留、幂等、积分账本、Worker 签名与租约、回收、自动计划、外部同步，以及 §13 的不变量检查。
- 验证：`node --test prototype/test/engine.test.js prototype/test/ui-smoke.test.js` → 49 个测试全部通过（47 个业务场景 + 2 个界面冒烟）。界面在预览面板中截图检查了账号市场、代练中心、执行端模拟、后台任务、积分页。
- 原型中确定的细节（Go 实现照此执行）：
  - 积分会员折扣向下取整（如 15 点 × 95% = 14 点）。
  - 服务端错误码表优先于 Worker 上报的 `retryable`。
  - 任务退回的积分按冻结时的批次逆序退回；退回的批次已过期则立即失效并记 `expire` 流水。
  - 预留审计宽限 5 分钟、每 10 分钟一次，最坏 25 分钟内释放悬挂预留。
  - 管理员重试失败任务 = 新建任务重新冻结，原任务保持终态。
- 未完成：Go 实现（阶段 0 阻塞：未固定上游 release/commit，且当前环境无 Go 工具链）；真实 PostgreSQL 并发测试；真实 AES-GCM 凭据加密。
- 风险：原型的"并发"是串行调用 + 回滚，不能替代数据库层并发测试。
- 下一步唯一建议：在 §15 决策确认后，固定 Dujiao-Next release，建 git 仓库，开始 S0。
