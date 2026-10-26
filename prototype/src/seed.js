/* 演示数据。游戏、区服、代练功能全部通过后台接口创建（和管理员在界面上操作走同一条路径）。
 * 全部是虚构内容，不对应任何真实游戏。 */
(function (root) {
  'use strict';

  const isNode = typeof module !== 'undefined' && module.exports;
  const G = isNode ? require('./engine.js') : root.GameEngine;
  if (isNode) require('./boost.js');
  const { Engine } = G;

  // ---------- 示例功能定义（也是 docs/boost-feature-schema.md 的范例） ----------
  function chapter(code, name, stages) {
    return { code, name, items: stages };
  }
  const DEFS = {
    // 星落幻境：主线关卡，通关 / 扫荡两种模式，不同模式不同价格，扫荡可选次数
    starfall_main: {
      schema_version: 1,
      code: 'main_stage',
      name: '主线关卡',
      description: '选择要处理的主线关卡。通关：首次打过关卡并拿三星；扫荡：已三星的关卡重复刷材料。',
      capability: 'main_stage',
      timeout_minutes: 240,
      max_active_per_user: 3,
      refund_policy: 'prorate',
      member_discount: true,
      catalogs: {
        main: {
          label: '关卡',
          groups: [
            chapter('ch1', '第一章 · 晨曦平原', [
              { code: '1-1', name: '1-1 迷雾入口', cost: { clear: 5, sweep: 2 }, tags: ['经验'] },
              { code: '1-2', name: '1-2 旧哨塔', cost: { clear: 5, sweep: 2 }, tags: ['金币'] },
              { code: '1-3', name: '1-3 断桥', cost: { clear: 6, sweep: 2 }, tags: ['突破材料'] },
              { code: '1-4', name: '1-4 守望者（BOSS）', cost: { clear: 10, sweep: 4 }, tags: ['BOSS', '武器'] },
            ]),
            chapter('ch2', '第二章 · 雾隐山谷', [
              { code: '2-1', name: '2-1 林间小径', cost: { clear: 7, sweep: 3 }, tags: ['经验'] },
              { code: '2-2', name: '2-2 采药人营地', cost: { clear: 7, sweep: 3 }, tags: ['天赋材料'] },
              { code: '2-3', name: '2-3 瀑布洞窟', cost: { clear: 8, sweep: 3 }, tags: ['突破材料'] },
              { code: '2-4', name: '2-4 雾影狼王（BOSS）', cost: { clear: 14, sweep: 5 }, tags: ['BOSS', '圣遗物'] },
            ]),
            chapter('ch3', '第三章 · 沉星海岸', [
              { code: '3-1', name: '3-1 潮汐礁石', cost: { clear: 9, sweep: 4 } },
              { code: '3-2', name: '3-2 沉船残骸', cost: { clear: 9, sweep: 4 }, tags: ['金币'] },
              { code: '3-3', name: '3-3 灯塔顶层', cost: { clear: 12 }, tags: ['剧情关', '不可扫荡'] },
              { code: '3-4', name: '3-4 深海巨像（BOSS）', cost: { clear: 20 }, tags: ['BOSS', '限时'], disabled: true },
            ]),
          ],
        },
      },
      fields: [
        { key: 'mode', type: 'radio', label: '模式', required: true, default: 'sweep', options: [{ value: 'clear', label: '通关（拿三星）' }, { value: 'sweep', label: '扫荡' }] },
        { key: 'stages', type: 'stage_picker', label: '选择关卡', catalog: 'main', multiple: true, max_select: 12, required: true, cost_by: 'mode', count: { label: '扫荡次数', min: 1, max: 99, default: 10, visible_when: { mode: 'sweep' } } },
        { key: 'use_stamina_potion', type: 'switch', label: '体力不足时使用体力药', help: '只消耗背包里已有的体力药，不会花钱购买', default: false, visible_when: { mode: 'sweep' } },
        { key: 'auto_team', type: 'switch', label: '自动编队', help: '关闭则使用你在游戏里保存的第一队', default: true, points: 0 },
        { key: 'remark', type: 'text', label: '备注', maxlength: 60, placeholder: '例如：第 3 关请带治疗' },
      ],
      limits: { max_units: 500 },
    },
    starfall_daily: {
      schema_version: 1,
      code: 'daily',
      name: '每日委托',
      description: '完成当日 4 个委托并领取奖励，可附加领取邮件、派遣。',
      capability: 'daily',
      timeout_minutes: 30,
      max_active_per_user: 3,
      refund_policy: 'full_on_system_fail',
      member_discount: true,
      base_points: 20,
      fields: [
        { key: 'extras', type: 'checkbox', label: '附加项目', options: [{ value: 'mail', label: '领取邮件', points: 0 }, { value: 'dispatch', label: '探索派遣（收取并重新派出）', points: 5 }, { value: 'shop', label: '兑换商店每日免费物品', points: 3 }], default: ['mail'] },
      ],
    },
    starfall_abyss: {
      schema_version: 1,
      code: 'abyss',
      name: '深境螺旋',
      description: '挑战深境螺旋指定楼层，按层计费。未能三星的层按完成层数扣点。',
      capability: 'abyss',
      timeout_minutes: 180,
      max_active_per_user: 1,
      refund_policy: 'prorate',
      member_discount: true,
      catalogs: {
        floors: {
          label: '楼层',
          groups: [
            { code: 'low', name: '低层（1–8）', items: [{ code: 'f5', name: '第 5 层', cost: 6 }, { code: 'f6', name: '第 6 层', cost: 6 }, { code: 'f7', name: '第 7 层', cost: 8 }, { code: 'f8', name: '第 8 层', cost: 8 }] },
            { code: 'high', name: '高层（9–12）', items: [{ code: 'f9', name: '第 9 层', cost: 15, tags: ['本期'] }, { code: 'f10', name: '第 10 层', cost: 18, tags: ['本期'] }, { code: 'f11', name: '第 11 层', cost: 25, tags: ['本期'] }, { code: 'f12', name: '第 12 层', cost: 35, tags: ['本期', '难'] }] },
          ],
        },
      },
      fields: [
        { key: 'floors', type: 'stage_picker', label: '挑战楼层', catalog: 'floors', multiple: true, required: true },
        { key: 'target', type: 'select', label: '目标', default: 'three_star', options: [{ value: 'pass', label: '通过即可' }, { value: 'three_star', label: '尽量三星', points: 10 }] },
      ],
    },
    starfall_idle: {
      schema_version: 1,
      code: 'idle_farm',
      name: '挂机采集',
      description: '在指定区域循环采集材料，按运行分钟扣点。',
      capability: 'idle',
      timeout_minutes: 540,
      max_active_per_user: 2,
      refund_policy: 'prorate',
      member_discount: false,
      fields: [
        { key: 'area', type: 'select', label: '采集区域', required: true, options: [{ value: 'plain', label: '晨曦平原' }, { value: 'valley', label: '雾隐山谷' }, { value: 'coast', label: '沉星海岸' }] },
        { key: 'minutes', type: 'number', label: '运行时长', unit: '分钟', min: 30, max: 480, default: 60, points_per: 1 },
      ],
    },
    // 铁冠征途：另一种形态的游戏，演示同一套 JSON 适配不同玩法
    ironcrest_farm: {
      schema_version: 1,
      code: 'farm',
      name: '资源收取 + 联盟帮助',
      description: '收取全部资源田、点满联盟帮助、领取在线奖励。',
      capability: 'farm',
      timeout_minutes: 20,
      refund_policy: 'full_on_system_fail',
      base_points: 10,
      fields: [{ key: 'heal', type: 'switch', label: '顺便治疗伤兵', default: false, points: 2 }],
    },
    ironcrest_march: {
      schema_version: 1,
      code: 'march',
      name: '自动打野',
      description: '按指定野怪等级循环出征，按完成次数扣点。',
      capability: 'march',
      timeout_minutes: 240,
      max_active_per_user: 2,
      refund_policy: 'prorate',
      member_discount: false,
      catalogs: {
        monsters: {
          label: '野怪',
          groups: [
            { code: 'normal', name: '普通野怪', items: [{ code: 'lv10', name: '10 级野怪', cost: 2 }, { code: 'lv15', name: '15 级野怪', cost: 3 }, { code: 'lv20', name: '20 级野怪', cost: 4 }] },
            { code: 'elite', name: '精英', items: [{ code: 'elite25', name: '25 级精英', cost: 8, tags: ['需要集结'] }] },
          ],
        },
      },
      fields: [
        { key: 'target', type: 'stage_picker', label: '目标野怪', catalog: 'monsters', multiple: false, required: true, count: { label: '出征次数', min: 1, max: 50, default: 10 } },
        { key: 'troop', type: 'radio', label: '出征部队', default: 'cavalry', options: [{ value: 'cavalry', label: '骑兵' }, { value: 'infantry', label: '步兵' }, { value: 'archer', label: '弓兵' }] },
      ],
    },
  };

  function seed() {
    const e = new Engine();
    const s = e.s;
    s.memberLevels = [
      { id: 1, slug: 'default', name: '普通会员', discountRate: 100, active: true },
      { id: 2, slug: 'gold', name: '黄金会员', discountRate: 90, active: true },
    ];
    s.users = [
      { id: 1, name: '小林', email: 'lin@example.test', levelId: 1 },
      { id: 2, name: '阿杰（黄金）', email: 'jie@example.test', levelId: 2 },
    ];
    s.admins = [
      { id: 1, name: '运营-周', perms: ['boost:*', 'order:refund'] },
      { id: 2, name: '客服-何', perms: ['boost:read'] },
    ];
    s.wallets = { 1: 0, 2: 0 };
    e.walletChange(1, 30000, 'recharge', 'seed:recharge:1', '演示充值');
    e.walletChange(2, 50000, 'recharge', 'seed:recharge:2', '演示充值');

    // 游戏、区服、功能 —— 全部通过后台接口
    const A = 1;
    const sf = e.adminSaveGame(A, { code: 'starfall', name: '星落幻境', short: '星落', color: '#2f6f6a', sort: 1 });
    const ic = e.adminSaveGame(A, { code: 'ironcrest', name: '铁冠征途', short: '铁冠', color: '#8a5a2b', sort: 2 });
    e.adminSaveServer(A, { gameId: sf.id, code: 'cn-sky', name: '国服 · 天空岛' });
    e.adminSaveServer(A, { gameId: sf.id, code: 'cn-world', name: '国服 · 世界树' });
    e.adminSaveServer(A, { gameId: sf.id, code: 'asia', name: '亚服' });
    e.adminSaveServer(A, { gameId: ic.id, code: 's101', name: '101 区 · 赤焰', platform: 'android' });
    e.adminSaveServer(A, { gameId: ic.id, code: 's102', name: '102 区 · 霜原', platform: 'ios' });
    for (const k of ['starfall_main', 'starfall_daily', 'starfall_abyss', 'starfall_idle']) e.adminSetFeatureStatus(A, e.adminSaveFeature(A, { gameId: sf.id, definition: DEFS[k] }).id, 'active');
    for (const k of ['ironcrest_farm', 'ironcrest_march']) e.adminSetFeatureStatus(A, e.adminSaveFeature(A, { gameId: ic.id, definition: DEFS[k] }).id, 'active');
    e.adminSetGameStatus(A, sf.id, 'active');
    e.adminSetGameStatus(A, ic.id, 'active');

    e.adminSavePack(A, { name: '500 点', price: 50, points: 500, bonus: 0, validDays: 365 });
    e.adminSavePack(A, { name: '1,200 点（送 100）', price: 110, points: 1100, bonus: 100, validDays: 365 });
    e.adminSavePack(A, { name: '3,000 点（送 400）', price: 260, points: 2600, bonus: 400, validDays: 365 });

    e.createWorker(null, { name: '机房 A-01', capabilities: [{ game: 'starfall', features: ['bind', 'main_stage', 'daily', 'abyss', 'idle'] }], maxConcurrency: 3 });
    e.createWorker(null, { name: '机房 A-02', capabilities: [{ game: 'starfall', features: ['bind', 'main_stage', 'daily'] }], maxConcurrency: 2 });
    e.createWorker(null, { name: '铁冠专用 B-01', capabilities: [{ game: 'ironcrest', features: ['bind', 'farm', 'march'] }], maxConcurrency: 2 });

    e.grantPoints(2, 300, 'promo', 'welcome:2', 30, '新用户体验积分');
    s.audit = [];
    s.log = [];
    e.emit('system', '演示数据已就绪：2 款游戏、6 个代练功能（全部由 JSON 定义）、3 个执行端');
    return e;
  }

  const api = { seed, DEFS };
  if (isNode) module.exports = api;
  else root.GameSeed = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
