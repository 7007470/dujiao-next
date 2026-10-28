<template>
  <div class="boost-page min-h-screen bg-background text-foreground pt-20 pb-24 lg:pb-16">
    <div class="container mx-auto max-w-5xl px-4">
      <div class="mt-8 mb-6 flex flex-wrap items-center justify-between gap-3">
        <router-link to="/boost" class="text-sm text-muted-foreground hover:text-primary">{{ t('boost.backToGames') }}</router-link>
      </div>

      <h1 class="mb-6 text-3xl font-black tracking-tight">{{ t('boost.pointsTitle') }}</h1>

      <!-- 余额 -->
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Card class="rounded-2xl p-5">
          <div class="text-sm text-muted-foreground">{{ t('boost.available') }}</div>
          <div class="mt-1 text-3xl font-black">{{ summary ? summary.available : '—' }}</div>
        </Card>
        <Card class="rounded-2xl p-5">
          <div class="text-sm text-muted-foreground">{{ t('boost.frozen') }}</div>
          <div class="mt-1 text-3xl font-black">{{ summary ? summary.frozen : '—' }}</div>
          <div class="mt-1 text-xs text-muted-foreground">{{ t('boost.frozenHint') }}</div>
        </Card>
        <Card class="rounded-2xl p-5">
          <div class="text-sm text-muted-foreground">{{ t('boost.expiringSoon') }}</div>
          <div class="mt-1 text-3xl font-black" :class="expiringTotal ? 'text-amber-600' : ''">{{ summary ? expiringTotal : '—' }}</div>
          <ul v-if="summary && summary.expiring_soon.length" class="mt-1 space-y-0.5 text-xs text-muted-foreground">
            <li v-for="e in summary.expiring_soon.slice(0, 3)" :key="e.lot_id">{{ t('boost.expiresOn', { amount: e.amount, date: fmtDate(e.expires_at) }) }}</li>
          </ul>
        </Card>
      </div>

      <!-- 积分包 -->
      <section class="mt-10">
        <h2 class="mb-1 text-xl font-bold">{{ t('boost.buyPoints') }}</h2>
        <p class="mb-4 text-sm text-muted-foreground">{{ t('boost.buyPointsHint') }}</p>
        <div v-if="packsLoading" class="grid grid-cols-2 gap-4 md:grid-cols-4">
          <div v-for="i in 4" :key="i" class="h-36 animate-pulse rounded-2xl border bg-muted"></div>
        </div>
        <EmptyState v-else-if="!packs.length" variant="soft" :title="t('boost.noPacks')" />
        <div v-else class="grid grid-cols-2 gap-4 md:grid-cols-4">
          <router-link
            v-for="p in packs"
            :key="p.id"
            :to="`/products/${p.product_slug}`"
            class="group flex flex-col rounded-2xl border bg-card p-5 shadow-sm transition-all hover:-translate-y-1 hover:shadow-md"
          >
            <div class="text-sm text-muted-foreground">{{ p.name }}</div>
            <div class="mt-2 text-3xl font-black">{{ p.points + p.bonus }}</div>
            <div class="text-xs text-muted-foreground">
              {{ t('boost.points') }}
              <span v-if="p.bonus" class="ml-1 text-emerald-600">{{ t('boost.bonusIncluded', { bonus: p.bonus }) }}</span>
            </div>
            <div class="mt-auto pt-4 text-lg font-bold text-primary">{{ formatPrice(p.price, siteCurrency) }}</div>
            <div class="text-xs text-muted-foreground">{{ p.valid_days ? t('boost.validDays', { days: p.valid_days }) : t('boost.permanent') }}</div>
          </router-link>
        </div>
      </section>

      <!-- 明细 -->
      <section class="mt-10">
        <h2 class="mb-4 text-xl font-bold">{{ t('boost.ledgerTitle') }}</h2>
        <Card class="overflow-hidden rounded-2xl">
          <div v-if="ledgerLoading" class="p-6 text-center text-sm text-muted-foreground">{{ t('boost.loading') }}</div>
          <div v-else-if="!ledger.length" class="p-6 text-center text-sm text-muted-foreground">{{ t('boost.noLedger') }}</div>
          <ul v-else class="divide-y">
            <li v-for="r in ledger" :key="r.id" class="flex items-center gap-3 px-4 py-3 text-sm">
              <div class="min-w-0 flex-1">
                <div class="font-medium">{{ t(`boost.ledgerTypes.${r.type}`) }}</div>
                <div class="truncate text-xs text-muted-foreground">{{ r.remark || '—' }} · {{ fmtTime(r.created_at) }}</div>
              </div>
              <div class="text-right">
                <div v-if="r.delta_available" class="font-mono font-semibold" :class="r.delta_available > 0 ? 'text-emerald-600' : 'text-foreground'">
                  {{ r.delta_available > 0 ? '+' : '' }}{{ r.delta_available }}
                </div>
                <div v-if="r.delta_frozen" class="font-mono text-xs text-muted-foreground">
                  {{ t('boost.frozenDelta', { delta: (r.delta_frozen > 0 ? '+' : '') + r.delta_frozen }) }}
                </div>
                <div class="text-xs text-muted-foreground">{{ t('boost.balanceAfter', { n: r.available_after }) }}</div>
              </div>
            </li>
          </ul>
          <div v-if="pagination.total_page > 1" class="flex items-center justify-center gap-3 border-t p-3 text-sm">
            <Button size="sm" variant="outline" :disabled="pagination.page <= 1" @click="loadLedger(pagination.page - 1)">{{ t('boost.prevPage') }}</Button>
            <span class="text-muted-foreground">{{ pagination.page }} / {{ pagination.total_page }}</span>
            <Button size="sm" variant="outline" :disabled="pagination.page >= pagination.total_page" @click="loadLedger(pagination.page + 1)">{{ t('boost.nextPage') }}</Button>
          </div>
        </Card>
        <p class="mt-3 text-xs text-muted-foreground">{{ t('boost.pointsRules') }}</p>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
