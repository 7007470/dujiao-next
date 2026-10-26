// 生成 JS/Go 对照用例：用原型 catalog.js 计算期望结果，Go 测试逐条比对。
// 用法（仓库根目录）：node prototype/tools/gen-featuredef-golden.js
// 输出：internal/modules/boost/domain/featuredef/testdata/golden.json
// 原型规则改动后必须重新生成并提交；Go 测试读取该文件，不依赖 Node。
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const S = require('../src/catalog.js');
const { DEFS } = require('../src/seed.js');

const clone = (v) => JSON.parse(JSON.stringify(v));

// 可复现的伪随机数（mulberry32），保证每次生成的文件相同
function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = rng(20261026);
const pick = (arr) => arr[Math.floor(rand() * arr.length)];
const chance = (p) => rand() < p;

const validateCases = [];
const compileCases = [];
const addValidate = (name, def) => validateCases.push({ name, def });
const addCompile = (name, def, params, rate) => compileCases.push({ name, def, params, rate });

// ---------- 1. 6 个示例定义本身 + catalog.test.js 里的非法定义 ----------
for (const [k, d] of Object.entries(DEFS)) addValidate('seed ' + k, d);
const sw = [{ key: 'a', type: 'switch', label: 'a' }];
addValidate('empty object', {});
addValidate('not object', [1, 2]);
addValidate('null', null);
addValidate('bad code', { code: 'Bad-Code', name: 'x', fields: sw });
addValidate('empty fields', { code: 'ok', name: 'x', fields: [] });
addValidate('schema_version 2', { code: 'ok', name: 'x', schema_version: 2, fields: sw });
addValidate('refund_policy', { code: 'ok', name: 'x', refund_policy: 'maybe', fields: sw });
addValidate('unknown top key', { code: 'ok', name: 'x', fields: sw, foo: 1 });
addValidate('field errors', {
  code: 'ok', name: 'x',
  fields: [
    { key: 'a', type: 'radio', label: 'A', options: [{ value: 'x', label: 'X' }, { value: 'x', label: 'Y' }], default: 'z' },
    { key: 'a', type: 'switch', label: 'dup' },
    { key: 'b', type: 'slider', label: 'B' },
    { key: 'c', type: 'number', label: 'C', min: 10, max: 5 },
  ],
});
addValidate('visible_when order', {
  code: 'ok', name: 'x',
  fields: [
    { key: 'b', type: 'switch', label: 'B', visible_when: { a: 'x' } },
    { key: 'a', type: 'radio', label: 'A', options: [{ value: 'x', label: 'X' }] },
    { key: 'c', type: 'switch', label: 'C', visible_when: { a: 'nope' } },
  ],
});
{
  const d = clone(DEFS.starfall_main);
  d.catalogs.main.groups[0].items[1].code = '1-1';
  d.catalogs.main.groups[1].items[0].cost = { clear: 7, raid: 3 };
  d.catalogs.main.groups[2].items[0].cost = -1;
  d.fields[1].catalog = 'missing';
  addValidate('catalog errors', d);
  const d2 = clone(DEFS.starfall_main);
  delete d2.fields[1].cost_by;
  addValidate('missing cost_by', d2);
  const d3 = clone(DEFS.starfall_main);
  d3.fields[1].cost_by = 'use_stamina_potion';
  addValidate('cost_by not radio', d3);
}
const uid = { code: 'uid_task', name: 't', base_points: 1, fields: [{ key: 'uid', type: 'text', label: 'UID', required: true, pattern: '^[0-9]{9}$', maxlength: 9 }] };
addValidate('text ok', uid);
addValidate('text bad pattern', { ...uid, fields: [{ ...uid.fields[0], pattern: '([' }] });

