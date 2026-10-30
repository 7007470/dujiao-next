<template>
  <div class="boost-page min-h-screen bg-background text-foreground pt-20 pb-24 lg:pb-16">
    <div class="container mx-auto max-w-4xl px-4">
      <div class="mt-8 mb-6 flex flex-wrap items-center justify-between gap-3">
        <router-link to="/boost" class="text-sm text-muted-foreground hover:text-primary">{{ t('boost.backToGames') }}</router-link>
        <div class="flex gap-3 text-sm">
          <router-link to="/boost/tasks" class="text-primary hover:underline">{{ t('boost.myTasks') }}</router-link>
          <router-link to="/boost/points" class="text-primary hover:underline">{{ t('boost.pointsTitle') }}</router-link>
        </div>
      </div>

      <div class="mb-6 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-3xl font-black tracking-tight">{{ t('boost.accountsTitle') }}</h1>
          <p class="mt-1 max-w-2xl text-sm text-muted-foreground">{{ t('boost.accountsHint') }}</p>
        </div>
        <Button v-if="!formOpen" @click="openForm()">{{ t('boost.addAccount') }}</Button>
      </div>

      <!-- 添加账号 -->
      <Card v-if="formOpen" class="mb-6 rounded-2xl p-5">
        <form class="grid grid-cols-1 gap-4 sm:grid-cols-2" autocomplete="off" @submit.prevent="submitForm">
          <div>
            <label for="ba-game" class="mb-1 block text-sm font-medium">{{ t('boost.game') }}</label>
            <select id="ba-game" v-model.number="form.game_id" class="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" required @change="form.server_id = 0">
              <option :value="0" disabled>—</option>
              <option v-for="g in games" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
          </div>
          <div>
            <label for="ba-server" class="mb-1 block text-sm font-medium">{{ t('boost.server') }}</label>
            <select id="ba-server" v-model.number="form.server_id" class="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" required>
              <option :value="0" disabled>—</option>
              <option v-for="s in serverOptions" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </div>
          <div>
            <label for="ba-login" class="mb-1 block text-sm font-medium">{{ t('boost.login') }}</label>
            <Input id="ba-login" v-model="form.login" maxlength="64" autocomplete="off" required />
          </div>
          <div>
            <label for="ba-pass" class="mb-1 block text-sm font-medium">{{ t('boost.password') }}</label>
            <Input id="ba-pass" v-model="form.password" type="password" maxlength="128" autocomplete="new-password" required />
          </div>
          <div>
            <label for="ba-name" class="mb-1 block text-sm font-medium">{{ t('boost.displayName') }}</label>
            <Input id="ba-name" v-model="form.display_name" maxlength="20" :placeholder="t('boost.displayNamePlaceholder')" />
          </div>
          <div class="flex items-end gap-2">
            <Button type="submit" :disabled="saving">{{ t('boost.save') }}</Button>
            <Button type="button" variant="outline" @click="closeForm">{{ t('boost.cancel') }}</Button>
          </div>
          <p v-if="formError" class="text-sm text-destructive sm:col-span-2" role="alert">{{ formError }}</p>
        </form>
      </Card>

      <div v-if="loading" class="space-y-3">
        <div v-for="i in 2" :key="i" class="h-24 rounded-2xl border bg-muted animate-pulse"></div>
      </div>
      <EmptyState v-else-if="!bindings.length" variant="soft" :title="t('boost.noAccounts')" />

      <ul v-else class="space-y-3">
        <li v-for="b in bindings" :key="b.id">
          <Card class="rounded-2xl p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="font-semibold">{{ b.display_name }} <span class="font-mono text-sm font-normal text-muted-foreground">{{ b.login_masked }}</span></div>
                <div class="text-sm text-muted-foreground">{{ b.game_name }} · {{ b.server_name }}</div>
                <div class="mt-1 text-xs text-muted-foreground">{{ b.user_message }}</div>
              </div>
              <div class="flex items-center gap-2">
                <span class="rounded-full border px-2.5 py-0.5 text-xs" :class="statusTone(b.status)">{{ t(`boost.bindingStatus.${b.status}`) }}</span>
                <Button size="sm" variant="ghost" @click="askUnbind(b)">{{ t('boost.unbind') }}</Button>
              </div>
            </div>

            <!-- 需要验证码 -->
            <form v-if="b.status === 'WAIT_INPUT' && b.input" class="mt-3 flex flex-wrap items-end gap-2 rounded-lg bg-amber-50 p-3 dark:bg-amber-950" @submit.prevent="submitCode(b)">
              <div>
                <label :for="`code-${b.id}`" class="mb-1 block text-sm">{{ b.input.prompt }}</label>
                <Input :id="`code-${b.id}`" v-model="codes[b.id]" class="w-40" maxlength="64" inputmode="numeric" autocomplete="one-time-code" />
              </div>
              <Button type="submit" size="sm" :disabled="busy === b.id">{{ t('boost.submitCode') }}</Button>
              <span class="text-xs text-muted-foreground">{{ t('boost.inputExpires', { time: fmtTime(b.input.expires_at) }) }}</span>
            </form>
            <!-- 需要游戏内确认 -->
            <div v-else-if="b.status === 'WAIT_USER' && b.input" class="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-amber-50 p-3 text-sm dark:bg-amber-950">
              <span>{{ b.input.prompt }}</span>
              <Button size="sm" :disabled="busy === b.id" @click="confirmInGame(b)">{{ t('boost.confirmedInGame') }}</Button>
              <span class="text-xs text-muted-foreground">{{ t('boost.inputExpires', { time: fmtTime(b.input.expires_at) }) }}</span>
            </div>

            <!-- 解绑确认 -->
            <div v-if="unbinding === b.id" class="mt-3 flex flex-wrap items-center gap-2 rounded-lg border border-destructive/40 p-3 text-sm" role="alertdialog" :aria-label="t('boost.unbind')">
              <span>{{ t('boost.unbindConfirm', { name: b.display_name }) }}</span>
              <Button size="sm" variant="destructive" :disabled="busy === b.id" @click="unbind(b)">{{ t('boost.unbind') }}</Button>
              <Button size="sm" variant="outline" @click="unbinding = 0">{{ t('boost.cancel') }}</Button>
            </div>
          </Card>
        </li>
      </ul>
    </div>
  </div>
