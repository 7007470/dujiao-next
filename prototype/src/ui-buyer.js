/* 用户端：代练中心（选游戏 → 选账号 → 选功能 → 自动生成的表单）、我的任务、我的游戏账号、积分 */
(function () {
  'use strict';
  const App = window.App;
  const { esc, yuan } = App;
  const { TERMINAL } = window.GameEngine;
  const S = window.FeatureSchema;

  App.formDefs = App.formDefs || {};

  App.views.buyer = function () {
    const ui = App.ui;
    const E = App.E;
    const u = App.user();
    const pa = E.pointAccount(u.id);
    const level = E.level(u.levelId);
    const live = E.s.tasks.filter((t) => t.userId === u.id && !TERMINAL.has(t.status)).length;
    const waiting = E.s.bindings.filter((b) => b.userId === u.id && ['WAIT_INPUT', 'WAIT_USER'].includes(b.status) && !b.deletedAt).length + E.s.tasks.filter((t) => t.userId === u.id && t.status === 'WAIT_INPUT').length;
    const tabs = [
      ['boost', '发起代练'],
      ['tasks', '我的任务' + (live ? ` (${live})` : '')],
      ['accounts', '我的游戏账号'],
      ['points', '积分'],
    ];
    return `
      <div class="who">
        <label class="row small">当前用户 ${App.select('userId', E.s.users.map((x) => [x.id, x.name]), { num: true, label: '切换用户' })}</label>
        <span class="stat"><small>等级</small>${esc(level ? level.name : '')}${level && level.discountRate < 100 ? ` <span class="pill ok">${level.discountRate / 10} 折</span>` : ''}</span>
        <span class="stat"><small>积分</small><span class="pts">${pa.available.toLocaleString()}</span>${pa.frozen ? `<small>冻结 ${pa.frozen}</small>` : ''}</span>
        <span class="stat"><small>钱包</small><span class="money">${yuan(E.walletBalance(u.id))}</span></span>
        ${waiting ? `<button class="pill warn" data-act="tab" data-group="buyerTab" data-v="${E.s.tasks.some((t) => t.userId === u.id && t.status === 'WAIT_INPUT') ? 'tasks' : 'accounts'}">${waiting} 项等待你输入验证码</button>` : ''}
      </div>
      <nav class="subnav">${tabs.map(([k, t]) => `<button data-act="tab" data-group="buyerTab" data-v="${k}" aria-pressed="${ui.buyerTab === k}">${esc(t)}</button>`).join('')}</nav>
      ${App.views['buyer_' + ui.buyerTab]()}`;
  };

  // ===================== 发起代练 =====================
  App.views.buyer_boost = function () {
    const E = App.E;
    const uid = App.ui.userId;
    const fl = App.ui.flow;
    const games = E.publicGames();
    if (!games.length) return '<div class="empty">暂无上线的游戏。管理员需要在后台添加游戏、区服和代练功能并上线。</div>';
    if (!games.some((g) => g.id === fl.gameId)) Object.assign(fl, { gameId: games[0].id, bindingId: null, featureId: null });
    const game = E.game(fl.gameId);
    const bindings = E.s.bindings.filter((b) => b.userId === uid && b.gameId === game.id && !b.deletedAt);
    const ok = bindings.filter((b) => b.status === 'SUCCESS');
    if (!ok.some((b) => b.id === fl.bindingId)) fl.bindingId = ok[0] ? ok[0].id : null;
    const features = E.publicFeatures(game.id);
    if (!features.some((f) => f.id === fl.featureId)) fl.featureId = features[0] ? features[0].id : null;
    const feature = features.find((f) => f.id === fl.featureId);

    return `
      <div class="game-strip" role="tablist" aria-label="游戏">${games
        .map((g) => `<button role="tab" aria-selected="${g.id === game.id}" data-act="flowGame" data-id="${g.id}" style="--gc:${esc(g.color)}"><span class="gdot"></span>${esc(g.name)}<small>${E.publicFeatures(g.id).length} 个功能</small></button>`)
        .join('')}</div>

      <div class="boost-layout">
        <aside class="feature-list" aria-label="代练功能">
          <h3>代练功能</h3>
          ${features
            .map((f) => {
              const d = f.definition;
              return `<button class="feat ${f.id === fl.featureId ? 'on' : ''}" data-act="flowFeature" data-id="${f.id}" aria-pressed="${f.id === fl.featureId}">
                <b>${esc(d.name)}</b><span>${esc(App.priceHint(d))}</span>
              </button>`;
            })
            .join('') || '<p class="muted small">这个游戏还没有上架的代练功能。</p>'}
        </aside>

        <section class="panel boost-main">
          ${feature ? App.views.featureOrder(game, feature, ok, bindings) : ''}
        </section>
      </div>`;
  };

  // 功能列表上的一句话价格提示，例如"2–20 点 / 关" "20 点起"
  App.priceHint = function (d) {
    const n = S.normalize(d);
    const costs = [];
    for (const f of n.fields) {
      if (f.type === 'stage_picker') {
        for (const g of n.catalogs[f.catalog].groups) for (const it of g.items) for (const c of typeof it.cost === 'number' ? [it.cost] : Object.values(it.cost)) costs.push(c);
        const per = f.count ? '次' : n.catalogs[f.catalog].label.slice(-1);
        if (costs.length) return `${Math.min(...costs)}–${Math.max(...costs)} 点 / ${per}`;
      }
      if (f.type === 'number' && f.points_per !== undefined) return `${f.points_per} 点 / ${f.unit || '单位'}`;
    }
    return n.base_points ? `${n.base_points} 点起` : '按选项计费';
  };

  App.views.featureOrder = function (game, feature, okBindings, allBindings) {
    const E = App.E;
    const uid = App.ui.userId;
    const fl = App.ui.flow;
    const d = feature.definition;
    const formId = `user-${feature.id}`;
    App.formDefs[formId] = () => E.feature(feature.id).definition;
    const rate = d.member_discount ? E.discountRateOf(uid) : 100;
    const { html, plan } = App.renderFeatureForm(formId, d, { rate });
    const pa = E.pointAccount(uid);
    const pendingBind = allBindings.filter((b) => !['SUCCESS', 'FAIL', 'CANCELLED'].includes(b.status));
    const canSubmit = fl.bindingId && (!plan.ok || plan.total <= pa.available);
    return `
      <div class="fo-head">
        <div><h2>${esc(d.name)}</h2><p class="muted">${esc(d.description)}</p></div>
      </div>

      <div class="fo-account">
        <span class="ff-label">使用账号</span>
        ${
          okBindings.length
            ? `<div class="acct-chips">${okBindings
                .map((b) => `<button class="chip ${b.id === fl.bindingId ? 'on' : ''}" data-act="flowBinding" data-id="${b.id}" aria-pressed="${b.id === fl.bindingId}">${esc(b.displayName)} <small>${esc(App.serverName(b.serverId))} · ${esc(b.loginMasked)}</small></button>`)
                .join('')}<button class="chip add" data-act="openBind" data-game="${game.id}">＋ 添加账号</button></div>`
            : `<div class="need">${pendingBind.length ? `你的${esc(game.name)}账号正在验证中：${esc(pendingBind[0].userMessage || '')}。<button class="btn sm" data-act="tab" data-group="buyerTab" data-v="accounts">去查看</button>` : `还没有可用的${esc(game.name)}账号。代练需要登录你自己的游戏账号。<button class="btn primary sm" data-act="openBind" data-game="${game.id}">添加游戏账号</button>`}</div>`
        }
      </div>

      <div class="fo-grid">
        <div class="fo-form">${html}</div>
        <aside class="fo-side">
          ${App.renderQuote(plan, pa.available)}
          <button class="btn primary block" data-act="submitTask" data-feature="${feature.id}" ${canSubmit ? '' : 'disabled'}>${plan.ok ? `冻结 ${plan.total} 积分并开始` : '开始代练'}</button>
          ${plan.ok && plan.total > pa.available ? `<button class="btn amber block" data-act="tab" data-group="buyerTab" data-v="points">购买积分</button>` : ''}
          <p class="muted small">${esc(App.refundPolicyText[d.refund_policy])}</p>
          <p class="muted small">积分先冻结，执行端按步骤完成后结算；单个任务最长 ${d.timeout_minutes} 分钟。</p>
        </aside>
      </div>`;
  };

  App.views.modal_bind = function (m) {
    const E = App.E;
    const b = App.ui.bind;
    return `<h2>添加游戏账号</h2>
      <p class="muted small">代练需要登录你的游戏账号。密码加密保存，只在执行端登录时解密一次；可以随时解绑，解绑后立即销毁。</p>
      <div class="cols c2" style="gap:8px">
        <label class="field"><span>游戏</span>${App.select('bind.gameId', E.publicGames().map((g) => [g.id, g.name]), { num: true, label: '游戏' })}</label>
        <label class="field"><span>区服</span>${App.select('bind.serverId', E.s.servers.filter((s) => s.gameId === Number(b.gameId) && s.status === 'active').map((s) => [s.id, s.name]), { num: true, label: '区服' })}</label>
      </div>
      <label class="field"><span>备注名</span>${App.input('bind.displayName', { placeholder: '例如：大号', label: '备注名' })}</label>
      <label class="field"><span>登录名（手机号或邮箱）</span>${App.input('bind.login', { placeholder: '13800000000', label: '登录名' })}</label>
      <label class="field"><span>密码</span>${App.input('bind.password', { type: 'password', label: '密码' })}</label>
      <p class="muted small">提交后执行端会登录验证，可能需要你输入短信验证码。验证不收积分。</p>
      <div class="foot"><button class="btn" data-act="closeModal">取消</button><button class="btn primary" data-act="createBinding">提交验证</button></div>`;
  };

  // ===================== 我的任务 =====================
  App.views.buyer_tasks = function () {
    const E = App.E;
    const tasks = E.s.tasks.filter((x) => x.userId === App.ui.userId).slice().reverse();
    if (!tasks.length) return '<div class="empty">还没有任务。到"发起代练"选一个功能开始。</div>';
    return `<section class="panel">${tasks.map((t) => App.views.taskRow(t)).join('')}</section>`;
  };

  App.views.taskRow = function (t) {
    const E = App.E;
    const b = E.s.bindings.find((x) => x.id === t.bindingId);
    const input = E.s.inputs.find((i) => i.target === 'task' && i.targetId === t.id && i.status === 'waiting');
    const open = App.ui.openTask === t.id;
    const prog = S.stepProgress(t.plan, t.unitsDone);
    const pct = Math.round((t.unitsDone / t.units) * 100);
    return `<article class="task ${t.status}">
      <div class="row between"><span><b>${esc(t.snapshot.name)}</b> <span class="muted small">${esc(App.gameName(t.gameId))} · ${esc(b ? b.displayName : '')} · ${esc(t.taskNo)}</span></span>${App.pill(t.status)}</div>
      <div class="small">${esc(t.summary)}</div>
      <div class="bar" role="progressbar" aria-valuenow="${pct}" aria-valuemin="0" aria-valuemax="100" aria-label="进度"><i style="width:${pct}%"></i></div>
      <div class="ledger">
        <span>完成 ${t.unitsDone}/${t.units}</span>
        <span>冻结 <b class="pts">${t.pointsHeld}</b></span>
        ${TERMINAL.has(t.status) ? `<span>消耗 <b class="pts">${t.pointsConsumed}</b></span><span>退回 <b class="pts" style="color:var(--jade)">${t.pointsReleased}</b></span>` : ''}
        <span class="muted">${esc(t.progressText || '')}${t.errorCode ? ` · ${esc(App.ERR_LABEL[t.errorCode] || t.errorCode)}` : ''}</span>
      </div>
      ${
        input
          ? `<div class="need"><span class="small">${esc(input.prompt || '请输入验证码')}（${App.left(input.expiresAt)} 后过期）</span>
             <div class="row">${App.input(`inputs.t${t.id}`, { placeholder: '验证码', cls: 'inline', label: '验证码' })}<button class="btn primary sm" data-act="submitInput" data-target="task" data-id="${t.id}">提交</button></div></div>`
          : ''
      }
      ${t.result && t.result.rewards ? `<span class="small">获得：${t.result.rewards.map(esc).join('、')}</span>` : ''}
      <div class="row between">
        <button class="btn ghost sm" data-act="toggleTask" data-id="${t.id}" aria-expanded="${open}">${open ? '收起' : '步骤与日志'}</button>
        ${!TERMINAL.has(t.status) ? `<button class="btn sm danger" data-act="cancelTask" data-no="${t.taskNo}" ${t.cancelRequested ? 'disabled' : ''}>${t.cancelRequested ? '等待执行端确认取消' : '取消'}</button>` : ''}
      </div>
      ${
        open
          ? `<div class="cols c2" style="gap:12px">
              <table class="steps"><thead><tr><th>#</th><th>步骤</th><th class="num">完成</th><th class="num">单价</th></tr></thead><tbody>
              ${prog.map((s) => `<tr class="${s.done >= s.count ? 'done' : s.done ? 'doing' : ''}"><td>${s.seq}</td><td>${esc(s.label)}</td><td class="num">${s.done}/${s.count}</td><td class="num">${s.unitPoints}</td></tr>`).join('')}
              ${t.plan.fixed.map((x) => `<tr><td></td><td class="muted">${esc(x.label)}</td><td></td><td class="num">${x.points}</td></tr>`).join('')}
              </tbody></table>
              <div class="events">${E.s.taskEvents.filter((e) => e.taskId === t.id).slice(-40).map((e) => `<div><span class="muted">#${e.seq}</span><span class="muted">${App.t(e.at).slice(11)}</span><span>${esc(e.message)}</span></div>`).join('')}</div>
            </div>`
          : ''
      }
    </article>`;
  };

  // ===================== 我的游戏账号 =====================
  App.views.buyer_accounts = function () {
    const E = App.E;
    const bindings = E.s.bindings.filter((b) => b.userId === App.ui.userId && !b.deletedAt);
    return `<section class="panel">
      <div class="panel-head"><h3>我的游戏账号</h3><button class="btn primary sm" data-act="openBind" data-game="${(E.publicGames()[0] || {}).id || ''}">添加游戏账号</button></div>
      ${bindings.map((b) => App.views.bindingCard(b)).join('') || '<p class="muted">还没有绑定游戏账号。</p>'}
    </section>`;
  };

  App.views.bindingCard = function (b) {
    const E = App.E;
    const input = E.s.inputs.find((i) => i.target === 'binding' && i.targetId === b.id && i.status === 'waiting');
    return `<div class="binding">
      <div class="row between"><b>${esc(b.displayName)}</b>${App.pill(b.status)}</div>
      <span class="small muted">${esc(App.gameName(b.gameId))} · ${esc(App.serverName(b.serverId))} · ${esc(b.loginMasked)}</span>
      ${['WAIT_INPUT', 'WAIT_USER'].includes(b.status) ? '' : `<span class="msg">${esc(b.userMessage || '')}</span>`}
      ${
        b.status === 'WAIT_INPUT' && input
          ? `<div class="need"><span class="small">${esc(input.prompt || '请输入验证码')}（${App.left(input.expiresAt)} 后过期）</span>
             <div class="row">${App.input(`inputs.b${b.id}`, { placeholder: '6 位验证码', cls: 'inline', label: '验证码' })}<button class="btn primary sm" data-act="submitInput" data-target="binding" data-id="${b.id}">提交</button></div></div>`
          : ''
      }
      ${b.status === 'WAIT_USER' ? `<div class="need"><span class="small">${esc(b.userMessage)}</span><button class="btn primary sm" data-act="confirmInGame" data-id="${b.id}">我已在游戏内确认</button></div>` : ''}
      <div class="row end"><button class="btn ghost sm" data-act="deleteBinding" data-id="${b.id}">解绑</button></div>
    </div>`;
  };

  // ===================== 积分 =====================
  App.views.buyer_points = function () {
    const E = App.E;
    const uid = App.ui.userId;
    const sum = E.pointSummary(uid);
    const soon = sum.expiringSoon.reduce((a, l) => a + l.amount, 0);
    const ledger = E.s.pointLedger.filter((r) => r.userId === uid).slice().reverse();
    const lots = E.s.pointLots.filter((l) => l.userId === uid).slice().reverse();
    const orders = E.s.orders.filter((o) => o.userId === uid).slice().reverse();
    return `
      <div class="balance">
        <div><small>可用积分</small><b class="pts">${sum.available.toLocaleString()}</b></div>
        <div><small>冻结中（任务进行中）</small><b class="pts" style="color:var(--violet)">${sum.frozen.toLocaleString()}</b></div>
        <div><small>30 天内到期</small><b class="pts" style="color:${soon ? 'var(--red)' : 'var(--ink-3)'}">${soon.toLocaleString()}</b></div>
      </div>
      <section class="panel">
        <div class="panel-head"><h3>购买积分包</h3><span class="muted small">积分只能用于代练，不能提现，也不能转回钱包</span></div>
        <div class="packs">${E.s.pointPacks
          .filter((p) => p.active)
          .map((p) => {
            const q = E.quotePack(uid, p.id);
            return `<div class="pack">
              <span class="big">${q.points.toLocaleString()} 点</span>
              <span class="bonus">${p.bonus ? `含赠送 ${p.bonus} 点` : ''}</span>
              <span class="row between"><span class="money">${yuan(q.total)}</span>${q.memberDiscount ? `<s class="muted small">${yuan(q.original)}</s>` : ''}</span>
              <span class="muted small">${p.validDays ? `有效期 ${p.validDays} 天` : '永久有效'}</span>
              <button class="btn amber" data-act="buyPoints" data-id="${p.id}">购买</button>
            </div>`;
          })
          .join('')}</div>
      </section>
      ${
        orders.some((o) => o.status === 'pending_payment')
          ? `<section class="panel"><h3>待支付</h3>${orders
              .filter((o) => o.status === 'pending_payment')
              .map((o) => `<div class="row between" style="padding:6px 0"><span><code>${esc(o.orderNo)}</code> ${esc(o.items[0].title)} · ${yuan(o.total - o.walletPaid)} · 剩余 ${App.left(o.expiresAt)}</span><span class="row"><button class="btn primary sm" data-act="openPay" data-no="${o.orderNo}">去支付</button><button class="btn sm" data-act="cancelOrder" data-no="${o.orderNo}">取消</button></span></div>`)
              .join('')}</section>`
          : ''
      }
      <div class="cols c2" style="margin-top:16px">
        <section class="panel">
          <h3>积分批次</h3>
          <p class="muted small" style="margin-top:-4px">消耗时先用最早到期的批次，任务退回的积分回到原批次。</p>
          <table><thead><tr><th>来源</th><th class="num">总量</th><th class="num">剩余</th><th>到期</th><th>状态</th></tr></thead><tbody>
          ${lots.map((l) => `<tr class="${l.status === 'active' ? '' : 'dim'}"><td>${{ order: '购买', grant: '发放', promo: '活动赠送' }[l.sourceType] || l.sourceType}</td><td class="num">${l.amount}</td><td class="num">${l.remaining}</td><td class="small">${l.expiresAt ? App.t(l.expiresAt).slice(0, 10) : '永久'}</td><td>${App.pill(l.status)}</td></tr>`).join('') || '<tr><td colspan="5" class="muted">暂无</td></tr>'}
          </tbody></table>
        </section>
        <section class="panel">
          <h3>积分流水</h3>
          <table><thead><tr><th>时间</th><th>类型</th><th class="num">可用</th><th class="num">冻结</th><th class="num">余额</th></tr></thead><tbody>
          ${ledger
            .slice(0, 40)
            .map(
              (r) => `<tr><td class="small muted">${App.t(r.at).slice(5)}</td><td>${App.LEDGER_TYPE[r.type] || r.type}${r.taskId ? ` <span class="muted small">${esc((E.s.tasks.find((t) => t.id === r.taskId) || {}).taskNo || '')}</span>` : ''}</td>
              <td class="num" style="color:${r.dAvailable > 0 ? 'var(--jade)' : r.dAvailable < 0 ? 'var(--red)' : 'var(--ink-3)'}">${r.dAvailable > 0 ? '+' : ''}${r.dAvailable || '·'}</td>
              <td class="num muted">${r.dFrozen > 0 ? '+' : ''}${r.dFrozen || '·'}</td><td class="num">${r.availableAfter}</td></tr>`
            )
            .join('') || '<tr><td colspan="5" class="muted">暂无</td></tr>'}
          </tbody></table>
        </section>
      </div>`;
  };

  App.views.modal_pay = function (m) {
    const E = App.E;
    const o = E.orderByNo(m.orderNo);
    const p = E.s.payments.find((x) => x.orderId === o.id && x.status === 'pending');
    return `<h2>模拟支付网关</h2>
      <p>订单 <code>${esc(o.orderNo)}</code>（${esc(o.items[0].title)}）需在线支付 <b class="money">${yuan(o.total - o.walletPaid)}</b>${o.walletPaid ? `，已用余额抵扣 ${yuan(o.walletPaid)}` : ''}。</p>
      <p class="muted small">真实环境这里跳转支付宝 / 微信 / USDT 网关，由独角数卡原生支付模块处理。按钮模拟网关回调，可以测试重复回调和少付。</p>
      ${p ? `<p class="small">支付单 <code>${esc(p.paymentNo)}</code> · 已收到回调 ${p.callbacks} 次</p>` : ''}
      <div class="foot" style="justify-content:space-between">
        <button class="btn" data-act="closeModal">关闭</button>
        <span class="row">
          <button class="btn sm" data-act="payCallback" data-no="${o.orderNo}" data-mode="under">少付 1 分</button>
          <button class="btn sm" data-act="payCallback" data-no="${o.orderNo}" data-mode="dup">回调 3 次</button>
          <button class="btn primary" data-act="payCallback" data-no="${o.orderNo}" data-mode="ok">支付成功</button>
        </span>
      </div>`;
  };

  // ===================== 动作 =====================
  Object.assign(App.actions, {
    flowGame(ds) {
      Object.assign(App.ui.flow, { gameId: Number(ds.id), bindingId: null, featureId: null });
    },
    flowFeature(ds) {
      App.ui.flow.featureId = Number(ds.id);
    },
    flowBinding(ds) {
      App.ui.flow.bindingId = Number(ds.id);
    },
    openBind(ds) {
      const gid = Number(ds.game) || (App.E.publicGames()[0] || {}).id;
      const sv = App.E.s.servers.find((s) => s.gameId === gid && s.status === 'active');
      Object.assign(App.ui.bind, { gameId: gid, serverId: sv ? sv.id : null, login: '', password: '', displayName: '' });
      App.ui.modal = { type: 'bind' };
    },
    createBinding() {
      const b = App.ui.bind;
      App.E.createBinding(App.ui.userId, { gameId: Number(b.gameId), serverId: Number(b.serverId), login: b.login, password: b.password, displayName: b.displayName, idempotencyKey: App.uid() });
      App.ui.modal = null;
      App.ui.buyerTab = 'accounts';
      App.toast(App.ui.auto ? '已提交，执行端正在登录验证' : '已提交。勾选顶部"执行端自动运行"，或到"执行端模拟"手动处理。');
    },
    deleteBinding(ds) {
      if (!confirm('解绑后凭据立即销毁。继续？')) return false;
      App.E.deleteBinding(App.ui.userId, Number(ds.id));
    },
    submitInput(ds) {
      const key = (ds.target === 'binding' ? 'b' : 't') + ds.id;
      App.E.submitInput(App.ui.userId, ds.target, Number(ds.id), App.ui.inputs[key]);
      App.ui.inputs[key] = '';
      App.toast('已提交，执行端下次领取时继续', 'ok');
    },
    confirmInGame(ds) {
      App.E.confirmInGame(App.ui.userId, Number(ds.id));
    },
    submitTask(ds) {
      const fid = Number(ds.feature);
      const formId = `user-${fid}`;
      const st = App.formState(formId);
      const def = App.E.feature(fid).definition;
      const plan = window.FeatureSchema.compile(def, st.values, { discountRate: def.member_discount ? App.E.discountRateOf(App.ui.userId) : 100 });
      if (!plan.ok) {
        st.submitted = true;
        App.toast(plan.errors[0], 'err');
        return;
      }
      const r = App.E.createTask(App.ui.userId, { bindingId: App.ui.flow.bindingId, featureId: fid, params: st.values, idempotencyKey: App.uid() });
      App.resetForm(formId);
      App.ui.buyerTab = 'tasks';
      App.ui.openTask = App.E.taskByNo(r.taskNo).id;
      App.toast(`任务 ${r.taskNo} 已创建，冻结 ${r.points} 积分`, 'ok');
    },
    cancelTask(ds) {
      const r = App.E.userCancelTask(App.ui.userId, ds.no);
      App.toast(r.cancelRequested ? '已请求取消，执行端确认后只扣已完成部分' : '已取消，积分全额退回');
    },
    toggleTask(ds) {
      App.ui.openTask = App.ui.openTask === Number(ds.id) ? null : Number(ds.id);
    },
    buyPoints(ds) {
      const r = App.E.createOrder(App.ui.userId, { packId: Number(ds.id), useBalance: true, idempotencyKey: App.uid() });
      if (r.status === 'pending_payment') {
        App.E.createPayment(r.orderNo);
        App.ui.modal = { type: 'pay', orderNo: r.orderNo };
      } else {
        App.E.runDueJobs();
        App.toast('余额支付成功，积分已到账', 'ok');
      }
    },
    openPay(ds) {
      App.E.createPayment(ds.no);
      App.ui.modal = { type: 'pay', orderNo: ds.no };
    },
    payCallback(ds) {
      const E = App.E;
      const p = E.createPayment(ds.no);
      if (ds.mode === 'under') E.paymentCallback(p.paymentNo, p.amount - 1);
      else for (let i = 0; i < (ds.mode === 'dup' ? 3 : 1); i++) E.paymentCallback(p.paymentNo, p.amount);
      E.runDueJobs();
      App.ui.modal = null;
      const o = E.orderByNo(ds.no);
      if (o.status === 'completed') App.toast('支付成功，积分已到账', 'ok');
      else if (o.exceptions.length) App.toast('支付回调异常，未到账，已提示管理员处理', 'err');
    },
    cancelOrder(ds) {
      App.E.cancelOrder(App.ui.userId, ds.no);
    },
  });
})();