// BOOST-EXT: 我的代练积分：余额、即将到期、积分包（跳到商品页用 Dujiao 原生下单支付）、积分明细。
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import EmptyState from '../components/EmptyState.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { usePageSeo } from '../composables/usePageSeo'
import { useLocalized } from '../composables/useProduct'
import { boostPointsAPI, type BoostLedgerRow, type BoostPagination, type BoostPointPack, type BoostPointsSummary } from '../api/boost'

const { t } = useI18n()
const { siteCurrency, formatPrice } = useLocalized()
usePageSeo({ title: () => t('boost.pointsTitle'), canonicalPath: () => '/boost/points' })

const summary = ref<BoostPointsSummary | null>(null)
const packs = ref<BoostPointPack[]>([])
const packsLoading = ref(true)
const ledger = ref<BoostLedgerRow[]>([])
const ledgerLoading = ref(true)
const pagination = ref<BoostPagination>({ page: 1, page_size: 20, total: 0, total_page: 1 })

const expiringTotal = computed(() => (summary.value?.expiring_soon || []).reduce((s, e) => s + e.amount, 0))
const fmtDate = (v: string) => new Date(v).toLocaleDateString()
const fmtTime = (v: string) => new Date(v).toLocaleString()

const loadLedger = async (page = 1) => {
  ledgerLoading.value = true
  try {
    const res = await boostPointsAPI.ledger(page)
    ledger.value = res.items
    pagination.value = res.pagination
  } catch (error) {
    console.error('Failed to load points ledger:', error)
  } finally {
    ledgerLoading.value = false
  }
}

onMounted(async () => {
  boostPointsAPI.summary().then((s) => (summary.value = s)).catch((error) => console.error('Failed to load points:', error))
  boostPointsAPI.packs()
    .then((p) => (packs.value = p || []))
    .catch((error) => console.error('Failed to load point packs:', error))
    .finally(() => (packsLoading.value = false))
  await loadLedger()
})
</script>
