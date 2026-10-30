// BOOST-EXT: 代练后台接口。服务端是唯一的规则实现：校验、报价都调用接口，前端不自己算。
import { api } from './client'

export type BoostGameStatus = 'draft' | 'active' | 'hidden'
export type BoostServerStatus = 'active' | 'disabled'
export type BoostFeatureStatus = 'draft' | 'active' | 'disabled'
export type BoostPlatform = 'all' | 'android' | 'ios' | 'pc'

export interface BoostGame {
  id: number
  code: string
  name: string
  short_name: string
  color: string
  icon_url: string
  sort: number
  status: BoostGameStatus
  version: number
  created_at: string
  updated_at: string
  server_count?: number
  active_server_count?: number
  feature_count?: number
  active_feature_count?: number
}

export interface BoostServer {
  id: number
  game_id: number
  code: string
  name: string
  platform: BoostPlatform
  sort: number
  status: BoostServerStatus
}

// ---- 功能定义（docs/boost-feature-schema.md）----

export interface BoostOption {
  value: string
  label: string
  points?: number
}

export interface BoostCatalogItem {
  code: string
  name: string
  cost: number | Record<string, number>
  tags?: string[]
  disabled?: boolean
}

export interface BoostCatalog {
  label: string
  groups: { code: string; name: string; items: BoostCatalogItem[] }[]
}

export interface BoostField {
  key: string
  type: 'radio' | 'select' | 'checkbox' | 'switch' | 'number' | 'text' | 'stage_picker'
  label: string
  help?: string
  required?: boolean
  default?: unknown
  visible_when?: Record<string, unknown>
  options?: BoostOption[]
  min_select?: number
  max_select?: number
  points?: number
  min?: number
  max?: number
  unit?: string
  points_per?: number
  maxlength?: number
  pattern?: string
  placeholder?: string
  catalog?: string
  multiple?: boolean
  cost_by?: string
  count?: { label?: string; min: number; max: number; default?: number; visible_when?: Record<string, unknown> }
}

export interface BoostDefinition {
  schema_version?: number
  code: string
  name: string
  description?: string
  base_points?: number
  member_discount?: boolean
  catalogs?: Record<string, BoostCatalog>
  fields: BoostField[]
  [key: string]: unknown
}

export interface BoostFeature {
  id: number
  game_id: number
  code: string
  name: string
  status: BoostFeatureStatus
  sort: number
  version: number
  definition: BoostDefinition
  created_at: string
  updated_at: string
}

export interface BoostFeatureVersion {
  feature_id: number
  version: number
  admin_id?: number
  created_at: string
  definition?: BoostDefinition
}

export interface BoostIssue {
  path: string
  msg: string
}

export interface BoostValidateResult {
  ok: boolean
  errors: BoostIssue[]
  warnings: BoostIssue[]
}

export interface BoostStep {
  seq: number
  label: string
  count: number
  unitPoints: number
  unitLabel: string
  ref: Record<string, unknown>
}

// Compile 结果，键名与服务端 featuredef.Plan 一致
export interface BoostPlan {
  ok: boolean
  values: Record<string, unknown>
  visible: Record<string, boolean>
  fieldErrors: Record<string, string>
  errors: string[]
  steps: BoostStep[]
  fixed: { label: string; points: number }[]
  units: number
  fixedPoints: number
  rawTotal: number
  total: number
  breakdown: { label: string; detail: string; points: number }[]
  rate: number
}

export interface BoostFeatureSaveResult {
  feature: BoostFeature
  changed: boolean
  warnings: BoostIssue[]
}

const unwrap = <T>(res: { data: any }) => res.data?.data as T

