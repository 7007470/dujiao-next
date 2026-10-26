/* 界面骨架：状态、渲染、事件分发、持久化、执行端自动运行。 */
(function () {
  'use strict';
  const { Engine, yuan, MIN, HOUR, DAY, TERMINAL } = window.GameEngine;
  const STORE_KEY = 'djgame-boost-prototype-v2';

  const App = (window.App = { views: {}, actions: {}, live: {} });

  // ---------- 持久化 ----------
  App.load = function () {
    try {
      const raw = localStorage.getItem(STORE_KEY);
      if (raw) return Engine.restore(raw);
    } catch (e) {
      /* 预览环境可能禁用存储 */
    }
    return window.GameSeed.seed();
  };
  App.save = function () {
    try {
      localStorage.setItem(STORE_KEY, App.E.serialize());
    } catch (e) {
      /* 忽略 */
    }
  };
  App.E = App.load();

  App.ui = {
    role: 'buyer',
    buyerTab: 'boost',
    adminTab: 'games',
    userId: 2,
    adminId: 1,
    // 代练流程：选游戏 → 选账号 → 选功能 → 填表
    flow: { gameId: null, bindingId: null, featureId: null },
    bind: { gameId: 1, serverId: 1, login: '', password: '', displayName: '', open: false },
    inputs: {},
    lease: {},
    http: [],
    forms: {},
    openTask: null,
    admin: { gameId: null, featureId: null, draft: null, draftFor: null, previewUser: 2, taskStatus: '', grant: { userId: 1, amount: 100, validDays: 30, reason: '' }, newGame: { code: '', name: '', short: '', color: '#2f6f6a' }, newServer: { code: '', name: '', platform: 'all' }, pack: {} },
    modal: null,
    drawer: null,
    auto: false,
    checks: null,
    drill: null,
  };

  // ---------- 工具 ----------
  const esc = (App.esc = (s) =>
    String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]));
  App.yuan = yuan;
  App.t = (ms) => App.E.fmtTime(ms);
  App.ago = function (ms) {
    const d = App.E.now - ms;
    if (d < MIN) return '刚刚';
    if (d < HOUR) return Math.floor(d / MIN) + ' 分钟前';
    if (d < DAY) return Math.floor(d / HOUR) + ' 小时前';
    return Math.floor(d / DAY) + ' 天前';
  };
  App.left = function (ms) {
    const d = ms - App.E.now;
    if (d <= 0) return '已到期';
    if (d < MIN) return Math.ceil(d / 1000) + ' 秒';
    if (d < HOUR) return Math.ceil(d / MIN) + ' 分钟';
    if (d < DAY) return Math.floor(d / HOUR) + ' 小时 ' + Math.floor((d % HOUR) / MIN) + ' 分';
    return Math.floor(d / DAY) + ' 天';
  };
  App.uid = () => Math.random().toString(36).slice(2, 10);

  const LABEL = {
    draft: ['草稿', ''], hidden: ['已隐藏', ''],
    pending_payment: ['待支付', 'warn'], fulfilling: ['交付中', 'run'], completed: ['已完成', 'ok'], canceled: ['已取消', ''], refunded: ['已退款', 'err'], partially_refunded: ['部分退款', 'err'],
    PENDING: ['排队中', 'info'], PROCESSING: ['执行中', 'run'], WAIT_INPUT: ['等待验证码', 'warn'], WAIT_USER: ['等待游戏内确认', 'warn'], SUCCESS: ['成功', 'ok'], FAIL: ['失败', 'err'], CANCELLED: ['已取消', ''],
    active: ['生效', 'ok'], paused: ['已暂停', ''], stopped: ['已停止', ''], stopped_insufficient_points: ['积分不足暂停', 'err'], exhausted: ['已用完', ''], expired: ['已过期', 'err'], revoked: ['已撤回', 'err'], disabled: ['已停用', 'err'],
  };
  App.label = (s) => (LABEL[s] ? LABEL[s][0] : s);
  App.pill = (s, text) => `<span class="pill ${LABEL[s] ? LABEL[s][1] : ''}">${esc(text || App.label(s))}</span>`;
  App.LEDGER_TYPE = { purchase: '购买到账', grant: '赠送/发放', hold: '任务冻结', release: '任务退回', consume: '任务消耗', expire: '到期失效', revoke: '撤回', admin_adjust: '人工扣减' };
  App.ERR_LABEL = Object.fromEntries(Object.entries(window.GameBoost.ERROR_CODES).map(([k, v]) => [k, v.label]));

  App.user = () => App.E.user(App.ui.userId);
  App.gameName = (id) => {
    const g = App.E.s.games.find((x) => x.id === id);
    return g ? g.name : '';
  };
  App.serverName = (id) => {
    const s = App.E.s.servers.find((x) => x.id === id);
    return s ? s.name : '';
  };
  App.getPath = function (obj, path) {
    return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj);
  };
  App.setPath = function (obj, path, value) {
    const keys = path.split('.');
    let o = obj;
    for (let i = 0; i < keys.length - 1; i++) {
      if (o[keys[i]] == null || typeof o[keys[i]] !== 'object') o[keys[i]] = {};
      o = o[keys[i]];
    }
    o[keys[keys.length - 1]] = value;
  };
  App.bound = function (path) {
    const v = App.getPath(App.ui, path);
    return v == null ? '' : v;
  };
  // 渲染一个双向绑定的输入框
  App.input = function (path, attrs) {
    const a = attrs || {};
    return `<input data-bind="${path}" value="${esc(App.bound(path))}" ${a.type ? `type="${a.type}"` : ''} ${a.min != null ? `min="${a.min}"` : ''} ${a.max != null ? `max="${a.max}"` : ''} ${a.placeholder ? `placeholder="${esc(a.placeholder)}"` : ''} ${a.live ? `data-live="${a.live}"` : ''} ${a.rerender ? 'data-rerender="1"' : ''} ${a.cls ? `class="${a.cls}"` : ''} ${a.id ? `id="${a.id}"` : ''} aria-label="${esc(a.label || '')}" autocomplete="off">`;
  };
  App.select = function (path, options, attrs) {
    const a = attrs || {};
    const cur = String(App.bound(path));
    return `<select data-bind="${path}" ${a.num ? 'data-num="1"' : ''} data-rerender="1" ${a.cls ? `class="${a.cls}"` : ''} aria-label="${esc(a.label || '')}">${options
      .map(([v, t]) => `<option value="${esc(v)}" ${String(v) === cur ? 'selected' : ''}>${esc(t)}</option>`)
      .join('')}</select>`;
  };

  // ---------- 提示 ----------
  let toastTimer = null;
  App.toast = function (msg, kind) {
    let el = document.querySelector('.toast');
    if (!el) {
      el = document.createElement('div');
      el.setAttribute('role', 'status');
      document.body.appendChild(el);
    }
    el.className = 'toast ' + (kind || '');
    el.textContent = msg;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.remove(), 3600);
  };

  // ---------- 渲染 ----------
  App.render = function () {
    const root = document.getElementById('app');
    const content = root.querySelector('.content');
    const scroll = content ? content.scrollTop : 0;
    const focusId = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.bind : null;
    const ui = App.ui;
    const E = App.E;
    let body = '';
    try {
      body = App.views[ui.role]();
    } catch (err) {
      console.error(err);
      body = `<div class="panel"><h3>渲染出错</h3><pre>${esc(err.stack || err.message)}</pre></div>`;
    }
    root.innerHTML = `
      <header class="top">
        <div class="brand"><b>独角数卡 · 自动代练</b><span>本地原型</span></div>
        <nav class="roles" aria-label="视角">
          ${[['buyer', '用户端'], ['admin', '管理后台'], ['worker', '执行端模拟']]
            .map(([k, t]) => `<button data-act="role" data-v="${k}" aria-pressed="${ui.role === k}">${t}</button>`)
            .join('')}
        </nav>
        <div class="clock">
          <label class="small" style="display:flex;gap:6px;align-items:center;color:#c9d5e0;cursor:pointer">
            <input type="checkbox" data-act="toggleAuto" ${ui.auto ? 'checked' : ''}> 执行端自动运行
          </label>
          <time title="引擎虚拟时间（北京时间）">${App.t(E.now)}</time>
          <div class="ff" role="group" aria-label="快进时间">
            ${[[MIN, '+1分'], [5 * MIN, '+5分'], [16 * MIN, '+16分'], [HOUR, '+1时'], [DAY, '+1天']]
              .map(([ms, t]) => `<button data-act="advance" data-ms="${ms}">${t}</button>`)
              .join('')}
            <button class="reset" data-act="reset" title="清空所有操作，恢复演示数据">重置</button>
          </div>
        </div>
      </header>
      <div class="shell">
        <main class="content">${body}</main>
        <aside class="log" aria-label="系统事件">
          <h3>系统事件 <span class="muted small">${E.s.jobs.length} 个排队作业</span></h3>
          <ol>${E.s.log
            .slice(-140)
            .reverse()
            .map((l) => `<li class="k-${l.kind}"><time>${App.t(l.at).slice(5)}</time>${esc(l.message)}</li>`)
            .join('')}</ol>
        </aside>
      </div>
      ${ui.drawer ? App.views.drawer() : ''}
      ${ui.modal ? `<div class="overlay" data-act="closeModalBg"><div class="modal ${ui.modal.wide ? 'wide' : ''}" role="dialog" aria-modal="true">${App.views.modal()}</div></div>` : ''}
    `;
    // 顶栏在窄屏会换行，按实际高度设置内容区高度
    const top = root.querySelector('.top');
    if (top && top.offsetHeight && document.documentElement.style) document.documentElement.style.setProperty('--top-h', top.offsetHeight + 'px');
    const nc = root.querySelector('.content');
    if (nc) nc.scrollTop = scroll;
    if (focusId) {
      const f = root.querySelector(`[data-bind="${focusId}"]`);
      if (f) {
        f.focus();
        if (f.setSelectionRange && typeof f.value === 'string' && f.type !== 'number') f.setSelectionRange(f.value.length, f.value.length);
      }
    }
  };

  App.views.modal = function () {
    const m = App.ui.modal;
    const fn = App.views['modal_' + m.type];
    return fn ? fn(m) : '';
  };
  App.views.drawer = function () {
    const d = App.ui.drawer;
    return `<aside class="drawer" role="dialog" aria-label="详情"><button class="btn ghost close" data-act="closeDrawer" aria-label="关闭">关闭</button>${App.views['drawer_' + d.type](d)}</aside>`;
  };

  // ---------- 事件 ----------
  App.run = function (name, ds, el, evt) {
    const fn = App.actions[name];
    if (!fn) return;
    try {
      const res = fn(ds || {}, el, evt);
      if (res !== false) {
        App.save();
        App.render();
      }
    } catch (err) {
      if (!err.code) console.error(err);
      App.toast(err.message || String(err), 'err');
      App.save();
      App.render();
    }
  };
  document.addEventListener('click', (e) => {
    const el = e.target.closest('[data-act]');
    if (!el) return;
    if (el.type === 'checkbox' && el.dataset.act === 'toggleAuto') return; // 交给 change
    if (el.dataset.act === 'closeModalBg' && e.target !== el) return;
    e.preventDefault();
    App.run(el.dataset.act, el.dataset, el, e);
  });
  document.addEventListener('change', (e) => {
    const el = e.target;
    if (el.dataset.act === 'toggleAuto') return App.run('toggleAuto', {}, el);
    if (!el.dataset.bind) return;
    let v = el.type === 'checkbox' ? el.checked : el.value;
    if (el.dataset.num && v !== '') v = Number(v);
    App.setPath(App.ui, el.dataset.bind, v);
    if (el.dataset.rerender || el.tagName === 'SELECT' || el.type === 'checkbox' || el.type === 'number') App.render();
  });
  document.addEventListener('input', (e) => {
    const el = e.target;
    if (!el.dataset.bind || el.type === 'checkbox' || el.tagName === 'SELECT') return;
    App.setPath(App.ui, el.dataset.bind, el.value);
    if (el.dataset.live && App.live[el.dataset.live]) App.live[el.dataset.live]();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && (App.ui.modal || App.ui.drawer)) {
      App.ui.modal ? (App.ui.modal = null) : (App.ui.drawer = null);
      App.render();
    }
    if (e.key === 'Enter' && e.target.dataset && e.target.dataset.enter) {
      e.preventDefault();
      App.setPath(App.ui, e.target.dataset.bind, e.target.value);
      App.run(e.target.dataset.enter, e.target.dataset, e.target);
    }
  });

  // ---------- 通用动作 ----------
  Object.assign(App.actions, {
    role(ds) {
      App.ui.role = ds.v;
      App.ui.drawer = null;
    },
    tab(ds) {
      App.ui[ds.group] = ds.v;
      App.ui.drawer = null;
      const c = document.querySelector('.content');
      if (c) c.scrollTop = 0;
    },
    advance(ds) {
      App.E.advance(Number(ds.ms));
    },
    reset() {
      if (!confirm('重置会清空所有订单、任务和流水，恢复初始演示数据。继续？')) return false;
      App.E = window.GameSeed.seed();
      App.ui.modal = null;
      App.ui.drawer = null;
      App.ui.http = [];
      App.ui.checks = null;
      App.ui.drill = null;
      App.ui.forms = {};
      App.ui.flow = { gameId: null, bindingId: null, featureId: null };
      App.ui.admin.draft = null;
      App.ui.admin.draftFor = null;
      App.toast('已恢复初始演示数据', 'ok');
    },
    closeModal() {
      App.ui.modal = null;
    },
    closeModalBg() {
      App.ui.modal = null;
    },
    closeDrawer() {
      App.ui.drawer = null;
    },
    toggleAuto(ds, el) {
      App.ui.auto = el.checked;
      App.toast(App.ui.auto ? '执行端会每秒自动领取并推进任务（每步时间前进 30 秒）' : '已停止自动执行');
    },
  });

  // ---------- Worker 调用记录 ----------
  App.callWorker = function (workerId, method, path, body, opts) {
    const r = App.E.workerCall(workerId, method, path, body, opts);
    App.ui.http.unshift({ at: App.E.now, workerId, ...r });
    App.ui.http = App.ui.http.slice(0, 30);
    return r.response;
  };

  // ---------- 执行端自动运行（模拟真实 Worker 的行为） ----------
  App.autoStep = function () {
    const E = App.E;
    const mem = App.ui.lease;
    for (const w of E.s.workers) {
      if (w.status !== 'active') continue;
      const res = App.callWorker(w.id, 'POST', '/api/v1/worker/pull', { kinds: ['bind', 'task'], max: 2 });
      for (const it of (res.body && res.body.items) || []) mem[it.lease_token] = { input: it.input, kind: it.kind };
    }
    for (const l of E.s.leases.filter((x) => x.active && x.expiresAt > E.now)) {
      const L = `/api/v1/worker/leases/${l.token}`;
      const m = mem[l.token] || {};
      if (l.kind === 'bind') {
        const b = E.binding(l.targetId);
        // 首次登录要求短信验证码，用户提交后第二次领取直接成功
        if (m.input || b.attempt > 1) App.callWorker(l.workerId, 'POST', L + '/finish', { request_id: 'auto-' + l.token, session_ref: 'sess-' + l.token.slice(-6) });
        else App.callWorker(l.workerId, 'POST', L + '/need-input', { kind: 'sms_code', prompt: '请输入发送到绑定手机的 6 位验证码' });
        continue;
      }
      const t = E.task(l.targetId);
      if (t.cancelRequested) {
        App.callWorker(l.workerId, 'POST', L + '/cancel-ack', { request_id: 'auto-c-' + l.token, units_done: t.unitsDone });
        continue;
      }
      // 每 30 秒完成 1 个执行单位；按分钟计费的功能每 30 秒完成 1 分钟（演示加速）
      const units = Math.min(t.units, t.unitsDone + 1);
      if (units >= t.units) {
        App.callWorker(l.workerId, 'POST', L + '/finish', { request_id: 'auto-f-' + l.token, units_done: t.units, result: App.fakeResult(t) });
      } else {
        const step = App.stepAt(t, units);
        App.callWorker(l.workerId, 'POST', L + '/events', { seq: t.eventSeq + 1, type: 'log', message: `${step.label} 第 ${step.n}/${step.count} ${step.unitLabel || '次'}完成` });
        App.callWorker(l.workerId, 'POST', L + '/heartbeat', { units_done: units, progress_text: `${step.label} ${step.n}/${step.count}` });
      }
    }
    E.advance(30 * 1000);
  };
  // 第 units 个执行单位落在哪个步骤
  App.stepAt = function (t, units) {
    let left = units;
    for (const st of t.plan.steps) {
      if (left <= st.count) return { label: st.label, n: left, count: st.count, unitLabel: st.unitLabel };
      left -= st.count;
    }
    const last = t.plan.steps[t.plan.steps.length - 1];
    return { label: last.label, n: last.count, count: last.count, unitLabel: last.unitLabel };
  };
  App.fakeResult = function (t) {
    const drops = t.plan.steps.slice(0, 4).map((st) => `${st.label.replace(/（.*）/, '')} 奖励 ×${st.count * 3}`);
    return { rewards: drops.length ? drops : ['完成'] };
  };
  setInterval(() => {
    if (!App.ui.auto || App.ui.modal) return;
    try {
      App.autoStep();
      App.save();
      App.render();
    } catch (err) {
      console.error(err);
      App.ui.auto = false;
      App.toast('自动执行出错：' + err.message, 'err');
      App.render();
    }
  }, 1000);

  // 首屏
  document.addEventListener('DOMContentLoaded', () => App.render());
  if (document.readyState !== 'loading') setTimeout(() => App.render(), 0);
})();
