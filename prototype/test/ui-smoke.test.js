// 界面冒烟测试：在最小 DOM 替身里加载全部脚本，渲染每个页面、执行关键动作。
// 不是真实浏览器测试，只保证视图都能渲染、动作不抛未处理异常、不出现 undefined/NaN。
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const FILES = ['crypto.js', 'catalog.js', 'engine.js', 'boost.js', 'seed.js', 'ui-core.js', 'ui-form.js', 'ui-buyer.js', 'ui-admin-games.js', 'ui-ops.js'];

function boot() {
  let html = '';
  const appEl = {
    set innerHTML(v) {
      html = v;
    },
    get innerHTML() {
      return html;
    },
    querySelector: () => null,
  };
  const listeners = {};
  const document = {
    readyState: 'complete',
    activeElement: null,
    documentElement: { style: { setProperty() {} } },
    getElementById: (id) => (id === 'app' ? appEl : null),
    querySelector: () => null,
    createElement: () => ({ setAttribute() {}, remove() {}, click() {} }),
    body: { appendChild() {} },
    addEventListener: (t, fn) => (listeners[t] = (listeners[t] || []).concat(fn)),
  };
  const store = {};
  const ctx = {
    console,
    document,
    localStorage: { getItem: (k) => store[k] || null, setItem: (k, v) => (store[k] = v) },
    setTimeout: (fn) => 0,
    setInterval: () => 0,
    clearTimeout: () => {},
    confirm: () => true,
    alert: () => {},
    performance: { now: () => Date.now() },
    TextEncoder,
    TextDecoder,
    btoa: (s) => Buffer.from(s, 'binary').toString('base64'),
    atob: (s) => Buffer.from(s, 'base64').toString('binary'),
  };
  ctx.window = ctx;
  ctx.globalThis = ctx;
  vm.createContext(ctx);
  for (const f of FILES) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'src', f), 'utf8'), ctx, { filename: f });
  const App = ctx.App;
  const render = () => {
    App.render();
    return html;
  };
  const act = (name, ds) => {
    App.actions[name](ds || {}, { checked: true }, {});
    return render();
  };
  // 模拟表单控件的 change 事件
  const change = (dataset, props) => {
    const el = { dataset, ...props };
    for (const fn of listeners.change || []) fn({ target: el });
    return html;
  };
  return { App, render, act, change };
}

function clean(h, where) {
  const text = h.replace(/<[^>]+>/g, ' ');
  assert.ok(!/\bundefined\b/.test(text) && !/\bNaN\b/.test(text) && !/\[object Object\]/.test(text), `${where} 出现 undefined/NaN/[object Object]：` + (text.match(/.{0,40}(undefined|NaN|\[object Object\]).{0,40}/) || [])[0]);
  assert.ok(!h.includes('渲染出错'), `${where} 渲染出错：` + (h.match(/<pre>([\s\S]*?)<\/pre>/) || [])[1]);
}

test('每个页面都能渲染', () => {
  const { App, render, act } = boot();
  const tabs = { buyer: ['boost', 'tasks', 'accounts', 'points'], worker: ['x'], admin: ['games', 'tasks', 'points', 'workers', 'audit', 'checks'] };
  for (const [role, list] of Object.entries(tabs)) {
    App.ui.role = role;
    for (const tab of list) {
      if (role === 'buyer') App.ui.buyerTab = tab;
      if (role === 'admin') App.ui.adminTab = tab;
      for (const uid of [1, 2]) {
        App.ui.userId = uid;
        clean(render(), `${role}/${tab}/user${uid}`);
      }
    }
  }
  // 每个游戏、每个功能的表单都能渲染
  App.ui.role = 'buyer';
  App.ui.buyerTab = 'boost';
  for (const g of App.E.publicGames()) {
    act('flowGame', { id: g.id });
    for (const f of App.E.publicFeatures(g.id)) clean(act('flowFeature', { id: f.id }), `功能 ${f.code}`);
  }
  // 后台每个功能的编辑器 + 预览
  App.ui.role = 'admin';
  App.ui.adminTab = 'games';
  for (const g of App.E.s.games) {
    act('pickGame', { id: g.id });
    for (const f of App.E.s.features.filter((x) => x.gameId === g.id)) {
      const h = act('editFeature', { id: f.id });
      clean(h, `编辑器 ${f.code}`);
      assert.ok(h.includes('结构正确'), `${f.code} 的编辑器应显示校验通过`);
      assert.ok(h.includes('用户端实时预览'));
    }
  }
});

