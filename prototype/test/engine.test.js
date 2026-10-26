// 引擎：后台配置、积分包、积分账本、绑定、任务、Worker 协议
// 运行：node --test prototype/test/catalog.test.js prototype/test/engine.test.js prototype/test/ui-smoke.test.js
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { seed, DEFS } = require('../src/seed.js');
const { MIN, HOUR, DAY, Engine } = require('../src/engine.js');

const clone = (v) => JSON.parse(JSON.stringify(v));
function invariantsOk(e) {
  const bad = e.checkInvariants().filter((x) => !x.ok);
  assert.deepEqual(bad, [], '不变量被破坏：' + JSON.stringify(bad));
}
function expectCode(fn, code) {
  assert.throws(fn, (err) => {
    assert.equal(err.code, code, `期望 ${code}，实际 ${err.code}: ${err.message}`);
    return true;
  });
}
const F = (e, code) => e.s.features.find((f) => f.code === code);
// 给用户一个验证成功的绑定
function verifiedBinding(e, userId, gameId) {
  const b = e.createBinding(userId || 2, { gameId: gameId || 1, serverId: gameId === 2 ? 4 : 1, login: 'player' + e.s.bindings.length + '@example.test', password: 'Secret-Pass-001', displayName: '主号' });
  const w = gameId === 2 ? 3 : 1;
  // 队列里可能还有别的待验证绑定（先进先出），一直领取到自己的那个为止；别人的放回去不处理
  for (let i = 0; i < 10 && e.binding(b.id).status !== 'SUCCESS'; i++) {
    const items = e.workerCall(w, 'POST', '/api/v1/worker/pull', { kinds: ['bind'], max: 3 }).response.body.items;
    for (const item of items) {
      if (item.id === b.id) e.workerCall(w, 'POST', `/api/v1/worker/leases/${item.lease_token}/finish`, { request_id: 'bind-ok-' + b.id, session_ref: 'sess' });
      else e.workerCall(w, 'POST', `/api/v1/worker/leases/${item.lease_token}/fail`, { request_id: 'skip-' + item.id + '-' + i, error_code: 'NETWORK' });
    }
  }
  return e.binding(b.id);
}
const pull = (e, w, kinds) => e.workerCall(w || 1, 'POST', '/api/v1/worker/pull', { kinds: kinds || ['task'] }).response.body.items || [];
const L = (item, action) => `/api/v1/worker/leases/${item.lease_token}/${action}`;
const SWEEP = (stages) => ({ mode: 'sweep', stages });

// ---------------- 后台：游戏与功能 ----------------
test('后台新建游戏：草稿 → 缺区服/功能不能上线 → 补齐后上线；前台只显示上线的', () => {
  const e = seed();
  const g = e.adminSaveGame(1, { code: 'moonlit', name: '月下城' });
  assert.equal(g.status, 'draft');
  assert.ok(!e.publicGames().some((x) => x.id === g.id));
  expectCode(() => e.adminSetGameStatus(1, g.id, 'active'), 'INVALID_TRANSITION');
  e.adminSaveServer(1, { gameId: g.id, code: 's1', name: '一区' });
  expectCode(() => e.adminSetGameStatus(1, g.id, 'active'), 'INVALID_TRANSITION');
  const def = clone(DEFS.starfall_daily);
  const f = e.adminSaveFeature(1, { gameId: g.id, definition: def });
  expectCode(() => e.adminSetGameStatus(1, g.id, 'active'), 'INVALID_TRANSITION'); // 功能还是草稿
  e.adminSetFeatureStatus(1, f.id, 'active');
  e.adminSetGameStatus(1, g.id, 'active');
  assert.ok(e.publicGames().some((x) => x.id === g.id));
  assert.equal(e.publicFeatures(g.id).length, 1);
  invariantsOk(e);
});

test('后台：游戏 code 唯一且不可修改；权限不足被拒绝', () => {
  const e = seed();
  expectCode(() => e.adminSaveGame(1, { code: 'starfall', name: '重复' }), 'DUPLICATE');
  expectCode(() => e.adminSaveGame(1, { id: 1, code: 'other', name: '改 code' }), 'PARAMS_INVALID');
  expectCode(() => e.adminSaveGame(2, { code: 'xx', name: '客服' }), 'FORBIDDEN');
  expectCode(() => e.adminSaveFeature(2, { gameId: 1, definition: DEFS.starfall_daily }), 'FORBIDDEN');
  expectCode(() => e.adminSaveServer(1, { gameId: 1, code: 'cn-sky', name: '重复区服' }), 'DUPLICATE');
});