export const boostAdminAPI = {
  listGames: () => api.get('/admin/boost/games').then(unwrap<BoostGame[]>),
  createGame: (data: Partial<BoostGame>) => api.post('/admin/boost/games', data).then(unwrap<BoostGame>),
  updateGame: (id: number, data: Partial<BoostGame>) => api.put(`/admin/boost/games/${id}`, data).then(unwrap<BoostGame>),
  setGameStatus: (id: number, status: BoostGameStatus) =>
    api.post(`/admin/boost/games/${id}/status`, { status }).then(unwrap<BoostGame>),

  listServers: (gameId: number) => api.get(`/admin/boost/games/${gameId}/servers`).then(unwrap<BoostServer[]>),
  createServer: (gameId: number, data: Partial<BoostServer>) =>
    api.post(`/admin/boost/games/${gameId}/servers`, data).then(unwrap<BoostServer>),
  updateServer: (gameId: number, serverId: number, data: Partial<BoostServer>) =>
    api.put(`/admin/boost/games/${gameId}/servers/${serverId}`, data).then(unwrap<BoostServer>),
  setServerStatus: (gameId: number, serverId: number, status: BoostServerStatus) =>
    api.post(`/admin/boost/games/${gameId}/servers/${serverId}/status`, { status }).then(unwrap<BoostServer>),

  listFeatures: (gameId: number) => api.get(`/admin/boost/games/${gameId}/features`).then(unwrap<BoostFeature[]>),
  getFeature: (id: number) => api.get(`/admin/boost/features/${id}`).then(unwrap<BoostFeature>),
  createFeature: (gameId: number, definition: string) =>
    api.post('/admin/boost/features', { game_id: gameId, definition }).then(unwrap<BoostFeatureSaveResult>),
  updateFeature: (id: number, version: number, definition: string) =>
    api.put(`/admin/boost/features/${id}`, { version, definition }).then(unwrap<BoostFeatureSaveResult>),
  setFeatureStatus: (id: number, status: BoostFeatureStatus) =>
    api.post(`/admin/boost/features/${id}/status`, { status }).then(unwrap<BoostFeature>),
  moveFeature: (id: number, delta: -1 | 1) =>
    api.post(`/admin/boost/features/${id}/move`, { delta }).then(unwrap<BoostFeature[]>),
  listFeatureVersions: (id: number) => api.get(`/admin/boost/features/${id}/versions`).then(unwrap<BoostFeatureVersion[]>),
  getFeatureVersion: (id: number, version: number) =>
    api.get(`/admin/boost/features/${id}/versions/${version}`).then(unwrap<BoostFeatureVersion>),

  // 编辑器实时校验：非法定义也返回 200 + ok=false，不弹错误提示
  validateFeature: (definition: string) =>
    api.post('/admin/boost/features/validate', { definition }).then(unwrap<BoostValidateResult>),
  previewFeature: (definition: string, params: Record<string, unknown>, discountRate: number) =>
    api.post('/admin/boost/features/preview', { definition, params, discount_rate: discountRate }).then(unwrap<BoostPlan>),
}

// ---- 积分（S4）----

export type BoostLedgerType = 'purchase' | 'grant' | 'hold' | 'release' | 'consume' | 'expire' | 'revoke' | 'admin_adjust'

export interface BoostPointPack {
  id: number
  product_id: number
  sku_id: number
  name: string
  points: number
  bonus: number
  valid_days?: number | null
  active: boolean
  product_slug: string
  product_title: string
  product_active: boolean
  fulfillment_type: string
  sku_code: string
  sku_active: boolean
  price: string
}

export interface BoostPointAccount {
  user_id: number
  email: string
  nickname: string
  available: number
  frozen: number
  updated_at: string
}

export interface BoostPointLot {
  id: number
  source_type: 'order' | 'grant' | 'promo'
  source_ref: string
  amount: number
  remaining: number
  status: 'active' | 'exhausted' | 'expired' | 'revoked'
  expires_at?: string | null
  suggest_refund?: string | null
  created_at: string
}

export interface BoostLedgerEntry {
  id: number
  user_id: number
  type: BoostLedgerType
  delta_available: number
  delta_frozen: number
  available_after: number
  frozen_after: number
  biz_key: string
  task_id?: number
  order_id?: number
  operator_admin_id?: number
  remark: string
  created_at: string
}