// ---------- 1b. 边界值：每个长度 / 数值上限的"刚好合法"和"刚好超出" ----------
{
  const base = () => ({ code: 'edge', name: 'x', fields: [{ key: 'a', type: 'switch', label: 'a', points: 1 }] });
  const at = (n, ch) => (ch || '字').repeat(n);
  const edge = (name, patch) => {
    const d = base();
    patch(d);
    addValidate('edge ' + name, d);
  };
  for (const n of [40, 41]) edge(`name ${n}`, (d) => (d.name = at(n)));
  edge('name emoji 20', (d) => (d.name = at(20, '😀'))); // 40 个 UTF-16 码元
  edge('name emoji 21', (d) => (d.name = at(21, '😀')));
  edge('name blank', (d) => (d.name = ' 　 '));
  for (const n of [300, 301]) edge(`description ${n}`, (d) => (d.description = at(n)));
  for (const n of [2, 1, 32, 33]) edge(`code len ${n}`, (d) => (d.code = 'a' + 'b'.repeat(n - 1)));
  for (const n of [30, 31]) edge(`label ${n}`, (d) => (d.fields[0].label = at(n)));
  for (const [k, lo, hi] of [['timeout_minutes', 5, 1440], ['max_active_per_user', 1, 20], ['base_points', 0, 100000]]) {
    for (const v of [lo - 1, lo, hi, hi + 1, lo + 0.5, String(lo)]) edge(`${k} ${v}`, (d) => (d[k] = v));
  }
  for (const v of [0, 1, 10000, 10001]) edge(`max_units ${v}`, (d) => (d.limits = { max_units: v }));
  edge('fields 30', (d) => (d.fields = Array.from({ length: 30 }, (_, i) => ({ key: 'f' + i, type: 'switch', label: 'f' + i }))));
  edge('fields 31', (d) => (d.fields = Array.from({ length: 31 }, (_, i) => ({ key: 'f' + i, type: 'switch', label: 'f' + i }))));
  for (const n of [32, 33]) edge(`option value ${n}`, (d) => (d.fields = [{ key: 'r', type: 'radio', label: 'r', options: [{ value: 'v'.repeat(n), label: 'x', points: 1 }] }]));
  for (const n of [8, 9]) edge(`unit ${n}`, (d) => (d.fields = [{ key: 'n', type: 'number', label: 'n', min: 1, max: 5, unit: at(n), points_per: 1 }]));
  for (const v of [0, 1, 200, 201]) edge(`maxlength ${v}`, (d) => d.fields.push({ key: 't', type: 'text', label: 't', maxlength: v }));
  for (const [mn, mx] of [[1, 999], [0, 5], [1, 1000], [5, 4]]) edge(`count ${mn}-${mx}`, (d) => {
    d.catalogs = { c: { label: 'c', groups: [{ code: 'g', name: 'g', items: [{ code: 'i', name: 'i', cost: 1 }] }] } };
    d.fields = [{ key: 's', type: 'stage_picker', label: 's', catalog: 'c', count: { min: mn, max: mx } }];
  });
  for (const v of [-1, 0, 50, 51]) edge(`min_select ${v}`, (d) => (d.fields = [{ key: 'c', type: 'checkbox', label: 'c', min_select: v, options: [{ value: 'a', label: 'a', points: 1 }] }]));
  for (const n of [32, 33]) edge(`item code ${n}`, (d) => {
    d.catalogs = { c: { label: 'c', groups: [{ code: 'g', name: 'g', items: [{ code: 'i'.repeat(n), name: 'i', cost: 1 }] }] } };
    d.fields = [{ key: 's', type: 'stage_picker', label: 's', catalog: 'c' }];
  });

  // 编译时的长度、数值边界
  const t = (maxlength) => ({ code: 'tx', name: 't', base_points: 1, fields: [{ key: 'u', type: 'text', label: 'U', maxlength }] });
  for (const n of [100, 101]) addCompile(`edge text default max ${n}`, t(undefined), { u: at(n) });
  for (const n of [9, 10]) addCompile(`edge text maxlength 9 got ${n}`, t(9), { u: at(n, '😀').slice(0, n) });
  addCompile('edge text trimmed', t(3), { u: '　 abc \n' });
  const num = { code: 'nm', name: 'n', fields: [{ key: 'n', type: 'number', label: 'N', min: 1, max: 10, points_per: 3, unit: '分钟' }] };
  for (const v of [0, 1, 10, 11, '5', '0b11', '1e1', true, [4], '']) addCompile(`edge number ${JSON.stringify(v)}`, num, { n: v }, 90);
}

