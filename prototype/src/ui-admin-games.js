/* 管理后台：游戏 / 区服 / 代练功能（JSON 编辑器 + 结构校验 + 用户端实时预览） */
(function () {
  'use strict';
  const App = window.App;
  const S = window.FeatureSchema;
  const { esc } = App;

  // 新功能的起始模板：几种常见形态，管理员选一个改
  App.FEATURE_TEMPLATES = {
    stages: {
      label: '关卡（通关 / 扫荡）',
      def: {
        schema_version: 1,
        code: 'new_stage',
        name: '新关卡功能',
        description: '选择关卡，通关或扫荡。',
        capability: 'new_stage',
        refund_policy: 'prorate',
        catalogs: {
          stages: {
            label: '关卡',
            groups: [
              { code: 'ch1', name: '第一章', items: [{ code: '1-1', name: '1-1', cost: { clear: 5, sweep: 2 } }, { code: '1-2', name: '1-2', cost: { clear: 5, sweep: 2 } }, { code: '1-3', name: '1-3（BOSS）', cost: { clear: 10, sweep: 4 }, tags: ['BOSS'] }] },
              { code: 'ch2', name: '第二章', items: [{ code: '2-1', name: '2-1', cost: { clear: 7, sweep: 3 } }, { code: '2-2', name: '2-2（剧情）', cost: { clear: 8 }, tags: ['不可扫荡'] }] },
            ],
          },
        },
        fields: [
          { key: 'mode', type: 'radio', label: '模式', required: true, default: 'sweep', options: [{ value: 'clear', label: '通关' }, { value: 'sweep', label: '扫荡' }] },
          { key: 'stages', type: 'stage_picker', label: '选择关卡', catalog: 'stages', multiple: true, required: true, cost_by: 'mode', count: { label: '扫荡次数', min: 1, max: 99, default: 10, visible_when: { mode: 'sweep' } } },
        ],
      },
    },
    fixed: {
      label: '固定价 + 附加项（日常）',
      def: {
        schema_version: 1,
        code: 'new_daily',
        name: '新日常功能',
        description: '每次固定价格，可附加项目。',
        capability: 'new_daily',
        refund_policy: 'full_on_system_fail',
        base_points: 20,
        fields: [{ key: 'extras', type: 'checkbox', label: '附加项目', options: [{ value: 'a', label: '附加 A', points: 5 }, { value: 'b', label: '附加 B', points: 3 }] }],
      },
    },
    duration: {
      label: '按时长计费（挂机）',
      def: {
        schema_version: 1,
        code: 'new_idle',
        name: '新挂机功能',
        description: '按运行分钟计费。',
        capability: 'new_idle',
        refund_policy: 'prorate',
        member_discount: false,
        fields: [
          { key: 'area', type: 'select', label: '区域', required: true, options: [{ value: 'a', label: '区域 A' }, { value: 'b', label: '区域 B' }] },
          { key: 'minutes', type: 'number', label: '运行时长', unit: '分钟', min: 30, max: 480, default: 60, points_per: 1 },
        ],
      },
    },
  };

  App.gamePill = (st) => App.pill(st === 'active' ? 'active' : st === 'hidden' ? 'disabled' : 'draft', { active: '已上线', hidden: '已隐藏', draft: '草稿' }[st] || st);

  // ===================== 游戏与功能 =====================
  App.views.admin_games = function () {
    const E = App.E;
    const a = App.ui.admin;
    const games = E.s.games.slice().sort((x, y) => x.sort - y.sort || x.id - y.id);
    if (!a.gameId && games.length) a.gameId = games[0].id;
    const g = games.find((x) => x.id === a.gameId);
    return `<div class="admin-games">
      <aside class="panel ag-list">
        <div class="panel-head"><h3>游戏</h3><button class="btn sm" data-act="newGame">新建</button></div>
        ${games
          .map((x) => `<button class="ag-item ${x.id === a.gameId ? 'on' : ''}" data-act="pickGame" data-id="${x.id}"><span class="gdot" style="background:${esc(x.color)}"></span><span><b>${esc(x.name)}</b><small>${esc(x.code)} · ${E.s.features.filter((f) => f.gameId === x.id).length} 个功能</small></span>${App.gamePill(x.status)}</button>`)
          .join('') || '<p class="muted small">还没有游戏</p>'}
      </aside>
      <div>${g ? App.views.gameDetail(g) : '<div class="empty">新建一个游戏开始。</div>'}</div>
    </div>`;
  };

  App.views.gameDetail = function (g) {
    const E = App.E;
    const a = App.ui.admin;
    const servers = E.s.servers.filter((s) => s.gameId === g.id);
    const features = E.s.features.filter((f) => f.gameId === g.id).sort((x, y) => x.sort - y.sort);
    if (a.featureId && !features.some((f) => f.id === a.featureId) && a.featureId !== 'new') a.featureId = null;
    const canLive = servers.some((s) => s.status === 'active') && features.some((f) => f.status === 'active');
    return `
      <section class="panel">
        <div class="panel-head">
          <h3>${esc(g.name)} <span class="muted small" style="font-family:var(--sans)">${esc(g.code)} · v${g.version}</span></h3>
          <span class="row">${App.gamePill(g.status)}
            ${g.status !== 'active' ? `<button class="btn primary sm" data-act="gameStatus" data-id="${g.id}" data-v="active" ${canLive ? '' : 'disabled title="需要至少一个启用的区服和一个已上架的功能"'}>上线</button>` : `<button class="btn sm" data-act="gameStatus" data-id="${g.id}" data-v="hidden">隐藏</button>`}
            <button class="btn sm" data-act="editGame" data-id="${g.id}">编辑</button></span>
        </div>
        ${!canLive && g.status !== 'active' ? '<p class="note">上线前需要：至少一个启用的区服，以及至少一个已上架的代练功能。</p>' : ''}
        <h4 class="sub">区服</h4>
        <table><thead><tr><th>名称</th><th>code</th><th>平台</th><th>状态</th><th></th></tr></thead><tbody>
          ${servers.map((s) => `<tr class="${s.status === 'active' ? '' : 'dim'}"><td>${esc(s.name)}</td><td><code>${esc(s.code)}</code></td><td class="small">${esc(s.platform)}</td><td>${App.pill(s.status === 'active' ? 'active' : 'disabled', s.status === 'active' ? '启用' : '停用')}</td><td><button class="btn sm" data-act="serverStatus" data-id="${s.id}" data-v="${s.status === 'active' ? 'disabled' : 'active'}">${s.status === 'active' ? '停用' : '启用'}</button></td></tr>`).join('')}
          <tr><td>${App.input('admin.newServer.name', { placeholder: '名称，如：国服 · 一区', cls: 'inline', label: '区服名称' })}</td><td>${App.input('admin.newServer.code', { placeholder: 'cn-1', cls: 'inline', label: '区服 code' })}</td>
            <td>${App.select('admin.newServer.platform', [['all', '全平台'], ['android', '安卓'], ['ios', 'iOS'], ['pc', 'PC']], { cls: 'inline', label: '平台' })}</td><td></td><td><button class="btn sm primary" data-act="addServer" data-id="${g.id}">添加</button></td></tr>
        </tbody></table>
      </section>

      <section class="panel">
        <div class="panel-head"><h3>代练功能</h3>
          <span class="row">新建：${Object.entries(App.FEATURE_TEMPLATES).map(([k, t]) => `<button class="btn sm" data-act="newFeature" data-tpl="${k}">${esc(t.label)}</button>`).join('')}</span></div>
        <table><thead><tr><th></th><th>功能</th><th>价格</th><th>字段</th><th>版本</th><th>状态</th><th></th></tr></thead><tbody>
          ${features
            .map(
              (f, i) => `<tr class="${a.featureId === f.id ? 'sel' : ''}">
              <td class="nowrap"><button class="btn ghost sm" data-act="moveFeature" data-id="${f.id}" data-d="-1" ${i === 0 ? 'disabled' : ''} aria-label="上移">↑</button><button class="btn ghost sm" data-act="moveFeature" data-id="${f.id}" data-d="1" ${i === features.length - 1 ? 'disabled' : ''} aria-label="下移">↓</button></td>
              <td><b>${esc(f.definition.name)}</b><br><code class="small">${esc(f.code)}</code></td>
              <td class="small">${esc(App.priceHint(f.definition))}</td>
              <td class="small muted">${f.definition.fields.map((x) => x.type === 'stage_picker' ? `${esc(f.definition.catalogs[x.catalog].label)}×${f.definition.catalogs[x.catalog].groups.reduce((n, gg) => n + gg.items.length, 0)}` : esc(x.label)).join('、')}</td>
              <td>v${f.version}</td>
              <td>${App.pill(f.status === 'disabled' ? 'disabled' : f.status, f.status === 'active' ? '已上架' : f.status === 'disabled' ? '已下架' : '草稿')}</td>
              <td class="row"><button class="btn sm primary" data-act="editFeature" data-id="${f.id}">编辑</button>
                ${f.status === 'active' ? `<button class="btn sm" data-act="featureStatus" data-id="${f.id}" data-v="disabled">下架</button>` : `<button class="btn sm" data-act="featureStatus" data-id="${f.id}" data-v="active">上架</button>`}</td></tr>`
            )
            .join('') || '<tr><td colspan="7" class="muted">还没有代练功能。从上面选一个模板新建。</td></tr>'}
        </tbody></table>
      </section>

      ${a.featureId ? App.views.featureEditor(g) : ''}`;
  };

  // ---------- 功能编辑器 ----------
  App.views.featureEditor = function (g) {
    const E = App.E;
    const a = App.ui.admin;
    const isNew = a.featureId === 'new';
    const f = isNew ? null : E.feature(a.featureId);
    const draftKey = isNew ? 'new' : f.id;
    if (a.draftFor !== draftKey) {
      a.draft = JSON.stringify(isNew ? App.FEATURE_TEMPLATES[a.tpl || 'stages'].def : App.cleanDef(f.definition), null, 2);
      a.draftFor = draftKey;
      App.resetForm('preview');
    }
    let def = null;
    let parseErr = '';
    try {
      def = JSON.parse(a.draft);
    } catch (e) {
      parseErr = e.message;
    }
    const v = def ? S.validateDefinition(def) : { ok: false, errors: [{ path: '', msg: 'JSON 格式错误：' + parseErr }], warnings: [] };
    const dirty = !isNew && def && v.ok && JSON.stringify(S.normalize(def)) !== JSON.stringify(f.definition);
    const versions = isNew ? [] : E.s.featureVersions.filter((x) => x.featureId === f.id).slice().reverse();
    const previewUser = E.user(Number(a.previewUser) || 2);
    let preview = '<p class="muted">修正左侧错误后显示预览。</p>';
    if (v.ok) {
      App.formDefs.preview = () => JSON.parse(App.ui.admin.draft);
      const rate = def.member_discount === false ? 100 : E.discountRateOf(previewUser.id);
      const r = App.renderFeatureForm('preview', def, { rate });
      App.formState('preview').submitted = true; // 预览里直接显示所有字段错误，便于检查文案
      preview = `<h2 style="font-size:19px">${esc(def.name)}</h2><p class="muted small">${esc(def.description || '')}</p>
        <div class="fo-form">${r.html}</div>${App.renderQuote(r.plan, null)}
        <details class="small" style="margin-top:8px"><summary>执行端会收到的步骤（${r.plan.steps.length}）</summary>
          <pre class="json-mini">${esc(JSON.stringify({ params: r.plan.values, steps: r.plan.steps.map((s) => ({ seq: s.seq, label: s.label, count: s.count, ref: s.ref })) }, null, 2))}</pre></details>`;
    }
    return `<section class="panel editor" id="feature-editor">
      <div class="panel-head">
        <h3>${isNew ? '新建代练功能' : `编辑「${esc(f.definition.name)}」 <span class="muted small" style="font-family:var(--sans)">当前 v${f.version}</span>`}</h3>
        <span class="row">
          <a class="btn ghost sm" href="#schema-help" data-act="toggleHelp">格式说明</a>
          <button class="btn sm" data-act="formatDraft" ${def ? '' : 'disabled'}>格式化</button>
          ${!isNew ? `<button class="btn sm" data-act="revertDraft" ${dirty ? '' : 'disabled'}>放弃修改</button>` : ''}
          <button class="btn sm" data-act="closeEditor">关闭</button>
          <button class="btn primary sm" data-act="saveFeature" data-game="${g.id}" ${v.ok && (isNew || dirty) ? '' : 'disabled'}>${isNew ? '保存为草稿' : '保存为新版本'}</button>
        </span>
      </div>
      ${App.ui.showHelp ? App.views.schemaHelp() : ''}
      <div class="editor-grid">
        <div>
          <textarea class="json" spellcheck="false" data-bind="admin.draft" data-live="draft" aria-label="功能定义 JSON">${esc(a.draft)}</textarea>
          <div class="v-result ${v.ok ? 'ok' : 'bad'}" id="v-result">${App.views.validation(v)}</div>
          ${!isNew ? `<p class="muted small">保存后生成新版本。已创建的任务保留下单时的版本和价格，不受影响。${dirty ? '<b> 有未保存的修改。</b>' : ''}</p>` : ''}
          ${versions.length > 1 ? `<details class="small"><summary>版本历史（${versions.length}）</summary><table>${versions.map((x) => `<tr><td>v${x.version}</td><td class="muted">${App.t(x.at)}</td><td class="muted">admin:${x.adminId}</td><td><button class="btn ghost sm" data-act="loadVersion" data-f="${f.id}" data-v="${x.version}">载入</button></td></tr>`).join('')}</table></details>` : ''}
        </div>
        <div class="preview">
          <div class="row between" style="margin-bottom:8px"><b class="small">用户端实时预览</b><label class="row small">按 ${App.select('admin.previewUser', E.s.users.map((u) => [u.id, u.name]), { cls: 'inline', label: '预览用户' })} 计价</label></div>
          <div class="preview-frame" id="preview-frame">${preview}</div>
        </div>
      </div>
    </section>`;
  };

  App.views.validation = function (v) {
    if (v.ok) return `✓ 结构正确${v.warnings.length ? `，${v.warnings.length} 条提示：` + v.warnings.map((w) => `<div class="small muted">${esc(w.path)}：${esc(w.msg)}</div>`).join('') : ''}`;
    return `✗ ${v.errors.length} 处错误` + v.errors.slice(0, 8).map((e) => `<div class="small"><code>${esc(e.path || '(根)')}</code> ${esc(e.msg)}</div>`).join('') + (v.errors.length > 8 ? `<div class="small">… 还有 ${v.errors.length - 8} 处</div>` : '');
  };

  // 编辑时只显示管理员写的内容，去掉 normalize 补出来的默认值，减少噪音
  App.cleanDef = function (d) {
    const out = JSON.parse(JSON.stringify(d));
    const defaults = { schema_version: 1, timeout_minutes: 120, max_active_per_user: 3, refund_policy: 'prorate', member_discount: true, base_points: 0 };
    for (const [k, v] of Object.entries(defaults)) if (out[k] === v && k !== 'schema_version') delete out[k];
    if (out.capability === out.code) delete out.capability;
    if (out.limits && out.limits.max_units === 500) delete out.limits;
    if (out.catalogs && !Object.keys(out.catalogs).length) delete out.catalogs;
    if (out.description === '') delete out.description;
    return out;
  };

  App.views.schemaHelp = function () {
    return `<div class="schema-help" id="schema-help">
      <div class="cols c2" style="gap:16px">
        <div>
          <b>顶层</b>
          <table class="small"><tbody>
            <tr><td><code>code</code></td><td>功能标识，创建后不可改，执行端按它路由</td></tr>
            <tr><td><code>name</code> / <code>description</code></td><td>用户看到的名称、说明</td></tr>
            <tr><td><code>capability</code></td><td>执行端能力名，默认等于 code</td></tr>
            <tr><td><code>base_points</code></td><td>每个任务固定收取（开始执行才收）</td></tr>
            <tr><td><code>refund_policy</code></td><td><code>prorate</code> 按完成量 / <code>full_on_system_fail</code> 系统失败全退 / <code>none</code></td></tr>
            <tr><td><code>member_discount</code></td><td>是否按会员等级打折，默认 true</td></tr>
            <tr><td><code>timeout_minutes</code> / <code>max_active_per_user</code></td><td>超时、每人并发</td></tr>
            <tr><td><code>catalogs</code></td><td>关卡 / 楼层 / 野怪等目录：<code>{label, groups:[{code,name,items:[{code,name,cost,tags,disabled}]}]}</code></td></tr>
          </tbody></table>
        </div>
        <div>
          <b>fields[].type</b>
          <table class="small"><tbody>
            <tr><td><code>radio</code> / <code>select</code></td><td>单选，<code>options[].points</code> 加价</td></tr>
            <tr><td><code>checkbox</code></td><td>多选附加项</td></tr>
            <tr><td><code>switch</code></td><td>开关，<code>points</code> 开启时加价</td></tr>
            <tr><td><code>number</code></td><td><code>min/max/unit</code>，<code>points_per</code> 按数量计费</td></tr>
            <tr><td><code>text</code></td><td><code>maxlength/pattern</code>，不计价</td></tr>
            <tr><td><code>stage_picker</code></td><td>从 <code>catalog</code> 选条目；<code>multiple</code>；<code>cost_by</code> 指定模式字段（cost 写成 <code>{"clear":5,"sweep":2}</code>）；<code>count</code> 每个条目可选次数</td></tr>
            <tr><td><code>visible_when</code></td><td>条件显示，例 <code>{"mode":"sweep"}</code>，只能引用前面的字段</td></tr>
          </tbody></table>
        </div>
      </div>
      <p class="small muted">完整说明见 docs/boost-feature-schema.md。价格单位都是积分（整数）。</p>
    </div>`;
  };

  // 编辑 JSON 时只局部刷新校验结果和预览，避免整页重绘打断输入
  App.live.draft = function () {
    clearTimeout(App._draftT);
    App._draftT = setTimeout(() => {
      const ta = document.querySelector('textarea.json');
      const pos = ta ? [ta.selectionStart, ta.selectionEnd, ta.scrollTop] : null;
      App.render();
      const nt = document.querySelector('textarea.json');
      if (nt && pos) {
        nt.focus();
        nt.setSelectionRange(pos[0], pos[1]);
        nt.scrollTop = pos[2];
      }
    }, 250);
  };

  Object.assign(App.actions, {
    pickGame(ds) {
      App.ui.admin.gameId = Number(ds.id);
      App.ui.admin.featureId = null;
      App.ui.admin.draftFor = null;
    },
    newGame() {
      App.ui.admin.newGame = { id: null, code: '', name: '', short: '', color: '#3a5f8f' };
      App.ui.modal = { type: 'game' };
    },
    editGame(ds) {
      const g = App.E.game(Number(ds.id));
      App.ui.admin.newGame = { id: g.id, code: g.code, name: g.name, short: g.short, color: g.color, version: g.version };
      App.ui.modal = { type: 'game' };
    },
    saveGame() {
      const n = App.ui.admin.newGame;
      const g = App.E.adminSaveGame(App.ui.adminId, n);
      App.ui.admin.gameId = g.id;
      App.ui.modal = null;
      App.toast(n.id ? '已保存' : `已新建「${g.name}」，接下来添加区服和代练功能`, 'ok');
    },
    gameStatus(ds) {
      App.E.adminSetGameStatus(App.ui.adminId, Number(ds.id), ds.v);
    },
    addServer(ds) {
      const n = App.ui.admin.newServer;
      App.E.adminSaveServer(App.ui.adminId, { gameId: Number(ds.id), ...n });
      App.ui.admin.newServer = { code: '', name: '', platform: 'all' };
    },
    serverStatus(ds) {
      App.E.adminSetServerStatus(App.ui.adminId, Number(ds.id), ds.v);
    },
    newFeature(ds) {
      const a = App.ui.admin;
      a.featureId = 'new';
      a.tpl = ds.tpl;
      a.draftFor = null;
      App.scrollToEditor();
    },
    editFeature(ds) {
      const a = App.ui.admin;
      a.featureId = Number(ds.id);
      a.draftFor = null;
      App.scrollToEditor();
    },
    closeEditor() {
      App.ui.admin.featureId = null;
      App.ui.admin.draftFor = null;
    },
    featureStatus(ds) {
      App.E.adminSetFeatureStatus(App.ui.adminId, Number(ds.id), ds.v);
    },
    moveFeature(ds) {
      App.E.adminReorderFeature(App.ui.adminId, Number(ds.id), Number(ds.d));
    },
    formatDraft() {
      const a = App.ui.admin;
      a.draft = JSON.stringify(JSON.parse(a.draft), null, 2);
    },
    revertDraft() {
      App.ui.admin.draftFor = null;
    },
    loadVersion(ds) {
      const v = App.E.s.featureVersions.find((x) => x.featureId === Number(ds.f) && x.version === Number(ds.v));
      App.ui.admin.draft = JSON.stringify(App.cleanDef(v.definition), null, 2);
      App.toast(`已载入 v${v.version}，保存后会成为新版本`);
    },
    toggleHelp() {
      App.ui.showHelp = !App.ui.showHelp;
    },
    saveFeature(ds) {
      const a = App.ui.admin;
      const isNew = a.featureId === 'new';
      const cur = isNew ? null : App.E.feature(a.featureId);
      const f = App.E.adminSaveFeature(App.ui.adminId, { id: isNew ? null : cur.id, gameId: Number(ds.game), definition: a.draft, version: isNew ? null : cur.version });
      a.featureId = f.id;
      a.draftFor = null;
      App.toast(isNew ? `已保存为草稿。确认预览无误后点"上架"。` : `已保存为 v${f.version}`, 'ok');
    },
  });
  App.scrollToEditor = function () {
    setTimeout(() => {
      const el = document.getElementById('feature-editor');
      if (el && el.scrollIntoView) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }, 30);
  };

  App.views.modal_game = function () {
    const n = App.ui.admin.newGame;
    return `<h2>${n.id ? '编辑游戏' : '新建游戏'}</h2>
      <label class="field"><span>名称</span>${App.input('admin.newGame.name', { placeholder: '例如：星落幻境', label: '名称' })}</label>
      <div class="cols c2" style="gap:8px">
        <label class="field"><span>code（小写字母、数字、下划线；创建后不可改）</span>${App.input('admin.newGame.code', { placeholder: 'starfall', label: 'code' }).replace('<input', n.id ? '<input disabled' : '<input')}</label>
        <label class="field"><span>简称</span>${App.input('admin.newGame.short', { placeholder: '星落', label: '简称' })}</label>
      </div>
      <label class="field"><span>主题色</span>${App.input('admin.newGame.color', { type: 'color', label: '主题色' })}</label>
      <p class="muted small">新游戏是草稿状态。添加区服和代练功能并上架后，才能上线给用户看到。</p>
      <div class="foot"><button class="btn" data-act="closeModal">取消</button><button class="btn primary" data-act="saveGame">保存</button></div>`;
  };
})();
