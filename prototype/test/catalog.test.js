// 代练功能 JSON 规范：校验与编译
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const S = require('../src/catalog.js');
const { DEFS } = require('../src/seed.js');

const clone = (v) => JSON.parse(JSON.stringify(v));
const errPaths = (def) => S.validateDefinition(def).errors.map((e) => e.path);

test('所有示例定义都通过校验', () => {
  for (const [k, d] of Object.entries(DEFS)) {
    const r = S.validateDefinition(d);
    assert.ok(r.ok, k + ' ' + JSON.stringify(r.errors));
  }
});

test('校验：基本字段', () => {
  assert.ok(errPaths({}).includes('code'));
  assert.ok(errPaths({ code: 'Bad-Code', name: 'x', fields: [{ key: 'a', type: 'switch', label: 'a' }] }).includes('code'));
  assert.ok(errPaths({ code: 'ok', name: 'x', fields: [] }).includes('fields'));
  assert.ok(errPaths({ code: 'ok', name: 'x', schema_version: 2, fields: [{ key: 'a', type: 'switch', label: 'a' }] }).includes('schema_version'));
  assert.ok(errPaths({ code: 'ok', name: 'x', refund_policy: 'maybe', fields: [{ key: 'a', type: 'switch', label: 'a' }] }).includes('refund_policy'));
  assert.ok(S.validateDefinition({ code: 'ok', name: 'x', fields: [{ key: 'a', type: 'switch', label: 'a' }], foo: 1 }).warnings.length === 1);
});

test('校验：字段重复、未知类型、选项', () => {
  const p = errPaths({
    code: 'ok', name: 'x',
    fields: [
      { key: 'a', type: 'radio', label: 'A', options: [{ value: 'x', label: 'X' }, { value: 'x', label: 'Y' }], default: 'z' },
      { key: 'a', type: 'switch', label: 'dup' },
      { key: 'b', type: 'slider', label: 'B' },
      { key: 'c', type: 'number', label: 'C', min: 10, max: 5 },
    ],
  });
  assert.ok(p.includes('fields[0].options[1].value'));
  assert.ok(p.includes('fields[0].default'));
  assert.ok(p.includes('fields[1].key'));
  assert.ok(p.includes('fields[2].type'));
  assert.ok(p.includes('fields[3]'));
});

test('校验：visible_when 只能引用前面的字段且值合法', () => {
  const p = errPaths({
    code: 'ok', name: 'x',
    fields: [
      { key: 'b', type: 'switch', label: 'B', visible_when: { a: 'x' } },
      { key: 'a', type: 'radio', label: 'A', options: [{ value: 'x', label: 'X' }] },
      { key: 'c', type: 'switch', label: 'C', visible_when: { a: 'nope' } },
    ],
  });
  assert.ok(p.includes('fields[0].visible_when.a'));
  assert.ok(p.includes('fields[2].visible_when.a'));
});

test('校验：关卡目录', () => {
  const d = clone(DEFS.starfall_main);
  d.catalogs.main.groups[0].items[1].code = '1-1'; // 重复
  d.catalogs.main.groups[1].items[0].cost = { clear: 7, raid: 3 }; // raid 不是模式
  d.catalogs.main.groups[2].items[0].cost = -1;
  d.fields[1].catalog = 'missing';
  const p = errPaths(d);
  assert.ok(p.some((x) => x.endsWith('items[1].code')));
  assert.ok(p.includes('fields[1].catalog'));
  assert.ok(p.some((x) => x.includes('cost')));
});

test('校验：分模式价格必须配 cost_by', () => {
  const d = clone(DEFS.starfall_main);
  delete d.fields[1].cost_by;
  assert.ok(errPaths(d).includes('fields[1].cost_by'));
  const d2 = clone(DEFS.starfall_main);
  d2.fields[1].cost_by = 'use_stamina_potion';
  assert.ok(errPaths(d2).includes('fields[1].cost_by')); // 只能引用前面的 radio/select
});

test('编译：扫荡按次数计价，通关每关一次', () => {
  const d = DEFS.starfall_main;
  const sweep = S.compile(d, { mode: 'sweep', stages: [{ item: '1-1', count: 10 }, { item: '1-4', count: 2 }] });
  assert.ok(sweep.ok, sweep.errors.join());
  assert.equal(sweep.total, 10 * 2 + 2 * 4);
  assert.equal(sweep.units, 12);
  assert.deepEqual(sweep.steps.map((s) => [s.label, s.count]), [['1-1 迷雾入口', 10], ['1-4 守望者（BOSS）', 2]]);
  const clear = S.compile(d, { mode: 'clear', stages: [{ item: '1-1', count: 10 }, '3-3'] });
  assert.ok(clear.ok, clear.errors.join());
  assert.equal(clear.total, 5 + 12); // 通关模式不显示次数，次数被忽略
  assert.equal(clear.units, 2);
});

