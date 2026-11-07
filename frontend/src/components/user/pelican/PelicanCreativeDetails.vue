<template>
  <details class="shrink-0 overflow-auto rounded-xl border border-gray-200 bg-gray-50 p-3 text-xs dark:border-dark-700 dark:bg-dark-800" style="max-height: 38vh" data-testid="creative-details">
    <summary class="cursor-pointer font-medium text-gray-700 dark:text-gray-200">
      {{ t('pelicanTests.creative.details') }} · {{ formatDateTimeToMinute(round.started_at) }}
    </summary>
    <div class="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      <p v-for="material in round.materials" :key="material.key" class="leading-relaxed">
        <span class="text-gray-400">{{ material.label }} · </span><span class="text-gray-700 dark:text-gray-200">{{ material.value }}</span>
      </p>
    </div>
    <template v-if="round.checks">
      <div class="mt-3 flex flex-wrap gap-2" data-testid="creative-checks">
        <span v-for="key in CHECKS" :key="key" class="rounded-md px-2 py-1" :class="round.checks[key] ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'">
          {{ round.checks[key] ? '✓' : '·' }} {{ t(`pelicanTests.creative.${key}`) }}
        </span>
        <span class="rounded-md bg-gray-100 px-2 py-1 text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ t('pelicanTests.creative.matched', { count: round.checks.matched.length, total: round.checks.matched.length + round.checks.missing.length }) }}</span>
      </div>
      <p class="mt-2 text-gray-400">{{ t('pelicanTests.creative.checkHint') }}</p>
    </template>
    <details v-if="round.prompt" class="mt-3">
      <summary class="cursor-pointer text-primary-600 dark:text-primary-400">{{ t('pelicanTests.creative.fullPrompt') }}</summary>
      <pre class="mt-2 select-text whitespace-pre-wrap break-words rounded-lg bg-white p-3 font-sans leading-relaxed text-gray-600 dark:bg-dark-900 dark:text-gray-300">{{ round.prompt }}</pre>
    </details>
    <p class="mt-3 break-all text-[10px] text-gray-400">{{ round.id }} · {{ t('pelicanTests.creative.seed') }} {{ round.seed }}</p>
  </details>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PelicanCreativeRound } from '@/api/pelicanCreative'
import { formatDateTimeToMinute } from '@/utils/format'

defineProps<{ round: PelicanCreativeRound }>()
const { t } = useI18n()
const CHECKS = ['has_svg', 'animated', 'no_external', 'complete'] as const
</script>
