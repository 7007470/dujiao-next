<script setup lang="ts">
// BOOST-EXT: 代练积分后台：积分包配置、用户积分账户、流水、管理员发放 / 扣减、积分包退款前撤回。
// 积分数量、到期、建议退款金额都以服务端为准，前端只展示。
import { computed, onMounted, reactive, ref } from 'vue'
import {
  boostPointsAPI,
  ledgerTypeText,
  type BoostLedgerEntry,
  type BoostPagination,
  type BoostPointAccount,
  type BoostPointLot,
  type BoostPointPack,
  type BoostPointsSummary,
  type BoostRevokeResult,
} from '@/api/boost'
import { adminAPI } from '@/api/admin'
import type { AdminProduct } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogHeader, DialogScrollContent, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import TableSkeleton from '@/components/TableSkeleton.vue'
import ListPagination from '@/components/ListPagination.vue'
import { notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'

type Tab = 'accounts' | 'packs' | 'ledger' | 'revoke'
const tab = ref<Tab>('accounts')
const tabs: { id: Tab; label: string }[] = [
  { id: 'accounts', label: '用户积分' },
  { id: 'packs', label: '积分包' },
  { id: 'ledger', label: '积分流水' },
  { id: 'revoke', label: '退款撤回' },
]

const fmtTime = (v?: string | null) => (v ? new Date(v).toLocaleString() : '—')
const fmtDelta = (n: number) => (n > 0 ? `+${n}` : String(n))
const newRequestId = () => `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
const emptyPage = (): BoostPagination => ({ page: 1, page_size: 20, total: 0, total_page: 1 })

// ---------------------------------------------------------------------------
// 用户积分
// ---------------------------------------------------------------------------
const accounts = ref<BoostPointAccount[]>([])
const accountsPage = ref<BoostPagination>(emptyPage())
const accountsLoading = ref(true)
const keyword = ref('')

const loadAccounts = async (page = 1) => {
  accountsLoading.value = true
  try {
    const kw = keyword.value.trim()
    const asId = /^\d+$/.test(kw) ? Number(kw) : undefined
    const res = await boostPointsAPI.listAccounts({ keyword: asId ? undefined : kw || undefined, user_id: asId, page, page_size: accountsPage.value.page_size })
    accounts.value = res.items
    accountsPage.value = res.pagination
  } catch {
    // client 已提示
  } finally {
    accountsLoading.value = false
  }
}

// 用户详情
const detailOpen = ref(false)
const detailUser = ref<{ id: number; email: string } | null>(null)
const detailSummary = ref<BoostPointsSummary | null>(null)
const detailLots = ref<BoostPointLot[]>([])
const detailLedger = ref<BoostLedgerEntry[]>([])
const lotStatusText: Record<BoostPointLot['status'], string> = { active: '有效', exhausted: '已用完', expired: '已到期', revoked: '已撤回' }
const lotSourceText: Record<BoostPointLot['source_type'], string> = { order: '购买', grant: '发放', promo: '活动' }

const openDetail = async (userId: number, email = '') => {
  detailUser.value = { id: userId, email }
  detailOpen.value = true
  detailSummary.value = null
  try {
    const [points, ledger] = await Promise.all([boostPointsAPI.userPoints(userId), boostPointsAPI.listLedger({ user_id: userId, page_size: 50 })])
    detailSummary.value = points.summary
    detailLots.value = points.lots || []
    detailLedger.value = ledger.items
  } catch {
    detailOpen.value = false
  }
}

// 发放 / 扣减
const adjustOpen = ref(false)
const adjustSaving = ref(false)
const adjustError = ref('')
const adjust = reactive({ mode: 'grant' as 'grant' | 'deduct', user_id: 0, email: '', amount: 100, valid_days: 0, reason: '', request_id: '' })

const openAdjust = (mode: 'grant' | 'deduct', userId = 0, email = '') => {
  Object.assign(adjust, { mode, user_id: userId, email, amount: 100, valid_days: 0, reason: '', request_id: newRequestId() })
  adjustError.value = ''
  adjustOpen.value = true
}

const submitAdjust = async () => {
  adjustError.value = ''
  const amount = Math.trunc(Number(adjust.amount))
  if (!adjust.user_id || !(amount > 0)) {
    adjustError.value = '请填写用户 ID 和正整数积分'
    return
  }
  if (!adjust.reason.trim()) {
    adjustError.value = '必须填写原因（会记入审计日志）'
    return
  }
  const verb = adjust.mode === 'grant' ? '发放' : '扣减'
  if (!(await confirmAction({ description: `确定给用户 #${adjust.user_id} ${verb} ${amount} 积分？` }))) return
  adjustSaving.value = true
  try {
    const payload = { user_id: adjust.user_id, amount, reason: adjust.reason.trim(), request_id: adjust.request_id, valid_days: adjust.mode === 'grant' ? Number(adjust.valid_days) || 0 : undefined }
    if (adjust.mode === 'grant') await boostPointsAPI.grant(payload)
    else await boostPointsAPI.deduct(payload)
    adjustOpen.value = false
    notifySuccess(`已${verb} ${amount} 积分`)
    await loadAccounts(accountsPage.value.page)
    if (detailOpen.value && detailUser.value?.id === adjust.user_id) await openDetail(adjust.user_id, detailUser.value.email)
  } catch (err: any) {
    // 请求号不变：网络重试不会重复发放
    adjustError.value = err?.message || `${verb}失败`
  } finally {
    adjustSaving.value = false
  }
}