test('用户流程：绑定账号 → 选关卡扫荡 → 报价 → 下单 → 执行 → 结算', () => {
  const { App, render, act, change } = boot();
  const E = () => App.E;
  App.ui.role = 'buyer';
  App.ui.userId = 2;
  let h = render();
  assert.ok(h.includes('还没有可用的星落幻境账号'));

  act('openBind', { game: 1 });
  Object.assign(App.ui.bind, { login: '13800001234', password: 'DemoPass123', displayName: '大号' });
  act('createBinding');
  App.autoStep(); // 执行端要验证码
  assert.equal(E().s.bindings[0].status, 'WAIT_INPUT');
  clean(render(), '我的游戏账号-等待验证码');
  App.ui.inputs.b1 = '123456';
  act('submitInput', { target: 'binding', id: '1' });
  App.autoStep();
  assert.equal(E().s.bindings[0].status, 'SUCCESS');

  App.ui.buyerTab = 'boost';
  const fid = E().s.features.find((f) => f.code === 'main_stage').id;
  act('flowFeature', { id: fid });
  const form = 'user-' + fid;
  // 模式默认扫荡；选两个关卡，第二个调次数
  change({ form, key: 'stages', ftype: 'stage' }, { value: '1-1', checked: true });
  change({ form, key: 'stages', ftype: 'stage' }, { value: '2-4', checked: true });
  act('ffCount', { form, key: 'stages', item: '2-4', d: '-1' }); // 10 → 9
  h = render();
  clean(h, '下单页-已选关卡');
  const q = E().taskQuote(2, { featureId: fid, params: App.formState(form).values });
  assert.ok(q.plan.ok, q.plan.errors.join());
  assert.equal(q.plan.rawTotal, 10 * 2 + 9 * 5);
  assert.ok(h.includes(`冻结 ${q.points} 积分并开始`));

  // 切到通关：次数输入消失；3-3 剧情关可选
  change({ form, key: 'mode', ftype: 'radio' }, { value: 'clear' });
  change({ form, key: 'stages', ftype: 'stage' }, { value: '3-3', checked: true });
  h = render();
  assert.ok(!h.includes('扫荡次数'));
  // 切回扫荡：3-3 不支持扫荡，报错且不能提交
  change({ form, key: 'mode', ftype: 'radio' }, { value: 'sweep' });
  h = render();
  assert.ok(h.includes('不支持扫荡'));
  assert.ok(h.includes('>开始代练</button>'), '有错误时按钮显示"开始代练"，点了才提示错误');
  const before = E().s.tasks.length;
  act('submitTask', { feature: fid });
  assert.equal(E().s.tasks.length, before, '有错误时不能创建任务');
  assert.ok(App.formState(form).submitted);
  change({ form, key: 'stages', ftype: 'stage' }, { value: '3-3', checked: false });

  act('submitTask', { feature: fid });
  assert.equal(App.ui.buyerTab, 'tasks');
  const t = E().s.tasks[0];
  assert.equal(t.units, 19);
  clean(render(), '我的任务-刚创建');
  for (let i = 0; i < 25; i++) App.autoStep();
  assert.equal(E().s.tasks[0].status, 'SUCCESS');
  assert.equal(E().s.tasks[0].pointsConsumed, q.points);
  clean(render(), '我的任务-完成，展开步骤');

  // 积分包
  App.ui.buyerTab = 'points';
  App.ui.userId = 1;
  act('buyPoints', { id: 2 });
  clean(render(), '积分页');
  assert.equal(E().pointAccount(1).available, 1200);
});

