<template>
  <details class="shrink-0 overflow-auto rounded-xl border border-gray-200 bg-gray-50 p-3 text-xs dark:border-dark-700 dark:bg-dark-800" style="max-height: 38vh" data-testid="creative-details">
    <summary class="cursor-pointer font-medium text-gray-700 dark:text-gray-200">
      {{ t('pelicanTests.creative.details') }} · {{ formatDateTimeToMinute(round.started_at) }}
      <span v-if="round.evaluation" class="ml-2" data-testid="creative-score">{{ t('pelicanTests.creative.visualProvider') }} · {{ round.evaluation.score === undefined ? '' : `${round.evaluation.score}/100 · ` }}{{ t(`pelicanTests.creative.${round.evaluation.status}`) }}</span>
    </summary>
    <section v-if="round.evaluation" class="mt-3 space-y-2 rounded-lg bg-white p-3 dark:bg-dark-900" data-testid="creative-evaluation">
      <p class="font-medium">{{ round.evaluation.reason }}</p>
      <p v-if="round.evaluation.model_id" class="text-gray-400">{{ t('pelicanTests.creative.judgeModel') }} · {{ round.evaluation.model_id }}</p>
      <div class="grid gap-2 sm:grid-cols-2">
        <div v-for="criterion in round.evaluation.criteria" :key="criterion.key" class="rounded-lg border border-gray-100 p-2 dark:border-dark-700">
          <p class="font-medium">{{ t(`pelicanTests.creative.criteria.${criterion.key}`) }} · {{ criterion.score }}/5</p>
          <p class="mt-1 leading-relaxed text-gray-500">{{ criterion.reason }}</p>
        </div>
      </div>
      <p v-for="material in round.evaluation.materials" :key="material.key" class="leading-relaxed"><strong>{{ round.materials.find(item => item.key === material.key)?.label }} · {{ t(`pelicanTests.creative.${material.status}`) }}</strong> — {{ material.reason }}</p>
      <p class="text-gray-400">{{ t('pelicanTests.creative.scoreHint') }}</p>
    </section>
    <p v-if="round.verification" class="mt-3 rounded-lg bg-white p-3 leading-relaxed dark:bg-dark-900" data-testid="creative-verification">
      <strong>{{ t('pelicanTests.creative.verificationProvider') }} · {{ t(`pelicanTests.creative.verification.${round.verification.quality}`) }}</strong> — {{ round.verification.reason }}
    </p>
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