// ---------------------------------------------------------------------------
// 积分包
// ---------------------------------------------------------------------------
const packs = ref<BoostPointPack[]>([])
const packsLoading = ref(false)
const packOpen = ref(false)
const packSaving = ref(false)
const packError = ref('')
const packForm = reactive({ id: 0, sku_id: 0, name: '', points: 1000, bonus: 0, valid_days: 365, active: true })
const pointsProducts = ref<AdminProduct[]>([])

const loadPacks = async () => {
  packsLoading.value = true
  try {
    packs.value = (await boostPointsAPI.listPacks()) || []
  } catch {
    // client 已提示
  } finally {
    packsLoading.value = false
  }
}

const usedSkuIds = computed(() => new Set(packs.value.map((p) => p.sku_id)))
const skuOptions = computed(() => {
  const out: { id: number; label: string }[] = []
  for (const p of pointsProducts.value) {
    const title = (p.title as any)?.['zh-CN'] || p.slug
    for (const sku of p.skus || []) {
      if (usedSkuIds.value.has(sku.id)) continue
      out.push({ id: sku.id, label: `${title} / ${sku.sku_code}（¥${Number(sku.price_amount).toFixed(2)}）` })
    }
  }
  return out
})

const openPack = async (p?: BoostPointPack) => {
  packError.value = ''
  if (p) {
    Object.assign(packForm, { id: p.id, sku_id: p.sku_id, name: p.name, points: p.points, bonus: p.bonus, valid_days: p.valid_days || 0, active: p.active })
  } else {
    Object.assign(packForm, { id: 0, sku_id: 0, name: '', points: 1000, bonus: 0, valid_days: 365, active: true })
    try {
      const res = await adminAPI.getProducts({ fulfillment_type: 'boost_points', page: 1, page_size: 100 })
      pointsProducts.value = (res.data?.data as AdminProduct[]) || []
    } catch {
      pointsProducts.value = []
    }
  }
  packOpen.value = true
}

const savePack = async () => {
  packError.value = ''
  packSaving.value = true
  try {
    const payload = {
      sku_id: packForm.sku_id || undefined,
      name: packForm.name.trim(),
      points: Math.trunc(Number(packForm.points)),
      bonus: Math.trunc(Number(packForm.bonus)) || 0,
      valid_days: Math.trunc(Number(packForm.valid_days)) || 0,
      active: packForm.active,
    }
    if (packForm.id) await boostPointsAPI.updatePack(packForm.id, payload)
    else await boostPointsAPI.createPack(payload)
    packOpen.value = false
    notifySuccess(packForm.id ? '积分包已更新' : '积分包已创建')
    await loadPacks()
  } catch (err: any) {
    packError.value = err?.message || '保存失败'
  } finally {
    packSaving.value = false
  }
}