test('后台流程：新建游戏 → 区服 → 用模板新建功能 → 改 JSON → 预览 → 上架 → 上线 → 用户可下单', () => {
  const { App, render, act, change } = boot();
  const E = () => App.E;
  App.ui.role = 'admin';
  App.ui.adminTab = 'games';
  act('newGame');
  Object.assign(App.ui.admin.newGame, { code: 'moonlit', name: '月下城', short: '月下' });
  act('saveGame');
  const g = E().s.games.find((x) => x.code === 'moonlit');
  assert.equal(App.ui.admin.gameId, g.id);
  let h = render();
  assert.ok(h.includes('上线前需要'));

  Object.assign(App.ui.admin.newServer, { code: 's1', name: '一区' });
  act('addServer', { id: g.id });

  act('newFeature', { tpl: 'stages' });
  h = render();
  assert.ok(h.includes('结构正确'));
  // 改 JSON：改 code / 名称，加一个第三章关卡
  const d = JSON.parse(App.ui.admin.draft);
  d.code = 'tower';
  d.name = '爬塔';
  d.catalogs.stages.groups.push({ code: 'ch3', name: '第三章', items: [{ code: '3-1', name: '3-1 塔顶', cost: { clear: 20, sweep: 8 } }] });
  App.ui.admin.draft = JSON.stringify(d, null, 2);
  h = render();
  assert.ok(h.includes('第三章'), '预览应出现新加的章节');
  // 写错一处：预览应提示错误，保存按钮禁用
  const broken = JSON.parse(App.ui.admin.draft);
  broken.fields[1].cost_by = 'nope';
  App.ui.admin.draft = JSON.stringify(broken);
  h = render();
  assert.ok(h.includes('处错误'));
  assert.ok(h.includes('修正左侧错误后显示预览'));
  App.ui.admin.draft = '{ 坏的 json';
  clean(render(), '编辑器-非法 JSON');
  App.ui.admin.draft = JSON.stringify(d, null, 2);
  // 在预览里操作一下
  change({ form: 'preview', key: 'stages', ftype: 'stage' }, { value: '3-1', checked: true });
  clean(render(), '预览-选关卡');
  act('saveFeature', { game: g.id });
  const f = E().s.features.find((x) => x.code === 'tower');
  assert.equal(f.status, 'draft');
  act('featureStatus', { id: f.id, v: 'active' });
  act('gameStatus', { id: g.id, v: 'active' });
  assert.ok(E().publicGames().some((x) => x.id === g.id));

  // 再改一次价格 → v2
  act('editFeature', { id: f.id });
  const d2 = JSON.parse(App.ui.admin.draft);
  d2.catalogs.stages.groups[0].items[0].cost.sweep = 3;
  App.ui.admin.draft = JSON.stringify(d2);
  act('saveFeature', { game: g.id });
  assert.equal(E().feature(f.id).version, 2);
  clean(render(), '编辑器-v2 版本历史');

  // 用户端能看到并下单
  App.ui.role = 'buyer';
  App.ui.userId = 2;
  App.ui.buyerTab = 'boost';
  act('flowGame', { id: g.id });
  h = render();
  assert.ok(h.includes('爬塔'));
  clean(h, '用户端-新游戏');
});

test('执行端模拟和后台其它页面的动作', () => {
  const { App, render, act } = boot();
  const E = () => App.E;
  const b = E().createBinding(2, { gameId: 1, serverId: 1, login: 'p@x.test', password: 'SecretPW1' });
  App.ui.role = 'worker';
  act('wPull', { id: 1 });
  const lb = E().s.leases.find((l) => l.active);
  act('wFinish', { t: lb.token });
  assert.equal(E().binding(b.id).status, 'SUCCESS');
  E().createTask(2, { bindingId: b.id, featureId: E().s.features.find((f) => f.code === 'main_stage').id, params: { mode: 'sweep', stages: [{ item: '1-1', count: 5 }] } });
  act('wPull', { id: 1 });
  clean(render(), '执行端-持有租约');
  const lt = E().s.leases.find((l) => l.active);
  act('wHeartbeat', { t: lt.token });
  act('wEvent', { t: lt.token });
  act('wBadSign', { id: 1 });
  act('wReplay', { id: 1 });
  App.ui.lease[lt.token].units = 2;
  act('wFail', { t: lt.token, c: 'STAGE_LOCKED' });
  clean(render(), '执行端-失败后');
  assert.ok(!JSON.stringify(E().s.taskEvents).includes('hunter2'));
  // 关卡未解锁属于用户原因，按完成量结算：2 次 × 2 点 = 4，黄金会员 9 折向下取整 = 3
  assert.equal(E().s.tasks[0].status, 'FAIL');
  assert.equal(E().s.tasks[0].pointsConsumed, 3);

  App.ui.role = 'admin';
  App.ui.adminTab = 'tasks';
  clean(render(), '后台任务');
  act('adminRetry', { no: E().s.tasks[0].taskNo });
  App.ui.adminTab = 'points';
  Object.assign(App.ui.admin.pack, { name: '测试包', price: 9.9, points: 100, bonus: 0, validDays: 7 });
  act('addPack');
  Object.assign(App.ui.admin.grant, { userId: 1, amount: 50, validDays: 7, reason: '测试' });
  act('grantPts');
  clean(render(), '后台积分');
  App.ui.adminTab = 'workers';
  act('rotateWorker', { id: 2 });
  clean(render(), '后台执行端');
  App.ui.adminTab = 'checks';
  act('runDrill');
  const h = act('runChecks');
  clean(h, '一致性校验');
  assert.ok(h.includes('全部通过'));
  assert.ok(App.ui.drill.includes('全部通过'), App.ui.drill);
  App.actions.reset();
  assert.equal(App.E.s.tasks.length, 0);
});
