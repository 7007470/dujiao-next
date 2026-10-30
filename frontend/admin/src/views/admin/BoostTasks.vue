<script setup lang="ts">
// BOOST-EXT: 代练任务后台：任务列表 / 详情（步骤进度、事件、结算）、游戏账号绑定、开发用模拟执行端。
// 积分结算、状态流转都以服务端为准；模拟执行端调用的是 S6 Worker 接口会用的同一套状态机，只在 boost.dev_simulator=true 时可用。
import { computed, onMounted, reactive, ref } from 'vue'
import {
  bindingStatusText,
  boostTaskAPI,
  statusTone,
  taskStatusText,
  type BoostBinding,
  type BoostPagination,
  type BoostSimAction,
  type BoostSimulatorQueue,
  type BoostTask,
  type BoostTaskDetail,
} from '@/api/boost'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogHeader, DialogScrollContent, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import TableSkeleton from '@/components/TableSkeleton.vue'
import ListPagination from '@/components/ListPagination.vue'
import { notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'

type Tab = 'tasks' | 'bindings' | 'simulator'
const tab = ref<Tab>('tasks')
const tabs: { id: Tab; label: string }[] = [
  { id: 'tasks', label: '任务' },
  { id: 'bindings', label: '游戏账号' },
  { id: 'simulator', label: '模拟执行端（开发）' },
]
const fmtTime = (v?: string | null) => (v ? new Date(v).toLocaleString() : '—')
const emptyPage = (): BoostPagination => ({ page: 1, page_size: 20, total: 0, total_page: 1 })

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------
const tasks = ref<BoostTask[]>([])
const tasksPage = ref<BoostPagination>(emptyPage())
const tasksLoading = ref(false)
const taskFilter = reactive({ status: '', task_no: '', user_id: '' })

const loadTasks = async (page = 1) => {
  tasksLoading.value = true
  try {
    const res = await boostTaskAPI.listTasks({
      status: taskFilter.status || undefined,
      task_no: taskFilter.task_no.trim() || undefined,
      user_id: Number(taskFilter.user_id) || undefined,
      page,
      page_size: 20,
    })
    tasks.value = res.items
    tasksPage.value = res.pagination
  } catch {
    // client 已提示
  } finally {
    tasksLoading.value = false
  }
}

const detailOpen = ref(false)
const detail = ref<BoostTaskDetail | null>(null)
const detailError = ref('')
const cancelling = ref(false)

const openTask = async (taskNo: string) => {
  detailOpen.value = true
  detail.value = null
  detailError.value = ''
  try {
    detail.value = (await boostTaskAPI.getTask(taskNo)).detail
  } catch (err: any) {
    detailError.value = err?.message || '加载失败'
  }
}

const canCancel = computed(() => {
  const s = detail.value?.task.status
  return !!s && ['PENDING', 'PROCESSING', 'WAIT_INPUT'].includes(s) && !detail.value?.task.cancel_requested
})

const cancelTask = async () => {
  const task = detail.value?.task
  if (!task) return
  const running = task.status === 'PROCESSING'
  const description = running
    ? `任务 ${task.task_no} 正在执行。取消后要等执行端确认，再按已完成量结算。确定取消？`
    : `取消任务 ${task.task_no}？没有执行过的部分全额退回 ${task.points_held} 积分。`
  if (!(await confirmAction({ description, variant: 'destructive' }))) return
  cancelling.value = true
  try {
    const res = await boostTaskAPI.cancelTask(task.task_no)
    notifySuccess(res.cancel_requested ? '已记下取消请求，等待执行端确认' : `已取消，退回 ${res.points_released} 积分`)
    await openTask(task.task_no)
    await loadTasks(tasksPage.value.page)
  } catch (err: any) {
    detailError.value = err?.message || '取消失败'
  } finally {
    cancelling.value = false
  }
}

// ---------------------------------------------------------------------------
// 绑定
// ---------------------------------------------------------------------------
const bindings = ref<BoostBinding[]>([])
const bindingsPage = ref<BoostPagination>(emptyPage())
const bindingsLoading = ref(false)
const bindingFilter = reactive({ status: '', user_id: '', include_deleted: false })

const loadBindings = async (page = 1) => {
  bindingsLoading.value = true
  try {
    const res = await boostTaskAPI.listBindings({
      status: bindingFilter.status || undefined,
      user_id: Number(bindingFilter.user_id) || undefined,
      include_deleted: bindingFilter.include_deleted ? '1' : undefined,
      page,
      page_size: 20,
    })
    bindings.value = res.items
    bindingsPage.value = res.pagination
  } catch {
    // client 已提示
  } finally {
    bindingsLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 模拟执行端
// ---------------------------------------------------------------------------
const sim = ref<BoostSimulatorQueue | null>(null)
const simLoading = ref(false)
const simBusy = ref('')
const simError = ref('')
const simLog = ref<{ at: string; text: string; ok: boolean }[]>([])
type SimStep = { seq: number; label: string; count: number; unit: number; done: number }
const simForm = reactive<Record<string, { code: string; steps: SimStep[] }>>({})

// 任务的步骤（来自下单时的执行计划快照）和当前每一步完成数
const taskSteps = (t: BoostTask): SimStep[] => {
  let plan: { steps?: { seq: number; label: string; count: number; unitPoints: number }[] } = {}
  let done: number[] = []
  try { plan = JSON.parse(t.plan_json || '{}') } catch { plan = {} }
  try { done = JSON.parse(t.steps_done_json || '[]') } catch { done = [] }
  return (plan.steps || []).map((st, i) => ({ seq: st.seq, label: st.label, count: st.count, unit: st.unitPoints, done: done[i] ?? 0 }))
}

// 每个绑定 / 任务一份表单（错误码、每一步完成数）；没有就按当前状态建一份
const row = (kind: 'b' | 't', id: number, t?: BoostTask) => {
  const key = kind + id
  let r = simForm[key]
  if (!r) {
    r = { code: kind === 't' ? 'INSUFFICIENT_STAMINA' : 'LOGIN_FAILED', steps: t ? taskSteps(t) : [] }
    simForm[key] = r
  }
  return r
}
const stepReport = (id: number) => row('t', id).steps.map((st) => ({ seq: st.seq, done: Math.max(0, Math.trunc(Number(st.done) || 0)) }))
const fillAll = (id: number) => row('t', id).steps.forEach((st) => (st.done = st.count))

const loadSim = async () => {
  simLoading.value = true
  simError.value = ''
  try {
    sim.value = await boostTaskAPI.simulatorQueue()
    for (const t of sim.value.tasks || []) {
      const r = row('t', t.id, t)
      // 服务端完成数只增不减：刷新时把表单里比服务端小的值抬上来
      taskSteps(t).forEach((st, i) => {
        if (r.steps[i] && r.steps[i].done < st.done) r.steps[i].done = st.done
      })
    }
  } catch (err: any) {
    simError.value = err?.message || '加载失败'
  } finally {
    simLoading.value = false
  }
}

const act = async (kind: 'binding' | 'task', id: number, action: BoostSimAction, extra: Record<string, unknown> = {}) => {
  const key = `${kind}${id}${action}`
  simBusy.value = key
  simError.value = ''
  const label = `${kind === 'task' ? '任务' : '绑定'} #${id} ${action}`
  try {
    const res = await boostTaskAPI.simulate({ kind, id, action, ...extra })
    simLog.value.unshift({ at: new Date().toLocaleTimeString(), text: `${label} → ${JSON.stringify(res)}`, ok: true })
  } catch (err: any) {
    simLog.value.unshift({ at: new Date().toLocaleTimeString(), text: `${label} 失败：${err?.message || err}`, ok: false })
  } finally {
    simBusy.value = ''
    simLog.value = simLog.value.slice(0, 30)
    await loadSim()
  }
}

const reap = async () => {
  try {
    const res = await boostTaskAPI.reap()
    simLog.value.unshift({ at: new Date().toLocaleTimeString(), text: `回收：处理 ${res.changed} 条（输入超时 / 任务超时）`, ok: true })
    await loadSim()
  } catch (err: any) {
    simError.value = err?.message || '回收失败'
  }
}

const switchTab = (t: Tab) => {
  tab.value = t
  if (t === 'bindings' && !bindings.value.length) loadBindings()
  if (t === 'simulator') loadSim()
}

onMounted(() => loadTasks())
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-semibold">代练任务</h1>
      <p class="text-sm text-muted-foreground">用户下单时冻结积分，执行结束后按实际完成量结算，未完成部分退回。任务保存下单时的功能版本，后台改价不影响已建任务。</p>
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

    <!-- 任务 -->
    <section v-if="tab === 'tasks'" class="space-y-3">
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="loadTasks(1)">
        <div>
          <label for="bt-status" class="mb-1 block text-xs text-muted-foreground">状态</label>
          <select id="bt-status" v-model="taskFilter.status" class="h-9 rounded-md border border-input bg-background px-2 text-sm">
            <option value="">全部</option>
            <option value="active">进行中（排队 / 执行 / 等待）</option>
            <option v-for="(label, key) in taskStatusText" :key="key" :value="key">{{ label }}</option>
          </select>
        </div>
        <div>
          <label for="bt-no" class="mb-1 block text-xs text-muted-foreground">任务号</label>
          <Input id="bt-no" v-model="taskFilter.task_no" class="w-48" placeholder="BT…" />
        </div>
        <div>
          <label for="bt-user" class="mb-1 block text-xs text-muted-foreground">用户 ID</label>
          <Input id="bt-user" v-model="taskFilter.user_id" class="w-28" inputmode="numeric" />
        </div>
        <Button type="submit" variant="outline">查询</Button>
      </form>

      <div class="rounded-xl border border-border bg-card shadow-sm">
        <TableSkeleton v-if="tasksLoading" :rows="5" :columns="7" />
        <div v-else-if="!tasks.length" class="p-10 text-center text-sm text-muted-foreground">没有任务。</div>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>任务</TableHead>
              <TableHead>用户 / 账号</TableHead>
              <TableHead>状态</TableHead>
              <TableHead class="text-right">进度</TableHead>
              <TableHead class="text-right">冻结 / 消耗 / 退回</TableHead>
              <TableHead>创建</TableHead>
              <TableHead class="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="r in tasks" :key="r.id">
              <TableCell class="max-w-xs">
                <div class="font-mono text-xs">{{ r.task_no }}</div>
                <div class="truncate text-sm" :title="r.summary">{{ r.game_name }} · {{ r.summary || '—' }}</div>
              </TableCell>
              <TableCell class="text-xs">
                <div>{{ r.user_email || '#' + r.user_id }}</div>
                <div class="text-muted-foreground">{{ r.binding_name }} {{ r.login_masked }}</div>
              </TableCell>
              <TableCell>
                <span class="rounded-full border px-2 py-0.5 text-xs" :class="statusTone(r.status)">{{ taskStatusText[r.status] || r.status }}</span>
                <div v-if="r.cancel_requested" class="mt-1 text-xs text-amber-700">已请求取消</div>
                <div v-if="r.error_code" class="mt-1 font-mono text-xs text-destructive">{{ r.error_code }}</div>
              </TableCell>
              <TableCell class="text-right font-mono text-xs">{{ r.units_done }} / {{ r.units }}</TableCell>
              <TableCell class="text-right font-mono text-xs">{{ r.points_held }} / {{ r.points_consumed }} / {{ r.points_released }}</TableCell>
              <TableCell class="whitespace-nowrap text-xs text-muted-foreground">{{ fmtTime(r.created_at) }}</TableCell>
              <TableCell class="text-right">
                <Button size="sm" variant="ghost" @click="openTask(r.task_no)">详情</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <ListPagination
          v-if="tasks.length"
          :page="tasksPage.page"
          :total-page="tasksPage.total_page"
          :total="tasksPage.total"
          @change-page="loadTasks"
        />
      </div>
    </section>

    <!-- 绑定 -->
    <section v-else-if="tab === 'bindings'" class="space-y-3">
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="loadBindings(1)">
        <div>
          <label for="bb-status" class="mb-1 block text-xs text-muted-foreground">状态</label>
          <select id="bb-status" v-model="bindingFilter.status" class="h-9 rounded-md border border-input bg-background px-2 text-sm">
            <option value="">全部</option>
            <option v-for="(label, key) in bindingStatusText" :key="key" :value="key">{{ label }}</option>
          </select>
        </div>
        <div>
          <label for="bb-user" class="mb-1 block text-xs text-muted-foreground">用户 ID</label>
          <Input id="bb-user" v-model="bindingFilter.user_id" class="w-28" inputmode="numeric" />
        </div>
        <label class="flex h-9 items-center gap-2 text-sm">
          <input v-model="bindingFilter.include_deleted" type="checkbox" class="h-4 w-4" />
          含已解绑
        </label>
        <Button type="submit" variant="outline">查询</Button>
      </form>
      <p class="text-xs text-muted-foreground">登录名只显示掩码；密码、会话和验证码只以密文保存，后台看不到，解绑时立即销毁。</p>
      <div class="rounded-xl border border-border bg-card shadow-sm">
        <TableSkeleton v-if="bindingsLoading" :rows="5" :columns="6" />
        <div v-else-if="!bindings.length" class="p-10 text-center text-sm text-muted-foreground">没有游戏账号。</div>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>用户</TableHead>
              <TableHead>游戏 / 区服</TableHead>
              <TableHead>账号</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>说明</TableHead>
              <TableHead>添加时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="b in bindings" :key="b.id">
              <TableCell class="text-xs">{{ b.user_email || '#' + b.user_id }}</TableCell>
              <TableCell class="text-sm">{{ b.game_name }} · {{ b.server_name }}</TableCell>
              <TableCell class="text-sm">
                <div>{{ b.display_name }}</div>
                <div class="font-mono text-xs text-muted-foreground">{{ b.login_masked }}</div>
              </TableCell>
              <TableCell>
                <span class="rounded-full border px-2 py-0.5 text-xs" :class="statusTone(b.status)">{{ bindingStatusText[b.status] || b.status }}</span>
                <div class="mt-1 text-xs text-muted-foreground">第 {{ b.attempt }} / {{ b.max_attempts }} 次</div>
              </TableCell>
              <TableCell class="max-w-xs text-xs">
                <div>{{ b.user_message }}</div>
                <div v-if="b.last_error_code" class="font-mono text-destructive">{{ b.last_error_code }}</div>
              </TableCell>
              <TableCell class="whitespace-nowrap text-xs text-muted-foreground">{{ fmtTime(b.created_at) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <ListPagination
          v-if="bindings.length"
          :page="bindingsPage.page"
          :total-page="bindingsPage.total_page"
          :total="bindingsPage.total"
          @change-page="loadBindings"
        />
      </div>
    </section>

    <!-- 模拟执行端 -->
    <section v-else class="space-y-4">
      <div v-if="sim && !sim.enabled" class="rounded-lg border border-dashed border-border bg-muted/30 p-6 text-sm text-muted-foreground">
        模拟执行端未开启。只在开发 / 测试环境的 <code class="font-mono">config.yml</code> 里设置
        <code class="font-mono">boost.dev_simulator: true</code> 后重启服务。生产环境必须关闭；S6 的真实 Worker 接口上线后这个面板会移除。
      </div>
      <template v-else>
        <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900">
          <span>开发用：手动扮演执行端推进绑定和任务。调用的是和 Worker 接口相同的状态机和结算，凭据和验证码不会显示在这里。</span>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" :disabled="simLoading" @click="loadSim">刷新</Button>
            <Button size="sm" variant="outline" @click="reap">立即回收超时</Button>
          </div>
        </div>
        <p v-if="simError" class="text-sm text-destructive" role="alert">{{ simError }}</p>

        <div class="grid gap-4 lg:grid-cols-2">
          <div class="rounded-xl border border-border bg-card p-4 shadow-sm">
            <h2 class="mb-3 font-medium">待验证的游戏账号</h2>
            <p v-if="!sim?.bindings?.length" class="text-sm text-muted-foreground">没有。</p>
            <div v-for="b in sim?.bindings || []" :key="b.id" class="mb-3 rounded-lg border p-3 text-sm">
              <div class="flex items-center justify-between gap-2">
                <div>
                  <b>#{{ b.id }}</b> {{ b.game_name }} · {{ b.display_name }} <span class="font-mono text-xs">{{ b.login_masked }}</span>
                  <div class="text-xs text-muted-foreground">{{ b.user_email }} · 第 {{ b.attempt }} 次</div>
                </div>
                <span class="rounded-full border px-2 py-0.5 text-xs" :class="statusTone(b.status)">{{ bindingStatusText[b.status] }}</span>
              </div>
              <div class="mt-2 flex flex-wrap items-center gap-1.5">
                <template v-if="b.status === 'PENDING'">
                  <Button size="sm" :disabled="!!simBusy" @click="act('binding', b.id, 'start')">领取验证</Button>
                </template>
                <template v-else-if="b.status === 'PROCESSING'">
                  <Button size="sm" :disabled="!!simBusy" @click="act('binding', b.id, 'finish')">验证成功</Button>
                  <Button size="sm" variant="outline" :disabled="!!simBusy" @click="act('binding', b.id, 'need_input', { input_kind: 'sms_code', prompt: '请输入短信验证码' })">要验证码</Button>
                  <Button size="sm" variant="outline" :disabled="!!simBusy" @click="act('binding', b.id, 'need_input', { input_kind: 'confirm', prompt: '请在游戏内确认登录' })">要游戏内确认</Button>
                  <select v-model="row('b', b.id).code" class="h-8 rounded-md border border-input bg-background px-1 text-xs" aria-label="错误码">
                    <option v-for="c in sim?.error_codes" :key="c" :value="c">{{ c }}</option>
                  </select>
                  <Button size="sm" variant="destructive" :disabled="!!simBusy" @click="act('binding', b.id, 'fail', { error_code: row('b', b.id).code })">失败</Button>
                </template>
                <span v-else class="text-xs text-muted-foreground">等待用户：{{ b.input?.prompt || b.user_message }}</span>
              </div>
            </div>
          </div>

          <div class="rounded-xl border border-border bg-card p-4 shadow-sm">
            <h2 class="mb-3 font-medium">进行中的任务</h2>
            <p v-if="!sim?.tasks?.length" class="text-sm text-muted-foreground">没有。</p>
            <div v-for="t in sim?.tasks || []" :key="t.id" class="mb-3 rounded-lg border p-3 text-sm">
              <div class="flex items-center justify-between gap-2">
                <div class="min-w-0">
                  <b>#{{ t.id }}</b> <span class="font-mono text-xs">{{ t.task_no }}</span>
                  <div class="truncate text-xs text-muted-foreground" :title="t.summary">{{ t.game_name }} · {{ t.summary }}</div>
                  <div class="text-xs">完成 {{ t.units_done }} / {{ t.units }} · 冻结 {{ t.points_held }} · 第 {{ t.attempt }} 次</div>
                </div>
                <div class="text-right">
                  <span class="rounded-full border px-2 py-0.5 text-xs" :class="statusTone(t.status)">{{ taskStatusText[t.status] }}</span>
                  <div v-if="t.cancel_requested" class="mt-1 text-xs text-amber-700">用户请求取消</div>
                </div>
              </div>
              <div class="mt-2 flex flex-wrap items-center gap-1.5">
                <template v-if="t.status === 'PENDING'">
                  <Button size="sm" :disabled="!!simBusy" @click="act('task', t.id, 'start')">领取执行</Button>
                </template>
                <template v-else-if="t.status === 'PROCESSING'">
                  <!-- 按步骤上报：每一步各自的完成数，结算按 完成数 × 单价，不看执行顺序 -->
                  <table class="w-full text-xs">
                    <thead class="text-muted-foreground"><tr><th class="text-left font-normal">步骤</th><th class="text-right font-normal">单价</th><th class="text-right font-normal">完成 / 计划</th></tr></thead>
                    <tbody>
                      <tr v-for="st in row('t', t.id).steps" :key="st.seq">
                        <td class="py-0.5 pr-2">{{ st.seq }}. {{ st.label }}</td>
                        <td class="text-right font-mono">{{ st.unit }}</td>
                        <td class="text-right">
                          <input v-model.number="st.done" type="number" min="0" :max="st.count" :aria-label="`${st.label} 完成数`" class="h-7 w-14 rounded-md border border-input bg-background px-1 text-right text-xs" />
                          <span class="ml-1 font-mono text-muted-foreground">/ {{ st.count }}</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <Button size="sm" variant="ghost" @click="fillAll(t.id)">全部填满</Button>
                  <Button size="sm" variant="outline" :disabled="!!simBusy" @click="act('task', t.id, 'progress', { steps: stepReport(t.id) })">上报进度</Button>
                  <Button size="sm" :disabled="!!simBusy" @click="act('task', t.id, 'finish', { steps: stepReport(t.id) })">完成</Button>
                  <Button size="sm" variant="outline" :disabled="!!simBusy" @click="act('task', t.id, 'need_input', { input_kind: 'sms_code', prompt: '请输入短信验证码' })">要验证码</Button>
                  <Button v-if="t.cancel_requested" size="sm" variant="outline" :disabled="!!simBusy" @click="act('task', t.id, 'cancel_ack', { steps: stepReport(t.id) })">确认取消</Button>
                  <select v-model="row('t', t.id).code" class="h-8 rounded-md border border-input bg-background px-1 text-xs" aria-label="错误码">
                    <option v-for="c in sim?.error_codes" :key="c" :value="c">{{ c }}</option>
                  </select>
                  <Button size="sm" variant="destructive" :disabled="!!simBusy" @click="act('task', t.id, 'fail', { error_code: row('t', t.id).code, steps: stepReport(t.id) })">失败</Button>
                </template>
                <span v-else class="text-xs text-muted-foreground">等待用户提交验证码</span>
              </div>
            </div>
          </div>
        </div>

        <div class="rounded-xl border border-border bg-card p-4 shadow-sm">
          <h2 class="mb-2 font-medium">操作记录</h2>
          <p v-if="!simLog.length" class="text-sm text-muted-foreground">还没有操作。</p>
          <ul class="space-y-1 font-mono text-xs" aria-live="polite">
            <li v-for="(l, i) in simLog" :key="i" :class="l.ok ? '' : 'text-destructive'">{{ l.at }} {{ l.text }}</li>
          </ul>
        </div>
      </template>
    </section>

    <!-- 任务详情 -->
    <Dialog v-model:open="detailOpen">
      <DialogScrollContent class="max-w-3xl">
        <DialogHeader>
          <DialogTitle>任务 {{ detail?.task.task_no }}</DialogTitle>
        </DialogHeader>
        <p v-if="detailError" class="text-sm text-destructive" role="alert">{{ detailError }}</p>
        <div v-if="!detail && !detailError" class="p-6 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else-if="detail" class="space-y-4 text-sm">
          <div class="flex flex-wrap items-center gap-2">
            <span class="rounded-full border px-2 py-0.5 text-xs" :class="statusTone(detail.task.status)">{{ taskStatusText[detail.task.status] }}</span>
            <span v-if="detail.task.cancel_requested" class="text-xs text-amber-700">已请求取消，等待执行端确认</span>
            <span v-if="detail.task.error_code" class="font-mono text-xs text-destructive">{{ detail.task.error_code }}</span>
            <span class="text-xs text-muted-foreground">{{ detail.task.progress_text }}</span>
          </div>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4">
            <div><dt class="text-xs text-muted-foreground">功能</dt><dd>{{ detail.snapshot.name }} v{{ detail.task.feature_version }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">用户</dt><dd>{{ detail.task.user_email || '#' + detail.task.user_id }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">账号</dt><dd>{{ detail.task.binding_name }} {{ detail.task.login_masked }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">退款策略</dt><dd class="font-mono text-xs">{{ detail.snapshot.refund_policy }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">冻结</dt><dd class="font-mono">{{ detail.task.points_held }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">消耗</dt><dd class="font-mono">{{ detail.task.points_consumed }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">退回</dt><dd class="font-mono">{{ detail.task.points_released }}</dd></div>
            <div><dt class="text-xs text-muted-foreground">时限</dt><dd class="text-xs">{{ fmtTime(detail.task.deadline_at) }}</dd></div>
          </dl>
          <div v-if="detail.settlement">
            <h3 class="mb-1 font-medium">结算明细 <span class="text-xs font-normal text-muted-foreground">{{ detail.settlement.final ? '已结算' : '预估' }}</span></h3>
            <table class="w-full text-xs">
              <thead class="text-muted-foreground"><tr><th class="py-1 text-left font-normal">步骤</th><th class="text-right font-normal">完成 / 计划</th><th class="text-right font-normal">单价</th><th class="text-right font-normal">小计</th></tr></thead>
              <tbody>
                <tr v-for="l in detail.settlement.lines" :key="l.seq" class="border-t">
                  <td class="py-1">{{ l.seq }}. {{ l.label }}</td>
                  <td class="text-right font-mono">{{ l.done }} / {{ l.count }}</td>
                  <td class="text-right font-mono">{{ l.unit_points }}</td>
                  <td class="text-right font-mono">{{ l.points }}</td>
                </tr>
                <tr v-if="detail.settlement.fixed_points" class="border-t">
                  <td class="py-1" colspan="3">基础价 / 附加项{{ detail.settlement.fixed_charged ? '' : '（未开始执行，不收）' }}</td>
                  <td class="text-right font-mono">{{ detail.settlement.fixed_charged ? detail.settlement.fixed_points : 0 }}</td>
                </tr>
                <tr class="border-t">
                  <td class="py-1" colspan="3">原价小计{{ detail.settlement.rate < 100 ? `，会员 ${detail.settlement.rate / 10} 折（向下取整，不超过冻结）` : '' }}</td>
                  <td class="text-right font-mono">{{ detail.settlement.raw_points }} → {{ detail.settlement.by_units }}</td>
                </tr>
                <tr class="border-t font-medium">
                  <td class="py-1" colspan="3">冻结 {{ detail.settlement.held }} · 扣除 {{ detail.settlement.consumed }} · 退回 {{ detail.settlement.released }}</td>
                  <td></td>
                </tr>
              </tbody>
            </table>
            <p class="mt-1 text-xs text-muted-foreground">{{ detail.settlement.rule }}</p>
          </div>
          <div>
            <h3 class="mb-1 font-medium">报价明细（下单时）</h3>
            <ul class="space-y-0.5 text-xs">
              <li v-for="(b, i) in detail.breakdown" :key="i" class="flex justify-between gap-2">
                <span>{{ b.label }} <span class="text-muted-foreground">{{ b.detail }}</span></span>
                <span class="font-mono">{{ b.points }}</span>
              </li>
            </ul>
          </div>
          <div>
            <h3 class="mb-1 font-medium">事件（最近 50 条，已脱敏）</h3>
            <ul class="max-h-60 space-y-0.5 overflow-auto rounded border bg-muted/30 p-2 font-mono text-xs">
              <li v-for="ev in detail.events" :key="ev.id">#{{ ev.seq }} {{ fmtTime(ev.created_at) }} [{{ ev.type }}] {{ ev.message }}</li>
            </ul>
          </div>
          <div class="flex justify-end">
            <Button v-if="canCancel" variant="destructive" :disabled="cancelling" @click="cancelTask">取消任务</Button>
          </div>
        </div>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
