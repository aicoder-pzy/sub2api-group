<template>
  <fieldset class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
    <legend class="input-label">{{ t('zakoFeatures.prompt.title') }}</legend>
    <p class="text-xs text-gray-500">{{ t('zakoFeatures.prompt.hint') }}</p>
    <p v-if="loadError" role="alert" class="text-sm text-red-500">{{ t('zakoFeatures.prompt.loadFailed') }}</p>
    <div v-for="(entry, index) in modelValue" :key="index" class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
      <div class="flex flex-wrap gap-2">
        <input :value="entry.from" class="input min-w-0 flex-1" maxlength="256" required :aria-label="t('zakoFeatures.prompt.model')" :placeholder="t('zakoFeatures.prompt.model')" @input="update(index, { from: ($event.target as HTMLInputElement).value })" />
        <select :value="entry.to.startsWith('custom:') ? 'custom' : entry.to" class="input min-w-0 flex-1" required :aria-label="t('zakoFeatures.prompt.profile')" @change="choose(index, ($event.target as HTMLSelectElement).value)">
          <option value="" disabled>{{ t('zakoFeatures.prompt.profile') }}</option>
          <option value="custom">{{ t('zakoFeatures.prompt.custom') }}</option>
          <option v-for="profile in compatible(entry.from)" :key="profile.id" :value="profile.id">{{ profile.name }} · {{ (profile.bytes / 1024).toFixed(1) }} KiB</option>
        </select>
        <button type="button" class="btn btn-secondary" :aria-label="t('common.delete')" @click="emit('update:modelValue', modelValue.filter((_, i) => i !== index))">×</button>
      </div>
      <textarea v-if="entry.to.startsWith('custom:')" :value="entry.to.slice(7)" rows="4" maxlength="16384" required class="input w-full" :aria-label="t('zakoFeatures.prompt.custom')" @input="update(index, { to: 'custom:' + ($event.target as HTMLTextAreaElement).value })" />
    </div>
    <button type="button" class="btn btn-secondary text-sm" :disabled="modelValue.length >= 100" @click="emit('update:modelValue', [...modelValue, { from: '', to: '' }])">{{ t('zakoFeatures.prompt.add') }}</button>
  </fieldset>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import type { KeyModelMappingEntry } from '@/utils/keyModelMapping'

interface Profile { id: string; name: string; models: string[]; bytes: number; template?: boolean }
const props = defineProps<{ modelValue: KeyModelMappingEntry[]; admin?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [entries: KeyModelMappingEntry[]] }>()
const { t } = useI18n()
const profiles = ref<Profile[]>([])
const loadError = ref(false)
onMounted(async () => {
  try { profiles.value = (await apiClient.get<Profile[]>(props.admin ? '/admin/api-keys/prompt-profiles' : '/keys/prompt-profiles')).data } catch { loadError.value = true }
})
const compatible = (model: string) => profiles.value.filter(profile => !(profile.template && model === '*') && (!model || profile.models.length === 0 || profile.models.includes(model)))
const update = (index: number, changes: Partial<KeyModelMappingEntry>) => emit('update:modelValue', props.modelValue.map((entry, i) => i === index ? { ...entry, ...changes } : entry))
function choose(index: number, id: string) {
  const profile = profiles.value.find(item => item.id === id)
  update(index, { to: id === 'custom' ? 'custom:' : id, from: props.modelValue[index].from || profile?.models[0] || (profile?.template ? '' : '*') })
}
</script>