test('编译：不支持的模式、禁用关卡、不存在的关卡、超出上限', () => {
  const d = DEFS.starfall_main;
  assert.match(S.compile(d, { mode: 'sweep', stages: ['3-3'] }).errors[0], /不支持扫荡/);
  assert.match(S.compile(d, { mode: 'clear', stages: ['3-4'] }).errors[0], /暂不可选/);
  assert.match(S.compile(d, { mode: 'clear', stages: ['9-9'] }).errors[0], /不存在/);
  assert.match(S.compile(d, { mode: 'sweep', stages: [{ item: '1-1', count: 100 }] }).errors[0], /1–99/);
  const many = ['1-1', '1-2', '1-3', '1-4', '2-1', '2-2', '2-3', '2-4', '3-1', '3-2', '3-3', '1-1'];
  assert.ok(S.compile(d, { mode: 'clear', stages: many }).ok); // 11 个（重复的被去掉）
  const tooMany = { ...clone(d) };
  tooMany.fields[1].max_select = 3;
  assert.match(S.compile(tooMany, { mode: 'clear', stages: many }).errors[0], /最多选 3/);
  assert.match(S.compile(d, { mode: 'sweep', stages: [] }).errors[0], /请选择/);
});

test('编译：隐藏字段不取值也不计价', () => {
  const r = S.compile(DEFS.starfall_main, { mode: 'clear', stages: ['1-1'], use_stamina_potion: true });
  assert.equal(r.visible.use_stamina_potion, false);
  assert.equal(r.values.use_stamina_potion, undefined);
});

test('编译：基础价 + 附加项 + 下拉加价', () => {
  const daily = S.compile(DEFS.starfall_daily, { extras: ['dispatch', 'shop', 'mail'] });
  assert.equal(daily.total, 20 + 5 + 3);
  assert.deepEqual(daily.values.extras, ['mail', 'dispatch', 'shop']);
  assert.match(S.compile(DEFS.starfall_daily, { extras: ['hack'] }).errors[0], /不合法/);
  const abyss = S.compile(DEFS.starfall_abyss, { floors: ['f9', 'f12'], target: 'three_star' });
  assert.equal(abyss.total, 15 + 35 + 10);
});

test('编译：按数值计费（分钟）', () => {
  const r = S.compile(DEFS.starfall_idle, { area: 'plain', minutes: 90 });
  assert.equal(r.total, 90);
  assert.equal(r.units, 90);
  assert.match(S.compile(DEFS.starfall_idle, { area: 'plain', minutes: 10 }).errors[0], /30–480/);
  assert.match(S.compile(DEFS.starfall_idle, { minutes: 60 }).errors[0], /采集区域/);
});

test('编译：单选目标 + 次数', () => {
  const r = S.compile(DEFS.ironcrest_march, { target: [{ item: 'lv15', count: 20 }], troop: 'archer' });
  assert.equal(r.total, 60);
  assert.match(S.compile(DEFS.ironcrest_march, { target: ['lv10', 'lv15'] }).errors[0], /只能选一个/);
});

test('会员折扣只在合计上计算一次，部分完成结算单调且不超总价', () => {
  const r = S.compile(DEFS.starfall_main, { mode: 'sweep', stages: [{ item: '1-1', count: 10 }, { item: '2-4', count: 3 }] }, { discountRate: 90 });
  assert.equal(r.rawTotal, 35);
  assert.equal(r.total, 31);
  let prev = -1;
  for (let u = 0; u <= r.units; u++) {
    const p = S.pointsForUnits(r, u);
    assert.ok(p >= prev && p <= r.total);
    prev = p;
  }
  assert.equal(S.pointsForUnits(r, r.units), r.total);
  assert.equal(S.pointsForUnits(r, 0), 0);
  // member_discount=false 的功能不打折
  assert.equal(S.compile(DEFS.starfall_idle, { area: 'plain', minutes: 60 }, { discountRate: 50 }).total, 60);
});

test('总价为 0 的定义不能下单；执行单位超过上限被拒绝', () => {
  const d = { code: 'free', name: '免费', fields: [{ key: 'a', type: 'switch', label: 'A' }] };
  assert.match(S.compile(d, {}).errors[0], /为 0/);
  const big = clone(DEFS.starfall_idle);
  big.limits = { max_units: 100 };
  assert.match(S.compile(big, { area: 'plain', minutes: 200 }).errors[0], /最多 100/);
});

test('文本字段：长度和正则', () => {
  const d = { code: 'uid_task', name: 't', base_points: 1, fields: [{ key: 'uid', type: 'text', label: 'UID', required: true, pattern: '^[0-9]{9}$', maxlength: 9 }] };
  assert.ok(S.validateDefinition(d).ok);
  assert.ok(S.compile(d, { uid: '123456789' }).ok);
  assert.match(S.compile(d, { uid: '12ab' }).errors[0], /格式/);
  assert.match(S.compile(d, {}).errors[0], /请填写/);
  assert.ok(!S.validateDefinition({ ...d, fields: [{ ...d.fields[0], pattern: '([' }] }).ok);
});
