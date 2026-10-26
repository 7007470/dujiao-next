/* 积分账本（§5）、绑定（§6.2）、代练任务（§6.3–6.5）、Worker 协议（§7）。
 * 以 mixin 方式挂到 Engine.prototype，和 engine.js 共用状态与事务。 */
(function (root) {
  'use strict';

  const isNode = typeof module !== 'undefined' && module.exports;
  const G = isNode ? require('./engine.js') : root.GameEngine;
  const C = isNode ? require('./crypto.js') : root.GameCrypto;
  const S = isNode ? require('./catalog.js') : root.FeatureSchema;
  const { Engine, GameError, fail, yuan, clone, MIN, HOUR, DAY, TZ_OFFSET, TERMINAL } = G;

  // §7.5 错误码：服务端权威表，Worker 上报的 retryable 只对未知错误码生效
  const ERROR_CODES = {
    NETWORK: { retryable: true, category: 'system', label: '网络异常' },
    GAME_MAINTENANCE: { retryable: true, category: 'system', label: '游戏维护' },
    WORKER_CRASH: { retryable: true, category: 'system', label: '执行端崩溃' },
    LEASE_EXPIRED: { retryable: true, category: 'system', label: '执行端失联' },
    LOGIN_FAILED: { retryable: false, category: 'system', label: '登录失败', bindFail: true },
    ACCOUNT_BANNED: { retryable: false, category: 'system', label: '账号被封禁', bindFail: true },
    PASSWORD_CHANGED: { retryable: false, category: 'system', label: '密码已修改', bindFail: true },
    INPUT_TIMEOUT: { retryable: false, category: 'system', label: '验证码超时' },
    STAGE_LOCKED: { retryable: false, category: 'user', label: '关卡未解锁' },
    INSUFFICIENT_STAMINA: { retryable: false, category: 'user', label: '体力不足' },
    NOT_UNLOCKED: { retryable: false, category: 'user', label: '功能未解锁' },
    USER_KICKED: { retryable: false, category: 'user', label: '用户上线顶号' },
    TIMEOUT: { retryable: false, category: 'user', label: '超过任务时限' },
  };

  const M = {};

  // ===================== §5 积分账本 =====================
  M.pointAccount = function (userId) {
    if (!this.s.pointAccounts[userId]) this.s.pointAccounts[userId] = { userId, available: 0, frozen: 0, version: 0 };
    return this.s.pointAccounts[userId];
  };
  M.ledger = function (userId, type, dA, dF, bizKey, extra) {
    const acc = this.pointAccount(userId);
    acc.available += dA;
    acc.frozen += dF;
    acc.version += 1;
    if (acc.available < 0 || acc.frozen < 0) fail('POINTS_INSUFFICIENT');
    const row = {
      id: this.nextId('ledger'),
      userId,
      type,
      dAvailable: dA,
      dFrozen: dF,
      availableAfter: acc.available,
      frozenAfter: acc.frozen,
      bizKey,
      at: this.now,
      ...(extra || {}),
    };
    this.s.pointLedger.push(row);
    return row;
  };
  M.ledgerByKey = function (bizKey) {
    return this.s.pointLedger.find((r) => r.bizKey === bizKey) || null;
  };
  M.grantPoints = function (userId, amount, sourceType, sourceRef, validDays, remark, adminId) {
    const bizKey = `${sourceType}:${sourceRef}`;
    const existing = this.ledgerByKey(bizKey);
    if (existing) return existing; // 幂等
    if (!Number.isInteger(amount) || amount <= 0) fail('PARAMS_INVALID', '积分必须是正整数');
    const lot = {
      id: this.nextId('lot'),
      userId,
      sourceType,
      sourceRef,
      amount,
      remaining: amount,
      expiresAt: validDays ? this.now + validDays * DAY : null,
      status: 'active',
      createdAt: this.now,
    };
    this.s.pointLots.push(lot);
    return this.ledger(userId, sourceType === 'order' ? 'purchase' : 'grant', amount, 0, bizKey, { lotAllocations: [{ lotId: lot.id, amount }], remark, operatorAdminId: adminId || null });
  };
  // FIFO：先过期的先用，永久积分最后用
  M.activeLots = function (userId) {
    return this.s.pointLots
      .filter((l) => l.userId === userId && l.status === 'active' && l.remaining > 0)
      .sort((a, b) => (a.expiresAt ?? Infinity) - (b.expiresAt ?? Infinity) || a.id - b.id);
  };
  M.takeFromLots = function (userId, amount) {
    const allocations = [];
    let left = amount;
    for (const lot of this.activeLots(userId)) {
      if (left <= 0) break;
      const take = Math.min(lot.remaining, left);
      lot.remaining -= take;
      if (lot.remaining === 0) lot.status = 'exhausted';
      allocations.push({ lotId: lot.id, amount: take });
      left -= take;
    }
    if (left > 0) fail('POINTS_INSUFFICIENT', `可用积分不足，还差 ${left} 点`);
    return allocations;
  };
  M.holdPoints = function (userId, amount, taskId) {
    const bizKey = `hold:task:${taskId}`;
    const existing = this.ledgerByKey(bizKey);
    if (existing) return existing;
    if (this.pointAccount(userId).available < amount) fail('POINTS_INSUFFICIENT', `需要 ${amount} 点，可用 ${this.pointAccount(userId).available} 点`);
    const allocations = this.takeFromLots(userId, amount);
    return this.ledger(userId, 'hold', -amount, amount, bizKey, { taskId, lotAllocations: allocations });
  };
  // 任务终态结算：consumed 从冻结中扣除，剩余按冻结时的批次逆序原路退回
  M.settleTask = function (task, consumed, reason) {
    const doneKey = `settle:task:${task.id}`;
    if (this.ledgerByKey(`consume:task:${task.id}`) || this.ledgerByKey(`release:task:${task.id}`) || this.s.idempotency[doneKey]) return;
    this.s.idempotency[doneKey] = true;
    consumed = Math.max(0, Math.min(consumed, task.pointsHeld));
    const release = task.pointsHeld - consumed;
    task.pointsConsumed = consumed;
    task.pointsReleased = release;
    if (consumed > 0) this.ledger(task.userId, 'consume', 0, -consumed, `consume:task:${task.id}`, { taskId: task.id, remark: reason });
    if (release > 0) {
      const hold = this.ledgerByKey(`hold:task:${task.id}`);
      const allocations = [];
      let left = release;
      // 消耗视为从最早过期的批次扣，所以退回从最后分配的批次开始
      for (const a of hold.lotAllocations.slice().reverse()) {
        if (left <= 0) break;
        const back = Math.min(a.amount, left);
        allocations.push({ lotId: a.lotId, amount: back });
        left -= back;
      }
      this.ledger(task.userId, 'release', release, -release, `release:task:${task.id}`, { taskId: task.id, lotAllocations: allocations, remark: reason });
      for (const a of allocations) {
        const lot = this.s.pointLots.find((l) => l.id === a.lotId);
        lot.remaining += a.amount;
        if (lot.status === 'exhausted') lot.status = 'active';
        // 已过期或已撤回的批次：退回后立即失效
        const dead = lot.status === 'expired' || lot.status === 'revoked' || (lot.expiresAt != null && lot.expiresAt <= this.now);
        if (dead) {
          const type = lot.status === 'revoked' ? 'revoke' : 'expire';
          if (lot.status === 'active') lot.status = 'expired';
          const amt = a.amount;
          lot.remaining -= amt;
          this.ledger(task.userId, type, -amt, 0, `${type}:lot:${lot.id}:task:${task.id}`, { lotAllocations: [{ lotId: lot.id, amount: amt }], remark: '退回的积分所在批次已失效' });
        }
      }
    }
  };
  M.expirePoints = function () {
    let n = 0;
    for (const lot of this.s.pointLots) {
      if (lot.status !== 'active' || lot.expiresAt == null || lot.expiresAt > this.now) continue;
      const amt = lot.remaining;
      lot.status = 'expired';
      if (amt > 0) {
        lot.remaining = 0;
        this.ledger(lot.userId, 'expire', -amt, 0, `expire:lot:${lot.id}`, { lotAllocations: [{ lotId: lot.id, amount: amt }], remark: '积分到期' });
        n++;
      }
    }
    if (n) this.emit('points', `${n} 个积分批次到期`);
    return n;
  };
  M.adminGrantPoints = function (adminId, userId, amount, validDays, reason, requestId) {
    if (!reason) fail('PARAMS_INVALID', '必须填写发放原因');
    return this.tx(() => {
      const row = this.grantPoints(userId, amount, 'grant', `admin:${adminId}:${requestId}`, validDays, reason, adminId);
      this.audit('admin:' + adminId, 'points.grant', 'user:' + userId, `${amount} 点：${reason}`);
      this.emit('points', `管理员给 ${this.user(userId).name} 发放 ${amount} 积分`);
      return row;
    });
  };
  M.adminDeductPoints = function (adminId, userId, amount, reason, requestId) {
    if (!reason) fail('PARAMS_INVALID', '必须填写扣减原因');
    return this.tx(() => {
      const bizKey = `admin_adjust:${adminId}:${requestId}`;
      if (this.ledgerByKey(bizKey)) return this.ledgerByKey(bizKey);
      const alloc = this.takeFromLots(userId, amount);
      const row = this.ledger(userId, 'admin_adjust', -amount, 0, bizKey, { lotAllocations: alloc, remark: reason, operatorAdminId: adminId });
      this.audit('admin:' + adminId, 'points.deduct', 'user:' + userId, `${amount} 点：${reason}`);
      return row;
    });
  };
  // §5.4 积分包退款前置：撤回剩余积分，给出建议退款金额
  M.revokePointsForOrder = function (adminId, orderNo) {
    return this.tx(() => {
      const order = this.orderByNo(orderNo);
      const item = order.items[0];
      const lot = this.s.pointLots.find((l) => l.sourceRef === 'order_item:' + item.id);
      if (!lot) fail('NOT_FOUND', '该订单没有积分批次');
      if (lot.status === 'revoked') return { already: true, suggestRefund: lot.suggestRefund };
      const remaining = lot.remaining;
      const paid = order.walletPaid + order.onlinePaid - order.refunded;
      const suggest = Math.floor((paid * remaining) / lot.amount);
      if (remaining > 0) this.ledger(lot.userId, 'revoke', -remaining, 0, `revoke:order_item:${item.id}`, { lotAllocations: [{ lotId: lot.id, amount: remaining }], remark: '积分包退款撤回' });
      lot.remaining = 0;
      lot.status = 'revoked';
      lot.suggestRefund = suggest;
      this.audit('admin:' + adminId, 'points.revoke', 'order:' + orderNo, `撤回 ${remaining}/${lot.amount} 点，建议退 ${yuan(suggest)}`);
      this.emit('points', `订单 ${orderNo} 撤回剩余 ${remaining} 积分，建议退款 ${yuan(suggest)}`);
      return { revoked: remaining, total: lot.amount, used: lot.amount - remaining, suggestRefund: suggest };
    });
  };
  M.grantPointsForOrder = function (orderId) {
    return this.tx(() => {
      const order = this.find('orders', orderId);
      if (this.s.fulfillments.some((f) => f.orderId === orderId)) return { duplicated: true };
      if (order.status !== 'fulfilling') fail('INVALID_TRANSITION', `订单状态 ${order.status} 不能发放积分`);
      const item = order.items[0];
      const p = item.snapshot;
      const amount = (p.points + p.bonus) * item.quantity;
      this.grantPoints(order.userId, amount, 'order', 'order_item:' + item.id, p.validDays, `订单 ${order.orderNo}`);
      this.s.fulfillments.push({ id: this.nextId('fulfillment'), orderId, type: 'boost_points', status: 'delivered', payload: `已到账 ${amount} 积分`, deliveryData: { points: amount }, deliveredAt: this.now });
      order.status = 'completed';
      order.completedAt = this.now;
      this.emit('deliver', `订单 ${order.orderNo} 到账 ${amount} 积分`);
      return { points: amount };
    });
  };
  M.pointSummary = function (userId) {
    const acc = this.pointAccount(userId);
    const soon = this.activeLots(userId)
      .filter((l) => l.expiresAt != null && l.expiresAt - this.now <= 30 * DAY)
      .map((l) => ({ amount: l.remaining, expiresAt: l.expiresAt, sourceType: l.sourceType }));
    return { available: acc.available, frozen: acc.frozen, expiringSoon: soon };
  };

  // ===================== 报价（由功能 JSON 编译） =====================
  // 前端实时报价和服务端创建任务都走这里，前端传来的价格一律不使用。
  M.taskQuote = function (userId, req) {
    const f = this.feature(Number(req.featureId));
    if (f.status !== 'active') fail('NOT_FOUND', '功能已下架');
    const g = this.game(f.gameId);
    if (g.status !== 'active') fail('NOT_FOUND', '游戏未上线');
    const plan = S.compile(f.definition, req.params || {}, { discountRate: this.discountRateOf(userId) });
    return { featureId: f.id, featureVersion: f.version, plan, points: plan.total, available: this.pointAccount(userId).available, ok: plan.ok };
  };
  // ===================== §6.2 绑定 =====================
  M.binding = function (id) {
    return this.find('bindings', id, '绑定');
  };
  M.createBinding = function (userId, req) {
    return this.idem('bind:' + userId, req.idempotencyKey, () =>
      this.tx(() => {
        const game = this.game(req.gameId);
        if (!req.login || !req.password) fail('PARAMS_INVALID', '请填写登录名和密码');
        const today = Math.floor((this.now + TZ_OFFSET) / DAY);
        const todayCount = this.s.bindings.filter((b) => b.userId === userId && Math.floor((b.createdAt + TZ_OFFSET) / DAY) === today).length;
        if (todayCount >= (this.s.settings.bindDailyLimit || 10)) fail('TASK_LIMIT', '今天的绑定次数已用完');
        const fingerprint = C.sha256Hex(game.id + ':' + String(req.login).trim().toLowerCase());
        if (this.s.bindings.some((b) => b.userId === userId && b.fingerprint === fingerprint && b.status !== 'CANCELLED' && b.status !== 'FAIL')) {
          fail('DUPLICATE', '这个游戏账号已经绑定过');
        }
        const b = {
          id: this.nextId('binding'),
          userId,
          gameId: game.id,
          serverId: req.serverId,
          displayName: req.displayName || '未命名',
          loginMasked: maskLogin(req.login),
          fingerprint,
          credentialCipher: C.sealCredential('k1', { login: req.login, password: req.password }),
          status: 'PENDING',
          attempt: 0,
          maxAttempts: 3,
          availableAt: this.now,
          lastError: null,
          userMessage: '排队等待验证',
          sessionRef: null,
          verifiedAt: null,
          createdAt: this.now,
          updatedAt: this.now,
        };
        this.s.bindings.push(b);
        this.emit('bind', `${this.user(userId).name} 提交绑定「${b.displayName}」(${b.loginMasked})`);
        return { id: b.id, status: b.status };
      })
    );
  };
  M.deleteBinding = function (userId, bindingId) {
    return this.tx(() => {
      const b = this.binding(bindingId);
      if (b.userId !== userId) fail('FORBIDDEN');
      if (this.s.tasks.some((t) => t.bindingId === b.id && !TERMINAL.has(t.status))) fail('BINDING_BUSY', '还有未结束的任务，不能解绑');
      if (!TERMINAL.has(b.status)) {
        if (b.leaseToken) this.closeLease(b.leaseToken, 'cancelled');
        b.status = 'CANCELLED';
      }
      b.deletedAt = this.now;
      b.credentialCipher = null; // 解绑即销毁凭据
      this.emit('bind', `绑定「${b.displayName}」已解除，凭据已销毁`);
      return b;
    });
  };
  M.submitInput = function (userId, target, targetId, value) {
    return this.tx(() => {
      const obj = target === 'binding' ? this.binding(targetId) : this.task(targetId);
      if (obj.userId !== userId) fail('FORBIDDEN');
      if (obj.status !== 'WAIT_INPUT') fail('INVALID_TRANSITION', '当前不需要输入');
      const input = this.s.inputs.find((i) => i.target === target && i.targetId === targetId && i.status === 'waiting');
      if (!input) fail('NOT_FOUND');
      if (input.expiresAt <= this.now) fail('INPUT_EXPIRED');
      if (!value || !String(value).trim()) fail('PARAMS_INVALID', '请输入内容');
      input.valueCipher = C.sealCredential('k1', { value: String(value).trim() });
      input.status = 'submitted';
      input.submittedAt = this.now;
      obj.status = 'PENDING';
      obj.availableAt = this.now;
      obj.userMessage = '已提交，等待继续执行';
      this.emit(target === 'binding' ? 'bind' : 'task', `用户提交了${input.kind === 'sms_code' ? '短信验证码' : '输入'}，${target === 'binding' ? '绑定' : '任务'} #${targetId} 回到排队`);
      return { status: obj.status };
    });
  };
  M.confirmInGame = function (userId, bindingId) {
    return this.tx(() => {
      const b = this.binding(bindingId);
      if (b.userId !== userId) fail('FORBIDDEN');
      if (b.status !== 'WAIT_USER') fail('INVALID_TRANSITION', '当前不需要确认');
      b.status = 'PENDING';
      b.availableAt = this.now;
      b.userMessage = '已确认，等待继续验证';
      const input = this.s.inputs.find((i) => i.target === 'binding' && i.targetId === bindingId && i.status === 'waiting');
      if (input) {
        // 游戏内确认没有需要传给 Worker 的值，直接标记为已消费
        input.status = 'consumed';
        input.submittedAt = this.now;
      }
      return { status: b.status };
    });
  };

  // ===================== §6.3 / 6.4 任务 =====================
  M.task = function (id) {
    return this.find('tasks', id, '任务');
  };
  M.taskByNo = function (no) {
    const t = this.s.tasks.find((x) => x.taskNo === no);
    if (!t) fail('NOT_FOUND', `任务 ${no} 不存在`);
    return t;
  };
  M.createTask = function (userId, req) {
    return this.idem('task:' + userId, req.idempotencyKey, () =>
      this.tx(() => {
        const b = this.binding(Number(req.bindingId));
        if (b.userId !== userId || b.deletedAt) fail('FORBIDDEN', '只能使用自己的游戏账号');
        if (b.status !== 'SUCCESS') fail('INVALID_TRANSITION', '游戏账号还没有验证成功');
        const feature = this.feature(Number(req.featureId));
        if (feature.gameId !== b.gameId) fail('PARAMS_INVALID', '功能和游戏账号不属于同一个游戏');
        const q = this.taskQuote(userId, req);
        if (!q.plan.ok) fail('PARAMS_INVALID', q.plan.errors[0], q.plan.fieldErrors);
        const def = feature.definition;
        const active = this.s.tasks.filter((t) => t.userId === userId && !TERMINAL.has(t.status));
        if (active.length >= def.max_active_per_user) fail('TASK_LIMIT', `同时进行的任务最多 ${def.max_active_per_user} 个`);
        const id = this.nextId('task');
        const d = new Date(this.now + TZ_OFFSET);
        const plan = q.plan;
        const task = {
          id,
          taskNo: `BT${pad2(d.getUTCMonth() + 1)}${pad2(d.getUTCDate())}${String(id).padStart(5, '0')}`,
          userId,
          bindingId: b.id,
          gameId: b.gameId,
          featureId: feature.id,
          // 快照：功能定义版本 + 编译后的执行计划。后台之后改价、改关卡不影响这个任务。
          snapshot: {
            code: def.code,
            name: def.name,
            version: feature.version,
            capability: def.capability,
            refundPolicy: def.refund_policy,
            timeoutMinutes: def.timeout_minutes,
            rate: plan.rate,
          },
          params: plan.values,
          plan: { steps: plan.steps, fixed: plan.fixed, fixedPoints: plan.fixedPoints, units: plan.units, rawTotal: plan.rawTotal, total: plan.total, rate: plan.rate, breakdown: plan.breakdown },
          summary: this.planSummary(feature, plan),
          units: plan.units,
          pointsHeld: plan.total,
          pointsConsumed: 0,
          pointsReleased: 0,
          status: 'PENDING',
          cancelRequested: false,
          unitsDone: 0,
          progressPercent: 0,
          progressText: '排队中',
          result: null,
          errorCode: null,
          attempt: 0,
          maxAttempts: 2,
          workerId: null,
          leaseToken: null,
          leaseExpiresAt: null,
          availableAt: this.now,
          deadlineAt: this.now + def.timeout_minutes * MIN,
          startedAt: null,
          finishedAt: null,
          createdAt: this.now,
          eventSeq: 0,
        };
        this.s.tasks.push(task);
        this.holdPoints(userId, plan.total, id);
        this.taskEvent(task, 'state', `已创建，冻结 ${plan.total} 积分（${plan.units} 个执行单位）`);
        this.emit('task', `${this.user(userId).name} 创建任务 ${task.taskNo}「${def.name}」${task.summary}，冻结 ${plan.total} 点`);
        return { taskNo: task.taskNo, points: plan.total, status: task.status };
      })
    );
  };
  // 一句话概括任务内容，例如"扫荡 · 1-1 ×10、1-2 ×5"
  M.planSummary = function (feature, plan) {
    const def = feature.definition;
    const parts = [];
    for (const f of def.fields) {
      if (!plan.visible[f.key]) continue;
      const v = plan.values[f.key];
      if (v === undefined || v === '' || (Array.isArray(v) && !v.length)) continue;
      if (f.type === 'radio' || f.type === 'select') parts.push((f.options.find((o) => o.value === v) || {}).label);
      else if (f.type === 'switch') {
        if (v) parts.push(f.label);
      } else if (f.type === 'checkbox') parts.push(v.map((x) => (f.options.find((o) => o.value === x) || {}).label).join('、'));
      else if (f.type === 'number') parts.push(`${f.label} ${v}${f.unit || ''}`);
      else if (f.type === 'stage_picker') {
        const idx = S.itemIndex(def.catalogs[f.catalog]);
        const items = v.map((e) => idx.get(e.item).name + (e.count > 1 ? ` ×${e.count}` : ''));
        parts.push(items.length > 3 ? `${items.slice(0, 3).join('、')} 等 ${items.length} 个${def.catalogs[f.catalog].label}` : items.join('、'));
      }
    }
    return parts.filter(Boolean).join(' · ');
  };
  M.taskEvent = function (task, type, message, data, seq) {
    const s = seq != null ? seq : ++task.eventSeq;
    if (seq != null && seq > task.eventSeq) task.eventSeq = seq;
    this.s.taskEvents.push({ id: this.nextId('taskEvt'), taskId: task.id, seq: s, type, message: this.redact(task, message), data: data || null, at: this.now });
  };
  // 日志脱敏：替换绑定凭据原文和常见敏感键值
  M.redact = function (task, message) {
    let msg = String(message || '').slice(0, 500);
    const b = this.s.bindings.find((x) => x.id === task.bindingId);
    if (b && b.credentialCipher) {
      const cred = C.openCredential(b.credentialCipher);
      for (const v of [cred.password, cred.login]) if (v && v.length >= 3) msg = msg.split(v).join('***');
    }
    return msg.replace(/(password|passwd|pwd|token|code|验证码|密码)\s*[:=：]\s*\S+/gi, '$1=***');
  };
  M.userCancelTask = function (userId, taskNo) {
    return this.tx(() => {
      const t = this.taskByNo(taskNo);
      if (userId != null && t.userId !== userId) fail('FORBIDDEN');
      return this.cancelTaskInner(t, userId == null ? 'admin' : 'user');
    });
  };
  M.cancelTaskInner = function (t, by) {
    if (TERMINAL.has(t.status)) fail('INVALID_TRANSITION', '任务已结束');
    if (t.status === 'PROCESSING') {
      t.cancelRequested = true;
      this.taskEvent(t, 'state', `${by === 'admin' ? '管理员' : '用户'}请求取消，等待执行端确认`);
      this.emit('task', `任务 ${t.taskNo} 请求取消，执行端下次心跳会收到`);
      return { status: t.status, cancelRequested: true };
    }
    this.finishTaskTerminal(t, 'CANCELLED', null, by === 'admin' ? '管理员取消' : '用户取消');
    return { status: t.status };
  };
  M.adminRetryTask = function (adminId, taskNo) {
    return this.tx(() => {
      const t = this.taskByNo(taskNo);
      if (t.status !== 'FAIL') fail('INVALID_TRANSITION', '只有失败任务可以重试');
      // 重试会新建一个任务并重新冻结积分，原任务保持终态，账本可追溯
      const res = this.createTask(t.userId, { bindingId: t.bindingId, featureId: t.featureId, params: this.paramsForResubmit(t), idempotencyKey: 'retry:' + t.id });
      this.audit('admin:' + adminId, 'task.retry', 'task:' + taskNo, '新任务 ' + res.taskNo);
      return res;
    });
  };
  // 结算规则（§6.4）
  // 结算规则：Worker 只上报完成了多少个执行单位，扣多少积分由服务端按快照里的执行计划计算
  // stage_picker 的规范化值是 [{item,count}]，可直接回填；其它字段原样
  M.paramsForResubmit = function (t) {
    return clone(t.params);
  };
  M.consumedFor = function (t, status, errorCode) {
    const byUnits = S.pointsForUnits(t.plan, Math.min(t.unitsDone, t.units));
    if (status === 'SUCCESS' || status === 'CANCELLED') return byUnits;
    const def = ERROR_CODES[errorCode];
    const policy = t.snapshot.refundPolicy;
    if (policy === 'none') return t.pointsHeld;
    if (policy === 'full_on_system_fail' && def && def.category === 'system') return 0;
    return byUnits;
  };
  M.finishTaskTerminal = function (t, status, errorCode, reason) {
    t.status = status;
    t.errorCode = errorCode || null;
    t.finishedAt = this.now;
    if (t.leaseToken) this.closeLease(t.leaseToken, status.toLowerCase());
    t.leaseToken = null;
    t.leaseExpiresAt = null;
    for (const i of this.s.inputs) if (i.target === 'task' && i.targetId === t.id && i.status === 'waiting') i.status = 'expired';
    const consumed = this.consumedFor(t, status, errorCode);
    this.settleTask(t, consumed, reason);
    t.progressText = reason;
    this.taskEvent(t, 'state', `${statusLabel(status)}：${reason}。消耗 ${t.pointsConsumed} 点，退回 ${t.pointsReleased} 点`);
    this.emit('task', `任务 ${t.taskNo} ${statusLabel(status)}（${reason}），消耗 ${t.pointsConsumed}，退回 ${t.pointsReleased}`);
  };

  // ===================== §7 Worker 协议 =====================
  M.worker = function (id) {
    return this.find('workers', id, 'Worker');
  };
  M.createWorker = function (adminId, data) {
    return this.tx(() => {
      const secret = this.token('ws_');
      const w = {
        id: this.nextId('worker'),
        name: data.name,
        apiKey: this.token('wk_'),
        secretCipher: C.sealCredential('k1', { secret }),
        capabilities: clone(data.capabilities),
        maxConcurrency: data.maxConcurrency || 2,
        status: 'active',
        lastSeenAt: null,
        stats: { pulled: 0, success: 0, fail: 0, rejected: 0 },
      };
      this.s.workers.push(w);
      if (adminId) this.audit('admin:' + adminId, 'worker.create', 'worker:' + w.id, w.name);
      return { worker: w, secret }; // secret 只返回这一次
    });
  };
  M.rotateWorkerSecret = function (adminId, workerId) {
    return this.tx(() => {
      const w = this.worker(workerId);
      const secret = this.token('ws_');
      w.secretCipher = C.sealCredential('k1', { secret });
      this.audit('admin:' + adminId, 'worker.rotate', 'worker:' + workerId, '');
      this.emit('worker', `Worker「${w.name}」密钥已轮换，旧密钥立即失效`);
      return { secret };
    });
  };
  M.workerSecretForSimulator = function (workerId) {
    // 仅供原型里的 Worker 模拟器使用；正式系统中密钥只在创建/轮换时显示一次
    return C.openCredential(this.worker(workerId).secretCipher).secret;
  };
  M.signRequest = function (apiKey, secret, method, path, body, opts) {
    const ts = (opts && opts.timestamp) || Math.floor(this.now / 1000);
    const nonce = (opts && opts.nonce) || this.token('n_');
    const raw = body == null ? '' : JSON.stringify(body);
    const sign = C.hmacSha256Hex(secret, `${method}\n${path}\n${ts}\n${C.md5Hex(raw)}`);
    return { headers: { 'X-Worker-Key': apiKey, 'X-Worker-Timestamp': String(ts), 'X-Worker-Nonce': nonce, 'X-Worker-Signature': sign }, raw };
  };
  M.authWorker = function (method, path, headers, raw) {
    const h = headers || {};
    const w = this.s.workers.find((x) => x.apiKey === h['X-Worker-Key']);
    if (!w || w.status !== 'active') fail('UNAUTHORIZED', 'API Key 无效或节点已停用');
    const ts = Number(h['X-Worker-Timestamp']);
    if (!Number.isFinite(ts) || Math.abs(Math.floor(this.now / 1000) - ts) > 60) fail('UNAUTHORIZED', '时间戳偏差超过 60 秒');
    const nonce = h['X-Worker-Nonce'];
    if (!nonce) fail('UNAUTHORIZED', '缺少 nonce');
    for (const [k, exp] of Object.entries(this.s.nonces)) if (exp < this.now) delete this.s.nonces[k];
    const secret = C.openCredential(w.secretCipher).secret;
    const expected = C.hmacSha256Hex(secret, `${method}\n${path}\n${ts}\n${C.md5Hex(raw || '')}`);
    if (!C.safeEqual(expected, h['X-Worker-Signature'])) fail('UNAUTHORIZED', '签名不匹配');
    if (this.s.nonces[w.id + ':' + nonce]) fail('UNAUTHORIZED', 'nonce 已使用（重放请求）');
    this.s.nonces[w.id + ':' + nonce] = this.now + 120 * 1000;
    w.lastSeenAt = this.now;
    return w;
  };
  // 统一入口：模拟 HTTP。返回 {status, body}，不会抛异常
  M.workerHttp = function (method, path, headers, raw) {
    let worker = null;
    try {
      const body = raw ? JSON.parse(raw) : {};
      worker = this.authWorker(method, path, headers, raw);
      const data = this.tx(() => this.routeWorker(worker, method, path, body));
      return { status: 200, body: data };
    } catch (err) {
      // 事务回滚后旧对象已失效，按 id 重新取
      const w = worker && this.s.workers.find((x) => x.id === worker.id);
      if (w && err instanceof GameError && err.code === 'LEASE_LOST') w.stats.rejected++;
      if (err instanceof GameError) {
        this.emit('worker', `Worker 请求 ${method} ${path} 被拒绝：${err.code}`);
        return { status: err.http, body: { error: err.code, message: err.message } };
      }
      return { status: 500, body: { error: 'INTERNAL', message: String(err.message || err) } };
    }
  };
  M.workerCall = function (workerId, method, path, body, opts) {
    const w = this.worker(workerId);
    const secret = (opts && opts.secret) || this.workerSecretForSimulator(workerId);
    const { headers, raw } = this.signRequest(w.apiKey, secret, method, path, body, opts);
    const res = this.workerHttp(method, path, headers, raw);
    return { request: { method, path, headers, body }, response: res };
  };
  M.routeWorker = function (w, method, path, body) {
    if (method !== 'POST') fail('NOT_FOUND');
    if (path === '/api/v1/worker/pull') return this.workerPull(w, body);
    const m = path.match(/^\/api\/v1\/worker\/leases\/([^/]+)\/([a-z-]+)$/);
    if (!m) fail('NOT_FOUND');
    const [, token, action] = m;
    const handler = { heartbeat: this.workerHeartbeat, events: this.workerEvents, 'need-input': this.workerNeedInput, finish: this.workerFinish, fail: this.workerFail, 'cancel-ack': this.workerCancelAck }[action];
    if (!handler) fail('NOT_FOUND');
    // 带 request_id 的结果类请求：先查幂等记录，保证重复提交结果一致（即使租约已关闭）
    if (body.request_id) {
      const key = `${w.id}:${body.request_id}`;
      if (this.s.workerRequests[key]) return clone(this.s.workerRequests[key]);
      const res = handler.call(this, w, token, body);
      this.s.workerRequests[key] = clone(res);
      return res;
    }
    if (['finish', 'fail', 'cancel-ack'].includes(action)) fail('PARAMS_INVALID', '缺少 request_id');
    return handler.call(this, w, token, body);
  };
  M.lease = function (w, token) {
    const l = this.s.leases.find((x) => x.token === token);
    if (!l || !l.active || l.workerId !== w.id) fail('LEASE_LOST');
    if (l.expiresAt <= this.now) fail('LEASE_LOST', '租约已过期');
    return l;
  };
  M.closeLease = function (token, why) {
    const l = this.s.leases.find((x) => x.token === token);
    if (l && l.active) {
      l.active = false;
      l.closedAt = this.now;
      l.closeReason = why;
    }
  };
  M.workerCan = function (w, gameId, cap) {
    const game = this.game(gameId);
    return w.capabilities.some((c) => c.game === game.code && c.features.includes(cap));
  };
  M.workerPull = function (w, body) {
    const kinds = body.kinds || ['task', 'bind'];
    const max = Math.min(Number(body.max || 1), 5);
    const activeLeases = this.s.leases.filter((l) => l.active && l.workerId === w.id && l.expiresAt > this.now).length;
    const room = Math.max(0, Math.min(max, w.maxConcurrency - activeLeases));
    const out = [];
    const leaseMs = this.s.settings.leaseSeconds * 1000;
    const busyBindings = new Set(this.s.tasks.filter((t) => t.status === 'PROCESSING').map((t) => t.bindingId));
    const candidates = [];
    if (kinds.includes('bind')) {
      for (const b of this.s.bindings) if (b.status === 'PENDING' && !b.deletedAt && b.availableAt <= this.now && this.workerCan(w, b.gameId, 'bind')) candidates.push({ kind: 'bind', obj: b, at: b.updatedAt });
    }
    if (kinds.includes('task')) {
      for (const t of this.s.tasks) if (t.status === 'PENDING' && t.availableAt <= this.now && !busyBindings.has(t.bindingId) && this.workerCan(w, t.gameId, t.snapshot.capability)) candidates.push({ kind: 'task', obj: t, at: t.createdAt });
    }
    candidates.sort((a, b) => a.at - b.at || a.obj.id - b.obj.id);
    for (const c of candidates) {
      if (out.length >= room) break;
      const o = c.obj;
      if (c.kind === 'task' && busyBindings.has(o.bindingId)) continue;
      // CAS：WHERE id=? AND status='PENDING'
      if (o.status !== 'PENDING') continue;
      const token = this.token('ls_');
      o.status = 'PROCESSING';
      o.attempt += 1;
      o.workerId = w.id;
      o.leaseToken = token;
      o.leaseExpiresAt = this.now + leaseMs;
      o.updatedAt = this.now;
      this.s.leases.push({ token, kind: c.kind, targetId: o.id, workerId: w.id, active: true, createdAt: this.now, expiresAt: this.now + leaseMs });
      const binding = c.kind === 'bind' ? o : this.binding(o.bindingId);
      const cred = C.openCredential(binding.credentialCipher);
      const input = this.s.inputs.find((i) => i.target === (c.kind === 'bind' ? 'binding' : 'task') && i.targetId === o.id && i.status === 'submitted');
      let inputValue = null;
      if (input) {
        inputValue = { kind: input.kind, value: input.valueCipher ? C.openCredential(input.valueCipher).value : null };
        input.status = 'consumed';
        input.valueCipher = null; // 消费后立即清空
      }
      const game = this.game(o.gameId);
      const item = {
        kind: c.kind,
        id: o.id,
        lease_token: token,
        lease_expires_at: o.leaseExpiresAt,
        attempt: o.attempt,
        game: game.code,
        credential: { login: cred.login, password: cred.password },
        session_ref: binding.sessionRef,
        input: inputValue,
      };
      if (c.kind === 'task') {
        Object.assign(item, { task_no: o.taskNo, feature: o.snapshot.code, feature_version: o.snapshot.version, params: o.params, steps: o.plan.steps.map((x) => ({ seq: x.seq, label: x.label, count: x.count, ref: x.ref })), units: o.units, units_done: o.unitsDone });
        if (!o.startedAt) o.startedAt = this.now;
        o.progressText = '执行中';
        this.taskEvent(o, 'state', `执行端「${w.name}」领取（第 ${o.attempt} 次）`);
        busyBindings.add(o.bindingId);
      } else {
        o.userMessage = '正在登录验证';
      }
      w.stats.pulled++;
      out.push(item);
      this.emit('worker', `「${w.name}」领取${c.kind === 'task' ? '任务 ' + o.taskNo : '绑定 #' + o.id}`);
    }
    return { items: out };
  };
  M.leaseTarget = function (l) {
    return l.kind === 'task' ? this.task(l.targetId) : this.binding(l.targetId);
  };
  M.workerHeartbeat = function (w, token, body) {
    const l = this.lease(w, token);
    const o = this.leaseTarget(l);
    l.expiresAt = this.now + this.s.settings.leaseSeconds * 1000;
    o.leaseExpiresAt = l.expiresAt;
    if (l.kind === 'task') {
      const units = Math.max(o.unitsDone, Math.min(Number(body.units_done || 0), o.units));
      o.unitsDone = units;
      o.progressPercent = Math.min(100, Math.max(0, Number(body.progress_percent != null ? body.progress_percent : Math.round((units / o.units) * 100))));
      if (body.progress_text) o.progressText = this.redact(o, body.progress_text).slice(0, 200);
    }
    return { lease_expires_at: l.expiresAt, cancel_requested: l.kind === 'task' ? o.cancelRequested : false };
  };
  M.workerEvents = function (w, token, body) {
    const l = this.lease(w, token);
    if (l.kind !== 'task') return { ok: true };
    const t = this.task(l.targetId);
    const seq = Number(body.seq);
    if (!Number.isInteger(seq) || seq < 1) fail('PARAMS_INVALID', 'seq 必须是正整数');
    if (this.s.taskEvents.some((e) => e.taskId === t.id && e.seq === seq)) return { ok: true, duplicated: true };
    this.taskEvent(t, body.type || 'log', body.message, null, seq);
    return { ok: true };
  };
  M.workerNeedInput = function (w, token, body) {
    const l = this.lease(w, token);
    const o = this.leaseTarget(l);
    const target = l.kind === 'task' ? 'task' : 'binding';
    if (this.s.inputs.some((i) => i.target === target && i.targetId === o.id && i.status === 'waiting')) fail('DUPLICATE', '已有等待中的输入');
    const kind = body.kind || 'sms_code';
    const toStatus = kind === 'confirm' ? 'WAIT_USER' : 'WAIT_INPUT';
    if (target === 'task' && toStatus === 'WAIT_USER') fail('PARAMS_INVALID', '代练任务不支持 WAIT_USER');
    const input = { id: this.nextId('input'), target, targetId: o.id, kind, prompt: String(body.prompt || '').slice(0, 120), status: 'waiting', expiresAt: this.now + this.s.settings.inputExpireMinutes * MIN, createdAt: this.now };
    this.s.inputs.push(input);
    o.status = toStatus;
    o.userMessage = input.prompt || (toStatus === 'WAIT_USER' ? '请在游戏内确认登录' : '请输入验证码');
    this.closeLease(token, 'wait_input');
    o.leaseToken = null;
    o.leaseExpiresAt = null;
    if (target === 'task') this.taskEvent(o, 'input_request', o.userMessage);
    this.emit(target === 'task' ? 'task' : 'bind', `${target === 'task' ? '任务 ' + o.taskNo : '绑定 #' + o.id} 等待用户：${o.userMessage}`);
    return { input_id: input.id, status: o.status, expires_at: input.expiresAt };
  };
  M.workerFinish = function (w, token, body) {
    const l = this.lease(w, token);
    const o = this.leaseTarget(l);
    if (l.kind === 'task') {
      o.unitsDone = Math.max(o.unitsDone, Math.min(Number(body.units_done != null ? body.units_done : o.units), o.units));
      o.result = body.result ? clone(body.result) : null;
      o.progressPercent = 100;
      if (body.session_ref) this.binding(o.bindingId).sessionRef = String(body.session_ref);
      this.finishTaskTerminal(o, 'SUCCESS', null, '执行完成');
    } else {
      o.status = 'SUCCESS';
      o.verifiedAt = this.now;
      o.sessionRef = body.session_ref ? String(body.session_ref) : null;
      o.userMessage = '验证成功，可以创建代练任务';
      this.closeLease(token, 'success');
      o.leaseToken = null;
      this.emit('bind', `绑定 #${o.id}「${o.displayName}」验证成功`);
    }
    w.stats.success++;
    return { status: o.status };
  };
  M.workerFail = function (w, token, body) {
    const l = this.lease(w, token);
    const o = this.leaseTarget(l);
    const code = String(body.error_code || 'NETWORK');
    const def = ERROR_CODES[code];
    const retryable = def ? def.retryable : !!body.retryable;
    w.stats.fail++;
    if (l.kind === 'task') {
      o.unitsDone = Math.max(o.unitsDone, Math.min(Number(body.units_done || 0), o.units));
      if (body.message) this.taskEvent(o, 'log', body.message);
      if (def && def.bindFail) {
        const b = this.binding(o.bindingId);
        b.status = 'FAIL';
        b.lastError = code;
        b.userMessage = def.label + '，请重新绑定';
      }
      if (retryable && o.attempt < o.maxAttempts && this.now < o.deadlineAt) {
        o.status = 'PENDING';
        o.errorCode = code;
        o.availableAt = this.now + 30 * 1000 * 2 ** (o.attempt - 1);
        this.closeLease(token, 'retry');
        o.leaseToken = null;
        o.progressText = `${(def && def.label) || code}，稍后重试`;
        this.taskEvent(o, 'state', `失败（${code}），将重试`);
        return { status: o.status, retry_at: o.availableAt };
      }
      this.finishTaskTerminal(o, 'FAIL', code, (def && def.label) || code);
      return { status: o.status, points_consumed: o.pointsConsumed, points_released: o.pointsReleased };
    }
    // 绑定
    this.closeLease(token, 'fail');
    o.leaseToken = null;
    o.lastError = code;
    if (retryable && o.attempt < o.maxAttempts) {
      o.status = 'PENDING';
      o.availableAt = this.now + 30 * 1000 * 2 ** (o.attempt - 1);
      o.userMessage = `${(def && def.label) || code}，稍后自动重试`;
      return { status: o.status, retry_at: o.availableAt };
    }
    o.status = 'FAIL';
    o.userMessage = `${(def && def.label) || code}，请检查账号后重新绑定`;
    this.emit('bind', `绑定 #${o.id} 失败：${code}`);
    return { status: o.status };
  };
  M.workerCancelAck = function (w, token, body) {
    const l = this.lease(w, token);
    if (l.kind !== 'task') fail('PARAMS_INVALID');
    const t = this.task(l.targetId);
    if (!t.cancelRequested) fail('INVALID_TRANSITION', '该任务没有取消请求');
    t.unitsDone = Math.max(t.unitsDone, Math.min(Number(body.units_done || 0), t.units));
    this.finishTaskTerminal(t, 'CANCELLED', null, '已按完成量结算并取消');
    return { status: t.status, points_consumed: t.pointsConsumed, points_released: t.pointsReleased };
  };

  // ===================== §7.4 回收 =====================
  M.reap = function () {
    let changed = 0;
    for (const l of this.s.leases) {
      if (!l.active || l.expiresAt > this.now) continue;
      const o = this.leaseTarget(l);
      this.closeLease(l.token, 'expired');
      changed++;
      if (o.status !== 'PROCESSING' || o.leaseToken !== l.token) continue;
      o.leaseToken = null;
      o.leaseExpiresAt = null;
      if (l.kind === 'task') {
        if (o.attempt < o.maxAttempts && this.now < o.deadlineAt) {
          o.status = 'PENDING';
          o.availableAt = this.now;
          o.progressText = '执行端失联，重新排队';
          this.taskEvent(o, 'state', '租约过期，重新排队');
          this.emit('task', `任务 ${o.taskNo} 租约过期，重新排队（已用 ${o.attempt}/${o.maxAttempts} 次）`);
        } else {
          this.finishTaskTerminal(o, 'FAIL', 'LEASE_EXPIRED', '执行端多次失联');
        }
      } else if (o.attempt < o.maxAttempts) {
        o.status = 'PENDING';
        o.availableAt = this.now;
        o.userMessage = '验证中断，重新排队';
      } else {
        o.status = 'FAIL';
        o.lastError = 'LEASE_EXPIRED';
        o.userMessage = '验证多次中断，请稍后重新绑定';
      }
    }
    for (const i of this.s.inputs) {
      if (i.status !== 'waiting' || i.expiresAt > this.now) continue;
      i.status = 'expired';
      changed++;
      if (i.target === 'task') {
        const t = this.task(i.targetId);
        if (t.status === 'WAIT_INPUT') this.finishTaskTerminal(t, 'FAIL', 'INPUT_TIMEOUT', '验证码超时未提交');
      } else {
        const b = this.binding(i.targetId);
        if (b.status === 'WAIT_INPUT' || b.status === 'WAIT_USER') {
          b.status = 'FAIL';
          b.lastError = 'INPUT_TIMEOUT';
          b.userMessage = '验证超时，请重新绑定';
          this.emit('bind', `绑定 #${b.id} 等待输入超时`);
        }
      }
    }
    for (const t of this.s.tasks) {
      if (TERMINAL.has(t.status) || t.deadlineAt > this.now) continue;
      this.finishTaskTerminal(t, 'FAIL', 'TIMEOUT', '超过任务时限');
      changed++;
    }
    return changed;
  };

  // ===================== 时钟推进（周期任务） =====================
  // 事件驱动的时钟：直接跳到下一个"有事发生"的时间点，而不是固定步长轮询。
  // 周期任务的语义保持不变：reaper 每 30 秒、积分过期每小时。
  M.nextDueAt = function () {
    const s = this.s;
    let t = Infinity;
    const take = (v) => {
      if (v != null && v > this.now && v < t) t = v;
    };
    for (const j of s.jobs) take(Math.max(j.runAt, this.now + 1));
    const tick = (period) => Math.ceil((this.now + 1) / period) * period;
    // reaper 关心的到期点，按 30 秒 tick 对齐
    const align30 = (v) => Math.ceil(v / 30000) * 30000;
    for (const l of s.leases) if (l.active) take(align30(l.expiresAt));
    for (const i of s.inputs) if (i.status === 'waiting') take(align30(i.expiresAt));
    for (const x of s.tasks) if (!TERMINAL.has(x.status)) take(align30(x.deadlineAt));
    for (const l of s.pointLots) if (l.status === 'active' && l.expiresAt != null) take(Math.max(tick(HOUR), Math.ceil(l.expiresAt / HOUR) * HOUR));
    return t;
  };
  M.tickAll = function () {
    this.runDueJobs();
    const needReap =
      this.s.leases.some((l) => l.active && l.expiresAt <= this.now) ||
      this.s.inputs.some((i) => i.status === 'waiting' && i.expiresAt <= this.now) ||
      this.s.tasks.some((x) => !TERMINAL.has(x.status) && x.deadlineAt <= this.now);
    if (needReap) this.tx(() => this.reap());
    if (this.now % HOUR === 0 && this.s.pointLots.some((l) => l.status === 'active' && l.expiresAt != null && l.expiresAt <= this.now)) this.tx(() => this.expirePoints());
  };
  M.advance = function (ms) {
    const target = this.now + ms;
    let guard = 20000;
    while (this.now < target && guard-- > 0) {
      const next = Math.min(this.nextDueAt(), target);
      this.s.now = next;
      this.tickAll();
    }
    this.emit('clock', `时间快进 ${fmtDuration(ms)}，现在 ${this.fmtTime(this.now)}`);
  };

  // ===================== 不变量检查（§13） =====================
  M.checkInvariants = function () {
    const results = [];
    const check = (name, ok, detail) => results.push({ name, ok: !!ok, detail: ok ? '' : detail });
    const s = this.s;

    const badWallet = Object.entries(s.wallets).filter(([uid, bal]) => {
      const sum = s.walletTxns.filter((t) => t.userId === Number(uid)).reduce((a, t) => a + (t.direction === 'in' ? t.amount : -t.amount), 0);
      return sum !== bal || bal < 0;
    });
    check('钱包余额等于流水合计且不为负', !badWallet.length, `用户 ${badWallet.map((d) => d[0]).join(', ')}`);

    const fulfillCount = {};
    for (const f of s.fulfillments) fulfillCount[f.orderId] = (fulfillCount[f.orderId] || 0) + 1;
    const multiF = Object.entries(fulfillCount).filter(([, n]) => n > 1);
    check('每个积分包订单最多到账一次', !multiF.length, `订单 ${multiF.map((d) => d[0]).join(', ')}`);

    const paidNoLot = s.orders.filter((o) => ['completed', 'partially_refunded', 'refunded'].includes(o.status) && !s.pointLots.some((l) => l.sourceRef === 'order_item:' + o.items[0].id));
    check('已完成的积分包订单都有积分批次', !paidNoLot.length, paidNoLot.map((o) => o.orderNo).join(', '));

    const badPoints = [];
    for (const acc of Object.values(s.pointAccounts)) {
      const lots = s.pointLots.filter((l) => l.userId === acc.userId && l.status === 'active').reduce((a, l) => a + l.remaining, 0);
      const led = s.pointLedger.filter((r) => r.userId === acc.userId);
      const la = led.reduce((a, r) => a + r.dAvailable, 0);
      const lf = led.reduce((a, r) => a + r.dFrozen, 0);
      const frozenTasks = s.tasks.filter((t) => t.userId === acc.userId && !TERMINAL.has(t.status)).reduce((a, t) => a + t.pointsHeld, 0);
      if (acc.available < 0 || acc.frozen < 0 || lots !== acc.available || la !== acc.available || lf !== acc.frozen || frozenTasks !== acc.frozen) {
        badPoints.push(`${acc.userId}(可用 ${acc.available}/批次 ${lots}/流水 ${la}，冻结 ${acc.frozen}/流水 ${lf}/任务 ${frozenTasks})`);
      }
    }
    check('积分：可用 = 批次剩余 = 流水合计，冻结 = 进行中任务', !badPoints.length, badPoints.join('；'));

    const badSettle = s.tasks.filter((t) => TERMINAL.has(t.status) && t.pointsConsumed + t.pointsReleased !== t.pointsHeld);
    check('已结束任务：消耗 + 退回 = 冻结', !badSettle.length, badSettle.map((t) => t.taskNo).join(', '));

    const overCharge = s.tasks.filter((t) => TERMINAL.has(t.status) && t.status !== 'FAIL' && t.pointsConsumed > S.pointsForUnits(t.plan, t.unitsDone));
    check('成功或取消的任务，扣点不超过已完成部分的价格', !overCharge.length, overCharge.map((t) => t.taskNo).join(', '));

    const planMismatch = s.tasks.filter((t) => t.pointsHeld !== t.plan.total || t.units !== t.plan.steps.reduce((a, x) => a + x.count, 0));
    check('任务冻结积分与执行计划一致', !planMismatch.length, planMismatch.map((t) => t.taskNo).join(', '));

    const keys = s.pointLedger.map((r) => r.bizKey);
    check('积分流水业务号唯一', new Set(keys).size === keys.length, '存在重复 bizKey');

    const leasePer = {};
    for (const l of s.leases) if (l.active) leasePer[l.kind + l.targetId] = (leasePer[l.kind + l.targetId] || 0) + 1;
    check('每个任务 / 绑定最多一个有效租约', !Object.values(leasePer).some((n) => n > 1), '存在多个有效租约');

    const procPerBinding = {};
    for (const t of s.tasks) if (t.status === 'PROCESSING') procPerBinding[t.bindingId] = (procPerBinding[t.bindingId] || 0) + 1;
    check('同一游戏账号同时只跑一个任务', !Object.values(procPerBinding).some((n) => n > 1), '存在并行任务');

    const statusOk = s.tasks.every((t) => G.TASK_STATUSES.includes(t.status)) && s.bindings.every((b) => G.BIND_STATUSES.includes(b.status));
    check('任务 / 绑定状态都在集中定义的集合内', statusOk, '出现未定义状态');

    const badDefs = s.features.filter((f) => !S.validateDefinition(f.definition).ok);
    check('所有代练功能定义都通过校验', !badDefs.length, badDefs.map((f) => f.code).join(', '));

    const activeNoGame = s.features.filter((f) => f.status === 'active' && !s.games.some((g) => g.id === f.gameId));
    check('上架功能都属于存在的游戏', !activeNoGame.length, activeNoGame.map((f) => f.code).join(', '));

    const secrets = [];
    for (const b of s.bindings) if (b.credentialCipher) secrets.push(C.openCredential(b.credentialCipher).password);
    const haystack = JSON.stringify(s.log) + JSON.stringify(s.taskEvents) + JSON.stringify(s.audit) + JSON.stringify(s.tasks);
    const leaked = secrets.filter((p) => p && p.length >= 6 && haystack.includes(p));
    check('日志、任务、审计中没有明文密码', !leaked.length, `发现 ${leaked.length} 处`);

    return results;
  };

  function maskLogin(login) {
    const s = String(login);
    if (s.includes('@')) {
      const [u, d] = s.split('@');
      return u.slice(0, 2) + '***@' + d;
    }
    return s.length <= 4 ? s[0] + '***' : s.slice(0, 3) + '****' + s.slice(-2);
  }
  function pad2(n) {
    return String(n).padStart(2, '0');
  }
  function statusLabel(s) {
    return { SUCCESS: '完成', FAIL: '失败', CANCELLED: '已取消' }[s] || s;
  }
  function fmtDuration(ms) {
    if (ms >= DAY) return `${Math.round(ms / DAY)} 天`;
    if (ms >= HOUR) return `${Math.round(ms / HOUR)} 小时`;
    if (ms >= MIN) return `${Math.round(ms / MIN)} 分钟`;
    return `${Math.round(ms / 1000)} 秒`;
  }

  Object.assign(Engine.prototype, M);
  Engine.prototype.jobHandlers['boost:grant_points'] = function (p) {
    this.grantPointsForOrder(p.orderId);
  };

  const api = { ERROR_CODES, maskLogin, fmtDuration };
  if (isNode) module.exports = api;
  else root.GameBoost = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
