/* 执行端模拟 + 管理后台 */
(function () {
  'use strict';
  const App = window.App;
  const { esc, yuan } = App;
  const { TERMINAL, MIN, HOUR, DAY } = window.GameEngine;

  // ===================== 执行端模拟 =====================
  App.views.worker = function () {
    const E = App.E;
    const now = E.now;
    const leases = E.s.leases.filter((l) => l.active);
    const pendingTasks = E.s.tasks.filter((t) => t.status === 'PENDING').length;
    const pendingBinds = E.s.bindings.filter((b) => b.status === 'PENDING' && !b.deletedAt).length;
    return `
      <div class="note">这里扮演真实的游戏执行程序。每个按钮都会生成一个带 HMAC 签名的 HTTP 请求，交给服务端校验后处理，右侧能看到完整请求和响应。
        协议见设计文档 §7：领取（pull）→ 心跳 → 上报事件 → 完成 / 失败 / 需要输入 / 确认取消。</div>
      <div class="workers">${E.s.workers
        .map(
          (w) => `<div class="worker">
            <div class="row between"><b>${esc(w.name)}</b>${App.pill(w.status)}</div>
            <span class="caps">${w.capabilities.map((c) => `${esc(App.E.s.games.find((g) => g.code === c.game).short)}：${c.features.join(' / ')}`).join('<br>')}</span>
            <code>key ${esc(w.apiKey.slice(0, 14))}…</code>
            <span class="small muted">并发 ${w.maxConcurrency} · 领取 ${w.stats.pulled} · 成功 ${w.stats.success} · 失败 ${w.stats.fail} · 被拒 ${w.stats.rejected}${w.lastSeenAt ? ` · ${App.ago(w.lastSeenAt)}在线` : ''}</span>
            <div class="row">
              <button class="btn primary sm" data-act="wPull" data-id="${w.id}" ${w.status !== 'active' ? 'disabled' : ''}>领取</button>
              <button class="btn sm" data-act="wBadSign" data-id="${w.id}" title="用错误密钥签名">错签名</button>
              <button class="btn sm" data-act="wReplay" data-id="${w.id}" title="重放上一次请求">重放</button>
            </div>
          </div>`
        )
        .join('')}</div>
      <div class="cols c2" style="align-items:start">
        <section>
          <div class="panel-head"><h3>持有中的租约</h3><span class="muted small">排队：任务 ${pendingTasks} · 绑定 ${pendingBinds}</span></div>
          ${
            leases.length
              ? leases
                  .map((l) => {
                    const o = l.kind === 'task' ? E.task(l.targetId) : E.binding(l.targetId);
                    const w = E.worker(l.workerId);
                    const expired = l.expiresAt <= now;
                    const L = (App.ui.lease[l.token] = App.ui.lease[l.token] || {});
                    if (l.kind === 'task' && (L.units == null || L.units === '' || Number(L.units) < o.unitsDone)) L.units = o.unitsDone;
                    return `<div class="lease ${l.kind === 'task' && o.cancelRequested ? 'cancel' : ''}">
                      <div class="row between"><b>${l.kind === 'task' ? `任务 ${esc(o.taskNo)} · ${esc(o.snapshot.name)}` : `绑定 #${o.id} · ${esc(o.displayName)}`}</b>
                        <span class="pill ${expired ? 'err' : 'run'}">${expired ? '租约已过期' : '剩 ' + App.left(l.expiresAt)}</span></div>
                      <span class="small muted">${esc(w.name)} · 第 ${o.attempt} 次 · token <code>${esc(l.token.slice(0, 12))}…</code>${L.input ? ` · 收到用户输入 <span class="cred">${esc(L.input.value || '已确认')}</span>` : ''}</span>
                      ${l.kind === 'task' && o.cancelRequested ? '<span class="pill err">用户请求取消，请确认</span>' : ''}
                      ${
                        l.kind === 'task'
                          ? `<label class="row small">已完成 ${App.input(`lease.${l.token}.units`, { type: 'number', min: 0, max: o.units, cls: 'inline', label: '完成量' })} / ${o.units} 个执行单位</label><span class="small muted">${esc(o.summary)}</span>`
                          : ''
                      }
                      <div class="row">
                        <button class="btn sm" data-act="wHeartbeat" data-t="${l.token}">心跳</button>
                        ${l.kind === 'task' ? `<button class="btn sm" data-act="wEvent" data-t="${l.token}">上报日志</button>` : ''}
                        <button class="btn sm" data-act="wNeedInput" data-t="${l.token}" data-k="sms_code">要验证码</button>
                        ${l.kind === 'bind' ? `<button class="btn sm" data-act="wNeedInput" data-t="${l.token}" data-k="confirm">要游戏内确认</button>` : ''}
                        <button class="btn primary sm" data-act="wFinish" data-t="${l.token}">完成</button>
                        ${l.kind === 'task' && o.cancelRequested ? `<button class="btn danger sm" data-act="wCancelAck" data-t="${l.token}">确认取消</button>` : ''}
                      </div>
                      <div class="row small">失败：
                        ${['NETWORK', 'GAME_MAINTENANCE', 'PASSWORD_CHANGED', 'STAGE_LOCKED', 'INSUFFICIENT_STAMINA', 'USER_KICKED']
                          .map((c) => `<button class="btn sm" data-act="wFail" data-t="${l.token}" data-c="${c}" title="${c}">${esc(App.ERR_LABEL[c])}</button>`)
                          .join('')}
                      </div>
                    </div>`;
                  })
                  .join('')
              : '<div class="empty">没有持有中的租约。先点上面的"领取"。</div>'
          }
          <p class="muted small">租约 ${E.s.settings.leaseSeconds} 秒，心跳续期。把时间快进 +5 分，可以看到过期的租约被回收、任务重新排队；之后旧 token 再提交会返回 409 LEASE_LOST。</p>
        </section>
        <section>
          <div class="panel-head"><h3>HTTP 请求记录</h3><button class="btn ghost sm" data-act="clearHttp">清空</button></div>
          <div class="http">${
            App.ui.http
              .slice(0, 12)
              .map((h) => {
                const s = h.response.status;
                const body = JSON.stringify(h.request.body);
                return `<span class="k">${esc(h.request.method)} ${esc(h.request.path)}</span>
X-Worker-Key: ${esc(h.request.headers['X-Worker-Key'].slice(0, 16))}…
X-Worker-Timestamp: ${esc(h.request.headers['X-Worker-Timestamp'])}
X-Worker-Nonce: ${esc(h.request.headers['X-Worker-Nonce'].slice(0, 12))}…
X-Worker-Signature: ${esc(h.request.headers['X-Worker-Signature'].slice(0, 24))}…
${esc(body.length > 160 ? body.slice(0, 160) + '…' : body)}
<span class="${s < 300 ? 's2' : 's4'}">← ${s}</span> ${esc(App.redactHttp(h.response.body))}
`;
              })
              .join('\n') || '还没有请求。'
          }</div>
        </section>
      </div>`;
  };
  // 原型界面上把 pull 响应里的密码遮住一半，提醒这是敏感响应（真实系统日志中间件不记录该响应体）
  App.redactHttp = function (body) {
    const s = JSON.stringify(body, (k, v) => (k === 'password' && typeof v === 'string' ? v.slice(0, 2) + '••••' : v));
    return s.length > 420 ? s.slice(0, 420) + '…' : s;
  };

  App.lastSigned = null;
  Object.assign(App.actions, {
    wPull(ds) {
      const res = App.callWorker(Number(ds.id), 'POST', '/api/v1/worker/pull', { kinds: ['bind', 'task'], max: 1 });
      const items = (res.body && res.body.items) || [];
      for (const it of items) App.ui.lease[it.lease_token] = { input: it.input, units: it.units_done || 0 };
      if (!items.length && res.status === 200) App.toast('没有可领取的工作（可能能力不匹配、并发已满，或同一游戏账号已有任务在跑）');
      const w = App.E.worker(Number(ds.id));
      App.lastSigned = App.E.signRequest(w.apiKey, App.E.workerSecretForSimulator(w.id), 'POST', '/api/v1/worker/pull', { kinds: ['bind', 'task'], max: 1 });
    },
    wBadSign(ds) {
      App.callWorker(Number(ds.id), 'POST', '/api/v1/worker/pull', { max: 1 }, { secret: 'not-the-secret' });
    },
    wReplay(ds) {
      const E = App.E;
      const w = E.worker(Number(ds.id));
      const signed = App.lastSigned || E.signRequest(w.apiKey, E.workerSecretForSimulator(w.id), 'POST', '/api/v1/worker/pull', { max: 1 });
      E.workerHttp('POST', '/api/v1/worker/pull', signed.headers, signed.raw);
      const res = E.workerHttp('POST', '/api/v1/worker/pull', signed.headers, signed.raw);
      App.ui.http.unshift({ at: E.now, workerId: w.id, request: { method: 'POST', path: '/api/v1/worker/pull', headers: signed.headers, body: JSON.parse(signed.raw || '{}') }, response: res });
    },
    wHeartbeat(ds) {
      const l = App.leaseOf(ds.t);
      const units = App.unitsOf(ds.t);
      const res = App.callWorker(l.workerId, 'POST', `/api/v1/worker/leases/${ds.t}/heartbeat`, l.kind === 'task' ? { units_done: units } : {});
      if (res.body && res.body.cancel_requested) App.toast('心跳返回 cancel_requested=true，执行端应停止并确认取消');
    },
    wEvent(ds) {
      const l = App.leaseOf(ds.t);
      const t = App.E.task(l.targetId);
      App.callWorker(l.workerId, 'POST', `/api/v1/worker/leases/${ds.t}/events`, { seq: t.eventSeq + 1, type: 'log', message: (() => { const st = App.stepAt(t, Math.max(1, App.unitsOf(ds.t))); return `${st.label} 第 ${st.n}/${st.count} 次完成（登录密码=hunter2）`; })() });
    },
    wNeedInput(ds) {
      const l = App.leaseOf(ds.t);
      App.callWorker(l.workerId, 'POST', `/api/v1/worker/leases/${ds.t}/need-input`, { kind: ds.k, prompt: ds.k === 'confirm' ? '请在游戏客户端点击「允许此设备登录」' : '请输入发送到绑定手机的 6 位验证码' });
    },
    wFinish(ds) {
      const l = App.leaseOf(ds.t);
      const body = { request_id: 'ui-' + App.uid(), session_ref: 'sess-' + ds.t.slice(-6) };
      if (l.kind === 'task') {
        const t = App.E.task(l.targetId);
        const u = App.ui.lease[ds.t] && App.ui.lease[ds.t].units != null && App.ui.lease[ds.t].units !== '' ? App.unitsOf(ds.t) : t.units;
        body.units_done = u;
        body.result = App.fakeResult(t);
      }
      App.callWorker(l.workerId, 'POST', `/api/v1/worker/leases/${ds.t}/finish`, body);
    },
    wFail(ds) {
      const l = App.leaseOf(ds.t);
      App.callWorker(l.workerId, 'POST', `/api/v1/worker/leases/${ds.t}/fail`, { request_id: 'ui-' + App.uid(), error_code: ds.c, units_done: App.unitsOf(ds.t), message: App.ERR_LABEL[ds.c] });
    },
    wCancelAck(ds) {
      const l = App.leaseOf(ds.t);
      App.callWorker(l.workerId, 'POST', `/api/v1/worker/leases/${ds.t}/cancel-ack`, { request_id: 'ui-' + App.uid(), units_done: App.unitsOf(ds.t) });
    },
    clearHttp() {
      App.ui.http = [];
    },
  });
  App.leaseOf = function (token) {
    const l = App.E.s.leases.find((x) => x.token === token);
    if (!l) throw new Error('租约不存在');
    return l;
  };
  App.unitsOf = function (token) {
    const v = App.ui.lease[token] && App.ui.lease[token].units;
    return Number(v || 0);
  };


  // ===================== 管理后台 =====================
  App.views.admin = function () {
    const ui = App.ui;
    const E = App.E;
    const alerts = E.s.deadJobs.length + E.s.orders.filter((o) => o.exceptions.length).length;
    const tabs = [
      ['games', '游戏与代练功能'],
      ['tasks', '代练任务'],
      ['points', '积分与订单'],
      ['workers', '执行端'],
      ['audit', '审计'],
      ['checks', '一致性校验'],
    ];
    return `
      <div class="who">
        <label class="row small">管理员 ${App.select('adminId', E.s.admins.map((a) => [a.id, a.name]), { num: true, label: '管理员' })}</label>
        <span class="small muted">权限：${esc(E.s.admins.find((a) => a.id === ui.adminId).perms.join('、'))}</span>
        ${alerts ? `<span class="pill err">${alerts} 条需人工处理</span>` : ''}
      </div>
      <nav class="subnav">${tabs.map(([k, t]) => `<button data-act="tab" data-group="adminTab" data-v="${k}" aria-pressed="${ui.adminTab === k}">${esc(t)}</button>`).join('')}</nav>
      ${App.views['admin_' + ui.adminTab]()}`;
  };

  App.views.admin_tasks = function () {
    const E = App.E;
    const a = App.ui.admin;
    let tasks = E.s.tasks.slice().reverse();
    if (a.taskStatus) tasks = tasks.filter((t) => t.status === a.taskStatus);
    const binds = E.s.bindings.slice().reverse();
    const count = E.s.tasks.reduce((m, t) => ((m[t.status] = (m[t.status] || 0) + 1), m), {});
    return `
      <div class="row" style="margin-bottom:12px;gap:6px">${window.GameEngine.TASK_STATUSES.map((s) => `<span class="pill ${count[s] ? '' : 'dim'}">${App.label(s)} ${count[s] || 0}</span>`).join('')}</div>
      <section class="panel">
        <div class="panel-head"><h3>代练任务</h3>${App.select('admin.taskStatus', [['', '全部状态']].concat(window.GameEngine.TASK_STATUSES.map((s) => [s, App.label(s)])), { cls: 'inline', label: '状态' })}</div>
        <table><thead><tr><th>任务</th><th>用户</th><th>功能 / 内容</th><th class="num">进度</th><th class="num">冻结</th><th class="num">消耗/退回</th><th>状态</th><th>执行端</th><th></th></tr></thead><tbody>
        ${tasks
          .slice(0, 50)
          .map(
            (t) => `<tr><td><code>${esc(t.taskNo)}</code><br><span class="muted small">${App.t(t.createdAt).slice(5)} · 第 ${t.attempt}/${t.maxAttempts} 次</span></td>
            <td class="small">${esc(E.user(t.userId).name)}</td>
            <td class="small"><b>${esc(App.gameName(t.gameId))} · ${esc(t.snapshot.name)}</b> <span class="muted">v${t.snapshot.version}</span><br><span class="muted">${esc(t.summary)}</span></td>
            <td class="num">${t.unitsDone}/${t.units}</td><td class="num">${t.pointsHeld}</td>
            <td class="num">${TERMINAL.has(t.status) ? `${t.pointsConsumed} / ${t.pointsReleased}` : '—'}</td>
            <td>${App.pill(t.status)}${t.errorCode ? `<br><span class="small muted">${esc(t.errorCode)}</span>` : ''}${t.cancelRequested && !TERMINAL.has(t.status) ? '<br><span class="pill err">待确认取消</span>' : ''}</td>
            <td class="small muted">${t.workerId ? esc(E.worker(t.workerId).name) : ''}${t.leaseExpiresAt ? '<br>租约剩 ' + App.left(t.leaseExpiresAt) : ''}</td>
            <td class="row">${!TERMINAL.has(t.status) ? `<button class="btn sm danger" data-act="adminCancelTask" data-no="${t.taskNo}">取消</button>` : ''}${t.status === 'FAIL' ? `<button class="btn sm" data-act="adminRetry" data-no="${t.taskNo}">重试</button>` : ''}</td></tr>`
          )
          .join('') || '<tr><td colspan="9" class="muted">暂无任务</td></tr>'}
        </tbody></table>
      </section>
      <section class="panel"><h3>游戏账号绑定</h3>
        <table><thead><tr><th>#</th><th>用户</th><th>游戏 / 区服</th><th>登录名</th><th>状态</th><th>说明</th><th>验证时间</th></tr></thead><tbody>
        ${binds.map((b) => `<tr class="${b.deletedAt ? 'dim' : ''}"><td>${b.id}</td><td class="small">${esc(E.user(b.userId).name)}</td><td class="small">${esc(App.gameName(b.gameId))} · ${esc(App.serverName(b.serverId))}</td><td><code>${esc(b.loginMasked)}</code></td><td>${App.pill(b.status)}${b.deletedAt ? ' <span class="pill">已解绑</span>' : ''}</td><td class="small muted">${esc(b.userMessage || '')}${b.lastError ? ' · ' + esc(b.lastError) : ''}</td><td class="small">${b.verifiedAt ? App.t(b.verifiedAt).slice(5) : ''}</td></tr>`).join('') || '<tr><td colspan="7" class="muted">暂无</td></tr>'}
        </tbody></table>
        <p class="muted small">后台只显示脱敏后的登录名；密码只在执行端领取时解密下发。</p>
      </section>`;
  };

  App.views.admin_points = function () {
    const E = App.E;
    const g = App.ui.admin.grant;
    const p = App.ui.admin.pack;
    const ledger = E.s.pointLedger.slice().reverse().slice(0, 40);
    const orders = E.s.orders.slice().reverse();
    return `<div class="cols c2" style="align-items:start">
      <section class="panel"><h3>积分包</h3>
        <table><thead><tr><th>名称</th><th class="num">价格</th><th class="num">积分</th><th class="num">赠送</th><th>有效期</th><th></th></tr></thead><tbody>
        ${E.s.pointPacks.map((x) => `<tr class="${x.active ? '' : 'dim'}"><td>${esc(x.name)}</td><td class="num">${yuan(x.price)}</td><td class="num">${x.points}</td><td class="num">${x.bonus}</td><td class="small">${x.validDays ? x.validDays + ' 天' : '永久'}</td><td><button class="btn sm" data-act="togglePack" data-id="${x.id}">${x.active ? '下架' : '上架'}</button></td></tr>`).join('')}
        <tr><td>${App.input('admin.pack.name', { placeholder: '名称', cls: 'inline', label: '名称' })}</td><td>${App.input('admin.pack.price', { type: 'number', placeholder: '元', cls: 'inline', label: '价格' }).replace('<input', '<input style="width:70px"')}</td><td>${App.input('admin.pack.points', { type: 'number', cls: 'inline', label: '积分' }).replace('<input', '<input style="width:70px"')}</td><td>${App.input('admin.pack.bonus', { type: 'number', cls: 'inline', label: '赠送' }).replace('<input', '<input style="width:60px"')}</td><td>${App.input('admin.pack.validDays', { type: 'number', placeholder: '天', cls: 'inline', label: '有效天数' }).replace('<input', '<input style="width:60px"')}</td><td><button class="btn sm primary" data-act="addPack">添加</button></td></tr>
        </tbody></table>
        <h3 style="margin-top:16px">用户积分</h3>
        <table><thead><tr><th>用户</th><th class="num">可用</th><th class="num">冻结</th></tr></thead><tbody>
        ${E.s.users.map((u) => { const a = E.pointAccount(u.id); return `<tr><td>${esc(u.name)}</td><td class="num">${a.available}</td><td class="num">${a.frozen}</td></tr>`; }).join('')}
        </tbody></table>
        <div class="cols c2" style="gap:8px;margin-top:10px">
          <label class="field"><span>用户</span>${App.select('admin.grant.userId', E.s.users.map((u) => [u.id, u.name]), { num: true, label: '用户' })}</label>
          <label class="field"><span>积分</span>${App.input('admin.grant.amount', { type: 'number', min: 1, label: '积分' })}</label>
          <label class="field"><span>有效天数（0 = 永久）</span>${App.input('admin.grant.validDays', { type: 'number', min: 0, label: '有效天数' })}</label>
          <label class="field"><span>原因（必填）</span>${App.input('admin.grant.reason', { placeholder: '活动补偿', label: '原因' })}</label>
        </div>
        <div class="row"><button class="btn amber" data-act="grantPts">发放</button><button class="btn danger" data-act="deductPts">扣减</button></div>
      </section>
      <section class="panel"><h3>积分包订单</h3>
        <table><thead><tr><th>订单</th><th>用户</th><th class="num">金额</th><th>状态</th><th></th></tr></thead><tbody>
        ${orders.slice(0, 20).map((o) => {
          const it = o.items[0];
          const lot = E.s.pointLots.find((l) => l.sourceRef === 'order_item:' + it.id);
          const can = ['completed', 'partially_refunded'].includes(o.status);
          return `<tr><td><code>${esc(o.orderNo)}</code><br><span class="small muted">${esc(it.title)}</span></td><td class="small">${esc(E.user(o.userId).name)}</td><td class="num">${yuan(o.total)}${o.refunded ? `<br><span class="small" style="color:var(--red)">−${yuan(o.refunded)}</span>` : ''}</td><td>${App.pill(o.status)}${o.exceptions.length ? '<br><span class="pill err">支付异常</span>' : ''}</td>
          <td>${can && lot && lot.status !== 'revoked' ? `<button class="btn sm" data-act="revokePts" data-no="${o.orderNo}">撤回积分</button>` : ''}${can && lot && lot.status === 'revoked' ? `<button class="btn sm danger" data-act="refund" data-no="${o.orderNo}" data-amt="${lot.suggestRefund}">退款 ${yuan(lot.suggestRefund)}</button>` : ''}</td></tr>`;
        }).join('') || '<tr><td colspan="5" class="muted">暂无</td></tr>'}
        </tbody></table>
        <p class="muted small">退款前必须先撤回剩余积分，系统按未使用比例给出建议退款金额。</p>
        <h3 style="margin-top:16px">积分流水</h3>
        <table><thead><tr><th>时间</th><th>用户</th><th>类型</th><th class="num">可用</th><th class="num">冻结</th></tr></thead><tbody>
        ${ledger.map((r) => `<tr><td class="small muted">${App.t(r.at).slice(5)}</td><td class="small">${esc(E.user(r.userId).name)}</td><td class="small">${App.LEDGER_TYPE[r.type] || r.type}</td><td class="num">${r.dAvailable || '·'}</td><td class="num muted">${r.dFrozen || '·'}</td></tr>`).join('')}
        </tbody></table>
      </section>
    </div>`;
  };

  App.views.admin_workers = function () {
    const E = App.E;
    const caps = new Set();
    for (const f of E.s.features) if (f.status === 'active') caps.add(E.game(f.gameId).code + ':' + f.definition.capability);
    const covered = new Set();
    for (const w of E.s.workers) if (w.status === 'active') for (const c of w.capabilities) for (const x of c.features) covered.add(c.game + ':' + x);
    const missing = [...caps].filter((c) => !covered.has(c));
    return `<section class="panel"><h3>执行端节点</h3>
      ${missing.length ? `<p class="note" style="background:var(--red-soft);color:var(--red)">以下已上架功能没有任何在线执行端能处理，用户下单后会一直排队：${missing.map(esc).join('、')}</p>` : ''}
      <table><thead><tr><th>名称</th><th>API Key</th><th>能力</th><th class="num">并发</th><th>最后在线</th><th>状态</th><th></th></tr></thead><tbody>
      ${E.s.workers.map((w) => `<tr><td>${esc(w.name)}</td><td><code class="small">${esc(w.apiKey)}</code></td><td class="small">${w.capabilities.map((c) => esc(c.game) + '：' + esc(c.features.join(', '))).join('<br>')}</td><td class="num">${w.maxConcurrency}</td><td class="small">${w.lastSeenAt ? App.ago(w.lastSeenAt) : '—'}</td><td>${App.pill(w.status)}</td>
        <td class="row"><button class="btn sm" data-act="rotateWorker" data-id="${w.id}">轮换密钥</button><button class="btn sm" data-act="toggleWorker" data-id="${w.id}">${w.status === 'active' ? '停用' : '启用'}</button></td></tr>`).join('')}
      </tbody></table>
      <p class="muted small">执行端按"游戏 code + 功能 capability"领取任务。新增功能后，需要执行端程序支持并声明对应能力。</p>
    </section>`;
  };

  App.views.admin_audit = function () {
    const E = App.E;
    return `<section class="panel"><h3>管理操作审计</h3>
      <table><thead><tr><th>时间</th><th>操作人</th><th>动作</th><th>对象</th><th>说明</th></tr></thead><tbody>
      ${E.s.audit.slice().reverse().slice(0, 80).map((a) => `<tr><td class="small muted">${App.t(a.at).slice(5)}</td><td class="small">${esc(a.actor)}</td><td class="small"><code>${esc(a.action)}</code></td><td class="small">${esc(a.target)}</td><td class="small">${esc(a.detail)}</td></tr>`).join('') || '<tr><td colspan="5" class="muted">暂无</td></tr>'}
      </tbody></table></section>`;
  };

  App.views.admin_checks = function () {
    const results = App.ui.checks || App.E.checkInvariants();
    const bad = results.filter((r) => !r.ok).length;
    return `<section class="panel">
      <div class="panel-head"><h3>一致性校验</h3><span class="row">${bad ? `<span class="pill err">${bad} 项失败</span>` : '<span class="pill ok">全部通过</span>'}<button class="btn sm" data-act="runChecks">重新检查</button><button class="btn sm amber" data-act="runDrill">运行压力演练</button></span></div>
      <p class="muted small">这些业务规则在任何操作之后都必须成立。命令行里的自动化测试会在每个场景结束时检查同样的规则。</p>
      <ul class="checks">${results.map((r) => `<li class="${r.ok ? '' : 'bad'}"><b>${r.ok ? '✓' : '✗'}</b><span>${esc(r.name)}${r.detail ? `<br><span class="small">${esc(r.detail)}</span>` : ''}</span></li>`).join('')}</ul>
      ${App.ui.drill ? `<div class="note" style="margin-top:14px">${App.ui.drill}</div>` : ''}
    </section>`;
  };

  Object.assign(App.actions, {
    revokePts(ds) {
      const r = App.E.revokePointsForOrder(App.ui.adminId, ds.no);
      App.toast(`已撤回 ${r.revoked} 点（已用 ${r.used} 点），建议退款 ${yuan(r.suggestRefund)}`);
    },
    refund(ds) {
      const amt = Number(ds.amt);
      if (!confirm(`退款 ${yuan(amt)} 到用户钱包？`)) return false;
      App.E.adminRefund(App.ui.adminId, ds.no, amt, App.uid(), '后台退款');
    },
    grantPts() {
      const g = App.ui.admin.grant;
      App.E.adminGrantPoints(App.ui.adminId, Number(g.userId), Math.trunc(Number(g.amount)), Number(g.validDays) || null, g.reason, App.uid());
      App.toast('已发放', 'ok');
    },
    deductPts() {
      const g = App.ui.admin.grant;
      App.E.adminDeductPoints(App.ui.adminId, Number(g.userId), Math.trunc(Number(g.amount)), g.reason, App.uid());
    },
    addPack() {
      App.E.adminSavePack(App.ui.adminId, App.ui.admin.pack);
      App.ui.admin.pack = {};
    },
    togglePack(ds) {
      const p = App.E.pack(Number(ds.id));
      p.active = !p.active;
      App.E.audit('admin:' + App.ui.adminId, 'pack.status', 'pack:' + p.id, p.active ? 'active' : 'disabled');
    },
    adminCancelTask(ds) {
      App.E.userCancelTask(null, ds.no);
    },
    adminRetry(ds) {
      const r = App.E.adminRetryTask(App.ui.adminId, ds.no);
      App.toast('已创建新任务 ' + r.taskNo, 'ok');
    },
    rotateWorker(ds) {
      const r = App.E.rotateWorkerSecret(App.ui.adminId, Number(ds.id));
      alert('新密钥只显示这一次，请复制到执行端配置：\n\n' + r.secret);
    },
    toggleWorker(ds) {
      const w = App.E.worker(Number(ds.id));
      w.status = w.status === 'active' ? 'disabled' : 'active';
      App.E.audit('admin:' + App.ui.adminId, 'worker.status', 'worker:' + w.id, w.status);
    },
    runChecks() {
      App.ui.checks = App.E.checkInvariants();
    },
    runDrill() {
      // 在当前数据的副本上跑，不影响正在看的演示状态
      const E = window.GameEngine.Engine.restore(App.E.serialize());
      let x = 99;
      const r = () => ((x = (x * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
      const pick = (a) => a[Math.floor(r() * a.length)];
      let ok = 0;
      let rejected = 0;
      const t0 = performance.now();
      const users = E.s.users.map((u) => u.id);
      for (const u of users) E.grantPoints(u, 5000, 'grant', 'drill:' + u + ':' + E.now, null, '演练');
      const binds = [];
      for (const u of users) for (const g of E.publicGames()) {
        const b = E.createBinding(u, { gameId: g.id, serverId: E.s.servers.find((s) => s.gameId === g.id && s.status === 'active').id, login: `drill${u}${g.id}${E.now}@x.test`, password: 'DrillPass' + u });
        E.binding(b.id).status = 'SUCCESS';
        binds.push(E.binding(b.id));
      }
      for (let i = 0; i < 400; i++) {
        try {
          const k = Math.floor(r() * 5);
          if (k <= 1) {
            const b = pick(binds);
            const f = pick(E.publicFeatures(b.gameId));
            const params = App.randomParams(f.definition, r);
            E.createTask(b.userId, { bindingId: b.id, featureId: f.id, params });
          } else if (k === 2) {
            for (const w of E.s.workers) {
              const res = E.workerCall(w.id, 'POST', '/api/v1/worker/pull', { kinds: ['task'], max: 2 }).response.body.items || [];
              for (const it of res) {
                const u = Math.floor(r() * (it.units + 2));
                const path = `/api/v1/worker/leases/${it.lease_token}/`;
                if (r() < 0.6) E.workerCall(w.id, 'POST', path + 'finish', { request_id: 'd' + i + it.id, units_done: u });
                else E.workerCall(w.id, 'POST', path + 'fail', { request_id: 'e' + i + it.id, error_code: pick(['NETWORK', 'STAGE_LOCKED', 'INSUFFICIENT_STAMINA']), units_done: u });
              }
            }
          } else if (k === 3) E.advance(Math.floor(r() * 4) * MIN);
          else {
            const live = E.s.tasks.filter((t) => !TERMINAL.has(t.status));
            if (live.length) E.userCancelTask(null, pick(live).taskNo);
          }
          ok++;
        } catch (err) {
          if (!err.code) throw err;
          rejected++;
        }
      }
      E.advance(6 * HOUR);
      const bad = E.checkInvariants().filter((q) => !q.ok);
      App.ui.drill = `压力演练（在当前数据的副本上）：400 步随机生成功能参数下单 / 执行端领取 / 部分完成 / 失败 / 取消 / 超时，用时 ${Math.round(performance.now() - t0)} ms。成功 ${ok} 步，被业务规则拒绝 ${rejected} 步，共 ${E.s.tasks.length} 个任务。不变量：${bad.length ? '<b style="color:var(--red)">' + bad.length + ' 项失败：' + bad.map((b) => esc(b.name)).join('、') + '</b>' : '全部通过'}。`;
    },
  });

  // 按功能定义随机生成一组参数（压力演练用；不保证合法，非法的会被服务端拒绝）
  App.randomParams = function (def, r) {
    const out = {};
    const pick = (a) => a[Math.floor(r() * a.length)];
    for (const f of def.fields) {
      if (f.type === 'radio' || f.type === 'select') out[f.key] = pick(f.options).value;
      else if (f.type === 'checkbox') out[f.key] = f.options.filter(() => r() < 0.5).map((o) => o.value);
      else if (f.type === 'switch') out[f.key] = r() < 0.5;
      else if (f.type === 'number') out[f.key] = f.min + Math.floor(r() * (Math.min(f.max, f.min + 60) - f.min + 1));
      else if (f.type === 'stage_picker') {
        const items = def.catalogs[f.catalog].groups.flatMap((g) => g.items);
        const n = f.multiple ? 1 + Math.floor(r() * 3) : 1;
        out[f.key] = Array.from({ length: n }, () => ({ item: pick(items).code, count: f.count ? f.count.min + Math.floor(r() * 5) : 1 }));
      }
    }
    return out;
  };
})();
