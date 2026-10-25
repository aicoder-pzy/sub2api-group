<template>
  <fieldset class="space-y-3">
    <legend class="input-label">{{ t('keys.modelMapping') }}</legend>
    <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('keys.modelMappingHint') }}</p>
    <div v-for="(entry, index) in modelValue" :key="index" class="flex flex-wrap items-center gap-2">
      <input :value="entry.from" :aria-label="t('keys.modelMappingFrom')" :placeholder="t('keys.modelMappingFrom')" class="input min-w-0 flex-1 font-mono text-sm" required maxlength="256" @input="update(index, 'from', ($event.target as HTMLInputElement).value)" />
      <span aria-hidden="true">→</span>
      <input :value="entry.to" :aria-label="t('keys.modelMappingTo')" :placeholder="t('keys.modelMappingTo')" class="input min-w-0 flex-1 font-mono text-sm" required maxlength="256" @input="update(index, 'to', ($event.target as HTMLInputElement).value)" />
      <button type="button" class="btn btn-secondary px-2" :aria-label="t('keys.modelMappingRemove')" @click="emit('update:modelValue', modelValue.filter((_, i) => i !== index))">×</button>
    </div>
    <button type="button" class="btn btn-secondary text-sm" :disabled="modelValue.length >= 100" @click="emit('update:modelValue', [...modelValue, { from: '', to: '' }])">{{ t('keys.modelMappingAdd') }}</button>
  </fieldset>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { KeyModelMappingEntry } from '@/utils/keyModelMapping'

const props = defineProps<{ modelValue: KeyModelMappingEntry[] }>()
const emit = defineEmits<{ 'update:modelValue': [entries: KeyModelMappingEntry[]] }>()
const { t } = useI18n()
const update = (index: number, field: 'from' | 'to', value: string) => {
  emit('update:modelValue', props.modelValue.map((entry, i) => i === index ? { ...entry, [field]: value } : entry))
}
</script>
