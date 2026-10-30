<template>
  <div class="boost-page min-h-screen bg-background text-foreground pt-20 pb-24 lg:pb-16">
    <div class="container mx-auto px-4 max-w-6xl">
      <div class="mt-8 mb-6">
        <router-link to="/boost" class="text-sm text-muted-foreground hover:text-primary">{{ t('boost.backToGames') }}</router-link>
      </div>

      <div v-if="loading" class="space-y-4">
        <div class="h-24 rounded-2xl border bg-muted animate-pulse"></div>
        <div class="h-96 rounded-2xl border bg-muted animate-pulse"></div>
      </div>

      <EmptyState v-else-if="!game" variant="soft" size="lg" :title="t('boost.notFound')" action-to="/boost" :action-label="t('boost.backToGames')" />

      <template v-else>
        <!-- 游戏头部 -->
        <div class="mb-6 flex items-center gap-4">
          <img v-if="game.icon_url" :src="getImageUrl(game.icon_url)" :alt="game.name" class="h-16 w-16 rounded-2xl object-cover shadow-sm" />
          <span v-else class="flex h-16 w-16 items-center justify-center rounded-2xl text-xl font-bold text-white" :style="{ background: game.color || '#2f6f6a' }">{{ game.short_name }}</span>
          <div class="min-w-0">
            <h1 class="text-2xl md:text-3xl font-black tracking-tight">{{ game.name }}</h1>
            <div v-if="game.servers.length" class="mt-1 flex flex-wrap gap-1.5 text-xs text-muted-foreground">
              <span>{{ t('boost.servers') }}：</span>
              <span v-for="s in game.servers" :key="s.id" class="rounded-full bg-secondary px-2 py-0.5">{{ s.name }}</span>
            </div>
          </div>
        </div>

        <EmptyState v-if="!features.length" variant="soft" :title="t('boost.noFeatures')" />

        <div v-else class="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_340px]">
          <div class="min-w-0 space-y-4">
            <!-- 功能选择 -->
            <div class="flex flex-wrap gap-2" role="tablist" :aria-label="t('boost.pickFeature')">
              <button
                v-for="f in features"
                :key="f.id"
                type="button"
                role="tab"
                :aria-selected="f.id === current?.id"
                class="rounded-full border px-4 py-1.5 text-sm transition-colors"
                :class="f.id === current?.id ? 'border-primary bg-primary text-primary-foreground' : 'bg-card hover:bg-secondary'"
                @click="selectFeature(f)"
              >
                {{ f.name }}
              </button>
            </div>

            <Card v-if="current" class="rounded-2xl p-5 md:p-6">
              <h2 class="text-xl font-bold">{{ current.definition.name }}</h2>
              <p v-if="current.definition.description" class="mt-1 mb-5 text-sm text-muted-foreground">{{ current.definition.description }}</p>
              <FeatureForm v-model="params" :definition="current.definition" :plan="plan" :show-errors="submitted" />
            </Card>
          </div>

          <!-- 报价 -->
          <aside class="lg:sticky lg:top-24 lg:self-start">
            <Card class="space-y-4 rounded-2xl p-5">
              <div class="flex items-baseline justify-between">
                <span class="text-sm text-muted-foreground">{{ t('boost.total') }}</span>
                <span class="text-3xl font-black">
                  <span :class="{ 'opacity-50': quoting }">{{ plan ? plan.total : '—' }}</span>
                  <span class="ml-1 text-sm font-normal text-muted-foreground">{{ t('boost.points') }}</span>
                </span>
              </div>
              <!-- BOOST-EXT S4: 积分余额（下单在 S5 开放） -->
              <div v-if="userAuthStore.isAuthenticated" class="flex items-center justify-between rounded-lg bg-secondary/60 px-3 py-2 text-xs">
                <span class="text-muted-foreground">{{ t('boost.myAvailable') }}：<b class="font-mono text-foreground">{{ available ?? '—' }}</b></span>
                <router-link to="/boost/points" class="text-primary hover:underline">{{ t('boost.buyPoints') }}</router-link>
              </div>
              <div v-if="plan && plan.rate < 100" class="text-xs text-emerald-600">{{ t('boost.memberRate', { rate: plan.rate / 10 }) }}</div>
              <div v-else-if="!userAuthStore.isAuthenticated" class="text-xs text-muted-foreground">
                <router-link :to="`/auth/login?redirect=${encodeURIComponent(route.fullPath)}`" class="text-primary hover:underline">{{ t('boost.loginForDiscount') }}</router-link>
              </div>

              <table v-if="plan && plan.breakdown.length" class="w-full text-sm">
                <tbody>
                  <tr v-for="(b, i) in plan.breakdown" :key="i" class="border-t first:border-t-0">
                    <td class="py-1.5 pr-2">{{ b.label }}<div class="text-xs text-muted-foreground">{{ b.detail }}</div></td>
                    <td class="py-1.5 text-right font-mono whitespace-nowrap" :class="b.points < 0 ? 'text-emerald-600' : ''">{{ b.points > 0 ? '+' : '' }}{{ b.points }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-if="plan" class="text-xs text-muted-foreground">{{ t('boost.units', { count: plan.units }) }}</p>

              <ul v-if="submitted && plan && plan.errors.length" class="space-y-1 text-xs text-destructive" role="alert">
                <li v-for="(e, i) in plan.errors" :key="i">{{ e }}</li>
              </ul>
              <p v-if="quoteError" class="text-xs text-destructive" role="alert">{{ quoteError }}</p>

              <!-- BOOST-EXT S5: 选择游戏账号并下单。价格只以服务端为准，请求里不带任何价格 -->
              <template v-if="userAuthStore.isAuthenticated">
                <div>
                  <label for="boost-binding" class="mb-1 block text-sm font-medium">{{ t('boost.pickAccount') }}</label>
                  <select
                    v-if="verifiedBindings.length"
                    id="boost-binding"
                    v-model.number="bindingId"
                    class="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
                  >
                    <option v-for="b in verifiedBindings" :key="b.id" :value="b.id">{{ b.display_name }} · {{ b.server_name }} · {{ b.login_masked }}</option>
                  </select>
                  <p v-else class="text-xs text-muted-foreground">
                    {{ t('boost.noVerifiedAccount') }}
                    <router-link :to="`/boost/accounts?game=${game.id}`" class="text-primary hover:underline">{{ t('boost.goAddAccount') }}</router-link>
                  </p>
                </div>
                <p v-if="plan && available !== null && plan.total > available" class="text-xs text-destructive">{{ t('boost.notEnoughPoints') }}</p>

                <div v-if="confirming && plan" class="space-y-2 rounded-lg border border-primary/40 bg-primary/5 p-3 text-sm" role="alertdialog" :aria-label="t('boost.confirmTitle')">
                  <div class="font-medium">{{ t('boost.confirmTitle') }}</div>
                  <p class="text-muted-foreground">{{ t('boost.confirmBody', { points: plan.total }) }}</p>
                  <div class="flex gap-2">
                    <Button class="flex-1" :disabled="ordering" @click="placeOrder">{{ t('boost.confirmOrder') }}</Button>
                    <Button variant="outline" :disabled="ordering" @click="confirming = false">{{ t('boost.cancel') }}</Button>
                  </div>
                </div>
                <Button v-else class="w-full" size="lg" :disabled="!plan || quoting || !bindingId" @click="submit">{{ t('boost.submit') }}</Button>
              </template>
              <router-link v-else :to="`/auth/login?redirect=${encodeURIComponent(route.fullPath)}`" class="block">
                <Button class="w-full" size="lg">{{ t('boost.loginToOrder') }}</Button>
              </router-link>
            </Card>
          </aside>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
// BOOST-EXT: 用户端游戏详情：选择代练功能，按功能 JSON 自动生成表单，价格实时向服务端报价。
// 选择已验证的游戏账号后下单：服务端重新编译报价并冻结积分，前端不传价格。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import EmptyState from '../components/EmptyState.vue'
import FeatureForm from '../components/boost/FeatureForm.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { getImageUrl } from '../utils/image'
import { usePageSeo } from '../composables/usePageSeo'
import { toast } from '../composables/useToast'
import { useUserAuthStore } from '../stores/userAuth'
import { boostAPI, boostPointsAPI, boostTaskAPI, newIdempotencyKey, type BoostBinding, type BoostFeature, type BoostGame, type BoostPlan } from '../api/boost'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const userAuthStore = useUserAuthStore()

const game = ref<BoostGame | null>(null)
const features = ref<BoostFeature[]>([])
const current = ref<BoostFeature | null>(null)
const loading = ref(true)

const params = ref<Record<string, unknown>>({})
const plan = ref<BoostPlan | null>(null)
const quoting = ref(false)
const quoteError = ref('')
const submitted = ref(false)
const available = ref<number | null>(null)

const bindings = ref<BoostBinding[]>([])
const bindingId = ref(0)
const verifiedBindings = computed(() => bindings.value.filter((b) => b.status === 'SUCCESS' && b.game_id === game.value?.id))
const confirming = ref(false)
const ordering = ref(false)
let idempotencyKey = ''

const loadBindings = async () => {
  if (!userAuthStore.isAuthenticated || !game.value) return
  try {
    bindings.value = (await boostTaskAPI.bindings(game.value.id)).items
    if (!verifiedBindings.value.some((b) => b.id === bindingId.value)) bindingId.value = verifiedBindings.value[0]?.id || 0
  } catch {
    bindings.value = []
  }
}

const loadPoints = async () => {
  if (!userAuthStore.isAuthenticated) return
  try {
    available.value = (await boostPointsAPI.summary()).available
  } catch {
    available.value = null
  }
}

usePageSeo({
  title: () => (game.value ? `${game.value.name} · ${t('boost.title')}` : t('boost.title')),
  canonicalPath: () => `/boost/${String(route.params.code || '')}`,
})

const load = async (code: string) => {
  loading.value = true
  game.value = null
  try {
    const data = await boostAPI.getGame(code)
    game.value = data.game
    features.value = data.features || []
    const wanted = Number(route.query.feature)
    selectFeature(features.value.find((f) => f.id === wanted) || features.value[0] || null)
    void loadBindings()
  } catch (error) {
    console.error('Failed to load boost game:', error)
  } finally {
    loading.value = false
  }
}

const selectFeature = (f: BoostFeature | null) => {
  if (current.value?.id === f?.id) return
  current.value = f
  params.value = {}
  plan.value = null
  submitted.value = false
  confirming.value = false
  void requestQuote()
}

// 参数变化后 250ms 报价；用序号丢弃过期响应
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
const requestQuote = async () => {
  if (!current.value) return
  const mySeq = ++seq
  quoting.value = true
  try {
    const result = await boostAPI.quote(current.value.id, params.value, userAuthStore.isAuthenticated)
    if (mySeq !== seq) return
    plan.value = result
    quoteError.value = ''
  } catch (error: any) {
    if (mySeq === seq) quoteError.value = error?.message || t('boost.quoteFailed')
  } finally {
    if (mySeq === seq) quoting.value = false
  }
}
watch(params, () => {
  confirming.value = false
  clearTimeout(timer)
  timer = setTimeout(requestQuote, 250)
}, { deep: true })
onBeforeUnmount(() => clearTimeout(timer))

const submit = () => {
  submitted.value = true
  if (!plan.value?.ok) {
    toast.error(t('boost.fixErrors'))
    return
  }
  if (!bindingId.value) return
  // 每次打开确认生成新的幂等键；确认按钮重试时保持不变
  idempotencyKey = newIdempotencyKey()
  confirming.value = true
}

const placeOrder = async () => {
  if (!current.value || !bindingId.value) return
  ordering.value = true
  try {
    const res = await boostTaskAPI.createTask({ binding_id: bindingId.value, feature_id: current.value.id, params: params.value, idempotency_key: idempotencyKey })
    toast.success(t('boost.orderCreated', { no: res.task.task_no }))
    confirming.value = false
    void router.push(`/boost/tasks/${res.task.task_no}`)
  } catch (err: any) {
    // 积分不足、并发上限、参数错误：服务端给出原因；报价刷新一次
    toast.error(err?.message || '')
    confirming.value = false
    void loadPoints()
    void requestQuote()
  } finally {
    ordering.value = false
  }
}

watch(() => route.params.code, (code) => {
  if (typeof code === 'string' && code) void load(code)
}, { immediate: true })
void loadPoints()
</script>
