<template>
  <div>
    <label class="block input-label">
      {{ t('admin.accounts.pelicanTest.maxOutputTokens') }}
      <input
        :value="Number.isNaN(modelValue) ? '' : modelValue"
        type="number"
        :min="PELICAN_OUTPUT_TOKEN_MIN"
        step="1"
        :disabled="disabled"
        :aria-invalid="!validPelicanOutputTokenLimit(modelValue)"
        class="input mt-1.5 w-full"
        data-testid="pelican-max-output-tokens"
        @input="updateLimit"
      />
    </label>
    <p class="input-hint mt-1.5">
      {{ t(validPelicanOutputTokenLimit(modelValue) ? 'admin.accounts.pelicanTest.maxOutputTokensHint' : 'admin.accounts.pelicanTest.maxOutputTokensInvalid') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { PELICAN_OUTPUT_TOKEN_MIN, validPelicanOutputTokenLimit } from '@/utils/intelligenceTest'

defineProps<{ modelValue?: number; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [limit: number | undefined] }>()
const { t } = useI18n()

function updateLimit(event: Event) {
  const input = event.target as HTMLInputElement
  emit('update:modelValue', input.value === '' && !input.validity.badInput ? undefined : input.valueAsNumber)
}
</script>
