<template>
  <div class="space-y-2">
    <label class="flex items-center gap-2 text-sm">
      <Toggle :model-value="!!(start || end)" @update:model-value="toggle" />
      {{ t('pelicanTests.window.enabled') }}
    </label>
    <div v-if="start || end" class="grid grid-cols-2 gap-3">
      <label class="text-xs">
        {{ t('pelicanTests.window.start') }}
        <input :value="start" type="time" required class="input mt-1 w-full" data-testid="pelican-window-start" @input="update('daily_start', ($event.target as HTMLInputElement).value)" />
      </label>
      <label class="text-xs">
        {{ t('pelicanTests.window.end') }}
        <input :value="end" type="time" required class="input mt-1 w-full" data-testid="pelican-window-end" @input="update('daily_end', ($event.target as HTMLInputElement).value)" />
      </label>
    </div>
    <p class="text-xs text-gray-500">{{ t('pelicanTests.window.hint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
const props = defineProps<{ start?: string; end?: string }>()
const emit = defineEmits<{ change: [value: { daily_start: string; daily_end: string }] }>()
const { t } = useI18n()
function toggle(enabled: boolean) {
  emit('change', { daily_start: enabled ? '06:00' : '', daily_end: enabled ? '01:00' : '' })
}
function update(key: 'daily_start' | 'daily_end', value: string) {
  emit('change', { daily_start: props.start || '', daily_end: props.end || '', [key]: value })
}
</script>
