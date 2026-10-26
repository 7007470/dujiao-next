# 两大业务板块详细设计

> 只设计：角色筛选账号交易、自动代练。会员、钱包、订单、支付和退款复用 Dujiao-Next。暂不设计批发、积分、分销、自动续费或人工派单。

## 1. 边界

```text
Dujiao-Next：用户 / 会员 / 钱包 / 商品 / 订单 / 支付 / 退款
       ├── 板块一：游戏 / 区服 / 角色属性 / 账号 / 预留 / 交付 / 售后
       └── 板块二：代练商品 / 用户绑定账号 / 权益 / 任务 / Worker / 结果
```

账号交易的“平台库存账号”和代练的“用户自有账号”是不同概念，可以共用游戏目录，不能共用凭据和状态。

## 2. 板块一：角色筛选账号交易

### 2.1 表

```text
game
  id, code, name, status, sort_order

game_server
  id, game_id, code, name, platform, status
  UNIQUE(game_id, code)

game_attribute_definition
  id, game_id, code, category, name, value_type, filterable, status
  UNIQUE(game_id, code)
game_account
  id, game_id, server_id, external_id, title
  public_summary_json, price, status, pickable, source_id, version
  reserved_order_id, sold_order_id, created_at, updated_at
game_account_attribute
  account_id, attribute_id, int_value, text_value
  PRIMARY KEY(account_id, attribute_id)
  INDEX(attribute_id, int_value)
game_account_credential
  account_id, key_version, cipher_text, fingerprint, updated_at
game_account_reservation
  id, account_id, order_id, order_item_id, status, reserved_at, released_at, consumed_at
game_account_event
  id, account_id, from_status, to_status, reason, order_id, actor_type, actor_id, created_at
```

第一版只做指定账号购买，不做批发数量、批量拿货和按 SKU 自动配号。

### 2.2 筛选规则

角色、等级、武器、皮肤等筛选数据通过属性定义和值表达，筛选字段必须可索引，公开 JSON 只做展示摘要。第一版多个角色条件固定为 AND，同一角色数量使用 `>=`；需要 OR 时先记录决策，不在查询代码中临时扩展。

### 2.3 状态

```text
DRAFT → ON_SALE → LOCKED → SOLD → REVOKED
          └──────→ OFF_SALE
LOCKED → ON_SALE
```

普通库存同步只能处理未锁定、未售出的账号。每次状态变化写事件。

### 2.4 购买和交付

```text
用户提交 account_id
→ 服务端检查可售并重新报价
→ 原子预留 ON_SALE → LOCKED
→ 创建 Dujiao 订单和商品快照
→ 支付成功
→ LOCKED → SOLD
→ 写订单项交付引用
→ 已购订单查看凭据
```

预留必须是数据库条件更新或行锁。不得在前端锁账号或相信前端价格。支付失败、订单取消和超时释放预留。凭据单独加密，列表和普通日志不返回明文。管理员查看凭据必须有独立权限、原因和审计。

第一版售后只提供管理员审核后的退款或换号入口。指定账号不未经规则确认自动替换其他账号。

## 3. 板块二：自动代练

### 3.1 表

```text
boost_plan
  id, game_id, code, name, description, price
  parameter_schema, max_runs, valid_days, status, sort_order
boost_user_binding
  id, user_id, game_id, server_id, display_name
  credential_cipher, key_version, status, last_error_code, verified_at
boost_entitlement
  id, user_id, order_item_id, plan_id, plan_snapshot_json
  remaining_runs, expire_at, status
  UNIQUE(order_item_id)
boost_task
  id, task_no, user_id, entitlement_id, binding_id, game_id, feature_code
  parameters_json, status, idempotency_key
  worker_id, lease_token, lease_expires_at, progress_percent, progress_text
  result_json, error_code, attempt, deadline_at, created_at, updated_at
boost_task_event
  id, task_id, sequence, event_type, message, data_json, created_at
  UNIQUE(task_id, sequence)
```

