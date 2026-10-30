// BOOST-EXT: 用户端代练接口。价格只以服务端报价为准，前端不做任何计算。
// 公开接口用 api（不带登录凭证），登录后接口必须用 userApi（带 Authorization），否则服务端返回 401
import { api, userApi } from './client'

export interface BoostServer {
  id: number
  code: string
  name: string
  platform: 'all' | 'android' | 'ios' | 'pc'
}

export interface BoostGame {
  id: number
  code: string
  name: string
  short_name: string
  color: string
  icon_url: string
  servers: BoostServer[]
  feature_count: number
}

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

export interface BoostField {
  key: string
  type: 'radio' | 'select' | 'checkbox' | 'switch' | 'number' | 'text' | 'stage_picker'
  label: string
  help?: string
  required?: boolean
  options?: BoostOption[]
  points?: number
  min?: number
  max?: number
  unit?: string
  maxlength?: number
  placeholder?: string
  catalog?: string
  multiple?: boolean
  cost_by?: string
  count?: { label?: string; min: number; max: number; default?: number; visible_when?: Record<string, unknown> }
}

export interface BoostDefinition {
  code: string
  name: string
  description?: string
  catalogs?: Record<string, { label: string; groups: { code: string; name: string; items: BoostCatalogItem[] }[] }>
  fields: BoostField[]
}

export interface BoostFeature {
  id: number
  code: string
  name: string
  version: number
  definition: BoostDefinition
}

export interface BoostPlan {
  ok: boolean
  values: Record<string, unknown>
  visible: Record<string, boolean>
  fieldErrors: Record<string, string>
  errors: string[]
  steps: { seq: number; label: string; count: number }[]
  units: number
  rawTotal: number
  total: number
  breakdown: { label: string; detail: string; points: number }[]
  rate: number
}

const unwrap = <T>(res: { data: any }) => res.data?.data as T

export const boostAPI = {
  listGames: () => api.get('/public/boost/games').then(unwrap<BoostGame[]>),
  getGame: (code: string) =>
    api.get(`/public/boost/games/${encodeURIComponent(code)}`).then(unwrap<{ game: BoostGame; features: BoostFeature[] }>),
  // 登录用户走 /boost/quote（按会员等级打折），未登录走公开接口（原价）
  quote: (featureId: number, params: Record<string, unknown>, signedIn: boolean) =>
    (signedIn ? userApi.post('/boost/quote', { feature_id: featureId, params }) : api.post('/public/boost/quote', { feature_id: featureId, params })).then(unwrap<BoostPlan>),
}

// ---- 积分（S4）----

export interface BoostPointPack {
  id: number
  name: string
  points: number
  bonus: number
  valid_days?: number | null
  product_id: number
  product_slug: string
  sku_id: number
  price: string
}

export interface BoostPointsSummary {
  available: number
  frozen: number
  expiring_soon: { lot_id: number; amount: number; expires_at: string; source_type: string }[]
}

export type BoostLedgerType = 'purchase' | 'grant' | 'hold' | 'release' | 'consume' | 'expire' | 'revoke' | 'admin_adjust'

export interface BoostLedgerRow {
  id: number
  type: BoostLedgerType
  delta_available: number
  delta_frozen: number
  available_after: number
  frozen_after: number
  task_id?: number
  order_id?: number
  remark: string
  created_at: string
}

export interface BoostPagination {
  page: number
  page_size: number
  total: number
  total_page: number
}

export const boostPointsAPI = {
  packs: () => api.get('/public/boost/point-packs').then(unwrap<BoostPointPack[]>),
  summary: () => userApi.get('/boost/points').then(unwrap<BoostPointsSummary>),
  ledger: (page = 1, pageSize = 20) =>
    userApi.get('/boost/points/ledger', { params: { page, page_size: pageSize } }).then((res: { data: any }) => ({
      items: (res.data?.data as BoostLedgerRow[]) || [],
      pagination: (res.data?.pagination as BoostPagination) || { page: 1, page_size: pageSize, total: 0, total_page: 1 },
    })),
}

