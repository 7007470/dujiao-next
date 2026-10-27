<template>
  <div class="boost-page min-h-screen bg-background text-foreground pt-20 pb-16">
    <div class="container mx-auto px-4">
      <div class="mb-12 mt-12 text-center">
        <h1 class="text-4xl md:text-5xl font-black mb-4 tracking-tight text-foreground">{{ t('boost.title') }}</h1>
        <p class="text-muted-foreground max-w-2xl mx-auto text-lg border-b pb-8">{{ t('boost.subtitle') }}</p>
      </div>

      <div v-if="loading" class="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4">
        <div v-for="i in 8" :key="i" class="h-44 rounded-2xl border bg-muted animate-pulse"></div>
      </div>

      <div v-else-if="games.length" class="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4">
        <router-link
          v-for="g in games"
          :key="g.id"
          :to="`/boost/${g.code}`"
          class="group flex flex-col items-center gap-3 rounded-2xl border bg-card p-6 text-center shadow-sm transition-all hover:-translate-y-1 hover:shadow-md"
        >
          <img v-if="g.icon_url" :src="getImageUrl(g.icon_url)" :alt="g.name" loading="lazy"
            class="h-20 w-20 rounded-2xl object-cover shadow-sm transition-transform group-hover:scale-105" />
          <span v-else class="flex h-20 w-20 items-center justify-center rounded-2xl text-2xl font-bold text-white shadow-sm"
            :style="{ background: g.color || '#2f6f6a' }">{{ g.short_name }}</span>
          <div>
            <div class="font-semibold text-foreground">{{ g.name }}</div>
            <div class="mt-1 text-xs text-muted-foreground">{{ t('boost.featureCount', { count: g.feature_count }) }}</div>
          </div>
        </router-link>
      </div>

      <EmptyState v-else variant="soft" size="lg" class="max-w-3xl mx-auto" :title="t('boost.empty')">
        <template #icon>
          <Gamepad2 class="w-20 h-20 text-muted-foreground opacity-70" :stroke-width="1.5" />
        </template>
      </EmptyState>
    </div>
  </div>
</template>

<script setup lang="ts">
// BOOST-EXT: 用户端代练游戏列表
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Gamepad2 } from 'lucide-vue-next'
import EmptyState from '../components/EmptyState.vue'
import { getImageUrl } from '../utils/image'
import { usePageSeo } from '../composables/usePageSeo'
import { boostAPI, type BoostGame } from '../api/boost'

const { t } = useI18n()
usePageSeo({ title: () => t('boost.title'), canonicalPath: () => '/boost' })

const games = ref<BoostGame[]>([])
const loading = ref(true)

onMounted(async () => {
  try {
    games.value = (await boostAPI.listGames()) || []
  } catch (error) {
    console.error('Failed to load boost games:', error)
  } finally {
    loading.value = false
  }
})
</script>