test('后台：非法 JSON 和不合法定义不能保存，且不留下半成品', () => {
  const e = seed();
  const n = e.s.features.length;
  expectCode(() => e.adminSaveFeature(1, { gameId: 1, definition: '{ bad json' }), 'DEFINITION_INVALID');
  const bad = clone(DEFS.starfall_main);
  bad.fields[1].catalog = 'nope';
  expectCode(() => e.adminSaveFeature(1, { gameId: 1, definition: bad }), 'DEFINITION_INVALID');
  expectCode(() => e.adminSaveFeature(1, { gameId: 1, definition: DEFS.starfall_daily }), 'DUPLICATE');
  assert.equal(e.s.features.length, n);
});

test('后台：修改功能生成新版本；已创建任务按旧快照结算；无变化不升版本', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const f = F(e, 'main_stage');
  const t1 = e.createTask(2, { bindingId: b.id, featureId: f.id, params: SWEEP([{ item: '1-1', count: 10 }]) });
  const def = clone(f.definition);
  def.catalogs.main.groups[0].items[0].cost.sweep = 20; // 涨价
  e.adminSaveFeature(1, { id: f.id, gameId: 1, definition: def, version: f.version });
  assert.equal(F(e, 'main_stage').version, 2);
  e.adminSaveFeature(1, { id: f.id, gameId: 1, definition: def });
  assert.equal(F(e, 'main_stage').version, 2);
  expectCode(() => e.adminSaveFeature(1, { id: f.id, gameId: 1, definition: def, version: 1 }), 'VERSION_CONFLICT');
  const task = e.taskByNo(t1.taskNo);
  assert.equal(task.snapshot.version, 1);
  const item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'f', units_done: 10 });
  assert.equal(e.taskByNo(t1.taskNo).pointsConsumed, t1.points); // 旧价
  const q2 = e.taskQuote(2, { featureId: f.id, params: SWEEP([{ item: '1-1', count: 10 }]) });
  assert.ok(q2.points > t1.points); // 新任务用新价
  invariantsOk(e);
});

test('后台：下架功能后用户不能再下单', () => {
  const e = seed();
  const b = verifiedBinding(e);
  e.adminSetFeatureStatus(1, F(e, 'daily').id, 'disabled');
  expectCode(() => e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} }), 'NOT_FOUND');
  assert.ok(!e.publicFeatures(1).some((f) => f.code === 'daily'));
});

// ---------------- 报价与创建任务 ----------------
test('报价：服务端按 JSON 计算；前端塞进来的价格字段无效；会员 9 折', () => {
  const e = seed();
  const f = F(e, 'main_stage');
  const q = e.taskQuote(2, { featureId: f.id, params: { ...SWEEP([{ item: '1-1', count: 10 }, { item: '2-4', count: 3 }]), points: 1 }, points: 1 });
  assert.equal(q.plan.rawTotal, 35);
  assert.equal(q.points, 31);
  assert.equal(e.taskQuote(1, { featureId: f.id, params: SWEEP([{ item: '1-1', count: 10 }, { item: '2-4', count: 3 }]) }).points, 35);
});

test('创建任务：冻结积分、记录执行计划和摘要；参数错误不冻结', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const before = e.pointAccount(2).available;
  expectCode(() => e.createTask(2, { bindingId: b.id, featureId: F(e, 'main_stage').id, params: SWEEP(['3-3']) }), 'PARAMS_INVALID');
  assert.equal(e.pointAccount(2).available, before);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'main_stage').id, params: SWEEP([{ item: '1-1', count: 5 }, { item: '1-2', count: 5 }]) });
  const t = e.taskByNo(r.taskNo);
  assert.equal(t.units, 10);
  assert.equal(t.plan.steps.length, 2);
  assert.match(t.summary, /扫荡.*1-1 迷雾入口 ×5/);
  assert.equal(e.pointAccount(2).frozen, r.points);
  invariantsOk(e);
});

