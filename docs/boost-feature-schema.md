# 代练功能定义 JSON 规范（schema_version 1）

后台为每个游戏添加代练功能时，提交一份 JSON。前端按这份 JSON 自动生成下单界面，服务端用同一份 JSON 校验参数、计算积分、生成执行步骤交给执行端。

同一份规则在三处使用，实现上必须共用一份代码：

| 位置 | 作用 |
|---|---|
| 后台保存 | `validateDefinition` 校验结构，不通过不能保存 |
| 用户端 | `compile` 计算可见字段、默认值、字段错误和实时报价 |
| 服务端下单 | 再次 `compile`，前端传来的价格一律不使用 |

原型实现见 `prototype/src/catalog.js`。Go 版本移植这一个文件即可，测试用例见 `prototype/test/catalog.test.js`。

## 1. 顶层

```json
{
  "schema_version": 1,
  "code": "main_stage",
  "name": "主线关卡",
  "description": "选择关卡，通关或扫荡。",
  "capability": "main_stage",
  "timeout_minutes": 240,
  "max_active_per_user": 3,
  "refund_policy": "prorate",
  "member_discount": true,
  "base_points": 0,
  "catalogs": { },
  "fields": [ ],
  "limits": { "max_units": 500 }
}
```

| 字段 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `schema_version` | 否 | 1 | 只支持 1 |
| `code` | 是 | — | 2–32 位小写字母、数字、下划线，字母开头。同一游戏内唯一，创建后不可改 |
| `name` | 是 | — | 最多 40 字 |
| `description` | 否 | 空 | 最多 300 字 |
| `capability` | 否 | 等于 `code` | 执行端按"游戏 code + capability"领取任务 |
| `timeout_minutes` | 否 | 120 | 5–1440。超时后任务失败，按退款规则结算 |
| `max_active_per_user` | 否 | 3 | 1–20。同一用户同时进行中的该功能任务数 |
| `refund_policy` | 否 | `prorate` | 见第 5 节 |
| `member_discount` | 否 | true | 是否按会员等级折扣 |
| `base_points` | 否 | 0 | 每个任务固定收取的积分，开始执行后才收 |
| `catalogs` | 否 | `{}` | 关卡、楼层、野怪等可选目录，见第 2 节 |
| `fields` | 是 | — | 1–30 个字段，按顺序渲染，见第 3 节 |
| `limits.max_units` | 否 | 500 | 单个任务最多的执行单位数 |

未知的顶层字段和字段属性会给出警告并被忽略，不会报错。

## 2. 目录 catalogs

目录是管理员提供的一组可选条目，例如关卡。按分组（章节）组织。

```json
"catalogs": {
  "main": {
    "label": "关卡",
    "groups": [
      {
        "code": "ch1",
        "name": "第一章 · 晨曦平原",
        "items": [
          { "code": "1-1", "name": "1-1 迷雾入口", "cost": { "clear": 5, "sweep": 2 }, "tags": ["经验"] },
          { "code": "1-4", "name": "1-4 守望者（BOSS）", "cost": { "clear": 10, "sweep": 4 }, "tags": ["BOSS"] },
          { "code": "3-3", "name": "3-3 灯塔顶层", "cost": { "clear": 12 }, "tags": ["不可扫荡"] },
          { "code": "3-4", "name": "3-4 深海巨像", "cost": { "clear": 20 }, "disabled": true }
        ]
      }
    ]
  }
}
```

| 属性 | 说明 |
|---|---|
| 目录名（键） | 小写字母开头，供 `stage_picker.catalog` 引用 |
| `label` | 条目的叫法，如"关卡""楼层""野怪"，用于界面和提示 |
| `groups[].code` / `name` | 分组，界面上显示为页签 |
| `items[].code` | 目录内唯一。字母、数字、`-` `_` `.`，最多 32 位。执行端用它定位关卡 |
| `items[].name` | 显示名 |
| `items[].cost` | 整数：每次的积分。或对象：按模式区分，如 `{"clear":5,"sweep":2}`；缺少某个模式表示该条目不支持这个模式 |
| `items[].tags` | 可选，字符串数组，显示为小标签，也参与搜索 |
| `items[].disabled` | 可选，true 表示暂不开放（显示但不能选） |

## 3. 字段 fields

所有字段都有：

| 属性 | 说明 |
|---|---|
| `key` | 小写字母开头，字段间唯一。用户提交的参数以它为键 |
| `type` | 见下表 |
| `label` | 最多 30 字 |
| `help` | 可选，字段下方的说明 |
| `required` | 可选，默认 false |
| `default` | 可选，默认值 |
| `visible_when` | 可选，条件显示，见第 4 节 |

### 3.1 类型

| type | 用户看到 | 计价 | 专有属性 |
|---|---|---|---|
| `radio` | 单选按钮 | `options[].points` 加价 | `options: [{value, label, points?}]` |
| `select` | 下拉 | 同上 | 同上 |
| `checkbox` | 多选 | 每个选中项的 `points` | `options`、`min_select`、`max_select`；`default` 为数组 |
| `switch` | 开关 | 开启时加 `points` | `points`；`default` 为布尔 |
| `number` | 数字输入 + 加减按钮 | 设了 `points_per` 时按数量计费（每个数量是一个执行单位） | `min`、`max`（必填整数）、`unit`、`points_per` |
| `text` | 文本框 | 不计价 | `maxlength`（1–200，默认 100）、`pattern`（正则）、`placeholder` |
| `stage_picker` | 关卡选择器：分组页签、搜索、勾选、每项次数 | 每个选中条目的 `cost × 次数` | 见 3.2 |

