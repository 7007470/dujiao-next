/* 代练功能定义（Feature Definition）JSON 规范 —— 校验、编译、报价。
 *
 * 这是后台"用 JSON 配置代练功能、前端自动生成界面"的核心。
 * 同一份代码同时被三处使用，保证三者永远一致：
 *   1. 后台保存功能时：validateDefinition() 校验 JSON；
 *   2. 用户端渲染表单和实时报价：compile() 计算可见字段、默认值、错误、积分；
 *   3. 服务端创建任务：compile() 重新计算，前端传来的价格一律不用。
 * 完整格式说明见 docs/boost-feature-schema.md。
 */
(function (root) {
  'use strict';

  const SCHEMA_VERSION = 1;
  const CODE_RE = /^[a-z][a-z0-9_]{1,31}$/;
  const KEY_RE = /^[a-z][a-z0-9_]{0,31}$/;
  const ITEM_RE = /^[A-Za-z0-9][A-Za-z0-9_\-.]{0,31}$/;
  const FIELD_TYPES = ['radio', 'select', 'checkbox', 'switch', 'number', 'text', 'stage_picker'];
  const OPTION_TYPES = new Set(['radio', 'select', 'checkbox']);
  const REFUND_POLICIES = ['prorate', 'full_on_system_fail', 'none'];
  const TOP_KEYS = new Set(['schema_version', 'code', 'name', 'description', 'capability', 'timeout_minutes', 'max_active_per_user', 'refund_policy', 'member_discount', 'base_points', 'catalogs', 'fields', 'limits']);
  const FIELD_KEYS = new Set(['key', 'type', 'label', 'help', 'required', 'default', 'visible_when', 'options', 'min_select', 'max_select', 'points', 'min', 'max', 'unit', 'points_per', 'maxlength', 'pattern', 'placeholder', 'catalog', 'multiple', 'cost_by', 'count']);

  const isInt = (v) => Number.isInteger(v);
  const isObj = (v) => v != null && typeof v === 'object' && !Array.isArray(v);
  const str = (v) => typeof v === 'string';

  // ====================================================================
  // 1. 校验功能定义
  // ====================================================================
  function validateDefinition(def) {
    const errors = [];
    const warnings = [];
    const err = (path, msg) => errors.push({ path, msg });
    const warn = (path, msg) => warnings.push({ path, msg });

    if (!isObj(def)) {
      err('', '功能定义必须是一个 JSON 对象');
      return { ok: false, errors, warnings };
    }
    for (const k of Object.keys(def)) if (!TOP_KEYS.has(k)) warn(k, `未知字段「${k}」会被忽略`);

    if (def.schema_version !== undefined && def.schema_version !== SCHEMA_VERSION) err('schema_version', `只支持 schema_version = ${SCHEMA_VERSION}`);
    if (!str(def.code) || !CODE_RE.test(def.code)) err('code', 'code 必须是 2–32 位小写字母、数字或下划线，并以字母开头');
    if (!str(def.name) || !def.name.trim() || def.name.length > 40) err('name', 'name 必填，最多 40 个字');
    if (def.description !== undefined && (!str(def.description) || def.description.length > 300)) err('description', 'description 最多 300 个字');
    if (def.capability !== undefined && (!str(def.capability) || !CODE_RE.test(def.capability))) err('capability', 'capability 格式同 code');
    intRange(def, 'timeout_minutes', 5, 1440, false, err);
    intRange(def, 'max_active_per_user', 1, 20, false, err);
    intRange(def, 'base_points', 0, 100000, false, err);
    if (def.refund_policy !== undefined && !REFUND_POLICIES.includes(def.refund_policy)) err('refund_policy', `refund_policy 只能是 ${REFUND_POLICIES.join(' / ')}`);
    if (def.member_discount !== undefined && typeof def.member_discount !== 'boolean') err('member_discount', 'member_discount 必须是 true 或 false');
    if (def.limits !== undefined) {
      if (!isObj(def.limits)) err('limits', 'limits 必须是对象');
      else intRange(def.limits, 'max_units', 1, 10000, false, err, 'limits.');
    }

    // catalogs
    const catalogs = def.catalogs === undefined ? {} : def.catalogs;
    if (!isObj(catalogs)) err('catalogs', 'catalogs 必须是对象，键为目录名');
    else {
      for (const [ck, cat] of Object.entries(catalogs)) {
        const p = `catalogs.${ck}`;
        if (!KEY_RE.test(ck)) err(p, `目录名「${ck}」格式不正确`);
        if (!isObj(cat)) {
          err(p, '目录必须是对象');
          continue;
        }
        if (!str(cat.label) || !cat.label.trim()) err(p + '.label', '目录需要 label，例如"关卡"');
        if (!Array.isArray(cat.groups) || !cat.groups.length) {
          err(p + '.groups', 'groups 必须是非空数组（例如按章节分组）');
          continue;
        }
        const groupCodes = new Set();
        const itemCodes = new Set();
        cat.groups.forEach((g, gi) => {
          const gp = `${p}.groups[${gi}]`;
          if (!isObj(g)) return err(gp, '分组必须是对象');
          if (!str(g.code) || !ITEM_RE.test(g.code)) err(gp + '.code', '分组 code 必填，字母数字');
          else if (groupCodes.has(g.code)) err(gp + '.code', `分组 code「${g.code}」重复`);
          groupCodes.add(g.code);
          if (!str(g.name) || !g.name.trim()) err(gp + '.name', '分组 name 必填');
          if (!Array.isArray(g.items) || !g.items.length) return err(gp + '.items', 'items 必须是非空数组');
          g.items.forEach((it, ii) => {
            const ip = `${gp}.items[${ii}]`;
            if (!isObj(it)) return err(ip, '条目必须是对象');
            if (!str(it.code) || !ITEM_RE.test(it.code)) err(ip + '.code', '条目 code 必填，字母、数字、- _ .');
            else if (itemCodes.has(it.code)) err(ip + '.code', `条目 code「${it.code}」在目录内重复`);
            itemCodes.add(it.code);
            if (!str(it.name) || !it.name.trim()) err(ip + '.name', '条目 name 必填');
            if (isInt(it.cost)) {
              if (it.cost < 0) err(ip + '.cost', 'cost 不能为负');
            } else if (isObj(it.cost)) {
              if (!Object.keys(it.cost).length) err(ip + '.cost', 'cost 对象不能为空');
              for (const [mk, mv] of Object.entries(it.cost)) if (!isInt(mv) || mv < 0) err(`${ip}.cost.${mk}`, 'cost 必须是非负整数');
            } else err(ip + '.cost', 'cost 必须是整数，或按模式区分的对象，例如 {"clear": 8, "sweep": 3}');
            if (it.tags !== undefined && (!Array.isArray(it.tags) || it.tags.some((t) => !str(t)))) err(ip + '.tags', 'tags 必须是字符串数组');
            if (it.disabled !== undefined && typeof it.disabled !== 'boolean') err(ip + '.disabled', 'disabled 必须是布尔值');
          });
        });
      }
    }

    // fields
    if (!Array.isArray(def.fields) || !def.fields.length) {
      err('fields', 'fields 必须是非空数组');
      return { ok: !errors.length, errors, warnings };
    }
    if (def.fields.length > 30) err('fields', '最多 30 个字段');
    const seen = new Map();
    def.fields.forEach((f, i) => {
      const p = `fields[${i}]`;
      if (!isObj(f)) return err(p, '字段必须是对象');
      for (const k of Object.keys(f)) if (!FIELD_KEYS.has(k)) warn(`${p}.${k}`, `未知属性「${k}」会被忽略`);
      if (!str(f.key) || !KEY_RE.test(f.key)) err(p + '.key', 'key 必须是小写字母开头的字母、数字、下划线');
      else if (seen.has(f.key)) err(p + '.key', `key「${f.key}」重复`);
      if (!FIELD_TYPES.includes(f.type)) {
        err(p + '.type', `type 只能是 ${FIELD_TYPES.join(' / ')}`);
        seen.set(f.key, f);
        return;
      }
      if (!str(f.label) || !f.label.trim() || f.label.length > 30) err(p + '.label', 'label 必填，最多 30 个字');
      if (f.required !== undefined && typeof f.required !== 'boolean') err(p + '.required', 'required 必须是布尔值');

      if (OPTION_TYPES.has(f.type)) {
        if (!Array.isArray(f.options) || !f.options.length) err(p + '.options', '需要非空 options');
        else {
          const vals = new Set();
          f.options.forEach((o, oi) => {
            const op = `${p}.options[${oi}]`;
            if (!isObj(o)) return err(op, '选项必须是对象 {value, label}');
            if (!str(o.value) || !o.value.trim() || o.value.length > 32) err(op + '.value', 'value 必须是非空字符串');
            else if (vals.has(o.value)) err(op + '.value', `value「${o.value}」重复`);
            vals.add(o.value);
            if (!str(o.label) || !o.label.trim()) err(op + '.label', 'label 必填');
            if (o.points !== undefined && (!isInt(o.points) || o.points < 0)) err(op + '.points', 'points 必须是非负整数');
          });
          if (f.default !== undefined) {
            const defs = f.type === 'checkbox' ? f.default : [f.default];
            if (!Array.isArray(defs) || defs.some((d) => !vals.has(d))) err(p + '.default', 'default 必须是 options 里的 value');
          }
        }
        if (f.type === 'checkbox') {
          intRange(f, 'min_select', 0, 50, false, err, p + '.');
          intRange(f, 'max_select', 1, 50, false, err, p + '.');
        }
      }
      if (f.type === 'switch') {
        if (f.points !== undefined && (!isInt(f.points) || f.points < 0)) err(p + '.points', 'points 必须是非负整数');
        if (f.default !== undefined && typeof f.default !== 'boolean') err(p + '.default', 'default 必须是布尔值');
      }
      if (f.type === 'number') {
        if (!isInt(f.min) || !isInt(f.max)) err(p, 'number 需要整数 min 和 max');
        else if (f.min > f.max) err(p, 'min 不能大于 max');
        else if (f.default !== undefined && (!isInt(f.default) || f.default < f.min || f.default > f.max)) err(p + '.default', `default 必须在 ${f.min}–${f.max} 之间`);
        if (f.points_per !== undefined && (!isInt(f.points_per) || f.points_per < 0)) err(p + '.points_per', 'points_per 必须是非负整数');
        if (f.unit !== undefined && (!str(f.unit) || f.unit.length > 8)) err(p + '.unit', 'unit 最多 8 个字');
      }
      if (f.type === 'text') {
        intRange(f, 'maxlength', 1, 200, false, err, p + '.');
        if (f.pattern !== undefined) {
          try {
            new RegExp(f.pattern);
          } catch (e) {
            err(p + '.pattern', 'pattern 不是合法的正则表达式');
          }
        }
      }
      if (f.type === 'stage_picker') {
        const cat = isObj(catalogs) ? catalogs[f.catalog] : null;
        if (!str(f.catalog) || !cat) err(p + '.catalog', `catalog 必须指向 catalogs 里的目录${str(f.catalog) ? `（找不到「${f.catalog}」）` : ''}`);
        if (f.multiple !== undefined && typeof f.multiple !== 'boolean') err(p + '.multiple', 'multiple 必须是布尔值');
        if (f.multiple) intRange(f, 'max_select', 1, 200, false, err, p + '.');
        if (f.count !== undefined) {
          if (!isObj(f.count)) err(p + '.count', 'count 必须是对象 {label, min, max, default}');
          else {
            if (!isInt(f.count.min) || !isInt(f.count.max) || f.count.min < 1 || f.count.min > f.count.max || f.count.max > 999) err(p + '.count', 'count 需要整数 min ≥ 1、max ≤ 999，且 min ≤ max');
            if (f.count.default !== undefined && (!isInt(f.count.default) || f.count.default < f.count.min || f.count.default > f.count.max)) err(p + '.count.default', 'count.default 超出范围');
            if (f.count.visible_when !== undefined) checkVisibleWhen(f.count.visible_when, `${p}.count.visible_when`, seen, err);
          }
        }
        if (f.cost_by !== undefined) {
          const ref = seen.get(f.cost_by);
          if (!ref || !['radio', 'select'].includes(ref.type)) err(p + '.cost_by', 'cost_by 必须引用前面的 radio 或 select 字段');
          else if (cat && Array.isArray(cat.groups)) {
            const modes = new Set((ref.options || []).map((o) => o.value));
            cat.groups.forEach((g, gi) =>
              (g.items || []).forEach((it, ii) => {
                if (isObj(it.cost)) for (const mk of Object.keys(it.cost)) if (!modes.has(mk)) err(`catalogs.${f.catalog}.groups[${gi}].items[${ii}].cost.${mk}`, `「${mk}」不是 ${f.cost_by} 的选项`);
              })
            );
          }
        } else if (cat && Array.isArray(cat.groups) && cat.groups.some((g) => (g.items || []).some((it) => isObj(it.cost)))) {
          err(p + '.cost_by', '目录里有按模式区分的 cost，需要用 cost_by 指定模式字段');
        }
      }
      if (f.visible_when !== undefined) checkVisibleWhen(f.visible_when, p + '.visible_when', seen, err);
      seen.set(f.key, f);
    });

    return { ok: !errors.length, errors, warnings };
  }

  function checkVisibleWhen(vw, path, seen, err) {
    if (!isObj(vw) || !Object.keys(vw).length) return err(path, 'visible_when 必须是对象，例如 {"mode": "sweep"}');
    for (const [k, v] of Object.entries(vw)) {
      const ref = seen.get(k);
      if (!ref) {
        err(`${path}.${k}`, `只能引用排在前面的字段，找不到「${k}」`);
        continue;
      }
      const vals = Array.isArray(v) ? v : [v];
      if (OPTION_TYPES.has(ref.type) && ref.type !== 'checkbox') {
        const ok = new Set((ref.options || []).map((o) => o.value));
        for (const x of vals) if (!ok.has(x)) err(`${path}.${k}`, `「${x}」不是 ${k} 的选项`);
      } else if (ref.type === 'switch') {
        if (vals.some((x) => typeof x !== 'boolean')) err(`${path}.${k}`, 'switch 条件只能是 true 或 false');
      } else err(`${path}.${k}`, 'visible_when 只能引用 radio、select 或 switch 字段');
    }
  }

  function intRange(obj, key, min, max, required, err, prefix) {
    const v = obj[key];
    const path = (prefix || '') + key;
    if (v === undefined) {
      if (required) err(path, `${key} 必填`);
      return;
    }
    if (!isInt(v) || v < min || v > max) err(path, `${key} 必须是 ${min}–${max} 之间的整数`);
  }

  // 补全默认值后的定义，供编译和展示使用
  function normalize(def) {
    return {
      schema_version: SCHEMA_VERSION,
      code: def.code,
      name: def.name,
      description: def.description || '',
      capability: def.capability || def.code,
      timeout_minutes: def.timeout_minutes || 120,
      max_active_per_user: def.max_active_per_user || 3,
      refund_policy: def.refund_policy || 'prorate',
      member_discount: def.member_discount !== false,
      base_points: def.base_points || 0,
      catalogs: def.catalogs || {},
      fields: def.fields,
      limits: { max_units: (def.limits && def.limits.max_units) || 500 },
    };
  }

  // ====================================================================
  // 2. 编译：用户填写的参数 → 规范化参数 + 执行步骤 + 积分
  // ====================================================================
  function matchWhen(vw, values) {
    if (!vw) return true;
    return Object.entries(vw).every(([k, v]) => (Array.isArray(v) ? v : [v]).includes(values[k]));
  }
  // 会员折扣只在合计上计算一次（向下取整），避免小额单价逐项取整导致折扣失真。
  // 每个条目先按原价累计，结算部分完成时同样先按原价累计再打折，保证单调且完成时等于总价。
  function applyRate(raw, rate) {
    if (!raw) return 0;
    if (!rate || rate >= 100) return raw;
    return Math.max(1, Math.floor((raw * rate) / 100));
  }
  const discounted = (points) => points;
  function itemIndex(cat) {
    const map = new Map();
    for (const g of cat.groups) for (const it of g.items) map.set(it.code, { ...it, group: g.code, groupName: g.name });
    return map;
  }
  // 条目在某模式下的单价；null 表示该模式不可用
  function itemCost(it, mode) {
    if (isInt(it.cost)) return it.cost;
    if (mode == null) return null;
    return Object.prototype.hasOwnProperty.call(it.cost, mode) ? it.cost[mode] : null;
  }

  /**
   * @param def      已通过校验的功能定义
   * @param raw      用户提交的参数（可以不完整）
   * @param opts     { discountRate: 100 }
   * @returns {
   *   ok, values, visible:{key:bool}, fieldErrors:{key:msg}, errors:[msg],
   *   steps:[{seq,label,count,unitPoints,ref}], fixed:[{label,points}],
   *   units, fixedPoints, total, breakdown:[{label,detail,points}]
   * }
   */
  function compile(def, raw, opts) {
    const d = normalize(def);
    const rate = d.member_discount ? (opts && opts.discountRate) || 100 : 100;
    const input = isObj(raw) ? raw : {};
    const values = {};
    const visible = {};
    const fieldErrors = {};
    const fixed = [];
    const steps = [];
    const breakdown = [];
    const fe = (k, m) => {
      if (!fieldErrors[k]) fieldErrors[k] = m;
    };

    if (d.base_points) {
      const pts = discounted(d.base_points, rate);
      fixed.push({ label: '基础服务', points: pts });
      breakdown.push({ label: '基础服务', detail: '每个任务一次', points: pts });
    }

    for (const f of d.fields) {
      const vis = matchWhen(f.visible_when, values);
      visible[f.key] = vis;
      if (!vis) continue; // 隐藏字段不取值、不计价、不校验
      let v = input[f.key];
      switch (f.type) {
        case 'radio':
        case 'select': {
          if (v === undefined || v === '' || v === null) v = f.default;
          if (v === undefined) {
            if (f.required) fe(f.key, `请选择${f.label}`);
            break;
          }
          const opt = f.options.find((o) => o.value === v);
          if (!opt) {
            fe(f.key, `${f.label}选项不合法`);
            break;
          }
          values[f.key] = v;
          if (opt.points) {
            const pts = discounted(opt.points, rate);
            fixed.push({ label: `${f.label}：${opt.label}`, points: pts });
            breakdown.push({ label: `${f.label}：${opt.label}`, detail: '附加', points: pts });
          }
          break;
        }
        case 'checkbox': {
          if (v === undefined) v = f.default || [];
          if (!Array.isArray(v)) v = [v];
          const okVals = f.options.map((o) => o.value);
          const picked = okVals.filter((x) => v.includes(x)); // 去重并按选项顺序
          if (v.some((x) => !okVals.includes(x))) fe(f.key, `${f.label}含有不合法的选项`);
          if (f.required && !picked.length) fe(f.key, `请至少选择一项${f.label}`);
          if (f.min_select && picked.length < f.min_select) fe(f.key, `${f.label}至少选 ${f.min_select} 项`);
          if (f.max_select && picked.length > f.max_select) fe(f.key, `${f.label}最多选 ${f.max_select} 项`);
          values[f.key] = picked;
          for (const x of picked) {
            const opt = f.options.find((o) => o.value === x);
            if (opt.points) {
              const pts = discounted(opt.points, rate);
              fixed.push({ label: `${f.label}：${opt.label}`, points: pts });
              breakdown.push({ label: `${f.label}：${opt.label}`, detail: '附加', points: pts });
            }
          }
          break;
        }
        case 'switch': {
          if (v === undefined || v === '') v = f.default === true;
          v = v === true || v === 'true' || v === 1;
          values[f.key] = v;
          if (v && f.points) {
            const pts = discounted(f.points, rate);
            fixed.push({ label: f.label, points: pts });
            breakdown.push({ label: f.label, detail: '附加', points: pts });
          }
          break;
        }
        case 'number': {
          if (v === undefined || v === '' || v === null) v = f.default;
          if (v === undefined) {
            if (f.required) fe(f.key, `请填写${f.label}`);
            break;
          }
          const n = Number(v);
          if (!Number.isInteger(n)) {
            fe(f.key, `${f.label}必须是整数`);
            break;
          }
          if (n < f.min || n > f.max) {
            fe(f.key, `${f.label}需在 ${f.min}–${f.max}${f.unit || ''} 之间`);
            break;
          }
          values[f.key] = n;
          if (f.points_per !== undefined && n > 0) {
            const unitPts = discounted(f.points_per, rate);
            steps.push({ label: f.label, count: n, unitPoints: unitPts, unitLabel: f.unit || '', ref: { field: f.key } });
            breakdown.push({ label: f.label, detail: `${n}${f.unit || ''} × ${unitPts}`, points: unitPts * n });
          }
          break;
        }
        case 'text': {
          if (v === undefined || v === null) v = '';
          v = String(v).trim();
          if (f.required && !v) fe(f.key, `请填写${f.label}`);
          const max = f.maxlength || 100;
          if (v.length > max) fe(f.key, `${f.label}最多 ${max} 个字`);
          if (v && f.pattern && !new RegExp(f.pattern).test(v)) fe(f.key, `${f.label}格式不正确`);
          if (v) values[f.key] = v;
          break;
        }
        case 'stage_picker': {
          const cat = d.catalogs[f.catalog];
          const idx = itemIndex(cat);
          const mode = f.cost_by ? values[f.cost_by] : null;
          const countVisible = f.count && matchWhen(f.count.visible_when, values);
          if (v === undefined || v === null || v === '') v = [];
          if (!Array.isArray(v)) v = [v];
          const list = [];
          const dup = new Set();
          for (const entry of v) {
            const code = isObj(entry) ? entry.item : entry;
            if (!idx.has(code)) {
              fe(f.key, `${cat.label}「${code}」不存在`);
              continue;
            }
            if (dup.has(code)) continue;
            dup.add(code);
            const it = idx.get(code);
            if (it.disabled) {
              fe(f.key, `「${it.name}」暂不可选`);
              continue;
            }
            const cost = itemCost(it, mode);
            if (cost === null) {
              const modeLabel = f.cost_by ? (d.fields.find((x) => x.key === f.cost_by).options.find((o) => o.value === mode) || {}).label : '';
              fe(f.key, mode == null ? `请先选择${d.fields.find((x) => x.key === f.cost_by).label}` : `「${it.name}」不支持${modeLabel || mode}`);
              continue;
            }
            let count = 1;
            if (countVisible) {
              const c = isObj(entry) && entry.count !== undefined ? Number(entry.count) : f.count.default || f.count.min;
              if (!Number.isInteger(c) || c < f.count.min || c > f.count.max) {
                fe(f.key, `「${it.name}」${f.count.label || '次数'}需在 ${f.count.min}–${f.count.max} 之间`);
                continue;
              }
              count = c;
            }
            list.push({ item: code, count });
            const unitPts = discounted(cost, rate);
            steps.push({ label: it.name, count, unitPoints: unitPts, unitLabel: '次', ref: { field: f.key, item: code, group: it.group, mode } });
            breakdown.push({ label: it.name, detail: `${count} 次 × ${unitPts}`, points: unitPts * count });
          }
          if (!f.multiple && list.length > 1) fe(f.key, `${f.label}只能选一个`);
          if (f.multiple && f.max_select && list.length > f.max_select) fe(f.key, `${f.label}最多选 ${f.max_select} 个`);
          if (f.required && !list.length) fe(f.key, `请选择${f.label.replace(/^选择/, '')}`);
          values[f.key] = list;
          break;
        }
      }
    }

    // 没有按量计费的字段时，生成一个隐含步骤，代表"执行一次"
    if (!steps.length) steps.push({ label: d.name, count: 1, unitPoints: 0, unitLabel: '次', ref: { implicit: true } });
    steps.forEach((s, i) => (s.seq = i + 1));
    const units = steps.reduce((a, s) => a + s.count, 0);
    const fixedPoints = fixed.reduce((a, x) => a + x.points, 0);
    const unitTotal = steps.reduce((a, s) => a + s.unitPoints * s.count, 0);
    const rawTotal = fixedPoints + unitTotal;
    const total = applyRate(rawTotal, rate);
    if (rawTotal > total) breakdown.push({ label: '会员折扣', detail: `${rate / 10} 折`, points: total - rawTotal });
    const errors = Object.values(fieldErrors);
    if (units > d.limits.max_units) errors.push(`单个任务最多 ${d.limits.max_units} 个执行单位，当前 ${units}`);
    if (!errors.length && total <= 0) errors.push('合计积分为 0，请检查功能定价');
    return { ok: !errors.length, values, visible, fieldErrors, errors, steps, fixed, units, fixedPoints, rawTotal, total, breakdown, rate };
  }

  // 已完成 unitsDone 个执行单位时应扣的积分（按步骤顺序累加；只要开始执行就收取固定部分）
  function pointsForUnits(plan, unitsDone) {
    let left = Math.max(0, Math.min(unitsDone, plan.units));
    if (left === 0) return 0;
    if (left === plan.units) return plan.total;
    let raw = plan.fixedPoints;
    for (const s of plan.steps) {
      if (left <= 0) break;
      const take = Math.min(s.count, left);
      raw += take * s.unitPoints;
      left -= take;
    }
    return Math.min(plan.total, applyRate(raw, plan.rate));
  }
  // 每个步骤的完成情况，供进度展示
  function stepProgress(plan, unitsDone) {
    let left = Math.max(0, unitsDone);
    return plan.steps.map((s) => {
      const done = Math.min(s.count, left);
      left -= done;
      return { ...s, done };
    });
  }

  const api = { applyRate, SCHEMA_VERSION, FIELD_TYPES, REFUND_POLICIES, validateDefinition, normalize, compile, pointsForUnits, stepProgress, itemIndex, itemCost, matchWhen };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.FeatureSchema = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
