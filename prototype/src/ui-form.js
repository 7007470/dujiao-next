/* 由功能 JSON 自动生成的代练表单。
 * 用户端"发起代练"和后台"实时预览"共用这一份渲染器，保证后台看到的就是用户看到的。
 *
 * 渲染器只依赖 compile() 的结果：可见性、默认值、错误、报价都由它算出，
 * 渲染器本身不做任何价格计算。
 */
(function () {
  'use strict';
  const App = window.App;
  const S = window.FeatureSchema;
  const { esc } = App;

  // 表单状态存在 App.ui.forms[formId] = { values, stageGroup, stageQuery }
  App.formState = function (formId) {
    const all = (App.ui.forms = App.ui.forms || {});
    if (!all[formId]) all[formId] = { values: {}, stageGroup: {}, stageQuery: {}, touched: {}, submitted: false };
    return all[formId];
  };
  App.resetForm = function (formId) {
    if (App.ui.forms) delete App.ui.forms[formId];
  };

  /**
   * @param formId  表单状态 key
   * @param def     功能定义
   * @param opts    { rate: 会员折扣率, interactive: true }
   * @returns { html, plan }
   */
  App.renderFeatureForm = function (formId, def, opts) {
    const st = App.formState(formId);
    const plan = S.compile(def, st.values, { discountRate: (opts && opts.rate) || 100 });
    const d = S.normalize(def);
    const html = d.fields
      .filter((f) => plan.visible[f.key])
      .map((f) => {
        // 字段错误只在用户动过这个字段、或点过提交之后才显示，避免一打开就满屏红字
        const err = st.submitted || st.touched[f.key] ? plan.fieldErrors[f.key] : null;
        const body = renderField(formId, d, f, plan, st);
        const tag = f.type === 'stage_picker' || f.type === 'checkbox' || f.type === 'radio' ? 'fieldset' : 'div';
        return `<${tag} class="ff ${err ? 'has-err' : ''}" data-ff="${esc(f.key)}">
          ${tag === 'fieldset' ? `<legend>${esc(f.label)}${f.required ? ' <i class="req">*</i>' : ''}</legend>` : `<label class="ff-label" for="ff-${formId}-${esc(f.key)}">${esc(f.label)}${f.required ? ' <i class="req">*</i>' : ''}</label>`}
          ${body}
          ${f.help ? `<p class="ff-help">${esc(f.help)}</p>` : ''}
          ${err ? `<p class="ff-err" role="alert">${esc(err)}</p>` : ''}
        </${tag}>`;
      })
      .join('');
    plan.showErrors = st.submitted || Object.keys(st.touched).length > 0;
    return { html, plan };
  };

  const pts = (n) => `<span class="pts-inline">${n} 点</span>`;
  function optPrice(o) {
    return o.points ? ` <span class="addon">+${o.points} 点</span>` : '';
  }

  function renderField(formId, d, f, plan, st) {
    const name = `${formId}:${f.key}`;
    const id = `ff-${formId}-${f.key}`;
    const v = plan.values[f.key];
    const data = `data-form="${esc(formId)}" data-key="${esc(f.key)}"`;
    switch (f.type) {
      case 'radio':
        return `<div class="seg-opts" role="radiogroup">${f.options
          .map((o) => `<label class="opt ${v === o.value ? 'on' : ''}"><input type="radio" name="${esc(name)}" value="${esc(o.value)}" ${v === o.value ? 'checked' : ''} ${data} data-ftype="radio">${esc(o.label)}${optPrice(o)}</label>`)
          .join('')}</div>`;
      case 'select':
        return `<select id="${id}" ${data} data-ftype="select"><option value="">请选择</option>${f.options
          .map((o) => `<option value="${esc(o.value)}" ${v === o.value ? 'selected' : ''}>${esc(o.label)}${o.points ? `（+${o.points} 点）` : ''}</option>`)
          .join('')}</select>`;
      case 'checkbox':
        return `<div class="chk-opts">${f.options
          .map((o) => `<label class="opt ${(v || []).includes(o.value) ? 'on' : ''}"><input type="checkbox" value="${esc(o.value)}" ${(v || []).includes(o.value) ? 'checked' : ''} ${data} data-ftype="checkbox">${esc(o.label)}${optPrice(o)}</label>`)
          .join('')}</div>`;
      case 'switch':
        return `<label class="switch"><input type="checkbox" id="${id}" role="switch" ${v ? 'checked' : ''} ${data} data-ftype="switch"><span class="track"></span><span>${v ? '开启' : '关闭'}${f.points ? ` <span class="addon">+${f.points} 点</span>` : ''}</span></label>`;
      case 'number': {
        const raw = st.values[f.key];
        const shown = raw !== undefined ? raw : v !== undefined ? v : '';
        return `<div class="num-row">
          <button type="button" class="btn sm" data-act="ffStep" data-form="${esc(formId)}" data-key="${esc(f.key)}" data-d="-${stepOf(f)}" aria-label="减少">−</button>
          <input id="${id}" type="number" inputmode="numeric" min="${f.min}" max="${f.max}" value="${esc(shown)}" ${data} data-ftype="number">
          <button type="button" class="btn sm" data-act="ffStep" data-form="${esc(formId)}" data-key="${esc(f.key)}" data-d="${stepOf(f)}" aria-label="增加">+</button>
          <span class="muted small">${esc(f.unit || '')}　${f.min}–${f.max}${f.points_per !== undefined ? `　每${esc(f.unit || '单位')} ${f.points_per} 点` : ''}</span>
        </div>`;
      }
      case 'text':
        return `<input id="${id}" type="text" maxlength="${f.maxlength || 100}" placeholder="${esc(f.placeholder || '')}" value="${esc(st.values[f.key] || '')}" ${data} data-ftype="text">`;
      case 'stage_picker':
        return renderStagePicker(formId, d, f, plan, st);
    }
    return '';
  }
  function stepOf(f) {
    const span = f.max - f.min;
    return span >= 200 ? 10 : span >= 50 ? 5 : 1;
  }

  // ---------- 关卡选择器 ----------
  function renderStagePicker(formId, d, f, plan, st) {
    const cat = d.catalogs[f.catalog];
    const mode = f.cost_by ? plan.values[f.cost_by] : null;
    const modeField = f.cost_by ? d.fields.find((x) => x.key === f.cost_by) : null;
    const modeLabel = modeField && mode ? (modeField.options.find((o) => o.value === mode) || {}).label : '';
    const countOn = f.count && S.matchWhen(f.count.visible_when, plan.values);
    const picked = new Map((plan.values[f.key] || []).map((x) => [x.item, x.count]));
    // 用户的原始选择（包括不合法的）也要保留勾选状态，方便取消
    const rawPicked = new Set((st.values[f.key] || []).map((x) => (typeof x === 'object' ? x.item : x)));
    const q = (st.stageQuery[f.key] || '').trim();
    const groupSel = st.stageGroup[f.key] || cat.groups[0].code;
    const groups = q ? cat.groups : cat.groups.filter((g) => g.code === groupSel);
    const data = `data-form="${esc(formId)}" data-key="${esc(f.key)}"`;
    const pickedCount = rawPicked.size;
    return `<div class="stage-picker">
      <div class="sp-head">
        <div class="sp-tabs" role="tablist">${cat.groups
          .map((g) => {
            const n = g.items.filter((it) => rawPicked.has(it.code)).length;
            return `<button type="button" role="tab" aria-selected="${!q && g.code === groupSel}" data-act="ffGroup" ${data} data-g="${esc(g.code)}">${esc(g.name)}${n ? ` <b>${n}</b>` : ''}</button>`;
          })
          .join('')}</div>
        <input type="search" class="sp-search" placeholder="搜索${esc(cat.label)}" value="${esc(q)}" data-form="${esc(formId)}" data-key="${esc(f.key)}" data-ftype="stage_search" aria-label="搜索${esc(cat.label)}">
      </div>
      ${f.cost_by && !mode ? `<p class="ff-help">先选择「${esc(modeField.label)}」，才能看到每个${esc(cat.label)}的价格。</p>` : ''}
      ${groups
        .map(
          (g) => `${q ? `<h5 class="sp-group">${esc(g.name)}</h5>` : ''}
        <div class="sp-grid">${g.items
          .filter((it) => !q || (it.name + it.code + (it.tags || []).join('')).includes(q))
          .map((it) => {
            const cost = S.itemCost(it, mode);
            const unsupported = cost === null && (mode != null || !f.cost_by);
            const off = it.disabled || unsupported;
            const on = rawPicked.has(it.code);
            const cnt = picked.get(it.code);
            const reason = it.disabled ? '暂不开放' : unsupported ? `不支持${esc(modeLabel)}` : '';
            return `<div class="sp-item ${on ? 'on' : ''} ${off ? 'off' : ''}">
              <label class="sp-main">
                <input type="${f.multiple ? 'checkbox' : 'radio'}" name="${esc(formId + ':' + f.key)}" value="${esc(it.code)}" ${on ? 'checked' : ''} ${off && !on ? 'disabled' : ''} ${data} data-ftype="stage">
                <span class="sp-name">${esc(it.name)}</span>
                <span class="sp-meta">${(it.tags || []).map((t) => `<span class="tag-s">${esc(t)}</span>`).join('')}</span>
                <span class="sp-cost">${reason ? `<span class="muted">${reason}</span>` : cost === null ? '' : `${cost} 点${countOn ? ' / 次' : ''}`}</span>
              </label>
              ${
                on && countOn && !off
                  ? `<div class="sp-count"><button type="button" class="btn sm" data-act="ffCount" ${data} data-item="${esc(it.code)}" data-d="-1" aria-label="减少次数">−</button>
                     <input type="number" min="${f.count.min}" max="${f.count.max}" value="${esc(cnt != null ? cnt : rawCount(st, f.key, it.code, f.count))}" ${data} data-item="${esc(it.code)}" data-ftype="stage_count" aria-label="${esc(it.name)} ${esc(f.count.label || '次数')}">
                     <button type="button" class="btn sm" data-act="ffCount" ${data} data-item="${esc(it.code)}" data-d="1" aria-label="增加次数">+</button>
                     <span class="muted small">${esc(f.count.label || '次数')}</span></div>`
                  : ''
              }
            </div>`;
          })
          .join('')}</div>`
        )
        .join('')}
      <div class="sp-foot"><span>已选 <b>${pickedCount}</b> 个${esc(cat.label)}${f.max_select ? `（最多 ${f.max_select}）` : ''}</span>${pickedCount ? `<button type="button" class="btn ghost sm" data-act="ffClear" ${data}>清空</button>` : ''}</div>
    </div>`;
  }
  function rawCount(st, key, code, count) {
    const e = (st.values[key] || []).find((x) => typeof x === 'object' && x.item === code);
    return e && e.count !== undefined ? e.count : count.default || count.min;
  }

  // ---------- 交互 ----------
  function stageList(st, key) {
    return (st.values[key] = (st.values[key] || []).map((x) => (typeof x === 'object' ? x : { item: x })));
  }
  function defOf(formId) {
    const fn = App.formDefs && App.formDefs[formId];
    return fn ? fn() : null;
  }
  function fieldOf(formId, key) {
    const d = defOf(formId);
    return d ? d.fields.find((x) => x.key === key) : null;
  }

  document.addEventListener('change', (e) => {
    const el = e.target;
    if (!el.dataset || !el.dataset.form || !el.dataset.ftype) return;
    const st = App.formState(el.dataset.form);
    const key = el.dataset.key;
    const t = el.dataset.ftype;
    st.touched[key] = true;
    if (t === 'radio' || t === 'select') st.values[key] = el.value || undefined;
    else if (t === 'switch') st.values[key] = el.checked;
    else if (t === 'checkbox') {
      const cur = new Set(st.values[key] || (fieldOf(el.dataset.form, key) || {}).default || []);
      el.checked ? cur.add(el.value) : cur.delete(el.value);
      st.values[key] = [...cur];
    } else if (t === 'number') st.values[key] = el.value === '' ? undefined : Number(el.value);
    else if (t === 'text') st.values[key] = el.value;
    else if (t === 'stage') {
      const f = fieldOf(el.dataset.form, key);
      let list = stageList(st, key);
      if (f && !f.multiple) list = st.values[key] = el.checked ? [{ item: el.value }] : [];
      else if (el.checked) {
        if (!list.some((x) => x.item === el.value)) list.push({ item: el.value });
      } else st.values[key] = list.filter((x) => x.item !== el.value);
    } else if (t === 'stage_count') {
      const it = stageList(st, key).find((x) => x.item === el.dataset.item);
      if (it) it.count = el.value === '' ? undefined : Number(el.value);
    } else return;
    App.render();
  });
  document.addEventListener('input', (e) => {
    const el = e.target;
    if (!el.dataset || el.dataset.ftype !== 'stage_search') return;
    App.formState(el.dataset.form).stageQuery[el.dataset.key] = el.value;
    clearTimeout(App._spT);
    App._spT = setTimeout(() => App.render(), 180);
  });

  Object.assign(App.actions, {
    ffGroup(ds) {
      const st = App.formState(ds.form);
      st.stageGroup[ds.key] = ds.g;
      st.stageQuery[ds.key] = '';
    },
    ffClear(ds) {
      App.formState(ds.form).values[ds.key] = [];
      App.formState(ds.form).touched[ds.key] = true;
    },
    ffCount(ds) {
      const st = App.formState(ds.form);
      App.formState(ds.form).touched[ds.key] = true;
      const f = fieldOf(ds.form, ds.key);
      const it = stageList(st, ds.key).find((x) => x.item === ds.item);
      if (!it || !f || !f.count) return false;
      const cur = it.count != null ? it.count : f.count.default || f.count.min;
      it.count = Math.max(f.count.min, Math.min(f.count.max, cur + Number(ds.d)));
    },
    ffStep(ds) {
      const st = App.formState(ds.form);
      App.formState(ds.form).touched[ds.key] = true;
      const f = fieldOf(ds.form, ds.key);
      if (!f) return false;
      const cur = st.values[ds.key] != null ? Number(st.values[ds.key]) : f.default != null ? f.default : f.min;
      st.values[ds.key] = Math.max(f.min, Math.min(f.max, cur + Number(ds.d)));
    },
  });

  // ---------- 报价单 ----------
  App.renderQuote = function (plan, available, opts) {
    const o = opts || {};
    const short = available != null && plan.total > available;
    const lines = plan.breakdown.slice(0, 12);
    return `<div class="quote-box ${!plan.ok ? 'bad' : short ? 'short' : ''}">
      <div class="qb-lines">${
        lines.length
          ? lines.map((l) => `<div><span>${esc(l.label)}</span><span class="muted">${esc(l.detail)}</span><b>${l.points > 0 ? '' : ''}${l.points}</b></div>`).join('') + (plan.breakdown.length > 12 ? `<div class="muted small">… 还有 ${plan.breakdown.length - 12} 项</div>` : '')
          : `<div class="muted">${plan.showErrors ? '还没有可计价的内容' : '选好内容后显示明细'}</div>`
      }</div>
      <div class="qb-total"><span>合计 <small class="muted">${plan.units} 个执行单位${plan.rate < 100 ? ` · 已按会员 ${plan.rate / 10} 折` : ''}</small></span><b class="pts">${plan.total}</b></div>
      ${!plan.ok && plan.errors.length && plan.showErrors && o.showErrors !== false ? `<p class="ff-err">${esc(plan.errors[0])}</p>` : ''}
      ${available != null ? `<div class="qb-avail ${short ? 'bad' : ''}">可用积分 ${available}${short ? `，还差 ${plan.total - available}` : ''}</div>` : ''}
    </div>`;
  };
  App.refundPolicyText = {
    full_on_system_fail: '系统原因失败（网络、维护、执行端故障、登录失败）全额退回；其它情况按已完成部分扣点。',
    prorate: '失败或取消时，只扣已完成部分的积分，其余退回。',
    none: '开始执行后不退积分。',
  };
})();