### 3.2 stage_picker

```json
{
  "key": "stages",
  "type": "stage_picker",
  "label": "选择关卡",
  "catalog": "main",
  "multiple": true,
  "max_select": 12,
  "required": true,
  "cost_by": "mode",
  "count": { "label": "扫荡次数", "min": 1, "max": 99, "default": 10, "visible_when": { "mode": "sweep" } }
}
```

| 属性 | 说明 |
|---|---|
| `catalog` | 必填，引用 `catalogs` 的目录名 |
| `multiple` | 默认 false（单选） |
| `max_select` | 多选时的上限 |
| `cost_by` | 条目 `cost` 是对象时必填，指向排在前面的一个 `radio` 或 `select` 字段，用它的值选价格 |
| `count` | 可选。存在时每个选中条目都可以设次数（`min ≥ 1`，`max ≤ 999`）；`count.visible_when` 不成立时次数固定为 1 |

提交值是数组，每项 `{"item": "1-1", "count": 10}`；没有次数时也可以只传 `"1-1"`。

## 4. 条件显示 visible_when

```json
{ "key": "use_stamina_potion", "type": "switch", "label": "体力不足时使用体力药", "visible_when": { "mode": "sweep" } }
```

- 只能引用排在前面的 `radio`、`select` 或 `switch` 字段。
- 值可以是单个值或数组（任一匹配即显示），多个键之间是"并且"。
- 隐藏的字段不取值、不计价、不校验，不会出现在提交给执行端的参数里。

## 5. 计价与结算

**报价：**

```text
原价 = base_points + 各附加项 points + Σ(条目 cost × 次数) + number 数量 × points_per
合计 = member_discount ? floor(原价 × 会员折扣率 / 100) : 原价（至少 1 点）
```

会员折扣只在合计上计算一次，再向下取整，避免小额单价逐项取整导致折扣失真。合计为 0 的组合不能下单。

**执行单位：** 每个选中条目的每一次、`number` 计费字段的每一个数量，都是一个执行单位。服务端把它们按字段顺序展开为步骤列表交给执行端：

```json
{ "steps": [
  { "seq": 1, "label": "1-1 迷雾入口", "count": 10, "ref": { "field": "stages", "item": "1-1", "group": "ch1", "mode": "sweep" } },
  { "seq": 2, "label": "1-4 守望者（BOSS）", "count": 3, "ref": { "field": "stages", "item": "1-4", "group": "ch1", "mode": "sweep" } }
] }
```

没有按量计费的字段时，生成一个隐含步骤，代表"执行一次"。

**结算：** 执行端按步骤分别上报完成数（`steps: [{seq, done}]`），不能决定扣多少积分。每一步按自己的完成数计价，**不假设执行顺序**（2026-10-29 用户选定；之前的"只报总数、按步骤顺序累加"已废弃，见 `docs/decisions/005-s5-binding-task.md` 补充）。

```text
已完成部分的价格 = min(合计, floor((base + 附加项 + Σ 每一步完成数 × 该步单价) × 折扣率 / 100))
一步都没开始 = 0（base 和附加项只在开始执行后收）
每一步都完成 = 合计
```

每一步的完成数只增不减、超过计划数按计划数计。例：断桥扫荡 10 次 × 2、BOSS 扫荡 10 次 × 4，冻结 60；先刷完 BOSS、断桥只做 8 次 → 扣 16 + 40 = 56，退 4。

| refund_policy | 成功 / 取消 | 失败（系统原因：网络、维护、执行端故障、登录失败、验证码超时、执行端失联） | 失败（用户原因：关卡未解锁、体力不足、顶号、超时） |
|---|---|---|---|
| `prorate` | 已完成部分 | 已完成部分 | 已完成部分 |
| `full_on_system_fail` | 已完成部分 | 0（全退） | 已完成部分 |
| `none` | 已完成部分 | 全部冻结积分 | 全部冻结积分 |

失败原因的分类以服务端错误码表为准，执行端上报的 `retryable` 只对未知错误码生效。

## 6. 版本

- 每次保存生成新版本（`v1`、`v2`…），内容不变时不升版本。
- 任务创建时保存功能版本号和编译后的执行计划快照。之后改价、改关卡、下架都不影响已创建的任务。
- 保存时带上当前版本号，版本已被别人改过则拒绝保存（`VERSION_CONFLICT`）。
- `code` 创建后不可修改。

## 7. 完整示例

`prototype/src/seed.js` 中的 `DEFS` 包含 6 个示例：主线关卡（通关/扫荡分价）、每日委托（基础价 + 多选附加）、深境螺旋（按楼层、下拉加价）、挂机采集（按分钟）、资源收取（基础价 + 开关）、自动打野（单选目标 + 次数）。