export interface BoostPointsSummary {
  available: number
  frozen: number
  expiring_soon: { lot_id: number; amount: number; expires_at: string; source_type: string }[]
}

export interface BoostRevokeResult {
  order_no: string
  total: number
  used: number
  revoked: number
  suggest_refund: string
  already: boolean
}

export interface BoostPointsAdjust {
  user_id: number
  amount: number
  valid_days?: number | null
  reason: string
  request_id: string
}

export interface BoostPagination {
  page: number
  page_size: number
  total: number
  total_page: number
}

export interface BoostPage<T> {
  items: T[]
  pagination: BoostPagination
}

const unwrapPage = <T>(res: { data: any }): BoostPage<T> => ({
  items: (res.data?.data as T[]) || [],
  pagination: res.data?.pagination || { page: 1, page_size: 20, total: 0, total_page: 1 },
})

export const boostPointsAPI = {
  listPacks: () => api.get('/admin/boost/point-packs').then(unwrap<BoostPointPack[]>),
  createPack: (data: Partial<BoostPointPack>) => api.post('/admin/boost/point-packs', data).then(unwrap<BoostPointPack>),
  updatePack: (id: number, data: Partial<BoostPointPack>) => api.put(`/admin/boost/point-packs/${id}`, data).then(unwrap<BoostPointPack>),

  listAccounts: (params: { keyword?: string; user_id?: number; page?: number; page_size?: number }) =>
    api.get('/admin/boost/point-accounts', { params }).then(unwrapPage<BoostPointAccount>),
  listLedger: (params: { user_id?: number; type?: string; page?: number; page_size?: number }) =>
    api.get('/admin/boost/point-ledger', { params }).then(unwrapPage<BoostLedgerEntry>),
  userPoints: (userId: number) =>
    api.get(`/admin/boost/users/${userId}/points`).then(unwrap<{ summary: BoostPointsSummary; lots: BoostPointLot[] }>),
  grant: (data: BoostPointsAdjust) => api.post('/admin/boost/points/grant', data).then(unwrap<BoostLedgerEntry>),
  deduct: (data: BoostPointsAdjust) => api.post('/admin/boost/points/deduct', data).then(unwrap<BoostLedgerEntry>),
  revokeOrder: (orderNo: string) => api.post('/admin/boost/points/revoke-order', { order_no: orderNo }).then(unwrap<BoostRevokeResult>),
}

export const ledgerTypeText: Record<BoostLedgerType, string> = {
  purchase: '购买',
  grant: '发放',
  hold: '冻结',
  release: '退回',
  consume: '消耗',
  expire: '到期',
  revoke: '撤回',
  admin_adjust: '扣减',
}

// ---- 绑定与任务（S5）----

export type BoostBindingStatus = 'PENDING' | 'PROCESSING' | 'WAIT_INPUT' | 'WAIT_USER' | 'SUCCESS' | 'FAIL' | 'CANCELLED'
export type BoostTaskStatus = 'PENDING' | 'PROCESSING' | 'WAIT_INPUT' | 'SUCCESS' | 'FAIL' | 'CANCELLED'

export interface BoostInputView {
  id: number
  kind: string
  prompt: string
  status: string
  expires_at: string
}

export interface BoostBinding {
  id: number
  user_id: number
  game_id: number
  server_id: number
  display_name: string
  login_masked: string
  status: BoostBindingStatus
  attempt: number
  max_attempts: number
  last_error_code: string
  user_message: string
  verified_at?: string
  created_at: string
  game_name: string
  game_code: string
  server_name: string
  user_email?: string
  input?: BoostInputView | null
}

export interface BoostTask {
  id: number
  steps_done_json?: string
  plan_json?: string
  task_no: string
  user_id: number
  binding_id: number
  game_id: number
  feature_id: number
  feature_version: number
  summary: string
  units: number
  units_done: number
  points_held: number
  points_consumed: number
  points_released: number
  status: BoostTaskStatus
  cancel_requested: boolean
  progress_percent: number
  progress_text: string
  error_code: string
  attempt: number
  max_attempts: number
  deadline_at?: string
  started_at?: string
  finished_at?: string
  created_at: string
  game_name: string
  game_code: string
  binding_name: string
  login_masked: string
  user_email?: string
}