const togglePack = async (p: BoostPointPack) => {
  try {
    await boostPointsAPI.updatePack(p.id, { name: p.name, points: p.points, bonus: p.bonus, valid_days: p.valid_days || 0, active: !p.active })
    notifySuccess(p.active ? '已停用' : '已启用')
    await loadPacks()
  } catch {
    // client 已提示
  }
}

// ---------------------------------------------------------------------------
// 流水
// ---------------------------------------------------------------------------
const ledger = ref<BoostLedgerEntry[]>([])
const ledgerPage = ref<BoostPagination>(emptyPage())
const ledgerLoading = ref(false)
const ledgerFilter = reactive({ user_id: '', type: '' })

const loadLedger = async (page = 1) => {
  ledgerLoading.value = true
  try {
    const res = await boostPointsAPI.listLedger({
      user_id: Number(ledgerFilter.user_id) || undefined,
      type: ledgerFilter.type || undefined,
      page,
      page_size: ledgerPage.value.page_size,
    })
    ledger.value = res.items
    ledgerPage.value = res.pagination
  } catch {
    // client 已提示
  } finally {
    ledgerLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 退款撤回
// ---------------------------------------------------------------------------
const revokeOrderNo = ref('')
const revoking = ref(false)
const revokeResult = ref<BoostRevokeResult | null>(null)
const revokeError = ref('')

const submitRevoke = async () => {
  const orderNo = revokeOrderNo.value.trim()
  revokeError.value = ''
  revokeResult.value = null
  if (!orderNo) {
    revokeError.value = '请输入订单号'
    return
  }
  if (!(await confirmAction({ description: `撤回订单 ${orderNo} 的剩余积分？撤回后不能恢复，已用掉的积分不受影响。`, variant: 'destructive' }))) return
  revoking.value = true
  try {
    revokeResult.value = await boostPointsAPI.revokeOrder(orderNo)
  } catch (err: any) {
    revokeError.value = err?.message || '撤回失败'
  } finally {
    revoking.value = false
  }
}

// ---------------------------------------------------------------------------

const switchTab = (t: Tab) => {
  tab.value = t
  if (t === 'packs' && !packs.value.length) loadPacks()
  if (t === 'ledger' && !ledger.value.length) loadLedger()
}

onMounted(() => loadAccounts())
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">代练积分</h1>
        <p class="text-sm text-muted-foreground">积分只用于代练：通过购买积分包或管理员发放获得，不能提现、转账或换回余额。</p>
      </div>
      <div class="flex gap-2">
        <Button variant="outline" @click="openAdjust('deduct')">扣减积分</Button>
        <Button @click="openAdjust('grant')">发放积分</Button>
      </div>
    </div>

    <div class="flex gap-1 border-b border-border" role="tablist">
      <button
        v-for="t in tabs"
        :key="t.id"
        role="tab"
        :aria-selected="tab === t.id"
        class="-mb-px border-b-2 px-4 py-2 text-sm transition-colors"
        :class="tab === t.id ? 'border-primary font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'"
        @click="switchTab(t.id)"
      >
        {{ t.label }}
      </button>
    </div>

    <!-- 用户积分 -->
    <section v-if="tab === 'accounts'" class="space-y-3">
      <form class="flex max-w-md gap-2" @submit.prevent="loadAccounts(1)">
        <Input v-model="keyword" placeholder="邮箱 / 昵称 / 用户 ID" aria-label="搜索用户" />
        <Button type="submit" variant="outline">搜索</Button>
      </form>
      <div class="rounded-xl border border-border bg-card shadow-sm">
        <TableSkeleton v-if="accountsLoading" :rows="4" :columns="5" />
        <div v-else-if="!accounts.length" class="p-10 text-center text-sm text-muted-foreground">
          没有积分账户。用户第一次购买积分包或被发放积分后会出现在这里。
        </div>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>用户</TableHead>
              <TableHead class="text-right">可用</TableHead>
              <TableHead class="text-right">冻结（进行中任务）</TableHead>
              <TableHead>最近变动</TableHead>
              <TableHead class="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="a in accounts" :key="a.user_id">
              <TableCell>
                <div class="font-medium">{{ a.email || '—' }}</div>
                <div class="text-xs text-muted-foreground">#{{ a.user_id }} {{ a.nickname }}</div>
              </TableCell>
              <TableCell class="text-right font-mono">{{ a.available }}</TableCell>
              <TableCell class="text-right font-mono">{{ a.frozen }}</TableCell>
              <TableCell class="text-xs text-muted-foreground">{{ fmtTime(a.updated_at) }}</TableCell>
              <TableCell class="space-x-1 text-right">
                <Button size="sm" variant="ghost" @click="openDetail(a.user_id, a.email)">明细</Button>
                <Button size="sm" variant="ghost" @click="openAdjust('grant', a.user_id, a.email)">发放</Button>
                <Button size="sm" variant="ghost" @click="openAdjust('deduct', a.user_id, a.email)">扣减</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <ListPagination
          v-if="accounts.length"
          :page="accountsPage.page"
          :total-page="accountsPage.total_page"
          :total="accountsPage.total"
          @change-page="loadAccounts"
        />
      </div>
    </section>

    <!-- 积分包 -->
    <section v-else-if="tab === 'packs'" class="space-y-3">
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-dashed border-border bg-muted/30 p-3 text-sm text-muted-foreground">
        <span>
          积分包是普通商品：先在「商品管理」新建商品，交付类型选<b>代练积分</b>并设置售价（会员价、支付渠道都按原有商品规则）；再在这里把 SKU 配成积分包。
        </span>
        <Button size="sm" @click="openPack()">新建积分包</Button>
      </div>
      <div class="rounded-xl border border-border bg-card shadow-sm">
        <TableSkeleton v-if="packsLoading" :rows="3" :columns="6" />
        <div v-else-if="!packs.length" class="p-10 text-center text-sm text-muted-foreground">还没有积分包。</div>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>积分包</TableHead>
              <TableHead>商品 / SKU</TableHead>
              <TableHead class="text-right">售价</TableHead>
              <TableHead class="text-right">到账积分</TableHead>
              <TableHead>有效期</TableHead>
              <TableHead>状态</TableHead>
              <TableHead class="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="p in packs" :key="p.id">
              <TableCell class="font-medium">{{ p.name }}</TableCell>
              <TableCell>
                <div>{{ p.product_title || p.product_slug || `#${p.product_id}` }}</div>
                <div class="text-xs text-muted-foreground">{{ p.sku_code }}</div>
                <div v-if="p.fulfillment_type !== 'boost_points'" class="text-xs text-destructive">商品交付类型已不是「代练积分」，支付后不会到账</div>
                <div v-else-if="!p.product_active || !p.sku_active" class="text-xs text-amber-600">商品或 SKU 已下架，用户买不到</div>
              </TableCell>
              <TableCell class="text-right font-mono">¥{{ p.price }}</TableCell>
              <TableCell class="text-right font-mono">
                {{ p.points + p.bonus }}
                <span v-if="p.bonus" class="block text-xs text-muted-foreground">{{ p.points }} + 赠 {{ p.bonus }}</span>
              </TableCell>
              <TableCell>{{ p.valid_days ? `${p.valid_days} 天` : '永久' }}</TableCell>
              <TableCell>
                <span class="rounded px-2 py-0.5 text-xs" :class="p.active ? 'bg-emerald-50 text-emerald-700' : 'bg-muted text-muted-foreground'">
                  {{ p.active ? '启用' : '停用' }}
                </span>
              </TableCell>
              <TableCell class="space-x-1 text-right">
                <Button size="sm" variant="ghost" @click="openPack(p)">编辑</Button>
                <Button size="sm" variant="ghost" @click="togglePack(p)">{{ p.active ? '停用' : '启用' }}</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <p class="text-xs text-muted-foreground">修改积分数量只影响之后支付的订单。停用积分包会让它从用户端「积分」页消失；已支付的订单仍然到账。</p>
    </section>

    <!-- 积分流水 -->
    <section v-else-if="tab === 'ledger'" class="space-y-3">
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="loadLedger(1)">
        <div class="space-y-1">
          <label class="text-xs text-muted-foreground" for="bp-ledger-user">用户 ID</label>
          <Input id="bp-ledger-user" v-model="ledgerFilter.user_id" class="w-32" inputmode="numeric" />
        </div>
        <div class="space-y-1">
          <label class="text-xs text-muted-foreground" for="bp-ledger-type">类型</label>
          <select id="bp-ledger-type" v-model="ledgerFilter.type" class="h-9 rounded-md border border-input bg-background px-2 text-sm">
            <option value="">全部</option>
            <option v-for="(label, key) in ledgerTypeText" :key="key" :value="key">{{ label }}</option>
          </select>
        </div>
        <Button type="submit" variant="outline">查询</Button>
      </form>
      <div class="rounded-xl border border-border bg-card shadow-sm">
        <TableSkeleton v-if="ledgerLoading" :rows="5" :columns="7" />
        <div v-else-if="!ledger.length" class="p-10 text-center text-sm text-muted-foreground">没有流水。</div>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>时间</TableHead>
              <TableHead>用户</TableHead>
              <TableHead>类型</TableHead>
              <TableHead class="text-right">可用变动</TableHead>
              <TableHead class="text-right">冻结变动</TableHead>
              <TableHead class="text-right">变动后</TableHead>
              <TableHead>说明</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="r in ledger" :key="r.id">
              <TableCell class="whitespace-nowrap text-xs">{{ fmtTime(r.created_at) }}</TableCell>
              <TableCell>
                <button class="text-primary underline-offset-2 hover:underline" @click="openDetail(r.user_id)">#{{ r.user_id }}</button>
              </TableCell>
              <TableCell>{{ ledgerTypeText[r.type] || r.type }}</TableCell>
              <TableCell class="text-right font-mono" :class="r.delta_available < 0 ? 'text-destructive' : r.delta_available > 0 ? 'text-emerald-700' : ''">
                {{ r.delta_available ? fmtDelta(r.delta_available) : '' }}
              </TableCell>
              <TableCell class="text-right font-mono">{{ r.delta_frozen ? fmtDelta(r.delta_frozen) : '' }}</TableCell>
              <TableCell class="text-right font-mono text-xs">{{ r.available_after }} / {{ r.frozen_after }}</TableCell>
              <TableCell class="max-w-xs text-xs">
                <div>{{ r.remark }}</div>
                <div class="truncate text-muted-foreground" :title="r.biz_key">
                  {{ r.biz_key }}<template v-if="r.operator_admin_id"> · 管理员 #{{ r.operator_admin_id }}</template>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <ListPagination v-if="ledger.length" :page="ledgerPage.page" :total-page="ledgerPage.total_page" :total="ledgerPage.total" @change-page="loadLedger" />
      </div>
    </section>

    <!-- 退款撤回 -->
    <section v-else class="max-w-2xl space-y-4">
      <div class="rounded-lg border border-border bg-card p-4 text-sm leading-relaxed text-muted-foreground">
        积分包订单退款前必须先撤回剩余积分，否则「订单管理」里的退款会被拒绝。撤回只撤还没用掉的部分（冻结在进行中任务里的，任务结束退回时自动作废），
        并按<b>未使用比例</b>给出建议退款金额（向下取整到分）。拿到建议金额后，到「订单管理」对该订单正常退款。
      </div>
      <form class="flex gap-2" @submit.prevent="submitRevoke">
        <Input v-model="revokeOrderNo" placeholder="积分包订单号" aria-label="积分包订单号" />
        <Button type="submit" variant="destructive" :disabled="revoking">{{ revoking ? '撤回中…' : '撤回剩余积分' }}</Button>
      </form>
      <p v-if="revokeError" class="text-sm text-destructive" role="alert">{{ revokeError }}</p>
      <div v-if="revokeResult" class="rounded-lg border border-emerald-200 bg-emerald-50 p-4 text-sm" role="status">
        <div class="font-medium text-emerald-800">{{ revokeResult.already ? '该订单已撤回过' : '撤回完成' }} · {{ revokeResult.order_no }}</div>
        <dl v-if="!revokeResult.already" class="mt-2 grid grid-cols-3 gap-2 text-emerald-900">
          <div><dt class="text-xs opacity-70">到账</dt><dd class="font-mono">{{ revokeResult.total }}</dd></div>
          <div><dt class="text-xs opacity-70">已用 / 冻结 / 到期</dt><dd class="font-mono">{{ revokeResult.used }}</dd></div>
          <div><dt class="text-xs opacity-70">本次撤回</dt><dd class="font-mono">{{ revokeResult.revoked }}</dd></div>
        </dl>
        <div class="mt-3 text-base text-emerald-900">建议退款金额：<b class="font-mono">¥{{ revokeResult.suggest_refund }}</b></div>
      </div>
    </section>

    <!-- 用户积分明细 -->
    <Dialog v-model:open="detailOpen">
      <DialogScrollContent class="max-w-3xl">
        <DialogHeader>
          <DialogTitle>用户 #{{ detailUser?.id }} 的积分 {{ detailUser?.email }}</DialogTitle>
        </DialogHeader>
        <div v-if="!detailSummary" class="p-6 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else class="space-y-4">
          <div class="grid grid-cols-3 gap-3 text-center">
            <div class="rounded-lg border p-3"><div class="text-xs text-muted-foreground">可用</div><div class="text-xl font-semibold">{{ detailSummary.available }}</div></div>
            <div class="rounded-lg border p-3"><div class="text-xs text-muted-foreground">冻结</div><div class="text-xl font-semibold">{{ detailSummary.frozen }}</div></div>
            <div class="rounded-lg border p-3">
              <div class="text-xs text-muted-foreground">30 天内到期</div>
              <div class="text-xl font-semibold">{{ detailSummary.expiring_soon.reduce((s, x) => s + x.amount, 0) }}</div>
            </div>
          </div>
          <div>
            <h3 class="mb-2 text-sm font-medium">批次（按使用顺序：先到期的先用，永久的最后用）</h3>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>#</TableHead><TableHead>来源</TableHead><TableHead class="text-right">剩余 / 总数</TableHead><TableHead>到期</TableHead><TableHead>状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="l in detailLots" :key="l.id">
                  <TableCell>{{ l.id }}</TableCell>
                  <TableCell>{{ lotSourceText[l.source_type] || l.source_type }} <span class="text-xs text-muted-foreground">{{ l.source_ref }}</span></TableCell>
                  <TableCell class="text-right font-mono">{{ l.remaining }} / {{ l.amount }}</TableCell>
                  <TableCell class="text-xs">{{ l.expires_at ? fmtTime(l.expires_at) : '永久' }}</TableCell>
                  <TableCell>
                    {{ lotStatusText[l.status] }}
                    <span v-if="l.suggest_refund" class="block text-xs text-muted-foreground">建议退 ¥{{ l.suggest_refund }}</span>
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
          <div>
            <h3 class="mb-2 text-sm font-medium">最近 50 条流水</h3>
            <ul class="divide-y rounded-lg border text-sm">
              <li v-for="r in detailLedger" :key="r.id" class="flex items-center justify-between gap-3 px-3 py-2">
                <span class="w-36 shrink-0 text-xs text-muted-foreground">{{ fmtTime(r.created_at) }}</span>
                <span class="w-12 shrink-0">{{ ledgerTypeText[r.type] || r.type }}</span>
                <span class="flex-1 truncate text-xs text-muted-foreground">{{ r.remark }}</span>
                <span class="w-28 shrink-0 text-right font-mono">
                  {{ r.delta_available ? fmtDelta(r.delta_available) : '' }}
                  <span v-if="r.delta_frozen" class="text-xs text-muted-foreground">冻 {{ fmtDelta(r.delta_frozen) }}</span>
                </span>
              </li>
              <li v-if="!detailLedger.length" class="px-3 py-4 text-center text-muted-foreground">没有流水</li>
            </ul>
          </div>
        </div>
      </DialogScrollContent>
    </Dialog>

    <!-- 发放 / 扣减 -->
    <Dialog v-model:open="adjustOpen">
      <DialogScrollContent class="max-w-md">
        <DialogHeader>
          <DialogTitle>{{ adjust.mode === 'grant' ? '发放积分' : '扣减积分' }}</DialogTitle>
        </DialogHeader>
        <form class="space-y-3" @submit.prevent="submitAdjust">
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-adj-user">用户 ID</label>
            <Input id="bp-adj-user" v-model.number="adjust.user_id" type="number" min="1" :disabled="!!adjust.email" />
            <p v-if="adjust.email" class="text-xs text-muted-foreground">{{ adjust.email }}</p>
          </div>
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-adj-amount">积分</label>
            <Input id="bp-adj-amount" v-model.number="adjust.amount" type="number" min="1" step="1" />
            <p v-if="adjust.mode === 'deduct'" class="text-xs text-muted-foreground">只能扣可用积分，冻结在进行中任务里的不能扣。按先到期先扣的顺序。</p>
          </div>
          <div v-if="adjust.mode === 'grant'" class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-adj-days">有效天数（0 = 永久）</label>
            <Input id="bp-adj-days" v-model.number="adjust.valid_days" type="number" min="0" max="3650" />
          </div>
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-adj-reason">原因（必填，用户能在积分明细里看到）</label>
            <Input id="bp-adj-reason" v-model="adjust.reason" maxlength="100" />
          </div>
          <p v-if="adjustError" class="text-sm text-destructive" role="alert">{{ adjustError }}</p>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="adjustOpen = false">取消</Button>
            <Button type="submit" :variant="adjust.mode === 'deduct' ? 'destructive' : 'default'" :disabled="adjustSaving">
              {{ adjustSaving ? '提交中…' : adjust.mode === 'grant' ? '发放' : '扣减' }}
            </Button>
          </div>
        </form>
      </DialogScrollContent>
    </Dialog>

    <!-- 积分包编辑 -->
    <Dialog v-model:open="packOpen">
      <DialogScrollContent class="max-w-md">
        <DialogHeader>
          <DialogTitle>{{ packForm.id ? '编辑积分包' : '新建积分包' }}</DialogTitle>
        </DialogHeader>
        <form class="space-y-3" @submit.prevent="savePack">
          <div v-if="!packForm.id" class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-pack-sku">商品 SKU（交付类型为「代练积分」的商品）</label>
            <select id="bp-pack-sku" v-model.number="packForm.sku_id" class="h-9 w-full rounded-md border border-input bg-background px-2 text-sm">
              <option :value="0" disabled>请选择</option>
              <option v-for="o in skuOptions" :key="o.id" :value="o.id">{{ o.label }}</option>
            </select>
            <p v-if="!skuOptions.length" class="text-xs text-amber-600">没有可用的 SKU。请先在「商品管理」新建交付类型为「代练积分」的商品。</p>
          </div>
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-pack-name">名称（可留空，默认"N 积分"）</label>
            <Input id="bp-pack-name" v-model="packForm.name" maxlength="64" />
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <label class="text-xs font-medium text-muted-foreground" for="bp-pack-points">积分</label>
              <Input id="bp-pack-points" v-model.number="packForm.points" type="number" min="1" />
            </div>
            <div class="space-y-1">
              <label class="text-xs font-medium text-muted-foreground" for="bp-pack-bonus">赠送</label>
              <Input id="bp-pack-bonus" v-model.number="packForm.bonus" type="number" min="0" />
            </div>
          </div>
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bp-pack-days">有效天数（0 = 永久，从到账时算）</label>
            <Input id="bp-pack-days" v-model.number="packForm.valid_days" type="number" min="0" max="3650" />
          </div>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="packForm.active" type="checkbox" class="h-4 w-4" /> 启用（在用户端展示）
          </label>
          <p class="text-xs text-muted-foreground">售价在商品 SKU 上改；买 N 份到账 N 倍积分。</p>
          <p v-if="packError" class="text-sm text-destructive" role="alert">{{ packError }}</p>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="packOpen = false">取消</Button>
            <Button type="submit" :disabled="packSaving || (!packForm.id && !packForm.sku_id)">{{ packSaving ? '保存中…' : '保存' }}</Button>
          </div>
        </form>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
