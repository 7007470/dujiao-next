<script setup lang="ts">
// BOOST-EXT: 按功能定义自动生成的下单表单。
// 渲染器只依赖服务端 compile 结果（plan）：可见性、默认值、错误、报价都由服务端算出，这里不做任何价格计算。
// 和原型 prototype/src/ui-form.js 的交互一致；S5 用户端下单页复用这个组件。
import { computed, reactive } from 'vue'
import type { BoostCatalogItem, BoostDefinition, BoostField, BoostPlan } from '@/api/boost'

const props = defineProps<{
  definition: BoostDefinition
  plan: BoostPlan | null
  modelValue: Record<string, unknown>
  showErrors?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: Record<string, unknown>): void
}>()

const ui = reactive({
  touched: {} as Record<string, boolean>,
  group: {} as Record<string, string>,
  query: {} as Record<string, string>,
})

const fields = computed(() => (props.definition.fields || []).filter((f) => !props.plan || props.plan.visible[f.key]))

const valueOf = (key: string) => (props.plan ? props.plan.values[key] : undefined)
const errorOf = (key: string) => {
  if (!props.plan) return ''
  return props.showErrors || ui.touched[key] ? props.plan.fieldErrors[key] || '' : ''
}

const set = (key: string, value: unknown) => {
  ui.touched[key] = true
  const next = { ...props.modelValue }
  if (value === undefined) delete next[key]
  else next[key] = value
  emit('update:modelValue', next)
}

const isChecked = (f: BoostField, value: string) => {
  const v = valueOf(f.key)
  return Array.isArray(v) && v.includes(value)
}
const toggleCheckbox = (f: BoostField, value: string) => {
  const current = Array.isArray(valueOf(f.key)) ? [...(valueOf(f.key) as string[])] : []
  const i = current.indexOf(value)
  if (i >= 0) current.splice(i, 1)
  else current.push(value)
  set(f.key, current)
}

const onNumber = (f: BoostField, raw: string) => set(f.key, raw === '' ? undefined : Number(raw))
const stepNumber = (f: BoostField, delta: number) => {
  const current = Number(valueOf(f.key) ?? props.modelValue[f.key] ?? f.min ?? 0)
  const step = f.max !== undefined && f.min !== undefined && f.max - f.min >= 100 ? 10 : 1
  const next = Math.min(f.max ?? Infinity, Math.max(f.min ?? -Infinity, current + delta * step))
  set(f.key, next)
}

// ---- stage_picker ----
type Picked = { item: string; count: number }

const catalogOf = (f: BoostField) => (f.catalog && props.definition.catalogs ? props.definition.catalogs[f.catalog] : undefined)
const pickedOf = (f: BoostField): Picked[] => {
  const v = valueOf(f.key)
  return Array.isArray(v) ? (v as Picked[]) : []
}
const rawPicked = (f: BoostField): Picked[] => {
  const raw = props.modelValue[f.key]
  if (!Array.isArray(raw)) return []
  return raw.map((x: any) => (typeof x === 'object' && x ? { item: String(x.item), count: Number(x.count ?? 0) } : { item: String(x), count: 0 }))
}
const modeOf = (f: BoostField) => (f.cost_by ? (valueOf(f.cost_by) as string | undefined) : undefined)
const countVisible = (f: BoostField) => {
  if (!f.count) return false
  const vw = f.count.visible_when
  if (!vw) return true
  return Object.entries(vw).every(([k, want]) => (Array.isArray(want) ? want : [want]).includes(valueOf(k)))
}
const itemCost = (f: BoostField, it: BoostCatalogItem): number | null => {
  if (typeof it.cost === 'number') return it.cost
  const mode = modeOf(f)
  if (!mode) return null
  return Object.prototype.hasOwnProperty.call(it.cost, mode) ? (it.cost[mode] ?? null) : null
}
const isPicked = (f: BoostField, code: string) => rawPicked(f).some((p) => p.item === code)
const togglePick = (f: BoostField, it: BoostCatalogItem) => {
  if (it.disabled) return
  let list = rawPicked(f)
  if (isPicked(f, it.code)) list = list.filter((p) => p.item !== it.code)
  else {
    const count = f.count ? f.count.default ?? f.count.min : 1
    list = f.multiple ? [...list, { item: it.code, count }] : [{ item: it.code, count }]
  }
  set(f.key, list)
}
const setCount = (f: BoostField, code: string, count: number) => {
  set(f.key, rawPicked(f).map((p) => (p.item === code ? { ...p, count } : p)))
}
const countFor = (f: BoostField, code: string) => {
  const done = pickedOf(f).find((p) => p.item === code)
  if (done) return done.count
  const raw = rawPicked(f).find((p) => p.item === code)
  return raw?.count ?? f.count?.default ?? f.count?.min ?? 1
}
const currentGroup = (f: BoostField) => {
  const cat = catalogOf(f)
  return ui.group[f.key] || cat?.groups[0]?.code || ''
}
const visibleItems = (f: BoostField) => {
  const cat = catalogOf(f)
  if (!cat) return []
  const q = (ui.query[f.key] || '').trim().toLowerCase()
  if (q) return cat.groups.flatMap((g) => g.items).filter((it) => it.name.toLowerCase().includes(q) || it.code.toLowerCase().includes(q))
  return cat.groups.find((g) => g.code === currentGroup(f))?.items || []
}
const itemName = (f: BoostField, code: string) => {
  const cat = catalogOf(f)
  return cat?.groups.flatMap((g) => g.items).find((it) => it.code === code)?.name || code
}
</script>

