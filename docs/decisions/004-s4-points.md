# 004 S4：积分

日期：2026-10-28　状态：已实现并验证（gofmt / vet / build / 全量 go test 191 包通过；admin vue-tsc 通过；真实二进制 + SQLite 冒烟通过）

## 决定

1. **积分包 = Dujiao 商品。** 新增交付类型 `boost_points`（`constants.FulfillmentTypeBoostPoints`）。售价、会员价、支付渠道都在商品 / SKU 上配置；`boost_point_packs` 只存 SKU → 积分数 / 赠送 / 有效天数。新建积分包时校验 SKU 所属商品的交付类型，每个 SKU 只能配一个积分包，SKU 创建后不可改。
2. **到账在支付成功时同步执行，不走队列。** 设计稿写的是 `boost:grant_points` 作业，但 `enqueueOrderPaidAsync` 在队列未启用时会提前返回（不开 Redis 就不到账），所以改为在这个函数里、"队列未启用就返回"之前同步调用 `BoostPointsGranter.GrantForPaidOrder`。失败不影响支付结果，由定时任务 `boost:points_maintain`（`@every 1m`，补发支付超过 2 分钟仍未到账的订单 + 处理到期）兜底。
3. **同一事务。** `workflows/boostpoints/infrastructure/gormuow` 在一个 GORM 事务里：锁订单行和交付记录 → 写积分批次 + 流水 → 写 `fulfillments`（type=`boost_points`，payload"已到账 N 积分"）→ 子订单置 `completed` → 同步父订单状态。只到账一次靠三层：交付记录存在即跳过；批次 `(order, order_item:{id})` 唯一；流水 `biz_key` 唯一。
4. **Dujiao 下单总是父订单 + 每个商品一个子订单。** 到账、撤回、退款检查都按子订单处理；入口传父订单 ID 或订单号时自动展开到含积分包的子订单。
5. **账户锁。** 所有积分写操作在事务里先 `LockAccount`（PostgreSQL `SELECT … FOR UPDATE`；SQLite 驱动忽略锁子句，靠单写事务串行），`SaveAccount` 再带 `version` 条件更新，版本不对整个事务回滚。SQLite 上的并发测试（20 个 goroutine 抢 100 点，每次 30）只成功 3 次。**PostgreSQL 真并发未验证**（沙箱没有 PostgreSQL），列入 S8。
6. **到期。** 批次 `expires_at` 从到账时算。除定时扫描外，冻结、扣减、查询前都先把该用户已到期的批次处理掉，所以定时任务没跑时过期积分也花不掉。退回到已过期 / 已撤回批次的积分立即失效（写 `expire` / `revoke` 流水）。
7. **退款拦截（C11）。** `refund.Service.AdminManualRefund` / `AdminRefundToWallet` 和 `OrderService.UpdateOrderStatus`（改成 `refunded` / `partially_refunded`）都调用 `RefundGuard.EnsureRefundable`：积分包子订单的批次没撤回就返回 `ErrOrderRefundBlocked`（`error.order_refund_points_revoke_required`）。检查在订单事务之前做（不和订单行锁嵌套），撤回本身也锁同一订单行，二者串行。
8. **撤回与建议金额。** `RevokeForOrder` 只撤批次剩余（已用、冻结中的不追回；冻结中的之后退回时因批次已撤回而作废），建议金额 = `(实付 − 已退) × 剩余 / 到账总数`，向下取整到分，写在批次 `suggest_refund` 上，重复调用返回原值。
9. **管理员发放 / 扣减。** 必须填原因（≤100 字）和请求号（前端每次打开对话框生成，网络重试不重复）。`biz_key` = `grant:admin:{admin}:{request}` / `admin_adjust:{admin}:{request}`。扣减只扣可用、按 FIFO。写操作记入 `authz_audit_logs`。
10. **权限。** `operations` 角色：积分包、账户 / 流水查询、发放 / 扣减。`finance` 角色：撤回（`/admin/boost/points/revoke-order`）、账户 / 流水查询。
11. **游客不能买积分包**（下单校验返回 `ErrProductPurchaseNotAllowed`）；购物车本来就只接受 manual / auto，用户端"加入购物车"对积分包改成直接立即购买。
12. **冻结 / 结算接口已就绪但还没有调用方。** `HoldPoints(repo, …)` / `SettleTaskPoints(repo, …)` 接收事务内的仓储，S5 建任务时在同一事务里调用。

