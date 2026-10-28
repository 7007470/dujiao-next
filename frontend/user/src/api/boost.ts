// BOOST-EXT: 用户端代练接口。价格只以服务端报价为准，前端不做任何计算。
import { api } from './client'

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
    api.post(signedIn ? '/boost/quote' : '/public/boost/quote', { feature_id: featureId, params }).then(unwrap<BoostPlan>),
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
  summary: () => api.get('/boost/points').then(unwrap<BoostPointsSummary>),
  ledger: (page = 1, pageSize = 20) =>
    api.get('/boost/points/ledger', { params: { page, page_size: pageSize } }).then((res: { data: any }) => ({
      items: (res.data?.data as BoostLedgerRow[]) || [],
      pagination: (res.data?.pagination as BoostPagination) || { page: 1, page_size: pageSize, total: 0, total_page: 1 },
    })),
}