// ---------- 2. 随机变异：每个示例定义改坏一处或几处 ----------
const junk = [undefined, null, true, false, 0, -1, 1.5, 3, 99999999, '', ' ', 'x', 'Bad Code', 'ok_code', [], [1], {}, { a: 1 }];
function paths(obj, prefix, out) {
  if (obj && typeof obj === 'object') {
    for (const k of Object.keys(obj)) {
      const p = prefix.concat([Array.isArray(obj) ? Number(k) : k]);
      out.push(p);
      paths(obj[k], p, out);
    }
  }
  return out;
}
function setAt(obj, p, v) {
  let o = obj;
  for (let i = 0; i < p.length - 1; i++) o = o[p[i]];
  const last = p[p.length - 1];
  if (v === undefined) {
    if (Array.isArray(o)) o.splice(last, 1);
    else delete o[last];
  } else o[last] = v;
}
for (const [k, d] of Object.entries(DEFS)) {
  const all = paths(d, [], []);
  for (let i = 0; i < 40; i++) {
    const m = clone(d);
    const n = 1 + Math.floor(rand() * 3);
    for (let j = 0; j < n; j++) {
      const live = paths(m, [], []);
      if (!live.length) break;
      setAt(m, pick(live), pick(junk));
    }
    addValidate(`mutate ${k} #${i}`, m);
  }
  void all;
}

// ---------- 3. 编译：catalog.test.js 的场景 ----------
const M = DEFS.starfall_main;
addCompile('sweep counts', M, { mode: 'sweep', stages: [{ item: '1-1', count: 10 }, { item: '1-4', count: 2 }] });
addCompile('clear ignores count', M, { mode: 'clear', stages: [{ item: '1-1', count: 10 }, '3-3'] });
addCompile('sweep unsupported', M, { mode: 'sweep', stages: ['3-3'] });
addCompile('disabled stage', M, { mode: 'clear', stages: ['3-4'] });
addCompile('missing stage', M, { mode: 'clear', stages: ['9-9'] });
addCompile('count over max', M, { mode: 'sweep', stages: [{ item: '1-1', count: 100 }] });
const many = ['1-1', '1-2', '1-3', '1-4', '2-1', '2-2', '2-3', '2-4', '3-1', '3-2', '3-3', '1-1'];
addCompile('many dedup', M, { mode: 'clear', stages: many });
{
  const t = clone(M);
  t.fields[1].max_select = 3;
  addCompile('max_select', t, { mode: 'clear', stages: many });
}
addCompile('empty sweep', M, { mode: 'sweep', stages: [] });
addCompile('hidden field', M, { mode: 'clear', stages: ['1-1'], use_stamina_potion: true });
addCompile('daily extras', DEFS.starfall_daily, { extras: ['dispatch', 'shop', 'mail'] });
addCompile('daily bad extra', DEFS.starfall_daily, { extras: ['hack'] });
addCompile('abyss', DEFS.starfall_abyss, { floors: ['f9', 'f12'], target: 'three_star' });
addCompile('idle 90', DEFS.starfall_idle, { area: 'plain', minutes: 90 });
addCompile('idle 10', DEFS.starfall_idle, { area: 'plain', minutes: 10 });
addCompile('idle no area', DEFS.starfall_idle, { minutes: 60 });
addCompile('march', DEFS.ironcrest_march, { target: [{ item: 'lv15', count: 20 }], troop: 'archer' });
addCompile('march two', DEFS.ironcrest_march, { target: ['lv10', 'lv15'] });
addCompile('discount 90', M, { mode: 'sweep', stages: [{ item: '1-1', count: 10 }, { item: '2-4', count: 3 }] }, 90);
addCompile('no member discount', DEFS.starfall_idle, { area: 'plain', minutes: 60 }, 50);
addCompile('free', { code: 'free', name: '免费', fields: sw }, {});
{
  const big = clone(DEFS.starfall_idle);
  big.limits = { max_units: 100 };
  addCompile('max_units', big, { area: 'plain', minutes: 200 });
}
addCompile('uid ok', uid, { uid: '123456789' });
addCompile('uid bad', uid, { uid: '12ab' });
addCompile('uid missing', uid, {});
addCompile('params not object', M, 'oops');
addCompile('params null', M, null);

