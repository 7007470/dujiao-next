# 002 S1：模块骨架和表结构

日期：2026-10-26　状态：已实现并验证（gofmt / vet / build / 全量 go test 189 包通过）

## 决定

1. **目录。** 按 Dujiao 现有模块的分层放代码：`internal/modules/boost/{domain,infrastructure/gormstore}` 和 `internal/modules/workerapi/{domain,infrastructure/gormstore}`。`application`、`contract`、`transport` 等到 S2 起有真实用例时再建，不建空目录，也不写占位代码（AGENTS.md §6）。`internal/workflows/boostpoints` 留到 S4。
2. **表。** 按 `docs/game-business-design.md` §2 建 15 张表，表名统一用复数（和 Dujiao 原表一致）：

   | 设计文档 | 实际表名 |
   |---|---|
   | boost_game / boost_game_server | `boost_games` / `boost_game_servers` |
   | boost_feature / boost_feature_version | `boost_features` / `boost_feature_versions` |
   | boost_point_pack / account / lot / ledger | `boost_point_packs` / `boost_point_accounts` / `boost_point_lots` / `boost_point_ledger_entries` |
   | boost_user_binding / boost_input | `boost_user_bindings` / `boost_inputs` |
   | boost_task / boost_task_event | `boost_tasks` / `boost_task_events` |
   | worker_node / worker_lease / worker_request | `worker_nodes` / `worker_leases` / `worker_requests` |

3. **迁移入口。** 两个模块各自提供 `gormstore.Migrate(db)`（参照 `reseller`），在中央 `bootstrap/database/migrations/registry.go` 的 `AutoMigrate()` 里调用。这是唯一一处改动原有代码的地方，标注了 `// BOOST-EXT:`。不改 Dujiao 原表。
4. **不变量下沉到数据库。** 能用索引保证的约束都建成唯一索引，应用层检查只是为了提前报出友好的错误：
   - 游戏 code 唯一；区服 / 功能 code 在游戏内唯一；功能版本主键是 `(feature_id, version)`；
   - 每个用户一个积分账户；每个积分批次来源只入账一次 `(source_type, source_ref)`；流水 `biz_key` 唯一；每个 SKU 只能对应一个积分包；
   - 任务号唯一；任务幂等键 `(user_id, idempotency_key)` 唯一；任务事件 `(task_id, seq)` 唯一；
   - **部分唯一索引**（SQLite 和 PostgreSQL 都支持，原生 SQL 建）：同一用户的有效绑定里同一登录名只能有一个；同一绑定账号同时只能有一个 `PROCESSING` 任务；同一目标同时只能有一个未消费的输入；同一目标同时只能有一个有效租约；
   - Worker `api_key` 唯一；请求去重主键是 `(worker_id, request_id)`。
5. **类型。** 积分、数量用 `int64` / `int`；建议退款金额用 `money.Amount`；JSON 字段用 `text` 保存原文（定义要逐字比较内容是否变化，快照要保持原样），不用 `jsonmap`。凭据、会话、输入值、Worker 密钥只有 `*Cipher` 字段，JSON 输出一律 `json:"-"`。
6. **积分包不重复存售价。** 价格以 Dujiao 商品 SKU 为准，`boost_point_packs` 只存 `product_id`、`sku_id` 和积分数量，避免两边价格不一致。和设计文档 §2.2 的 `price` 字段不同，以这里为准。

## 不做

S1 不注册任何路由，所以 `rbac_coverage_test` 不受影响；新管理路由在 S3 加入时同步补角色种子。

## 验证

- `internal/modules/boost/infrastructure/gormstore/migrate_test.go`：迁移跑两次不出错、只建 `boost_` 表、所有索引存在、每条唯一约束都会拒绝重复数据；
- `internal/modules/workerapi/infrastructure/gormstore/migrate_test.go`：同上，外加"旧租约关闭后才能发新租约"；
- `internal/bootstrap/database/migrations/sku_migration_test.go` 的 `TestAutoMigrateOwnsBoostAndWorkerSchema`：中央迁移跑两次，15 张表都在；
- 全量 `go test ./...`，包括 `internal/architecture` 的分层、文件数预算、禁止类型别名等检查。

PostgreSQL 上的部分唯一索引还没有实测，等 S4 装好 PostgreSQL 后补测。