export interface BoostTaskEvent {
  id: number
  seq: number
  type: string
  message: string
  created_at: string
}

export interface BoostSettlement {
  lines: { seq: number; label: string; count: number; done: number; unit_points: number; points: number }[]
  fixed_points: number
  fixed_charged: boolean
  raw_points: number
  rate: number
  by_units: number
  held: number
  consumed: number
  released: number
  final: boolean
  rule: string
}

export interface BoostTaskDetail {
  task: BoostTask
  snapshot: { code: string; name: string; version: number; refund_policy: string; timeout_minutes: number; rate: number }
  steps: { seq: number; label: string; count: number; done: number; unit_points: number }[]
  breakdown: { label: string; detail: string; points: number }[]
  settlement?: BoostSettlement // 旧版后端没有这个字段
  events: BoostTaskEvent[]
  input: BoostInputView | null
}

export interface BoostSimulatorQueue {
  enabled: boolean
  error_codes: string[]
  tasks?: BoostTask[]
  bindings?: BoostBinding[]
}

export type BoostSimAction = 'start' | 'progress' | 'need_input' | 'finish' | 'fail' | 'cancel_ack'

export interface BoostSimRequest {
  kind: 'binding' | 'task'
  id: number
  action: BoostSimAction
  steps?: { seq: number; done: number }[]
  error_code?: string
  input_kind?: string
  prompt?: string
  message?: string
}

export const boostTaskAPI = {
  listTasks: (params: { status?: string; task_no?: string; user_id?: number; game_id?: number; page?: number; page_size?: number }) =>
    api.get('/admin/boost/tasks', { params }).then(unwrapPage<BoostTask>),
  getTask: (taskNo: string) =>
    api.get(`/admin/boost/tasks/${encodeURIComponent(taskNo)}`).then(unwrap<{ detail: BoostTaskDetail; simulator: boolean }>),
  cancelTask: (taskNo: string) =>
    api.post(`/admin/boost/tasks/${encodeURIComponent(taskNo)}/cancel`).then(unwrap<{ status: string; cancel_requested: boolean; points_consumed: number; points_released: number }>),
  listBindings: (params: { status?: string; user_id?: number; game_id?: number; include_deleted?: string; page?: number; page_size?: number }) =>
    api.get('/admin/boost/bindings', { params }).then(unwrapPage<BoostBinding>),
  simulatorQueue: () => api.get('/admin/boost/simulator').then(unwrap<BoostSimulatorQueue>),
  simulate: (data: BoostSimRequest) => api.post('/admin/boost/simulator', data).then(unwrap<Record<string, unknown>>),
  reap: () => api.post('/admin/boost/simulator/reap').then(unwrap<{ changed: number }>),
}

export const taskStatusText: Record<BoostTaskStatus, string> = {
  PENDING: '排队中',
  PROCESSING: '执行中',
  WAIT_INPUT: '等待验证码',
  SUCCESS: '已完成',
  FAIL: '失败',
  CANCELLED: '已取消',
}

export const bindingStatusText: Record<BoostBindingStatus, string> = {
  PENDING: '等待验证',
  PROCESSING: '验证中',
  WAIT_INPUT: '等待验证码',
  WAIT_USER: '等待游戏内确认',
  SUCCESS: '已验证',
  FAIL: '失败',
  CANCELLED: '已取消',
}

export const statusTone = (s: string) =>
  s === 'SUCCESS'
    ? 'bg-emerald-50 text-emerald-700 border-emerald-200'
    : s === 'FAIL'
      ? 'bg-red-50 text-red-700 border-red-200'
      : s === 'CANCELLED'
        ? 'bg-muted text-muted-foreground border-border'
        : s.startsWith('WAIT')
          ? 'bg-amber-50 text-amber-800 border-amber-200'
          : 'bg-sky-50 text-sky-700 border-sky-200'