test('创建任务：游戏不匹配、绑定未验证、并发上限、积分不足', () => {
  const e = seed();
  const b = verifiedBinding(e);
  expectCode(() => e.createTask(2, { bindingId: b.id, featureId: F(e, 'farm').id, params: {} }), 'PARAMS_INVALID');
  const pending = e.createBinding(2, { gameId: 1, serverId: 1, login: 'new@x.test', password: 'pwpwpwpw' });
  expectCode(() => e.createTask(2, { bindingId: pending.id, featureId: F(e, 'daily').id, params: {} }), 'INVALID_TRANSITION');
  e.createTask(2, { bindingId: b.id, featureId: F(e, 'abyss').id, params: { floors: ['f5'] } });
  expectCode(() => e.createTask(2, { bindingId: b.id, featureId: F(e, 'abyss').id, params: { floors: ['f6'] } }), 'TASK_LIMIT_REACHED');
  expectCode(() => e.createTask(1, { bindingId: verifiedBinding(e, 1).id, featureId: F(e, 'daily').id, params: {} }), 'POINTS_INSUFFICIENT');
  invariantsOk(e);
});

test('创建任务幂等：同一个幂等键只冻结一次', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const a = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {}, idempotencyKey: 'k' });
  const c = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {}, idempotencyKey: 'k' });
  assert.equal(a.taskNo, c.taskNo);
  assert.equal(e.s.tasks.length, 1);
});

// ---------------- 执行与结算 ----------------
test('扫荡完成：Worker 收到步骤列表，完成后全额扣除', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'main_stage').id, params: SWEEP([{ item: '1-1', count: 3 }, { item: '1-4', count: 2 }]) });
  const item = pull(e)[0];
  assert.equal(item.feature, 'main_stage');
  assert.deepEqual(item.steps.map((s) => [s.label, s.count, s.ref.item, s.ref.mode]), [['1-1 迷雾入口', 3, '1-1', 'sweep'], ['1-4 守望者（BOSS）', 2, '1-4', 'sweep']]);
  assert.equal(item.units, 5);
  e.workerCall(1, 'POST', L(item, 'heartbeat'), { units_done: 3 });
  e.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'x', units_done: 5, result: { drops: ['经验书 ×12'] } });
  const t = e.taskByNo(r.taskNo);
  assert.equal(t.status, 'SUCCESS');
  assert.equal(t.pointsConsumed, r.points);
  invariantsOk(e);
});

test('部分完成：体力不足失败，按已完成关卡的价格结算（BOSS 关更贵）', () => {
  const e = seed();
  const b = verifiedBinding(e, 1);
  e.grantPoints(1, 500, 'grant', 't', null, '');
  const r = e.createTask(1, { bindingId: b.id, featureId: F(e, 'main_stage').id, params: SWEEP([{ item: '1-1', count: 3 }, { item: '1-4', count: 2 }]) });
  assert.equal(r.points, 3 * 2 + 2 * 4);
  const item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'fail'), { request_id: 'y', error_code: 'INSUFFICIENT_STAMINA', units_done: 4, retryable: true });
  const t = e.taskByNo(r.taskNo);
  assert.equal(t.status, 'FAIL'); // 服务端错误码表优先
  assert.equal(t.pointsConsumed, 3 * 2 + 1 * 4);
  assert.equal(t.pointsReleased, 4);
  invariantsOk(e);
});

test('系统原因失败全额退（full_on_system_fail）；用户原因按完成量', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const before = e.pointAccount(2).available;
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: { extras: ['dispatch'] } });
  const item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'fail'), { request_id: 'z', error_code: 'PASSWORD_CHANGED' });
  assert.equal(e.pointAccount(2).available, before);
  assert.equal(e.binding(b.id).status, 'FAIL');
  assert.equal(e.taskByNo(r.taskNo).pointsReleased, r.points);
  invariantsOk(e);
});

test('取消：排队中全额退；执行中等待 Worker 确认后按完成量结算', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const before = e.pointAccount(2).available;
  const r1 = e.createTask(2, { bindingId: b.id, featureId: F(e, 'idle_farm').id, params: { area: 'plain', minutes: 60 } });
  e.userCancelTask(2, r1.taskNo);
  assert.equal(e.pointAccount(2).available, before);
  const r2 = e.createTask(2, { bindingId: b.id, featureId: F(e, 'idle_farm').id, params: { area: 'plain', minutes: 60 } });
  const item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'heartbeat'), { units_done: 20 });
  e.userCancelTask(2, r2.taskNo);
  assert.equal(e.workerCall(1, 'POST', L(item, 'heartbeat'), { units_done: 25 }).response.body.cancel_requested, true);
  e.workerCall(1, 'POST', L(item, 'cancel-ack'), { request_id: 'c', units_done: 25 });
  const t = e.taskByNo(r2.taskNo);
  assert.equal(t.status, 'CANCELLED');
  assert.equal(t.pointsConsumed, 25);
  assert.equal(t.pointsReleased, 35);
  invariantsOk(e);
});