第一版优先做按次权益，不做积分、自动续费和每日计划。

### 3.2 购买到任务

```text
创建代练商品
→ 用户购买
→ Dujiao 支付成功
→ 幂等创建 boost_entitlement
→ 用户选择绑定账号和功能
→ 原子消耗一次权益
→ 创建 PENDING 任务
```

权益消费和任务创建必须在一个事务或等价的幂等流程中完成；重复请求返回原任务。

### 3.3 绑定

```text
保存游戏账号 → Worker 验证 → WAIT_INPUT
→ 用户提交验证码 → PENDING → Worker 继续 → SUCCESS / FAIL
```

绑定只服务于代练，不独立成第三个板块。绑定状态和代练任务状态分开保存。凭据和验证码加密且有有效期。

### 3.4 Worker

逻辑接口：

```text
POST /worker/auth
POST /worker/tasks/pull
POST /worker/tasks/{id}/heartbeat
POST /worker/tasks/{id}/progress
POST /worker/tasks/{id}/finish
POST /worker/tasks/{id}/fail
POST /worker/tasks/{id}/cancel
```

真实 URL、认证和扩展位置以固定版本源码为准。领取必须原子化；只有当前租约 Token 能写入；旧 Token 返回 `LEASE_LOST`；所有完成、失败、取消回调带 request ID 或 sequence 去重。Worker 只上报执行事实，不能决定退款金额。

## 4. 页面

用户端：游戏列表、角色筛选账号列表、账号详情购买、已购账号凭据、代练商品、我的绑定账号、创建任务、进度、输入提示和结果。订单、会员、钱包复用 Dujiao 页面。

管理端：游戏/区服/角色属性、账号库存、上下架和同步、账号售后、代练商品、绑定任务、代练任务、Worker 节点、订单退款和审计。

## 5. 必测场景

### 账号交易

- 角色筛选准确；
- 100 并发购买一个账号只有一个成功；
- 重复支付只交付一次；
- 超时释放账号；
- 同步不覆盖 `LOCKED`/`SOLD`；
- 订单快照不随账号编辑改变；
- 凭据接口有用户隔离、权限和审计。

### 自动代练

- 支付只创建一份权益；
- 并发创建不超出权益次数；
- 两个 Worker 不能同时持有任务；
- 重复完成、失败和取消不重复结算；
- Worker 崩溃后任务可恢复或最终失败；
- `WAIT_INPUT` 可以恢复；
- 结果可见且凭据不进日志。

## 6. 实施顺序

```text
S0 固定版本和测试环境
S1 两个模块骨架
S2 账号和角色筛选
S3 账号预留、支付、交付和售后
S4 代练商品、绑定和权益
S5 Worker 任务和租约
S6 最小页面和权限
S7 并发、幂等、安全和重启测试
S8 迁移设计（不执行真实迁移）
```

## 7. 决策记录

### 2026-10-23 先做自动代练，账号交易暂缓

- 板块一（账号交易）暂缓，第一阶段只做板块二。原型中已移除账号市场和批发。
- 计费改为**积分**（覆盖 3.1 中"第一版做按次权益，不做积分"）：用户购买积分包，任务创建时冻结、完成后按实际完成量结算、剩余退回。`boost_plan` / `boost_entitlement` 由积分包 + 积分账本替代。
- 每日自动计划第一版不做。
- 游戏、区服、代练功能全部由管理后台添加。代练功能用 JSON 定义，前端按 JSON 自动生成界面（含关卡选择、通关/扫荡、次数），格式见 `docs/boost-feature-schema.md`。
- 表结构调整：`boost_feature(id, game_id, code, status, sort, version, definition_json)` + `boost_feature_version(feature_id, version, definition_json, admin_id, created_at)`；`boost_task` 增加 `feature_version, plan_json, units, units_done, points_held, points_consumed, points_released`。
- 原型：`prototype/index.html`；测试：`node --test prototype/test/catalog.test.js prototype/test/engine.test.js prototype/test/ui-smoke.test.js`，47 个全部通过。
