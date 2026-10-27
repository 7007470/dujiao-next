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
