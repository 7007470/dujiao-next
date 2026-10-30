<template>
  <div class="boost-page min-h-screen bg-background text-foreground pt-20 pb-24 lg:pb-16">
    <div class="container mx-auto max-w-4xl px-4">
      <div class="mt-8 mb-6 flex flex-wrap items-center justify-between gap-3">
        <router-link v-if="taskNo" to="/boost/tasks" class="text-sm text-muted-foreground hover:text-primary">{{ t('boost.backToTasks') }}</router-link>
        <router-link v-else to="/boost" class="text-sm text-muted-foreground hover:text-primary">{{ t('boost.backToGames') }}</router-link>
        <div class="flex gap-3 text-sm">
          <router-link to="/boost/accounts" class="text-primary hover:underline">{{ t('boost.myAccounts') }}</router-link>
          <router-link to="/boost/points" class="text-primary hover:underline">{{ t('boost.pointsTitle') }}</router-link>
        </div>
      </div>

      <!-- 列表 -->
      <template v-if="!taskNo">
        <div class="mb-4 flex flex-wrap items-end justify-between gap-3">
          <h1 class="text-3xl font-black tracking-tight">{{ t('boost.tasksTitle') }}</h1>
          <div class="flex gap-1" role="tablist">
            <button
              v-for="f in filters"
              :key="f.id"
              type="button"
              role="tab"
              :aria-selected="filter === f.id"
              class="rounded-full border px-3 py-1 text-sm"
              :class="filter === f.id ? 'border-primary bg-primary text-primary-foreground' : 'bg-card hover:bg-secondary'"
              @click="setFilter(f.id)"
            >
              {{ f.label }}
            </button>
          </div>
        </div>
        <div v-if="listLoading" class="space-y-3">
          <div v-for="i in 3" :key="i" class="h-20 rounded-2xl border bg-muted animate-pulse"></div>
        </div>
        <EmptyState v-else-if="!tasks.length" variant="soft" :title="t('boost.noTasks')" action-to="/boost" :action-label="t('boost.backToGames')" />
        <ul v-else class="space-y-3">
          <li v-for="task in tasks" :key="task.id">
            <router-link :to="`/boost/tasks/${task.task_no}`" class="block">
              <Card class="rounded-2xl p-4 transition-colors hover:bg-secondary/40">
                <div class="flex flex-wrap items-start justify-between gap-3">
                  <div class="min-w-0">
                    <div class="truncate font-semibold">{{ task.game_name }} · {{ task.summary }}</div>
                    <div class="text-xs text-muted-foreground">{{ task.task_no }} · {{ task.binding_name }} {{ task.login_masked }} · {{ fmtTime(task.created_at) }}</div>
                  </div>
                  <span class="rounded-full border px-2.5 py-0.5 text-xs" :class="statusTone(task.status)">{{ t(`boost.taskStatus.${task.status}`) }}</span>
                </div>
                <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-secondary" role="progressbar" :aria-valuenow="task.progress_percent" aria-valuemin="0" aria-valuemax="100" :aria-label="t('boost.progress')">
                  <div class="h-full rounded-full bg-primary transition-all" :style="{ width: `${task.progress_percent}%` }"></div>
                </div>
                <div class="mt-2 flex flex-wrap justify-between gap-2 text-xs text-muted-foreground">
                  <span>{{ task.progress_text }}</span>
                  <span class="font-mono">{{ t('boost.held') }} {{ task.points_held }} · {{ t('boost.consumed') }} {{ task.points_consumed }} · {{ t('boost.released') }} {{ task.points_released }}</span>
                </div>
              </Card>
            </router-link>
          </li>
        </ul>
        <div v-if="pagination.total_page > 1" class="mt-6 flex items-center justify-center gap-3 text-sm">
          <Button size="sm" variant="outline" :disabled="pagination.page <= 1" @click="loadList(pagination.page - 1)">{{ t('boost.prevPage') }}</Button>
          <span class="text-muted-foreground">{{ pagination.page }} / {{ pagination.total_page }}</span>
          <Button size="sm" variant="outline" :disabled="pagination.page >= pagination.total_page" @click="loadList(pagination.page + 1)">{{ t('boost.nextPage') }}</Button>
        </div>
      </template>

      <!-- 详情 -->
      <template v-else>
        <div v-if="!detail && detailLoading" class="h-96 rounded-2xl border bg-muted animate-pulse"></div>
        <EmptyState v-else-if="!detail" variant="soft" :title="t('boost.noTasks')" action-to="/boost/tasks" :action-label="t('boost.backToTasks')" />
        <div v-else class="space-y-4">
          <Card class="rounded-2xl p-5">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <h1 class="text-2xl font-black tracking-tight">{{ detail.snapshot.name }}</h1>
                <div class="text-sm text-muted-foreground">{{ detail.task.game_name }} · {{ detail.task.binding_name }} {{ detail.task.login_masked }}</div>
                <div class="mt-1 text-xs text-muted-foreground">{{ detail.task.task_no }} · {{ t('boost.createdAt') }} {{ fmtTime(detail.task.created_at) }}</div>
              </div>
              <span class="rounded-full border px-3 py-1 text-sm" :class="statusTone(detail.task.status)">{{ t(`boost.taskStatus.${detail.task.status}`) }}</span>
            </div>
            <p class="mt-3 text-sm">{{ detail.task.summary }}</p>

            <div class="mt-4 h-2 overflow-hidden rounded-full bg-secondary" role="progressbar" :aria-valuenow="detail.task.progress_percent" aria-valuemin="0" aria-valuemax="100" :aria-label="t('boost.progress')">
              <div class="h-full rounded-full bg-primary transition-all" :style="{ width: `${detail.task.progress_percent}%` }"></div>
            </div>
            <div class="mt-2 text-sm text-muted-foreground" aria-live="polite">{{ detail.task.progress_text }}</div>
            <p v-if="detail.task.cancel_requested" class="mt-2 text-sm text-amber-700">{{ t('boost.cancelRequested') }}</p>

            <dl class="mt-4 grid grid-cols-3 gap-3 text-center">
              <div class="rounded-lg bg-secondary/60 p-3"><dt class="text-xs text-muted-foreground">{{ t('boost.held') }}</dt><dd class="font-mono text-xl font-bold">{{ detail.task.points_held }}</dd></div>
              <div class="rounded-lg bg-secondary/60 p-3"><dt class="text-xs text-muted-foreground">{{ t('boost.consumed') }}</dt><dd class="font-mono text-xl font-bold">{{ detail.task.points_consumed }}</dd></div>
              <div class="rounded-lg bg-secondary/60 p-3"><dt class="text-xs text-muted-foreground">{{ t('boost.released') }}</dt><dd class="font-mono text-xl font-bold text-emerald-600">{{ detail.task.points_released }}</dd></div>
            </dl>
          </Card>

          <!-- 需要验证码 -->
          <Card v-if="detail.task.status === 'WAIT_INPUT' && detail.input" class="rounded-2xl border-amber-300 p-5">
            <form class="flex flex-wrap items-end gap-2" @submit.prevent="submitCode">
              <div>
                <label for="task-code" class="mb-1 block text-sm font-medium">{{ detail.input.prompt || t('boost.needCode') }}</label>
                <Input id="task-code" v-model="code" class="w-44" maxlength="64" inputmode="numeric" autocomplete="one-time-code" />
              </div>
              <Button type="submit" :disabled="busy">{{ t('boost.submitCode') }}</Button>
              <span class="text-xs text-muted-foreground">{{ t('boost.inputExpires', { time: new Date(detail.input.expires_at).toLocaleTimeString() }) }}</span>
            </form>
          </Card>

          <!-- 结算明细：每一步 完成数 × 单价，和服务端结算同一套数据 -->
          <Card v-if="detail.settlement" class="rounded-2xl p-5">
            <h2 class="mb-2 font-semibold">
              {{ t('boost.settlementTitle') }}
              <span class="ml-1 text-xs font-normal text-muted-foreground">{{ detail.settlement.final ? t('boost.settled') : t('boost.estimate') }}</span>
            </h2>
            <table class="w-full text-sm">
              <thead class="text-xs text-muted-foreground">
                <tr>
                  <th class="py-1 text-left font-normal">{{ t('boost.steps') }}</th>
                  <th class="text-right font-normal">{{ t('boost.doneOfPlan') }}</th>
                  <th class="text-right font-normal">{{ t('boost.unitPrice') }}</th>
                  <th class="text-right font-normal">{{ t('boost.subtotal') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="l in detail.settlement.lines" :key="l.seq" class="border-t">
                  <td class="py-1.5" :class="l.done >= l.count ? 'text-muted-foreground' : ''">{{ l.label }}</td>
                  <td class="text-right font-mono text-xs" :class="l.done >= l.count ? 'text-emerald-600' : ''">{{ l.done }} / {{ l.count }}</td>
                  <td class="text-right font-mono text-xs">{{ l.unit_points }}</td>
                  <td class="text-right font-mono">{{ l.points }}</td>
                </tr>
                <tr v-if="detail.settlement.fixed_points" class="border-t">
                  <td class="py-1.5" colspan="3">{{ detail.settlement.fixed_charged ? t('boost.fixedCharged') : t('boost.fixedNotCharged') }}</td>
                  <td class="text-right font-mono">{{ detail.settlement.fixed_charged ? detail.settlement.fixed_points : 0 }}</td>
                </tr>
                <tr v-if="detail.settlement.rate < 100" class="border-t">
                  <td class="py-1.5" colspan="3">{{ t('boost.settleDiscount', { rate: detail.settlement.rate / 10, raw: detail.settlement.raw_points }) }}</td>
                  <td class="text-right font-mono">{{ detail.settlement.by_units }}</td>
                </tr>
              </tbody>
            </table>
            <p class="mt-3 text-xs text-muted-foreground">{{ detail.settlement.rule }}</p>
          </Card>

          <Card class="rounded-2xl p-5">
            <h2 class="mb-2 font-semibold">{{ t('boost.events') }}</h2>
            <ul class="space-y-1 text-sm">
              <li v-for="ev in detail.events" :key="ev.id" class="flex gap-3">
                <span class="shrink-0 font-mono text-xs text-muted-foreground">{{ new Date(ev.created_at).toLocaleTimeString() }}</span>
                <span>{{ ev.message }}</span>
              </li>
            </ul>
          </Card>

          <div v-if="canCancel" class="flex flex-wrap items-center justify-end gap-2">
            <template v-if="confirmingCancel">
              <span class="text-sm" role="alert">{{ detail.task.status === 'PROCESSING' ? t('boost.cancelRunningConfirm') : t('boost.cancelQueuedConfirm', { points: detail.task.points_held }) }}</span>
              <Button variant="destructive" :disabled="busy" @click="cancel">{{ t('boost.cancelTask') }}</Button>
              <Button variant="outline" @click="confirmingCancel = false">{{ t('boost.cancel') }}</Button>
            </template>
            <Button v-else variant="outline" @click="confirmingCancel = true">{{ t('boost.cancelTask') }}</Button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
// BOOST-EXT: 用户代练任务列表和详情。状态、进度、结算都来自服务端，进行中时自动刷新。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import EmptyState from '../components/EmptyState.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { toast } from '../composables/useToast'
import { usePageSeo } from '../composables/usePageSeo'
import { boostTaskAPI, isActiveTask, statusTone, type BoostPagination, type BoostTask, type BoostTaskDetail } from '../api/boost'

const { t } = useI18n()
const route = useRoute()
const taskNo = computed(() => (typeof route.params.taskNo === 'string' ? route.params.taskNo : ''))
usePageSeo({ title: () => t('boost.tasksTitle'), canonicalPath: () => '/boost/tasks' })

const fmtTime = (v?: string) => (v ? new Date(v).toLocaleString() : '—')

// ---- 列表 ----
type Filter = 'active' | ''
const filters: { id: Filter; label: string }[] = [
  { id: 'active', label: t('boost.filterActive') },
  { id: '', label: t('boost.filterAll') },
]
const filter = ref<Filter>('')
const tasks = ref<BoostTask[]>([])
const pagination = ref<BoostPagination>({ page: 1, page_size: 20, total: 0, total_page: 1 })
const listLoading = ref(true)

const loadList = async (page = 1, quiet = false) => {
  if (!quiet) listLoading.value = true
  try {
    const res = await boostTaskAPI.tasks(filter.value, page)
    tasks.value = res.items
    pagination.value = res.pagination
  } catch {
    // client 已提示
  } finally {
    listLoading.value = false
  }
}
const setFilter = (f: Filter) => {
  filter.value = f
  void loadList(1)
}

// ---- 详情 ----
const detail = ref<BoostTaskDetail | null>(null)
const detailLoading = ref(false)
const busy = ref(false)
const code = ref('')
const confirmingCancel = ref(false)
const canCancel = computed(() => !!detail.value && isActiveTask(detail.value.task.status) && !detail.value.task.cancel_requested)

const loadDetail = async (quiet = false) => {
  if (!taskNo.value) return
  if (!quiet) detailLoading.value = true
  try {
    detail.value = await boostTaskAPI.task(taskNo.value)
  } catch {
    if (!quiet) detail.value = null
  } finally {
    detailLoading.value = false
  }
}

const submitCode = async () => {
  const value = code.value.trim()
  if (!value) return
  busy.value = true
  try {
    await boostTaskAPI.taskInput(taskNo.value, value)
    code.value = ''
  } catch (err: any) {
    toast.error(err?.message || '')
  } finally {
    busy.value = false
    await loadDetail(true)
  }
}

const cancel = async () => {
  busy.value = true
  try {
    const res = await boostTaskAPI.cancelTask(taskNo.value)
    toast.success(res.cancel_requested ? t('boost.cancelRequested') : t('boost.cancelled', { points: res.points_released }))
    confirmingCancel.value = false
  } catch (err: any) {
    toast.error(err?.message || '')
  } finally {
    busy.value = false
    await loadDetail(true)
  }
}

// 进行中自动刷新（每 5 秒），结束后停止
let timer: ReturnType<typeof setInterval> | undefined
const startPolling = () => {
  clearInterval(timer)
  timer = setInterval(() => {
    if (taskNo.value) {
      if (detail.value && isActiveTask(detail.value.task.status)) void loadDetail(true)
    } else if (tasks.value.some((x) => isActiveTask(x.status))) {
      void loadList(pagination.value.page, true)
    }
  }, 5000)
}

watch(
  taskNo,
  (no) => {
    confirmingCancel.value = false
    if (no) void loadDetail()
    else void loadList(1)
    startPolling()
  },
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(timer))
</script>
