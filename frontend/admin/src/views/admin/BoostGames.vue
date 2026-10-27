<script setup lang="ts">
// BOOST-EXT: 代练游戏列表 + 区服管理。
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { boostAdminAPI, type BoostGame, type BoostGameStatus, type BoostServer } from '@/api/boost'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogHeader, DialogScrollContent, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import TableSkeleton from '@/components/TableSkeleton.vue'
import MediaPicker from '@/components/admin/MediaPicker.vue'
import { getImageUrl } from '@/utils/image'
import { notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'

const router = useRouter()

const games = ref<BoostGame[]>([])
const loading = ref(true)

const statusText: Record<BoostGameStatus, string> = { draft: '草稿', active: '已上线', hidden: '已隐藏' }
const statusClass: Record<BoostGameStatus, string> = {
  draft: 'text-muted-foreground border-border bg-muted/30',
  active: 'text-emerald-700 border-emerald-200 bg-emerald-50',
  hidden: 'text-amber-700 border-amber-200 bg-amber-50',
}

const load = async () => {
  loading.value = true
  try {
    games.value = (await boostAdminAPI.listGames()) || []
  } catch {
    // 错误已由 client 统一提示
  } finally {
    loading.value = false
  }
}

// ---- 新建 / 编辑游戏 ----
const gameDialog = ref(false)
const gameSaving = ref(false)
const gameError = ref('')
const gameForm = reactive({ id: 0, version: 0, code: '', name: '', short_name: '', color: '#2f6f6a', icon_url: '', sort: 0 })

const openGame = (g?: BoostGame) => {
  Object.assign(gameForm, g
    ? { id: g.id, version: g.version, code: g.code, name: g.name, short_name: g.short_name, color: g.color, icon_url: g.icon_url || '', sort: g.sort }
    : { id: 0, version: 0, code: '', name: '', short_name: '', color: '#2f6f6a', icon_url: '', sort: games.value.length + 1 })
  gameError.value = ''
  gameDialog.value = true
}

const saveGame = async () => {
  gameSaving.value = true
  gameError.value = ''
  try {
    const payload = { code: gameForm.code.trim(), name: gameForm.name.trim(), short_name: gameForm.short_name.trim(), color: gameForm.color, icon_url: gameForm.icon_url, sort: Number(gameForm.sort) || 0 }
    if (gameForm.id) await boostAdminAPI.updateGame(gameForm.id, { ...payload, version: gameForm.version })
    else await boostAdminAPI.createGame(payload)
    gameDialog.value = false
    notifySuccess(gameForm.id ? '游戏已更新' : '游戏已创建')
    await load()
  } catch (err: any) {
    gameError.value = err?.message || '保存失败'
  } finally {
    gameSaving.value = false
  }
}

const setStatus = async (g: BoostGame, status: BoostGameStatus) => {
  if (status === 'hidden' && !(await confirmAction({ description: `隐藏后用户将看不到「${g.name}」，进行中的任务不受影响。确定隐藏？` }))) return
  try {
    await boostAdminAPI.setGameStatus(g.id, status)
    notifySuccess(`「${g.name}」${statusText[status]}`)
    await load()
  } catch {
    // 例如"上线前至少要有一个启用的区服"，client 已提示
  }
}

// ---- 区服 ----
const serverDialog = ref(false)
const serverGame = ref<BoostGame | null>(null)
const servers = ref<BoostServer[]>([])
const serverError = ref('')
const serverForm = reactive({ id: 0, code: '', name: '', platform: 'all' as BoostServer['platform'], sort: 0 })
const platformText: Record<BoostServer['platform'], string> = { all: '全平台', android: '安卓', ios: 'iOS', pc: 'PC' }

const openServers = async (g: BoostGame) => {
  serverGame.value = g
  serverDialog.value = true
  resetServerForm()
  servers.value = (await boostAdminAPI.listServers(g.id)) || []
}
const resetServerForm = () => {
  Object.assign(serverForm, { id: 0, code: '', name: '', platform: 'all', sort: servers.value.length + 1 })
  serverError.value = ''
}
const editServer = (s: BoostServer) => {
  Object.assign(serverForm, { id: s.id, code: s.code, name: s.name, platform: s.platform, sort: s.sort })
  serverError.value = ''
}
const saveServer = async () => {
  if (!serverGame.value) return
  serverError.value = ''
  const gid = serverGame.value.id
  const payload = { code: serverForm.code.trim(), name: serverForm.name.trim(), platform: serverForm.platform, sort: Number(serverForm.sort) || 0 }
  try {
    if (serverForm.id) await boostAdminAPI.updateServer(gid, serverForm.id, payload)
    else await boostAdminAPI.createServer(gid, payload)
    servers.value = (await boostAdminAPI.listServers(gid)) || []
    resetServerForm()
    await load()
  } catch (err: any) {
    serverError.value = err?.message || '保存失败'
  }
}
const toggleServer = async (s: BoostServer) => {
  if (!serverGame.value) return
  try {
    await boostAdminAPI.setServerStatus(serverGame.value.id, s.id, s.status === 'active' ? 'disabled' : 'active')
    servers.value = (await boostAdminAPI.listServers(serverGame.value.id)) || []
    await load()
  } catch {
    // client 已提示
  }
}

const readiness = (g: BoostGame) => {
  const missing: string[] = []
  if (!g.active_server_count) missing.push('启用的区服')
  if (!g.active_feature_count) missing.push('已上架的功能')
  return missing
}
const empty = computed(() => !loading.value && games.value.length === 0)

onMounted(load)
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">代练游戏</h1>
        <p class="text-sm text-muted-foreground">添加游戏和区服，再为游戏配置代练功能。游戏上线前至少要有一个启用的区服和一个已上架的功能。</p>
      </div>
      <Button @click="openGame()">新建游戏</Button>
    </div>

    <div class="rounded-xl border border-border bg-card shadow-sm">
      <TableSkeleton v-if="loading" :rows="4" :columns="6" />
      <div v-else-if="empty" class="p-10 text-center text-sm text-muted-foreground">
        还没有游戏。点击右上角"新建游戏"开始。
      </div>
      <Table v-else>
        <TableHeader>
          <TableRow>
            <TableHead>游戏</TableHead>
            <TableHead>code</TableHead>
            <TableHead>状态</TableHead>
            <TableHead>区服</TableHead>
            <TableHead>功能</TableHead>
            <TableHead class="text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="g in games" :key="g.id">
            <TableCell>
              <div class="flex items-center gap-2">
                <img v-if="g.icon_url" :src="getImageUrl(g.icon_url)" :alt="g.name" class="h-8 w-8 rounded-md object-cover" />
                <span v-else class="inline-flex h-8 w-8 items-center justify-center rounded-md text-xs font-semibold text-white" :style="{ background: g.color }">{{ g.short_name }}</span>
                <span class="font-medium">{{ g.name }}</span>
              </div>
            </TableCell>
            <TableCell class="font-mono text-xs">{{ g.code }}</TableCell>
            <TableCell>
              <span class="rounded-full border px-2 py-0.5 text-xs" :class="statusClass[g.status]">{{ statusText[g.status] }}</span>
              <div v-if="g.status !== 'active' && readiness(g).length" class="mt-1 text-[11px] text-muted-foreground">上线还缺：{{ readiness(g).join('、') }}</div>
            </TableCell>
            <TableCell class="text-sm">{{ g.active_server_count }} / {{ g.server_count }}</TableCell>
            <TableCell class="text-sm">{{ g.active_feature_count }} / {{ g.feature_count }}</TableCell>
            <TableCell class="space-x-1 text-right whitespace-nowrap">
              <Button size="sm" variant="outline" @click="router.push(`/boost/games/${g.id}/features`)">功能</Button>
              <Button size="sm" variant="outline" @click="openServers(g)">区服</Button>
              <Button size="sm" variant="ghost" @click="openGame(g)">编辑</Button>
              <Button v-if="g.status !== 'active'" size="sm" :disabled="readiness(g).length > 0" :title="readiness(g).length ? '上线还缺：' + readiness(g).join('、') : ''" @click="setStatus(g, 'active')">上线</Button>
              <Button v-else size="sm" variant="ghost" @click="setStatus(g, 'hidden')">隐藏</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <!-- 游戏 -->
    <Dialog v-model:open="gameDialog">
      <DialogScrollContent class="max-w-lg">
        <DialogHeader><DialogTitle>{{ gameForm.id ? '编辑游戏' : '新建游戏' }}</DialogTitle></DialogHeader>
        <form class="space-y-4" @submit.prevent="saveGame">
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bg-code">code（创建后不可修改，执行端按它识别游戏）</label>
            <Input id="bg-code" v-model="gameForm.code" :disabled="!!gameForm.id" placeholder="例如 starfall" maxlength="32" />
          </div>
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground" for="bg-name">名称</label>
            <Input id="bg-name" v-model="gameForm.name" maxlength="30" />
          </div>
          <div class="space-y-1">
            <label class="text-xs font-medium text-muted-foreground">图标（建议正方形，至少 128×128）</label>
            <MediaPicker v-model="gameForm.icon_url" scene="category" />
          </div>
          <div class="grid grid-cols-3 gap-3">
            <div class="space-y-1">
              <label class="text-xs font-medium text-muted-foreground" for="bg-short">简称（无图标时显示）</label>
              <Input id="bg-short" v-model="gameForm.short_name" maxlength="4" placeholder="默认取名称前两字" />
            </div>
            <div class="space-y-1">
              <label class="text-xs font-medium text-muted-foreground" for="bg-color">颜色</label>
              <input id="bg-color" v-model="gameForm.color" type="color" class="h-9 w-full rounded-md border border-input" />
            </div>
            <div class="space-y-1">
              <label class="text-xs font-medium text-muted-foreground" for="bg-sort">排序</label>
              <Input id="bg-sort" v-model.number="gameForm.sort" type="number" />
            </div>
          </div>
          <p v-if="gameError" class="text-sm text-destructive" role="alert">{{ gameError }}</p>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="gameDialog = false">取消</Button>
            <Button type="submit" :disabled="gameSaving">{{ gameSaving ? '保存中…' : '保存' }}</Button>
          </div>
        </form>
      </DialogScrollContent>
    </Dialog>

    <!-- 区服 -->
    <Dialog v-model:open="serverDialog">
      <DialogScrollContent class="max-w-2xl">
        <DialogHeader><DialogTitle>区服 · {{ serverGame?.name }}</DialogTitle></DialogHeader>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名称</TableHead>
              <TableHead>code</TableHead>
              <TableHead>平台</TableHead>
              <TableHead>状态</TableHead>
              <TableHead class="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-if="!servers.length">
              <TableCell colspan="5" class="text-center text-sm text-muted-foreground">还没有区服</TableCell>
            </TableRow>
            <TableRow v-for="s in servers" :key="s.id">
              <TableCell>{{ s.name }}</TableCell>
              <TableCell class="font-mono text-xs">{{ s.code }}</TableCell>
              <TableCell>{{ platformText[s.platform] }}</TableCell>
              <TableCell>{{ s.status === 'active' ? '启用' : '停用' }}</TableCell>
              <TableCell class="space-x-1 text-right">
                <Button size="xs" variant="ghost" @click="editServer(s)">编辑</Button>
                <Button size="xs" variant="ghost" @click="toggleServer(s)">{{ s.status === 'active' ? '停用' : '启用' }}</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <form class="mt-4 grid grid-cols-1 gap-3 rounded-lg border border-border p-3 sm:grid-cols-5" @submit.prevent="saveServer">
          <div class="text-xs font-medium text-muted-foreground sm:col-span-5">{{ serverForm.id ? '修改区服' : '添加区服' }}</div>
          <Input v-model="serverForm.name" placeholder="名称，例如 国服 · 天空岛" aria-label="区服名称" class="sm:col-span-2" />
          <Input v-model="serverForm.code" placeholder="code，例如 cn-sky" aria-label="区服 code" />
          <select v-model="serverForm.platform" aria-label="平台" class="h-9 rounded-md border border-input bg-transparent px-2 text-sm">
            <option v-for="(label, value) in platformText" :key="value" :value="value">{{ label }}</option>
          </select>
          <div class="flex gap-2">
            <Button type="submit" size="sm" class="flex-1">{{ serverForm.id ? '保存' : '添加' }}</Button>
            <Button v-if="serverForm.id" type="button" size="sm" variant="ghost" @click="resetServerForm">取消</Button>
          </div>
          <p v-if="serverError" class="text-sm text-destructive sm:col-span-5" role="alert">{{ serverError }}</p>
        </form>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