## 改动的 Dujiao 原有代码（都标了 `// BOOST-EXT`）

| 位置 | 改动 |
|---|---|
| `constants/constants.go` | `FulfillmentTypeBoostPoints`、`TaskBoostPointsMaintain` |
| `catalog/product/domain/purchase_policy.go` | `NormalizeFulfillmentType` 接受新类型 |
| `catalog/product/transport/http/public_stock.go` | 积分包始终可售（否则走 auto 分支显示售罄） |
| `notification/application/format/order_format.go` | 新类型不当成人工交付（否则发"待人工发货"通知） |
| `order/application/order_service_validate.go` | 放行新类型；游客拒绝 |
| `order/application/{errors,order_service,order_service_child}.go` | `ErrOrderRefundBlocked`、`SetRefundGuard`、状态接口拦截 |
| `order/application/refund/{errors,service,wallet}.go` | `RefundGuard`，两个退款入口拦截 |
| `order/transport/http/{admin_handler,admin_refund_handler}.go`、`bootstrap/order/adapters.go` | 错误映射 |
| `payment/application/payment_service{,_callback_dispatch}.go` | `BoostPointsGranter` 选项和调用 |
| `app/container/*`、`app/httpserver/routes_*`、`authz/bootstrap.go`、`i18n/messages.go` | 装配、路由、角色种子、错误文案 |
| `app/jobs/*`、`queue/tasks.go`、`architecture/worker_consumer_structure_test.go` | 维护任务 |
| 前端 admin `ProductEditModal.vue`；user `useProduct.ts` / `useProductDetail.ts` / `fulfillment.ts` | 新交付类型选项和文案；积分包跳过购物车 |

`shouldMarkFulfilling` 没改：支付后子订单停在 `paid`，到账时直接置 `completed`（和自动交付一样）。

## 页面

- 后台"代练管理 → 代练积分"（`/boost/points`）：用户积分（搜索、明细：批次 + 流水、发放、扣减）、积分包（选 `boost_points` 商品的 SKU）、积分流水（按用户 / 类型筛选）、退款撤回（输入订单号 → 撤回 → 显示建议金额）。
- 商品编辑的交付类型多了"代练积分"。
- 用户端 `/boost/points`（需登录）：可用 / 冻结 / 30 天内到期、积分包卡片（点进去是普通商品页，原生下单支付）、积分明细（不显示操作管理员和 biz_key，平台发放 / 扣减加"平台调整："前缀）。游戏页报价卡显示可用积分和"购买积分"链接。

## 验证

- `internal/modules/boost/integrationtest/points_*_test.go`（15 个）用真实的 Dujiao 订单 / 支付 / 退款服务和完整迁移跑 SQLite：在线支付重复回调只到账一次、余额支付、超时取消、游客拒绝、配置缺失后补发、积分包校验、FIFO / 到期 / 发放幂等、退回到已过期批次、冻结不足 / 幂等 / 过期不可花、并发冻结不超额、发放扣减幂等和校验、退款拦截（手动 / 余额 / 状态接口）+ 撤回比例 + 撤回后退款、600 步随机操作不变量、HTTP 全流程和审计。
- 真实二进制（`-mode api`，SQLite，无 Redis）冒烟：建分类 → 建 `boost_points` 商品 → 配积分包 → 公开列表 / 商品页可售 → 发放（重复请求同一流水）→ 超额扣减被拒 → 扣减 → 账户 / 流水 / 批次 → 审计记录 → 不变量。
- user 前端：沙箱缺 `@unhead/vue`、`@tiptap/extension-underline` 两个依赖，借用 admin 的 node_modules 跑 vue-tsc，只剩这两个包的"找不到模块"错误，本次改动的文件无错误。

## 未验证

- PostgreSQL 上的真并发（行锁）。
- 浏览器里的页面效果。
- 带 Redis 的 worker 模式下 `boost:points_maintain` 的调度（逻辑在测试里直接调用验证过）。
