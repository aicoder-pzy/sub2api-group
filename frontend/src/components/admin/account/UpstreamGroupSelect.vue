<template>
  <div class="space-y-3">
    <label class="block">
      <span class="input-label">{{ t('upstreamSites.upstreamGroup') }}</span>
      <select :value="modelValue" required class="input" :disabled="disabled" :aria-label="t('upstreamSites.upstreamGroup')" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
        <option value="">{{ t('upstreamSites.choose') }}</option>
        <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} · {{ group.multiplier === undefined ? t('upstreamSites.unknownGroupRate') : '×' + group.multiplier }}</option>
      </select>
    </label>
    <section v-if="selected" data-upstream-group-detail class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800">
      <div class="flex flex-wrap items-center justify-between gap-2 text-sm font-medium">
        <span class="break-words">{{ selected.name }}</span>
        <span class="shrink-0 text-primary-600 dark:text-primary-400">{{ t('upstreamSites.groupMultiplier') }}：{{ selected.multiplier === undefined ? t('upstreamSites.unknownGroupRate') : '×' + selected.multiplier }}</span>
      </div>
      <p class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-gray-600 dark:text-gray-300">{{ selected.description || t('upstreamSites.noGroupDescription') }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SiteGroup } from '@/api/admin/upstreamSites'

const props = defineProps<{ modelValue: string; groups: SiteGroup[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const selected = computed(() => props.groups.find(group => group.id === props.modelValue))
</script>
