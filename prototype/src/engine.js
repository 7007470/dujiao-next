/* 自动代练 —— 业务引擎核心（原型）
 *
 * 本文件：基础设施（虚拟时钟、事务、幂等、队列）、钱包、积分包订单/支付/退款、
 *         后台管理的游戏、区服、代练功能（含 JSON 定义和版本）。
 * boost.js：积分账本、游戏账号绑定、代练任务、Worker 协议、回收、不变量。
 *
 * 约定：金额用"分"，积分用整数，时间用引擎虚拟时钟 now（毫秒）。
 * 所有失败抛 GameError(code, http, message)。
 */
(function (root) {
  'use strict';

  const isNode = typeof module !== 'undefined' && module.exports;
  const C = isNode ? require('./crypto.js') : root.GameCrypto;
  const S = isNode ? require('./catalog.js') : root.FeatureSchema;

  const MIN = 60 * 1000;
  const HOUR = 60 * MIN;
  const DAY = 24 * HOUR;
  const TZ_OFFSET = 8 * HOUR; // Asia/Shanghai

  class GameError extends Error {
    constructor(code, http, message, detail) {
      super(message);
      this.code = code;
      this.http = http;
      this.detail = detail;
    }
  }
  const E = {
    POINTS_INSUFFICIENT: ['POINTS_INSUFFICIENT', 409, '可用积分不足'],
    BINDING_BUSY: ['BINDING_BUSY', 409, '该游戏账号有任务正在进行'],
    TASK_LIMIT: ['TASK_LIMIT_REACHED', 409, '进行中的任务已达上限'],
    LEASE_LOST: ['LEASE_LOST', 409, '租约已失效'],
    INVALID_TRANSITION: ['INVALID_TRANSITION', 409, '当前状态不允许该操作'],
    PARAMS_INVALID: ['PARAMS_INVALID', 422, '参数不合法'],
    DEFINITION_INVALID: ['DEFINITION_INVALID', 422, '功能定义不合法'],
    INPUT_EXPIRED: ['INPUT_EXPIRED', 410, '输入已过期'],
    FORBIDDEN: ['FORBIDDEN', 403, '无权操作'],
    NOT_FOUND: ['NOT_FOUND', 404, '记录不存在'],
    UNAUTHORIZED: ['WORKER_UNAUTHORIZED', 401, 'Worker 认证失败'],
    WALLET_INSUFFICIENT: ['WALLET_INSUFFICIENT', 409, '钱包余额不足'],
    DUPLICATE: ['DUPLICATE', 409, '记录重复'],
    REFUND_BLOCKED: ['REFUND_BLOCKED', 409, '退款前置条件未满足'],
    VERSION_CONFLICT: ['VERSION_CONFLICT', 409, '数据已被其他人修改，请刷新后重试'],
  };
  function fail(key, message, detail) {
    const [code, http, msg] = E[key];
    throw new GameError(code, http, message || msg, detail);
  }

  const BIND_STATUSES = ['PENDING', 'PROCESSING', 'WAIT_INPUT', 'WAIT_USER', 'SUCCESS', 'FAIL', 'CANCELLED'];
  const TASK_STATUSES = ['PENDING', 'PROCESSING', 'WAIT_INPUT', 'SUCCESS', 'FAIL', 'CANCELLED'];
  const TERMINAL = new Set(['SUCCESS', 'FAIL', 'CANCELLED']);

  const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
  const pad = (n, w) => String(n).padStart(w || 2, '0');
  function yuan(cents) {
    const sign = cents < 0 ? '-' : '';
    const v = Math.abs(cents);
    return `${sign}¥${Math.floor(v / 100)}.${pad(v % 100, 2)}`;
  }

  class Engine {
    constructor(state) {
      this.s = state || Engine.emptyState();
      this.listeners = [];
    }
    static emptyState() {
      return {
        now: Date.UTC(2026, 9, 23, 1, 0, 0), // 2026-10-23 09:00 北京时间
        seq: {},
        memberLevels: [],
        users: [],
        admins: [],
        wallets: {},
        walletTxns: [],
        games: [],
        servers: [],
        features: [],
        featureVersions: [],
        pointPacks: [],
        orders: [],
        payments: [],
        fulfillments: [],
        refunds: [],
        idempotency: {},
        pointAccounts: {},
        pointLots: [],
        pointLedger: [],
        bindings: [],
        inputs: [],
        tasks: [],
        taskEvents: [],
        workers: [],
        leases: [],
        workerRequests: {},
        nonces: {},
        jobs: [],
        deadJobs: [],
        audit: [],
        log: [],
        settings: { orderExpireMinutes: 15, leaseSeconds: 90, inputExpireMinutes: 5, bindDailyLimit: 10 },
      };
    }

    // ---------------- 基础设施 ----------------
    get now() {
      return this.s.now;
    }
    nextId(name) {
      this.s.seq[name] = (this.s.seq[name] || 0) + 1;
      return this.s.seq[name];
    }
    token(prefix) {
      const n = this.nextId('token_' + prefix);
      return prefix + C.sha256Hex(prefix + ':' + n + ':' + this.s.now).slice(0, 24);
    }
    emit(kind, message, data) {
      const entry = { id: this.nextId('log'), at: this.now, kind, message, data: data || null };
      this.s.log.push(entry);
      if (this.s.log.length > 600) this.s.log.splice(0, this.s.log.length - 600);
      for (const fn of this.listeners) fn(entry);
    }
    audit(actor, action, target, detail) {
      this.s.audit.push({ id: this.nextId('audit'), at: this.now, actor, action, target, detail: detail || '' });
    }
    // 事务：失败时原地恢复状态（this.s 引用不变，但嵌套对象被替换，失败后需按 id 重新查找）
    tx(fn) {
      const snapshot = JSON.stringify(this.s);
      const logLen = this.s.log.length;
      try {
        return fn();
      } catch (err) {
        const restored = JSON.parse(snapshot);
        restored.log = this.s.log.slice(0, logLen);
        for (const k of Object.keys(this.s)) delete this.s[k];
        Object.assign(this.s, restored);
        throw err;
      }
    }
    idem(scope, key, fn) {
      if (!key) return fn();
      const k = scope + ':' + key;
      if (Object.prototype.hasOwnProperty.call(this.s.idempotency, k)) return clone(this.s.idempotency[k]);
      const result = fn();
      this.s.idempotency[k] = clone(result);
      return result;
    }
    fmtTime(ms) {
      const d = new Date(ms + TZ_OFFSET);
      return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
    }
    serialize() {
      return JSON.stringify(this.s);
    }
    static restore(json) {
      return new Engine(JSON.parse(json));
    }
    find(list, id, label) {
      const row = this.s[list].find((r) => r.id === id);
      if (!row) fail('NOT_FOUND', `${label || list} #${id} 不存在`);
      return row;
    }
    user(id) {
      return this.find('users', id, '用户');
    }
    game(id) {
      return this.find('games', id, '游戏');
    }
    level(id) {
      return this.s.memberLevels.find((l) => l.id === id) || null;
    }
    discountRateOf(userId) {
      const l = this.level(this.user(userId).levelId);
      return l && l.active ? l.discountRate : 100;
    }
    requirePerm(adminId, perm) {
      const a = this.s.admins.find((x) => x.id === adminId);
      if (!a) fail('FORBIDDEN', '管理员不存在');
      const ok = a.perms.some((p) => p === perm || (p.endsWith(':*') && perm.startsWith(p.slice(0, -1))));
      if (!ok) fail('FORBIDDEN', `缺少权限 ${perm}`);
      return a;
    }

    // ---------------- 队列 ----------------
    enqueue(type, payload, delayMs, opts) {
      const job = { id: this.nextId('job'), type, payload, runAt: this.now + (delayMs || 0), attempts: 0, maxRetry: (opts && opts.maxRetry) || 3, unique: (opts && opts.unique) || null };
      if (job.unique && this.s.jobs.some((j) => j.unique === job.unique)) return null;
      this.s.jobs.push(job);
      return job;
    }
    runDueJobs(limit) {
      let guard = limit || 500;
      for (;;) {
        const due = this.s.jobs.filter((j) => j.runAt <= this.now).sort((a, b) => a.runAt - b.runAt || a.id - b.id);
        if (!due.length || guard-- <= 0) break;
        const job = due[0];
        this.s.jobs = this.s.jobs.filter((j) => j.id !== job.id);
        try {
          this.jobHandlers[job.type].call(this, job.payload);
        } catch (err) {
          job.attempts += 1;
          const retryable = !(err instanceof GameError) || err.http >= 500;
          if (retryable && job.attempts <= job.maxRetry) {
            job.runAt = this.now + 2 ** job.attempts * 1000;
            this.s.jobs.push(job);
          } else {
            this.s.deadJobs.push({ ...job, error: err.message, at: this.now });
            this.emit('alert', `作业 ${job.type} 放弃重试，需人工处理：${err.message}`, job.payload);
          }
        }
      }
    }

    // ---------------- 钱包（模拟原生 wallet 模块的外部行为） ----------------
    walletBalance(userId) {
      return this.s.wallets[userId] || 0;
    }
    walletChange(userId, delta, type, reference, remark, orderId) {
      const existing = this.s.walletTxns.find((t) => t.reference === reference);
      if (existing) return existing;
      const before = this.walletBalance(userId);
      const after = before + delta;
      if (after < 0) fail('WALLET_INSUFFICIENT');
      this.s.wallets[userId] = after;
      const txn = { id: this.nextId('wtxn'), userId, orderId: orderId || null, type, direction: delta >= 0 ? 'in' : 'out', amount: Math.abs(delta), before, after, reference, remark, at: this.now };
      this.s.walletTxns.push(txn);
      return txn;
    }

    // ================================================================
    // 后台：游戏与区服
    // ================================================================
    adminSaveGame(adminId, data) {
      this.requirePerm(adminId, 'boost:game:write');
      return this.tx(() => {
        const code = String(data.code || '').trim();
        const name = String(data.name || '').trim();
        if (!/^[a-z][a-z0-9_]{1,31}$/.test(code)) fail('PARAMS_INVALID', '游戏 code 需为 2–32 位小写字母、数字、下划线');
        if (!name || name.length > 30) fail('PARAMS_INVALID', '游戏名称必填，最多 30 个字');
        let g;
        if (data.id) {
          g = this.game(data.id);
          if (data.version != null && g.version !== data.version) fail('VERSION_CONFLICT');
          if (g.code !== code) fail('PARAMS_INVALID', '游戏 code 创建后不能修改（执行端能力和历史任务都引用它）');
        } else {
          if (this.s.games.some((x) => x.code === code)) fail('DUPLICATE', `游戏 code「${code}」已存在`);
          g = { id: this.nextId('game'), code, status: 'draft', version: 0, createdAt: this.now };
          this.s.games.push(g);
        }
        g.name = name;
        g.short = String(data.short || name.slice(0, 2)).slice(0, 4);
        g.color = /^#[0-9a-f]{6}$/i.test(data.color || '') ? data.color : g.color || '#2f6f6a';
        g.sort = Number.isInteger(Number(data.sort)) ? Number(data.sort) : g.sort || 0;
        g.version += 1;
        g.updatedAt = this.now;
        this.audit('admin:' + adminId, data.id ? 'game.update' : 'game.create', 'game:' + g.id, g.name);
        this.emit('admin', `${data.id ? '更新' : '新建'}游戏「${g.name}」`);
        return g;
      });
    }
    adminSetGameStatus(adminId, gameId, status) {
      this.requirePerm(adminId, 'boost:game:write');
      return this.tx(() => {
        const g = this.game(gameId);
        if (!['draft', 'active', 'hidden'].includes(status)) fail('PARAMS_INVALID');
        if (status === 'active') {
          if (!this.s.servers.some((s) => s.gameId === g.id && s.status === 'active')) fail('INVALID_TRANSITION', '上线前至少要有一个启用的区服');
          if (!this.s.features.some((f) => f.gameId === g.id && f.status === 'active')) fail('INVALID_TRANSITION', '上线前至少要有一个已上架的代练功能');
        }
        g.status = status;
        g.version += 1;
        this.audit('admin:' + adminId, 'game.status', 'game:' + g.id, status);
        this.emit('admin', `游戏「${g.name}」${{ active: '已上线', hidden: '已隐藏', draft: '回到草稿' }[status]}`);
        return g;
      });
    }
    adminSaveServer(adminId, data) {
      this.requirePerm(adminId, 'boost:game:write');
      return this.tx(() => {
        const g = this.game(Number(data.gameId));
        const code = String(data.code || '').trim();
        const name = String(data.name || '').trim();
        if (!/^[a-z0-9][a-z0-9_-]{0,31}$/i.test(code)) fail('PARAMS_INVALID', '区服 code 需为字母、数字、- _');
        if (!name) fail('PARAMS_INVALID', '区服名称必填');
        let s;
        if (data.id) {
          s = this.find('servers', data.id, '区服');
          if (s.code !== code && this.s.servers.some((x) => x.gameId === g.id && x.code === code)) fail('DUPLICATE', '区服 code 重复');
        } else {
          if (this.s.servers.some((x) => x.gameId === g.id && x.code === code)) fail('DUPLICATE', `区服 code「${code}」已存在`);
          s = { id: this.nextId('server'), gameId: g.id, status: 'active' };
          this.s.servers.push(s);
        }
        s.code = code;
        s.name = name;
        s.platform = ['all', 'android', 'ios', 'pc'].includes(data.platform) ? data.platform : 'all';
        this.audit('admin:' + adminId, data.id ? 'server.update' : 'server.create', 'server:' + s.id, `${g.name} / ${s.name}`);
        return s;
      });
    }
    adminSetServerStatus(adminId, serverId, status) {
      this.requirePerm(adminId, 'boost:game:write');
      const s = this.find('servers', serverId, '区服');
      s.status = status === 'active' ? 'active' : 'disabled';
      this.audit('admin:' + adminId, 'server.status', 'server:' + s.id, s.status);
      return s;
    }

    // ================================================================
    // 后台：代练功能（JSON 定义 + 版本）
    // ================================================================
    feature(id) {
      return this.find('features', id, '代练功能');
    }
    // 保存功能：校验 JSON → 新版本。已创建的任务保留下单时的快照，不受影响。
    adminSaveFeature(adminId, data) {
      this.requirePerm(adminId, 'boost:feature:write');
      return this.tx(() => {
        const g = this.game(Number(data.gameId));
        let def = data.definition;
        if (typeof def === 'string') {
          try {
            def = JSON.parse(def);
          } catch (e) {
            fail('DEFINITION_INVALID', 'JSON 格式错误：' + e.message);
          }
        }
        const v = S.validateDefinition(def);
        if (!v.ok) fail('DEFINITION_INVALID', `功能定义有 ${v.errors.length} 处错误：${v.errors[0].path} ${v.errors[0].msg}`, v.errors);
        const norm = S.normalize(def);
        let f;
        if (data.id) {
          f = this.feature(data.id);
          if (data.version != null && f.version !== data.version) fail('VERSION_CONFLICT');
          if (f.gameId !== g.id) fail('PARAMS_INVALID', '不能把功能移到另一个游戏');
          if (f.code !== norm.code) fail('PARAMS_INVALID', '功能 code 创建后不能修改');
        } else {
          if (this.s.features.some((x) => x.gameId === g.id && x.code === norm.code)) fail('DUPLICATE', `该游戏已有功能 code「${norm.code}」`);
          f = { id: this.nextId('feature'), gameId: g.id, code: norm.code, status: 'draft', version: 0, sort: this.s.features.filter((x) => x.gameId === g.id).length, createdAt: this.now };
          this.s.features.push(f);
        }
        if (f.version > 0 && JSON.stringify(f.definition) === JSON.stringify(norm)) return f; // 无变化不升版本
        f.definition = norm;
        f.version += 1;
        f.updatedAt = this.now;
        this.s.featureVersions.push({ featureId: f.id, version: f.version, definition: clone(norm), adminId, at: this.now });
        this.audit('admin:' + adminId, data.id ? 'feature.update' : 'feature.create', 'feature:' + f.id, `${g.name} / ${norm.name} v${f.version}`);
        this.emit('admin', `${data.id ? '更新' : '新建'}代练功能「${norm.name}」v${f.version}${v.warnings.length ? `（${v.warnings.length} 条提示）` : ''}`);
        return f;
      });
    }
    adminSetFeatureStatus(adminId, featureId, status) {
      this.requirePerm(adminId, 'boost:feature:write');
      return this.tx(() => {
        const f = this.feature(featureId);
        if (!['draft', 'active', 'disabled'].includes(status)) fail('PARAMS_INVALID');
        f.status = status;
        this.audit('admin:' + adminId, 'feature.status', 'feature:' + f.id, status);
        this.emit('admin', `代练功能「${f.definition.name}」${{ active: '已上架', disabled: '已下架', draft: '回到草稿' }[status]}`);
        return f;
      });
    }
    adminReorderFeature(adminId, featureId, delta) {
      this.requirePerm(adminId, 'boost:feature:write');
      const f = this.feature(featureId);
      const list = this.s.features.filter((x) => x.gameId === f.gameId).sort((a, b) => a.sort - b.sort);
      const i = list.indexOf(f);
      const j = i + delta;
      if (j < 0 || j >= list.length) return f;
      [list[i], list[j]] = [list[j], list[i]];
      list.forEach((x, k) => (x.sort = k));
      return f;
    }
    // 用户可见的游戏与功能
    publicGames() {
      return this.s.games.filter((g) => g.status === 'active').sort((a, b) => a.sort - b.sort || a.id - b.id);
    }
    publicFeatures(gameId) {
      return this.s.features.filter((f) => f.gameId === gameId && f.status === 'active').sort((a, b) => a.sort - b.sort);
    }

    // ================================================================
    // 积分包：订单、支付、交付、退款
    // ================================================================
    pack(id) {
      return this.find('pointPacks', id, '积分包');
    }
    adminSavePack(adminId, data) {
      this.requirePerm(adminId, 'boost:points:write');
      return this.tx(() => {
        const price = Math.round(Number(data.price) * 100);
        const points = Math.trunc(Number(data.points));
        const bonus = Math.trunc(Number(data.bonus || 0));
        const validDays = Math.trunc(Number(data.validDays || 0));
        if (!(price > 0) || !(points > 0) || bonus < 0 || validDays < 0) fail('PARAMS_INVALID', '价格、积分必须为正数');
        let p;
        if (data.id) p = this.pack(data.id);
        else {
          p = { id: this.nextId('pack'), active: true };
          this.s.pointPacks.push(p);
        }
        Object.assign(p, { name: String(data.name || `${points + bonus} 点`), price, points, bonus, validDays: validDays || null });
        this.audit('admin:' + adminId, data.id ? 'pack.update' : 'pack.create', 'pack:' + p.id, `${p.name} ${yuan(price)}`);
        return p;
      });
    }
    quotePack(userId, packId) {
      const p = this.pack(packId);
      if (!p.active) fail('NOT_FOUND', '积分包已下架');
      const rate = this.discountRateOf(userId);
      const unit = rate < 100 ? Math.round((p.price * rate) / 100) : p.price;
      return { packId: p.id, original: p.price, total: unit, memberDiscount: p.price - unit, points: p.points + p.bonus, validDays: p.validDays, levelName: (this.level(this.user(userId).levelId) || {}).name || '' };
    }
    createOrder(userId, req) {
      return this.idem('order.create:' + userId, req.idempotencyKey, () =>
        this.tx(() => {
          const q = this.quotePack(userId, req.packId);
          const p = this.pack(req.packId);
          const id = this.nextId('order');
          const d = new Date(this.now + TZ_OFFSET);
          const orderNo = `DJ${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}${pad(id, 5)}`;
          const order = {
            id, orderNo, userId, status: 'pending_payment',
            items: [{ id: this.nextId('orderItem'), packId: p.id, title: `代练积分包 · ${p.name}`, quantity: 1, original: q.original, memberDiscount: q.memberDiscount, total: q.total, fulfillmentType: 'boost_points', snapshot: { points: p.points, bonus: p.bonus, validDays: p.validDays }, levelName: q.levelName }],
            total: q.total, walletPaid: 0, onlinePaid: 0, refunded: 0,
            expiresAt: this.now + this.s.settings.orderExpireMinutes * MIN, createdAt: this.now, paidAt: null, completedAt: null, exceptions: [],
          };
          this.s.orders.push(order);
          if (req.useBalance) {
            const pay = Math.min(this.walletBalance(userId), order.total);
            if (pay > 0) {
              this.walletChange(userId, -pay, 'order_pay', `order:${order.id}:wallet_pay`, `订单 ${orderNo} 余额抵扣`, order.id);
              order.walletPaid = pay;
            }
          }
          this.emit('order', `${this.user(userId).name} 下单 ${p.name}，应付 ${yuan(order.total)}`);
          if (order.walletPaid >= order.total) this.markPaid(order, 'wallet');
          else this.enqueue('order:timeout_cancel', { orderId: order.id }, order.expiresAt - this.now);
          return { orderNo, total: order.total, walletPaid: order.walletPaid, due: order.total - order.walletPaid, status: order.status };
        })
      );
    }
    orderByNo(orderNo) {
      const o = this.s.orders.find((r) => r.orderNo === orderNo);
      if (!o) fail('NOT_FOUND', `订单 ${orderNo} 不存在`);
      return o;
    }
    createPayment(orderNo) {
      const order = this.orderByNo(orderNo);
      if (order.status !== 'pending_payment') fail('INVALID_TRANSITION', '订单不是待支付状态');
      const existing = this.s.payments.find((p) => p.orderId === order.id && p.status === 'pending');
      if (existing) return existing;
      const p = { id: this.nextId('payment'), paymentNo: 'PAY' + pad(this.nextId('paymentNo'), 8), orderId: order.id, amount: order.total - order.walletPaid, status: 'pending', callbacks: 0, createdAt: this.now };
      this.s.payments.push(p);
      return p;
    }
    paymentCallback(paymentNo, amount) {
      return this.tx(() => {
        const p = this.s.payments.find((x) => x.paymentNo === paymentNo);
        if (!p) fail('NOT_FOUND', '支付单不存在');
        p.callbacks += 1;
        if (p.status === 'success') {
          this.emit('payment', `支付单 ${paymentNo} 重复回调（第 ${p.callbacks} 次），已忽略`);
          return { duplicated: true };
        }
        const order = this.find('orders', p.orderId);
        p.status = 'success';
        p.paidAt = this.now;
        if (order.status !== 'pending_payment') {
          order.exceptions.push('closed_order_payment_succeeded');
          this.emit('alert', `订单 ${order.orderNo} 已${order.status === 'canceled' ? '取消' : order.status}，但收到支付成功，已标记异常待人工处理`);
          return { exception: 'closed_order_payment_succeeded' };
        }
        if (amount < p.amount) {
          order.exceptions.push('underpaid_payment_succeeded');
          this.emit('alert', `订单 ${order.orderNo} 实付 ${yuan(amount)} 少于应付 ${yuan(p.amount)}，不交付`);
          return { exception: 'underpaid_payment_succeeded' };
        }
        order.onlinePaid = amount;
        this.markPaid(order, 'online');
        return { ok: true };
      });
    }
    markPaid(order, via) {
      order.status = 'fulfilling';
      order.paidAt = this.now;
      this.s.jobs = this.s.jobs.filter((j) => !(j.type === 'order:timeout_cancel' && j.payload.orderId === order.id));
      this.enqueue('boost:grant_points', { orderId: order.id }, 0, { maxRetry: 5, unique: 'grant:' + order.id });
      this.emit('payment', `订单 ${order.orderNo} 支付成功（${via === 'wallet' ? '余额' : '在线'}）`);
    }
    cancelOrder(userId, orderNo) {
      return this.tx(() => {
        const order = this.orderByNo(orderNo);
        if (userId != null && order.userId !== userId) fail('FORBIDDEN', '不是你的订单');
        if (order.status !== 'pending_payment') fail('INVALID_TRANSITION', '只有待支付订单可以取消');
        this.cancelPending(order, 'cancel');
        return order;
      });
    }
    cancelPending(order, reason) {
      order.status = 'canceled';
      order.canceledAt = this.now;
      if (order.walletPaid > 0) this.walletChange(order.userId, order.walletPaid, 'order_refund', `order:${order.id}:wallet_release`, `订单 ${order.orderNo} 取消退回余额`, order.id);
      for (const p of this.s.payments) if (p.orderId === order.id && p.status === 'pending') p.status = 'expired';
      this.emit('order', `订单 ${order.orderNo} ${reason === 'timeout' ? '超时' : '已'}取消`);
    }
    adminRefund(adminId, orderNo, amount, requestId, reason) {
      this.requirePerm(adminId, 'order:refund');
      return this.idem('refund:' + orderNo, requestId, () =>
        this.tx(() => {
          const order = this.orderByNo(orderNo);
          const item = order.items[0];
          const lot = this.s.pointLots.find((l) => l.sourceRef === 'order_item:' + item.id);
          if (lot && lot.status !== 'revoked') fail('REFUND_BLOCKED', '积分包订单必须先撤回剩余积分，再按建议金额退款');
          if (!['completed', 'partially_refunded'].includes(order.status)) fail('INVALID_TRANSITION', '只有已完成订单可以退款');
          const paid = order.walletPaid + order.onlinePaid;
          if (amount <= 0 || order.refunded + amount > paid) fail('PARAMS_INVALID', `可退金额最多 ${yuan(paid - order.refunded)}`);
          this.walletChange(order.userId, amount, 'admin_refund', `refund:order:${order.id}:${requestId}`, `订单 ${order.orderNo} 退款：${reason || ''}`, order.id);
          order.refunded += amount;
          order.status = order.refunded >= paid ? 'refunded' : 'partially_refunded';
          this.s.refunds.push({ id: this.nextId('refund'), orderId: order.id, orderNo, amount, reason: reason || '', adminId, at: this.now });
          this.audit('admin:' + adminId, 'order.refund', 'order:' + orderNo, `${yuan(amount)} ${reason || ''}`);
          this.emit('refund', `订单 ${orderNo} 退款 ${yuan(amount)} 到钱包`);
          return { refunded: amount, status: order.status };
        })
      );
    }
  }

  Engine.prototype.jobHandlers = {
    'order:timeout_cancel'(p) {
      this.tx(() => {
        const order = this.find('orders', p.orderId);
        if (order.status === 'pending_payment') this.cancelPending(order, 'timeout');
      });
    },
  };

  const api = { Engine, GameError, E, fail, yuan, clone, pad, MIN, HOUR, DAY, TZ_OFFSET, BIND_STATUSES, TASK_STATUSES, TERMINAL };
  if (isNode) module.exports = api;
  else root.GameEngine = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