<template>
  <div class="space-y-4">
    <div v-for="f in fields" :key="f.key" class="space-y-2" :data-field="f.key">
      <label class="text-sm font-medium">
        {{ f.label }}<span v-if="f.required" class="text-destructive"> *</span>
      </label>

      <!-- radio：分段按钮 -->
      <div v-if="f.type === 'radio'" class="flex flex-wrap gap-2" role="radiogroup">
        <button
          v-for="o in f.options"
          :key="o.value"
          type="button"
          role="radio"
          :aria-checked="valueOf(f.key) === o.value"
          class="rounded-md border px-3 py-1.5 text-sm transition-colors"
          :class="valueOf(f.key) === o.value ? 'border-primary bg-primary/10 text-primary' : 'border-input hover:bg-muted'"
          @click="set(f.key, o.value)"
        >
          {{ o.label }}<span v-if="o.points" class="ml-1 text-xs text-muted-foreground">+{{ o.points }}</span>
        </button>
      </div>

      <!-- select -->
      <select
        v-else-if="f.type === 'select'"
        class="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
        :value="(valueOf(f.key) as string) ?? ''"
        @change="set(f.key, ($event.target as HTMLSelectElement).value || undefined)"
      >
        <option value="">请选择</option>
        <option v-for="o in f.options" :key="o.value" :value="o.value">
          {{ o.label }}{{ o.points ? `（+${o.points} 积分）` : '' }}
        </option>
      </select>

      <!-- checkbox -->
      <div v-else-if="f.type === 'checkbox'" class="flex flex-wrap gap-2">
        <label
          v-for="o in f.options"
          :key="o.value"
          class="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 text-sm"
          :class="isChecked(f, o.value) ? 'border-primary bg-primary/10' : 'border-input'"
        >
          <input type="checkbox" :checked="isChecked(f, o.value)" @change="toggleCheckbox(f, o.value)" />
          {{ o.label }}<span v-if="o.points" class="text-xs text-muted-foreground">+{{ o.points }}</span>
        </label>
      </div>

      <!-- switch -->
      <label v-else-if="f.type === 'switch'" class="flex cursor-pointer items-center gap-2 text-sm">
        <input type="checkbox" role="switch" :checked="!!valueOf(f.key)" @change="set(f.key, ($event.target as HTMLInputElement).checked)" />
        {{ valueOf(f.key) ? '开启' : '关闭' }}
        <span v-if="f.points" class="text-xs text-muted-foreground">+{{ f.points }} 积分</span>
      </label>

      <!-- number -->
      <div v-else-if="f.type === 'number'" class="flex items-center gap-2">
        <button type="button" class="h-9 w-9 rounded-md border border-input" aria-label="减少" @click="stepNumber(f, -1)">−</button>
        <input
          type="number"
          inputmode="numeric"
          class="h-9 w-32 rounded-md border border-input bg-transparent px-3 text-sm"
          :min="f.min"
          :max="f.max"
          :value="(modelValue[f.key] as number | undefined) ?? (valueOf(f.key) as number | undefined) ?? ''"
          @input="onNumber(f, ($event.target as HTMLInputElement).value)"
        />
        <button type="button" class="h-9 w-9 rounded-md border border-input" aria-label="增加" @click="stepNumber(f, 1)">+</button>
        <span class="text-sm text-muted-foreground">{{ f.unit }}（{{ f.min }}–{{ f.max }}）</span>
      </div>

      <!-- text -->
      <input
        v-else-if="f.type === 'text'"
        type="text"
        class="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
        :maxlength="f.maxlength || 100"
        :placeholder="f.placeholder || ''"
        :value="(modelValue[f.key] as string) ?? ''"
        @input="set(f.key, ($event.target as HTMLInputElement).value)"
      />

      <!-- stage_picker：按分组页签 + 搜索 + 已选清单 -->
      <div v-else-if="f.type === 'stage_picker' && catalogOf(f)" class="space-y-2 rounded-md border border-input p-3">
        <div class="flex flex-wrap items-center gap-2">
          <button
            v-for="g in catalogOf(f)!.groups"
            :key="g.code"
            type="button"
            class="rounded px-2 py-1 text-xs"
            :class="currentGroup(f) === g.code && !ui.query[f.key] ? 'bg-primary text-primary-foreground' : 'bg-muted'"
            @click="ui.group[f.key] = g.code; ui.query[f.key] = ''"
          >
            {{ g.name }}
          </button>
          <input
            v-model="ui.query[f.key]"
            type="search"
            class="ml-auto h-7 w-36 rounded border border-input bg-transparent px-2 text-xs"
            :placeholder="`搜索${catalogOf(f)!.label}`"
          />
        </div>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <button
            v-for="it in visibleItems(f)"
            :key="it.code"
            type="button"
            :disabled="it.disabled"
            class="rounded-md border px-3 py-2 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50"
            :class="isPicked(f, it.code) ? 'border-primary bg-primary/10' : 'border-input hover:bg-muted'"
            @click="togglePick(f, it)"
          >
            <div class="flex items-center justify-between gap-2">
              <span>{{ it.name }}</span>
              <span class="text-xs text-muted-foreground">
                <template v-if="it.disabled">暂不可选</template>
                <template v-else-if="itemCost(f, it) === null">{{ modeOf(f) ? '不支持' : '先选模式' }}</template>
                <template v-else>{{ itemCost(f, it) }} 积分</template>
              </span>
            </div>
            <div v-if="it.tags?.length" class="mt-1 flex flex-wrap gap-1">
              <span v-for="tag in it.tags" :key="tag" class="rounded bg-muted px-1.5 text-[11px] text-muted-foreground">{{ tag }}</span>
            </div>
          </button>
        </div>
        <div v-if="rawPicked(f).length" class="space-y-1 border-t border-input pt-2">
          <div class="text-xs text-muted-foreground">已选 {{ rawPicked(f).length }} 项</div>
          <div v-for="p in rawPicked(f)" :key="p.item" class="flex items-center gap-2 text-sm">
            <span class="flex-1">{{ itemName(f, p.item) }}</span>
            <template v-if="countVisible(f)">
              <span class="text-xs text-muted-foreground">{{ f.count?.label || '次数' }}</span>
              <input
                type="number"
                class="h-7 w-20 rounded border border-input bg-transparent px-2 text-xs"
                :min="f.count?.min"
                :max="f.count?.max"
                :value="countFor(f, p.item)"
                @input="setCount(f, p.item, Number(($event.target as HTMLInputElement).value))"
              />
            </template>
            <button type="button" class="text-xs text-muted-foreground hover:text-destructive" @click="togglePick(f, { code: p.item, name: '', cost: 0 })">移除</button>
          </div>
        </div>
      </div>

      <p v-if="f.help" class="text-xs text-muted-foreground">{{ f.help }}</p>
      <p v-if="errorOf(f.key)" class="text-xs text-destructive" role="alert">{{ errorOf(f.key) }}</p>
    </div>
  </div>
</template>