test('Worker 上报超过计划的完成量被截断', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'main_stage').id, params: SWEEP([{ item: '1-1', count: 3 }]) });
  const item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'f', units_done: 999 });
  assert.equal(e.taskByNo(r.taskNo).unitsDone, 3);
  assert.equal(e.taskByNo(r.taskNo).pointsConsumed, r.points);
});

test('有基础价的功能：开始执行后才收基础价，未执行取消全退', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  const item = pull(e)[0];
  e.userCancelTask(2, r.taskNo);
  e.workerCall(1, 'POST', L(item, 'cancel-ack'), { request_id: 'c', units_done: 0 });
  assert.equal(e.taskByNo(r.taskNo).pointsConsumed, 0);
  invariantsOk(e);
});

// ---------------- Worker 协议 ----------------
test('两个 Worker 同时领取同一任务只有一个拿到', () => {
  const e = seed();
  const b = verifiedBinding(e);
  e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  assert.equal(pull(e, 1).length + pull(e, 2).length, 1);
});

test('租约过期后旧 Token 写入返回 LEASE_LOST；多次失联后失败并按系统原因处理', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const before = e.pointAccount(2).available;
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  const old = pull(e)[0];
  e.advance(2 * MIN);
  assert.equal(e.taskByNo(r.taskNo).status, 'PENDING');
  assert.ok(pull(e, 2)[0]);
  const late = e.workerCall(1, 'POST', L(old, 'finish'), { request_id: 'late', units_done: 1 });
  assert.equal(late.response.body.error, 'LEASE_LOST');
  e.advance(2 * MIN);
  assert.equal(e.taskByNo(r.taskNo).status, 'FAIL');
  assert.equal(e.pointAccount(2).available, before);
  invariantsOk(e);
});

test('完成请求重复提交结果一致，只结算一次', () => {
  const e = seed();
  const b = verifiedBinding(e);
  e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  const item = pull(e)[0];
  const r1 = e.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'same' }).response;
  const r2 = e.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'same' }).response;
  assert.deepEqual(r1, r2);
  assert.equal(e.s.pointLedger.filter((x) => x.type === 'consume').length, 1);
});

test('同一游戏账号同时只跑一个任务；能力不匹配拉不到', () => {
  const e = seed();
  const b = verifiedBinding(e);
  e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  e.createTask(2, { bindingId: b.id, featureId: F(e, 'idle_farm').id, params: { area: 'coast', minutes: 30 } });
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', { kinds: ['task'], max: 3 }).response.body.items.length, 1);
  assert.equal(pull(e, 2).length, 0);
  assert.equal(pull(e, 3).length, 0);
});

test('签名错误、时间戳偏差、重放、篡改请求体、停用节点、轮换旧密钥都返回 401', () => {
  const e = seed();
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', {}, { secret: 'wrong' }).response.status, 401);
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', {}, { timestamp: Math.floor(e.now / 1000) - 61 }).response.status, 401);
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', {}, { nonce: 'n1' }).response.status, 200);
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', {}, { nonce: 'n1' }).response.status, 401);
  const w = e.worker(1);
  const signed = e.signRequest(w.apiKey, e.workerSecretForSimulator(1), 'POST', '/api/v1/worker/pull', { max: 1 });
  assert.equal(e.workerHttp('POST', '/api/v1/worker/pull', signed.headers, JSON.stringify({ max: 5 })).status, 401);
  const old = e.workerSecretForSimulator(1);
  e.rotateWorkerSecret(1, 1);
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', {}, { secret: old }).response.status, 401);
  e.worker(1).status = 'disabled';
  assert.equal(e.workerCall(1, 'POST', '/api/v1/worker/pull', {}).response.status, 401);
});

test('任务 WAIT_INPUT：提交后恢复；超时失败全额退', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  let item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'need-input'), { kind: 'sms_code', prompt: '请输入验证码' });
  e.submitInput(2, 'task', e.taskByNo(r.taskNo).id, '482913');
  item = pull(e)[0];
  assert.equal(item.input.value, '482913');
  e.workerCall(1, 'POST', L(item, 'need-input'), { kind: 'sms_code' });
  const before = e.pointAccount(2).available;
  e.advance(6 * MIN);
  assert.equal(e.taskByNo(r.taskNo).errorCode, 'INPUT_TIMEOUT');
  assert.equal(e.pointAccount(2).available, before + r.points);
  invariantsOk(e);
});

