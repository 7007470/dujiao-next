<script setup lang="ts">
// BOOST-EXT: 某个游戏的代练功能：列表、上下架、排序，以及 JSON 编辑器 + 实时校验 + 下单表单预览 + 版本历史。
// 校验和报价全部调用服务端（同一份 featuredef 实现），前端只负责展示。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  boostAdminAPI,
  type BoostDefinition,
  type BoostFeature,
  type BoostFeatureStatus,
  type BoostFeatureVersion,
  type BoostGame,
  type BoostIssue,
  type BoostPlan,
} from '@/api/boost'
import FeatureFormPreview from '@/components/boost/FeatureFormPreview.vue'
import templates from '@/components/boost/templates.json'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import TableSkeleton from '@/components/TableSkeleton.vue'
import { notifyInfo, notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'
import { formatDate } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const gameId = computed(() => Number(route.params.id))

const game = ref<BoostGame | null>(null)
const features = ref<BoostFeature[]>([])
const loading = ref(true)

const statusText: Record<BoostFeatureStatus, string> = { draft: '草稿', active: '已上架', disabled: '已下架' }

const load = async () => {
  loading.value = true
  try {
    const [games, list] = await Promise.all([boostAdminAPI.listGames(), boostAdminAPI.listFeatures(gameId.value)])
    game.value = (games || []).find((g) => g.id === gameId.value) || null
    features.value = list || []
  } catch {
    // client 已提示
  } finally {
    loading.value = false
  }
}

const setStatus = async (f: BoostFeature, status: BoostFeatureStatus) => {
  if (status === 'disabled' && !(await confirmAction({ description: `下架后用户不能再下单「${f.name}」，已创建的任务不受影响。确定下架？` }))) return
  try {
    await boostAdminAPI.setFeatureStatus(f.id, status)
    notifySuccess(`「${f.name}」${statusText[status]}`)
    await load()
  } catch {
    // client 已提示
  }
}

const move = async (f: BoostFeature, delta: -1 | 1) => {
  try {
    features.value = (await boostAdminAPI.moveFeature(f.id, delta)) || features.value
  } catch {
    // client 已提示
  }
}

// ---------------------------------------------------------------------------
// 编辑器
// ---------------------------------------------------------------------------
const editing = ref<BoostFeature | null>(null) // null + editorOpen = 新建
const editorOpen = ref(false)
const source = ref('')
const savedSource = ref('')
const saving = ref(false)

const validation = ref<BoostValidateState>({ state: 'idle', errors: [], warnings: [] })
interface BoostValidateState {
  state: 'idle' | 'checking' | 'ok' | 'error' | 'json'
  errors: BoostIssue[]
  warnings: BoostIssue[]
  jsonError?: string
}

const previewDef = ref<BoostDefinition | null>(null)
const previewParams = ref<Record<string, unknown>>({})
const previewPlan = ref<BoostPlan | null>(null)
const previewRate = ref(100)
const previewSubmitted = ref(false)

const dirty = computed(() => source.value !== savedSource.value)

const pretty = (v: unknown) => JSON.stringify(v, null, 2)

const openEditor = (f?: BoostFeature) => {
  editing.value = f || null
  source.value = pretty(f ? f.definition : templates[0]?.definition ?? {})
  savedSource.value = f ? source.value : ''
  previewParams.value = {}
  previewSubmitted.value = false
  versions.value = []
  editorOpen.value = true
  void loadVersions()
}

const closeEditor = async () => {
  if (dirty.value && !(await confirmAction({ description: '有未保存的修改，确定放弃？', variant: 'destructive' }))) return
  editorOpen.value = false
}

const loadTemplate = async (key: string) => {
  const tpl = (templates as { key: string; definition: unknown }[]).find((x) => x.key === key)
  if (!tpl) return
  if (dirty.value && !(await confirmAction({ description: '载入示例会覆盖当前编辑内容，确定？' }))) return
  source.value = pretty(tpl.definition)
  previewParams.value = {}
}

const formatSource = () => {
  try {
    source.value = pretty(JSON.parse(source.value))
  } catch {
    notifyInfo('JSON 有语法错误，无法格式化')
  }
}

// 输入停顿 400ms 后：本地解析 JSON → 服务端校验 → 合法时服务端编译预览
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
const scheduleCheck = () => {
  clearTimeout(timer)
  timer = setTimeout(runCheck, 400)
}
const runCheck = async () => {
  const mySeq = ++seq
  let parsed: unknown
  try {
    parsed = JSON.parse(source.value)
  } catch (e: any) {
    validation.value = { state: 'json', errors: [], warnings: [], jsonError: e?.message || 'JSON 语法错误' }
    return
  }
  validation.value = { ...validation.value, state: 'checking' }
  try {
    const result = await boostAdminAPI.validateFeature(source.value)
    if (mySeq !== seq) return
    validation.value = { state: result.ok ? 'ok' : 'error', errors: result.errors || [], warnings: result.warnings || [] }
    if (result.ok) {
      previewDef.value = parsed as BoostDefinition
      await runPreview()
    }
  } catch {
    if (mySeq === seq) validation.value = { state: 'idle', errors: [], warnings: [] }
  }
}
const runPreview = async () => {
  if (!previewDef.value || validation.value.state !== 'ok') return
  const mySeq = seq
  try {
    const plan = await boostAdminAPI.previewFeature(source.value, previewParams.value, previewRate.value)
    if (mySeq === seq) previewPlan.value = plan
  } catch {
    // client 已提示
  }
}

watch(source, scheduleCheck)
watch([previewParams, previewRate], () => void runPreview(), { deep: true })
onBeforeUnmount(() => clearTimeout(timer))

const save = async () => {
  saving.value = true
  try {
    const result = editing.value
      ? await boostAdminAPI.updateFeature(editing.value.id, editing.value.version, source.value)
      : await boostAdminAPI.createFeature(gameId.value, source.value)
    editing.value = result.feature
    source.value = pretty(result.feature.definition)
    savedSource.value = source.value
    if (result.changed) notifySuccess(`已保存为 v${result.feature.version}${result.warnings?.length ? `（${result.warnings.length} 条提示）` : ''}`)
    else notifyInfo('内容没有变化，未生成新版本')
    await Promise.all([load(), loadVersions()])
  } catch {
    // 校验错误、版本冲突等已由 client 提示；编辑器下方的校验结果会标出具体位置
    await runCheck()
  } finally {
    saving.value = false
  }
}

// ---- 版本历史 ----
const versions = ref<BoostFeatureVersion[]>([])
const loadVersions = async () => {
  if (!editing.value) return
  try {
    versions.value = (await boostAdminAPI.listFeatureVersions(editing.value.id)) || []
  } catch {
    versions.value = []
  }
}
const loadVersion = async (v: BoostFeatureVersion) => {
  if (!editing.value) return
  if (dirty.value && !(await confirmAction({ description: `载入 v${v.version} 会覆盖当前编辑内容，确定？` }))) return
  const full = await boostAdminAPI.getFeatureVersion(editing.value.id, v.version)
  source.value = pretty(full.definition)
  notifyInfo(`已载入 v${v.version}，保存后会成为新版本`)
}

// 错误路径 → 行号（在格式化后的 JSON 里找到对应的键），方便定位
const lineOf = (path: string) => {
  if (!path) return 0
  const lastKey = path.split('.').pop()?.replace(/\[\d+\]$/, '') || ''
  if (!lastKey) return 0
  const lines = source.value.split('\n')
  const i = lines.findIndex((l) => l.includes(`"${lastKey}"`))
  return i >= 0 ? i + 1 : 0
}
const editorRef = ref<HTMLTextAreaElement | null>(null)
const jumpTo = (path: string) => {
  const line = lineOf(path)
  const el = editorRef.value
  if (!line || !el) return
  const offset = source.value.split('\n').slice(0, line - 1).join('\n').length + (line > 1 ? 1 : 0)
  el.focus()
  el.setSelectionRange(offset, offset + (source.value.split('\n')[line - 1] || '').length)
  el.scrollTop = Math.max(0, (line - 5) * 20)
}

const onKeydown = (e: KeyboardEvent) => {
  if ((e.metaKey || e.ctrlKey) && e.key === 's') {
    e.preventDefault()
    if (validation.value.state === 'ok' && !saving.value) void save()
  }
  if (e.key === 'Tab') {
    e.preventDefault()
    const el = e.target as HTMLTextAreaElement
    const { selectionStart: s, selectionEnd: end } = el
    source.value = source.value.slice(0, s) + '  ' + source.value.slice(end)
    requestAnimationFrame(() => el.setSelectionRange(s + 2, s + 2))
  }
}

onMounted(load)
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <button type="button" class="text-sm text-muted-foreground hover:underline" @click="router.push('/boost/games')">← 代练游戏</button>
        <h1 class="text-2xl font-semibold">{{ game?.name || '…' }} · 代练功能</h1>
        <p class="text-sm text-muted-foreground">每个功能用一份 JSON 定义：关卡目录、模式和价格、可选项。用户端下单界面按它自动生成。</p>
      </div>
      <Button v-if="!editorOpen" @click="openEditor()">新建功能</Button>
    </div>

    <!-- 列表 -->
    <div v-if="!editorOpen" class="rounded-xl border border-border bg-card shadow-sm">
      <TableSkeleton v-if="loading" :rows="3" :columns="5" />
      <div v-else-if="!features.length" class="p-10 text-center text-sm text-muted-foreground">还没有功能。点击"新建功能"，可以从示例开始改。</div>
      <Table v-else>
        <TableHeader>
          <TableRow>
            <TableHead class="w-20">排序</TableHead>
            <TableHead>功能</TableHead>
            <TableHead>code</TableHead>
            <TableHead>版本</TableHead>
            <TableHead>状态</TableHead>
            <TableHead class="text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(f, i) in features" :key="f.id">
            <TableCell>
              <Button size="icon-sm" variant="ghost" :disabled="i === 0" aria-label="上移" @click="move(f, -1)">↑</Button>
              <Button size="icon-sm" variant="ghost" :disabled="i === features.length - 1" aria-label="下移" @click="move(f, 1)">↓</Button>
            </TableCell>
            <TableCell class="font-medium">{{ f.name }}</TableCell>
            <TableCell class="font-mono text-xs">{{ f.code }}</TableCell>
            <TableCell>v{{ f.version }}</TableCell>
            <TableCell>{{ statusText[f.status] }}</TableCell>
            <TableCell class="space-x-1 text-right whitespace-nowrap">
              <Button size="sm" variant="outline" @click="openEditor(f)">编辑</Button>
              <Button v-if="f.status !== 'active'" size="sm" @click="setStatus(f, 'active')">上架</Button>
              <Button v-else size="sm" variant="ghost" @click="setStatus(f, 'disabled')">下架</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <!-- 编辑器 -->
    <div v-else class="grid grid-cols-1 gap-4 xl:grid-cols-2">
      <section class="flex min-w-0 flex-col gap-3 rounded-xl border border-border bg-card p-4 shadow-sm">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="mr-auto font-semibold">
            {{ editing ? `编辑「${editing.name}」 · 当前 v${editing.version}` : '新建功能' }}
            <span v-if="dirty" class="ml-1 text-xs font-normal text-amber-600">未保存</span>
          </h2>
          <select class="h-8 rounded-md border border-input bg-transparent px-2 text-xs" aria-label="载入示例" @change="loadTemplate(($event.target as HTMLSelectElement).value); ($event.target as HTMLSelectElement).value = ''">
            <option value="">载入示例…</option>
            <option v-for="tpl in templates" :key="tpl.key" :value="tpl.key">{{ tpl.label }}</option>
          </select>
          <Button size="sm" variant="outline" @click="formatSource">格式化</Button>
        </div>

        <textarea
          ref="editorRef"
          v-model="source"
          spellcheck="false"
          aria-label="功能定义 JSON"
          class="h-[560px] w-full resize-y rounded-md border border-input bg-muted/20 p-3 font-mono text-xs leading-5 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          @keydown="onKeydown"
        />

        <div class="rounded-md border p-3 text-sm" :class="{
          'border-emerald-200 bg-emerald-50 text-emerald-800': validation.state === 'ok',
          'border-destructive/40 bg-destructive/5': validation.state === 'error' || validation.state === 'json',
          'border-border text-muted-foreground': validation.state === 'idle' || validation.state === 'checking',
        }" role="status">
          <template v-if="validation.state === 'checking'">校验中…</template>
          <template v-else-if="validation.state === 'json'">JSON 语法错误：{{ validation.jsonError }}</template>
          <template v-else-if="validation.state === 'ok'">定义合法，可以保存<span v-if="validation.warnings.length">（{{ validation.warnings.length }} 条提示）</span></template>
          <template v-else-if="validation.state === 'error'">
            <div class="mb-1 font-medium text-destructive">{{ validation.errors.length }} 处错误，修正后才能保存：</div>
            <ul class="max-h-48 space-y-1 overflow-auto">
              <li v-for="(e, i) in validation.errors" :key="i">
                <button type="button" class="text-left hover:underline" @click="jumpTo(e.path)">
                  <code class="rounded bg-muted px-1 text-xs">{{ e.path || '(根)' }}</code>
                  <span v-if="lineOf(e.path)" class="ml-1 text-xs text-muted-foreground">第 {{ lineOf(e.path) }} 行</span>
                  {{ e.msg }}
                </button>
              </li>
            </ul>
          </template>
          <ul v-if="validation.warnings.length" class="mt-2 space-y-1 text-xs text-amber-700">
            <li v-for="(w, i) in validation.warnings" :key="i"><code>{{ w.path }}</code> {{ w.msg }}</li>
          </ul>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <Button :disabled="validation.state !== 'ok' || saving || (!!editing && !dirty)" @click="save">
            {{ saving ? '保存中…' : editing ? '保存为新版本' : '创建功能' }}
          </Button>
          <Button variant="outline" @click="closeEditor">返回列表</Button>
          <span class="text-xs text-muted-foreground">Ctrl/⌘ + S 保存。保存只影响之后的新任务，已创建的任务按下单时的版本执行。</span>
        </div>

        <div v-if="editing && versions.length" class="space-y-1 border-t border-border pt-3">
          <div class="text-xs font-medium text-muted-foreground">版本历史</div>
          <div v-for="v in versions" :key="v.version" class="flex items-center gap-2 text-sm">
            <span class="w-10 font-mono">v{{ v.version }}</span>
            <span class="flex-1 text-xs text-muted-foreground">{{ formatDate(v.created_at) }}<span v-if="v.admin_id"> · 管理员 #{{ v.admin_id }}</span></span>
            <span v-if="v.version === editing.version" class="text-xs text-emerald-700">当前</span>
            <Button v-else size="xs" variant="ghost" @click="loadVersion(v)">载入</Button>
          </div>
        </div>
      </section>

      <section class="flex min-w-0 flex-col gap-3 rounded-xl border border-border bg-card p-4 shadow-sm">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="mr-auto font-semibold">下单界面预览</h2>
          <label class="flex items-center gap-1 text-xs text-muted-foreground">
            会员折扣
            <select v-model.number="previewRate" class="h-7 rounded border border-input bg-transparent px-1">
              <option :value="100">无</option>
              <option :value="95">95 折</option>
              <option :value="90">9 折</option>
              <option :value="80">8 折</option>
            </select>
          </label>
          <Button size="xs" variant="ghost" @click="previewParams = {}; previewSubmitted = false">清空</Button>
          <Button size="xs" variant="ghost" @click="previewSubmitted = true">模拟提交</Button>
        </div>
        <p class="text-xs text-muted-foreground">这就是用户看到的表单。价格由服务端按当前编辑内容实时计算，和正式下单是同一套规则。</p>

        <div v-if="validation.state !== 'ok' && !previewDef" class="p-6 text-center text-sm text-muted-foreground">定义合法后显示预览</div>
        <template v-else-if="previewDef">
          <div v-if="validation.state !== 'ok'" class="rounded-md bg-amber-50 p-2 text-xs text-amber-800">当前定义有错误，下面是上一次合法版本的预览</div>
          <div>
            <div class="text-lg font-semibold">{{ previewDef.name }}</div>
            <p v-if="previewDef.description" class="text-sm text-muted-foreground">{{ previewDef.description }}</p>
          </div>
          <FeatureFormPreview v-model="previewParams" :definition="previewDef" :plan="previewPlan" :show-errors="previewSubmitted" />

          <div v-if="previewPlan" class="space-y-2 rounded-lg border border-border bg-muted/20 p-3">
            <div class="flex items-baseline justify-between">
              <span class="text-sm text-muted-foreground">合计</span>
              <span class="text-2xl font-semibold">{{ previewPlan.total }} <span class="text-sm font-normal">积分</span></span>
            </div>
            <table class="w-full text-xs">
              <tbody>
                <tr v-for="(b, i) in previewPlan.breakdown" :key="i" class="border-t border-border/60">
                  <td class="py-1">{{ b.label }}</td>
                  <td class="py-1 text-muted-foreground">{{ b.detail }}</td>
                  <td class="py-1 text-right font-mono">{{ b.points > 0 ? '+' : '' }}{{ b.points }}</td>
                </tr>
              </tbody>
            </table>
            <div class="text-xs text-muted-foreground">执行单位 {{ previewPlan.units }} 个 · 按完成量结算，未完成部分退回</div>
            <ul v-if="previewSubmitted && previewPlan.errors.length" class="space-y-1 text-xs text-destructive" role="alert">
              <li v-for="(e, i) in previewPlan.errors" :key="i">{{ e }}</li>
            </ul>
            <div v-else-if="previewSubmitted && previewPlan.ok" class="text-xs text-emerald-700">参数合法，可以下单</div>
            <details class="text-xs">
              <summary class="cursor-pointer text-muted-foreground">执行端会收到的步骤（{{ previewPlan.steps.length }}）</summary>
              <pre class="mt-1 max-h-60 overflow-auto rounded bg-muted p-2">{{ pretty({ params: previewPlan.values, steps: previewPlan.steps.map((s) => ({ seq: s.seq, label: s.label, count: s.count, ref: s.ref })) }) }}</pre>
            </details>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>