</template>

<script setup lang="ts">
// BOOST-EXT: 用户游戏账号。提交后由执行端验证（可能要验证码或游戏内确认），状态全部来自服务端。
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import EmptyState from '../components/EmptyState.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { toast } from '../composables/useToast'
import { usePageSeo } from '../composables/usePageSeo'
import { boostAPI, boostTaskAPI, statusTone, type BoostBinding, type BoostGame } from '../api/boost'

const { t } = useI18n()
const route = useRoute()
usePageSeo({ title: () => t('boost.accountsTitle'), canonicalPath: () => '/boost/accounts' })

const fmtTime = (v?: string) => (v ? new Date(v).toLocaleTimeString() : '')

const games = ref<BoostGame[]>([])
const bindings = ref<BoostBinding[]>([])
const loading = ref(true)
const busy = ref(0)
const unbinding = ref(0)
const codes = reactive<Record<number, string>>({})

const formOpen = ref(false)
const saving = ref(false)
const formError = ref('')
const form = reactive({ game_id: 0, server_id: 0, display_name: '', login: '', password: '' })
const serverOptions = computed(() => games.value.find((g) => g.id === form.game_id)?.servers || [])

const load = async () => {
  try {
    bindings.value = (await boostTaskAPI.bindings()).items
  } catch {
    // client 已提示
  } finally {
    loading.value = false
  }
}

const openForm = (gameId = 0) => {
  Object.assign(form, { game_id: gameId, server_id: 0, display_name: '', login: '', password: '' })
  const g = games.value.find((x) => x.id === gameId)
  if (g && g.servers.length === 1) form.server_id = g.servers[0]!.id
  formError.value = ''
  formOpen.value = true
}
const closeForm = () => {
  form.password = ''
  formOpen.value = false
}

const submitForm = async () => {
  formError.value = ''
  saving.value = true
  try {
    await boostTaskAPI.createBinding({ ...form })
    toast.success(t('boost.bindSubmitted'))
    closeForm()
    await load()
  } catch (err: any) {
    formError.value = err?.message || ''
  } finally {
    saving.value = false
  }
}

const submitCode = async (b: BoostBinding) => {
  const value = (codes[b.id] || '').trim()
  if (!value) return
  busy.value = b.id
  try {
    await boostTaskAPI.bindingInput(b.id, value)
    codes[b.id] = ''
    await load()
  } catch (err: any) {
    toast.error(err?.message || '')
    await load()
  } finally {
    busy.value = 0
  }
}

const confirmInGame = async (b: BoostBinding) => {
  busy.value = b.id
  try {
    await boostTaskAPI.bindingConfirm(b.id)
    await load()
  } catch (err: any) {
    toast.error(err?.message || '')
    await load()
  } finally {
    busy.value = 0
  }
}

const askUnbind = (b: BoostBinding) => {
  unbinding.value = unbinding.value === b.id ? 0 : b.id
}
const unbind = async (b: BoostBinding) => {
  busy.value = b.id
  try {
    await boostTaskAPI.deleteBinding(b.id)
    toast.success(t('boost.unbound'))
    unbinding.value = 0
    await load()
  } catch (err: any) {
    toast.error(err?.message || '')
  } finally {
    busy.value = 0
  }
}

// 验证在执行端异步进行：有未结束的绑定时每 5 秒刷新一次
let timer: ReturnType<typeof setInterval> | undefined
onMounted(async () => {
  try {
    games.value = await boostAPI.listGames()
  } catch {
    games.value = []
  }
  await load()
  const wanted = Number(route.query.game)
  if (wanted) openForm(wanted)
  timer = setInterval(() => {
    if (bindings.value.some((b) => b.status === 'PENDING' || b.status === 'PROCESSING')) void load()
  }, 5000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>