// ---- 游戏账号绑定与代练任务（S5）----
// 账号密码只在提交时发给服务端，服务端加密保存；之后接口只返回掩码登录名。

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
  game_id: number
  server_id: number
  display_name: string
  login_masked: string
  status: BoostBindingStatus
  last_error_code: string
  user_message: string
  verified_at?: string
  created_at: string
  game_name: string
  game_code: string
  server_name: string
  input?: BoostInputView | null
}

export interface BoostTask {
  id: number
  task_no: string
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
  deadline_at?: string
  started_at?: string
  finished_at?: string
  created_at: string
  game_name: string
  game_code: string
  binding_name: string
  login_masked: string
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
  events: { id: number; seq: number; type: string; message: string; created_at: string }[]
  input: BoostInputView | null
}

export interface BoostCancelResult {
  status: BoostTaskStatus
  cancel_requested: boolean
  points_consumed: number
  points_released: number
}

const unwrapList = <T>(res: { data: any }) => ({
  items: (res.data?.data as T[]) || [],
  pagination: (res.data?.pagination || { page: 1, page_size: 20, total: 0, total_page: 1 }) as BoostPagination,
})

export const boostTaskAPI = {
  bindings: (gameId?: number) =>
    userApi.get('/boost/bindings', { params: { game_id: gameId || undefined, page_size: 100 } }).then(unwrapList<BoostBinding>),
  createBinding: (data: { game_id: number; server_id: number; display_name: string; login: string; password: string }) =>
    userApi.post('/boost/bindings', data).then(unwrap<BoostBinding>),
  deleteBinding: (id: number) => userApi.delete(`/boost/bindings/${id}`).then(unwrap<{ ok: boolean }>),
  bindingInput: (id: number, value: string) => userApi.post(`/boost/bindings/${id}/input`, { value }).then(unwrap<{ ok: boolean }>),
  bindingConfirm: (id: number) => userApi.post(`/boost/bindings/${id}/confirm`).then(unwrap<{ ok: boolean }>),

  // 不传任何价格：服务端按 params 重新编译报价并冻结
  createTask: (data: { binding_id: number; feature_id: number; params: Record<string, unknown>; idempotency_key: string }) =>
    userApi.post('/boost/tasks', data).then(unwrap<{ task: BoostTask; duplicated: boolean }>),
  tasks: (status = '', page = 1) =>
    userApi.get('/boost/tasks', { params: { status: status || undefined, page, page_size: 20 } }).then(unwrapList<BoostTask>),
  task: (taskNo: string) => userApi.get(`/boost/tasks/${encodeURIComponent(taskNo)}`).then(unwrap<BoostTaskDetail>),
  cancelTask: (taskNo: string) => userApi.post(`/boost/tasks/${encodeURIComponent(taskNo)}/cancel`).then(unwrap<BoostCancelResult>),
  taskInput: (taskNo: string, value: string) =>
    userApi.post(`/boost/tasks/${encodeURIComponent(taskNo)}/input`, { value }).then(unwrap<{ ok: boolean }>),
}

export const isActiveTask = (s: string) => s === 'PENDING' || s === 'PROCESSING' || s === 'WAIT_INPUT'

export const statusTone = (s: string) =>
  s === 'SUCCESS'
    ? 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950 dark:text-emerald-300'
    : s === 'FAIL'
      ? 'bg-red-50 text-red-700 border-red-200 dark:bg-red-950 dark:text-red-300'
      : s === 'CANCELLED'
        ? 'bg-muted text-muted-foreground border-border'
        : s.startsWith('WAIT')
          ? 'bg-amber-50 text-amber-800 border-amber-200 dark:bg-amber-950 dark:text-amber-300'
          : 'bg-sky-50 text-sky-700 border-sky-200 dark:bg-sky-950 dark:text-sky-300'

/** 每次打开下单确认生成一次；网络重试时不变，服务端据此只建一个任务 */
export const newIdempotencyKey = () =>
  (typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`)