test('绑定：验证码、游戏内确认、超时；解绑销毁凭据', () => {
  const e = seed();
  const b = e.createBinding(1, { gameId: 1, serverId: 1, login: '13800000000', password: 'MyGamePwd!', displayName: '大号' });
  let item = pull(e, 1, ['bind'])[0];
  e.workerCall(1, 'POST', L(item, 'need-input'), { kind: 'sms_code' });
  e.submitInput(1, 'binding', b.id, '112233');
  item = pull(e, 1, ['bind'])[0];
  e.workerCall(1, 'POST', L(item, 'need-input'), { kind: 'confirm' });
  assert.equal(e.binding(b.id).status, 'WAIT_USER');
  e.confirmInGame(1, b.id);
  item = pull(e, 1, ['bind'])[0];
  e.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'bf' });
  assert.equal(e.binding(b.id).status, 'SUCCESS');
  assert.equal(e.binding(b.id).loginMasked, '138****00');
  e.deleteBinding(1, b.id);
  assert.equal(e.binding(b.id).credentialCipher, null);
  const b2 = e.createBinding(1, { gameId: 1, serverId: 2, login: 'second@x.test', password: 'pw-second' });
  item = pull(e, 1, ['bind'])[0];
  e.workerCall(1, 'POST', L(item, 'need-input'), { kind: 'sms_code' });
  e.advance(6 * MIN);
  assert.equal(e.binding(b2.id).lastError, 'INPUT_TIMEOUT');
  invariantsOk(e);
});

test('日志脱敏：任务事件里的密码、验证码被遮盖', () => {
  const e = seed();
  const b = verifiedBinding(e);
  e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  const item = pull(e)[0];
  e.workerCall(1, 'POST', L(item, 'events'), { seq: 5, message: '登录 Secret-Pass-001 成功，验证码: 556677' });
  const msg = e.s.taskEvents.find((x) => x.seq === 5).message;
  assert.ok(!msg.includes('Secret-Pass-001') && !msg.includes('556677'));
});

// ---------------- 积分包与积分账本 ----------------
test('积分包：余额支付直接到账；在线支付重复回调只到账一次；超时取消退余额', () => {
  const e = seed();
  const o = e.createOrder(2, { packId: 1, useBalance: true });
  e.runDueJobs();
  assert.equal(e.orderByNo(o.orderNo).status, 'completed');
  assert.equal(e.pointAccount(2).available, 300 + 500);
  const o2 = e.createOrder(1, { packId: 3, useBalance: true }); // 260 元，余额 300 足够
  e.runDueJobs();
  assert.equal(e.pointAccount(1).available, 3000);
  e.s.wallets[1] = 0;
  e.s.walletTxns.push({ id: 999, userId: 1, type: 'adjust', direction: 'out', amount: 4000, reference: 'test:zero', at: e.now });
  const o3 = e.createOrder(1, { packId: 2 });
  const p = e.createPayment(o3.orderNo);
  for (let i = 0; i < 3; i++) e.paymentCallback(p.paymentNo, p.amount);
  e.runDueJobs();
  assert.equal(e.pointAccount(1).available, 3000 + 1200);
  const o4 = e.createOrder(1, { packId: 1 });
  e.advance(16 * MIN);
  assert.equal(e.orderByNo(o4.orderNo).status, 'canceled');
  assert.ok(o2);
  invariantsOk(e);
});

test('积分包退款：先撤回剩余积分（只撤剩余），按比例建议退款，退款幂等', () => {
  const e = seed();
  const o = e.createOrder(2, { packId: 1, useBalance: true });
  e.runDueJobs();
  expectCode(() => e.adminRefund(1, o.orderNo, 100, 'r', '申请'), 'REFUND_BLOCKED');
  const r = e.revokePointsForOrder(1, o.orderNo);
  assert.equal(r.revoked, 500); // 体验积分先过期先用，没动到这个批次
  const paid = e.orderByNo(o.orderNo).total;
  assert.equal(r.suggestRefund, paid);
  e.adminRefund(1, o.orderNo, r.suggestRefund, 'r', '申请');
  e.adminRefund(1, o.orderNo, r.suggestRefund, 'r', '申请');
  assert.equal(e.orderByNo(o.orderNo).refunded, paid);
  invariantsOk(e);
});