// ---------- 4. 编译：随机参数（合法值、边界值、垃圾值混合） ----------
function fieldValue(def, f) {
  const r = rand();
  if (r < 0.08) return pick(junk);
  switch (f.type) {
    case 'radio':
    case 'select':
      return chance(0.85) ? pick(f.options).value : pick(['nope', '', null, 1]);
    case 'checkbox': {
      const vals = f.options.map((o) => o.value);
      const out = vals.filter(() => chance(0.5));
      if (chance(0.2)) out.push(pick(vals));
      if (chance(0.1)) out.push('hack');
      return chance(0.1) ? pick(vals) : out;
    }
    case 'switch':
      return pick([true, false, 'true', 'false', 1, 0, '', undefined]);
    case 'number': {
      const span = f.max - f.min;
      return pick([f.min, f.max, f.min - 1, f.max + 1, f.min + Math.floor(rand() * (span + 1)), String(f.min + 1), '12.5', 'abc', ' 60 ', '0x10']);
    }
    case 'text':
      return pick(['', '  hello  ', '123456789', 'x'.repeat(150), 'a'.repeat(200 + 1), '备注内容', 42]);
    case 'stage_picker': {
      const cat = def.catalogs[f.catalog];
      const codes = cat.groups.flatMap((g) => g.items.map((it) => it.code));
      const n = Math.floor(rand() * 5);
      const out = [];
      for (let i = 0; i < n; i++) {
        const code = chance(0.9) ? pick(codes) : 'zz-9';
        if (chance(0.5)) out.push(code);
        else out.push({ item: code, count: pick([undefined, 1, 2, 5, 10, 0, 1000, '3', 2.5, 'x']) });
      }
      return chance(0.1) ? pick(codes) : out;
    }
  }
  return undefined;
}
for (const [k, d] of Object.entries(DEFS)) {
  for (let i = 0; i < 60; i++) {
    const params = {};
    for (const f of d.fields) {
      if (chance(0.15)) continue;
      const v = fieldValue(d, f);
      if (v !== undefined) params[f.key] = v;
    }
    addCompile(`random ${k} #${i}`, d, params, pick([undefined, 100, 95, 90, 85, 70, 50, 1]));
  }
}

// ---------- 计算期望结果 ----------
const crashes = {};
for (const c of validateCases) { try { S.validateDefinition(c.def); } catch (e) { const where = (e.stack.split("\n")[1] || "").trim(); (crashes[where] = crashes[where] || []).push(c.name + " :: " + e.message); } }
if (Object.keys(crashes).length) { for (const [w, l] of Object.entries(crashes)) console.log(l.length, w, "\n   e.g.", l[0]); process.exit(3); }
const expectedValidate = validateCases.map((c) => ({ ...c, want: S.validateDefinition(c.def) }));
const expectedCompile = [];
for (const c of compileCases) {
  if (!S.validateDefinition(c.def).ok) throw new Error('compile case uses invalid def: ' + c.name);
  const plan = S.compile(c.def, c.params, c.rate === undefined ? undefined : { discountRate: c.rate });
  const settle = [];
  for (let u = 0; u <= plan.units + 1; u++) settle.push(S.pointsForUnits(plan, u));
  const progress = [0, 1, Math.floor(plan.units / 2), plan.units].map((u) => ({ units: u, steps: S.stepProgress(plan, u).map((s) => s.done) }));
  expectedCompile.push({ ...c, want: { plan, settle, progress } });
}

const out = path.join(__dirname, '../../internal/modules/boost/domain/featuredef/testdata/golden.json');
fs.mkdirSync(path.dirname(out), { recursive: true });
fs.writeFileSync(out, JSON.stringify({ validate: expectedValidate, compile: expectedCompile }) + '\n');
console.log(`validate ${expectedValidate.length} cases (ok=${expectedValidate.filter((c) => c.want.ok).length}), compile ${expectedCompile.length} cases (ok=${expectedCompile.filter((c) => c.want.plan.ok).length}) -> ${path.relative(process.cwd(), out)}`);