test('积分 FIFO：先用快到期的；到期定时失效；发放幂等', () => {
  const e = seed();
  e.adminGrantPoints(1, 2, 100, 10, '补偿', 'req');
  e.adminGrantPoints(1, 2, 100, 10, '补偿', 'req');
  assert.equal(e.pointAccount(2).available, 400);
  const b = verifiedBinding(e);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  const hold = e.ledgerByKey('hold:task:' + e.taskByNo(r.taskNo).id);
  assert.equal(hold.lotAllocations[0].amount, r.points); // 10 天批次先用
  e.userCancelTask(2, r.taskNo);
  e.advance(31 * DAY);
  assert.equal(e.pointAccount(2).available, 0);
  invariantsOk(e);
});

test('序列化恢复后继续执行（模拟服务重启）', () => {
  const e = seed();
  const b = verifiedBinding(e);
  const r = e.createTask(2, { bindingId: b.id, featureId: F(e, 'daily').id, params: {} });
  const item = pull(e)[0];
  const e2 = Engine.restore(e.serialize());
  e2.workerCall(1, 'POST', L(item, 'finish'), { request_id: 'after' });
  assert.equal(e2.taskByNo(r.taskNo).status, 'SUCCESS');
  invariantsOk(e2);
});

test('随机压力：600 步随机操作后所有不变量仍成立', () => {
  const e = seed();
  let x = 11;
  const rnd = () => ((x = (x * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  const pick = (a) => a[Math.floor(rnd() * a.length)];
  const b = verifiedBinding(e);
  const b2 = verifiedBinding(e);
  e.grantPoints(2, 20000, 'grant', 'stress', null, '');
  e.s.features.forEach((f) => (f.definition.max_active_per_user = 20));
  const stages = ['1-1', '1-2', '1-3', '1-4', '2-1', '2-4', '3-1', '3-3'];
  for (let i = 0; i < 600; i++) {
    try {
      const op = Math.floor(rnd() * 8);
      if (op === 0 || op === 1) {
        const mode = rnd() < 0.5 ? 'sweep' : 'clear';
        const list = [];
        for (let k = 0; k < 1 + Math.floor(rnd() * 3); k++) list.push({ item: pick(stages), count: 1 + Math.floor(rnd() * 8) });
        e.createTask(2, { bindingId: pick([b.id, b2.id]), featureId: F(e, 'main_stage').id, params: { mode, stages: list } });
      } else if (op === 2) e.createTask(2, { bindingId: pick([b.id, b2.id]), featureId: F(e, 'idle_farm').id, params: { area: 'plain', minutes: 30 + Math.floor(rnd() * 60) } });
      else if (op === 3) {
        for (const it of pull(e, pick([1, 2]))) {
          const k = rnd();
          const u = Math.floor(rnd() * (it.units + 3));
          if (k < 0.45) e.workerCall(it.lease_token ? e.s.leases.find((l) => l.token === it.lease_token).workerId : 1, 'POST', L(it, 'finish'), { request_id: 'f' + i, units_done: u });
          else if (k < 0.7) e.workerCall(e.s.leases.find((l) => l.token === it.lease_token).workerId, 'POST', L(it, 'fail'), { request_id: 'e' + i, error_code: pick(['NETWORK', 'INSUFFICIENT_STAMINA', 'STAGE_LOCKED', 'LOGIN_FAILED']), units_done: u });
          else if (k < 0.85) e.workerCall(e.s.leases.find((l) => l.token === it.lease_token).workerId, 'POST', L(it, 'heartbeat'), { units_done: u });
        }
      } else if (op === 4) {
        const live = e.s.tasks.filter((t) => !['SUCCESS', 'FAIL', 'CANCELLED'].includes(t.status));
        if (live.length) e.userCancelTask(2, pick(live).taskNo);
      } else if (op === 5) e.advance(Math.floor(rnd() * 5) * MIN);
      else if (op === 6) {
        for (const bb of [b, b2]) if (e.binding(bb.id).status === 'FAIL') e.binding(bb.id).status = 'SUCCESS';
      } else {
        const f = F(e, 'main_stage');
        const d = clone(f.definition);
        d.catalogs.main.groups[0].items[0].cost.sweep = 1 + Math.floor(rnd() * 5);
        e.adminSaveFeature(1, { id: f.id, gameId: 1, definition: d });
      }
    } catch (err) {
      if (!err.code) throw err;
    }
  }
  e.advance(HOUR * 10);
  invariantsOk(e);
});
