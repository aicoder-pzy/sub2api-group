<template>
  <form class="grid min-w-0 gap-4 sm:grid-cols-2" @submit.prevent="emit('save')">
    <fieldset :disabled="busy" class="contents">
      <label><span class="input-label">{{ tr('name') }}</span><input v-model="form.name" required maxlength="100" class="input" /></label>
      <label><span class="input-label">{{ tr('kind') }}</span><select v-model="form.kind" class="input"><option value="sub2api">Sub2API</option><option value="newapi">New API</option></select></label>
      <label class="sm:col-span-2"><span class="input-label">{{ tr('managementUrl') }}</span><input v-model="form.base_url" type="url" required class="input" placeholder="https://console.example.com" /></label>
      <label class="sm:col-span-2"><span class="input-label">{{ tr('inferenceUrl') }}</span><input v-model="form.inference_url" type="url" class="input" placeholder="https://api.example.com" /><span class="mt-1 block text-xs text-gray-500">{{ tr('inferenceHint') }}</span></label>
      <label class="sm:col-span-2"><span class="input-label">{{ tr('token') }}</span><input v-model="form.access_token" type="password" autocomplete="new-password" :required="isNew" class="input" /><span class="mt-1 block text-xs text-gray-500">{{ tr('tokenHint') }}</span></label>
      <label v-if="form.kind === 'newapi'"><span class="input-label">{{ tr('userId') }}</span><input v-model.number="form.user_id" type="number" min="0" class="input" /></label>
      <label><span class="input-label">{{ tr('proxy') }}</span><select v-model="form.proxy_id" class="input"><option :value="null">{{ tr('direct') }}</option><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }}</option></select></label>
      <label class="sm:col-span-2"><span class="input-label">{{ tr('note') }}</span><textarea v-model="form.purchase_note" class="input" rows="2" maxlength="2000"></textarea></label>
      <div class="flex flex-wrap gap-3 sm:col-span-2"><button type="submit" class="btn btn-primary">{{ busy ? tr('loading') : tr('save') }}</button><slot></slot></div>
    </fieldset>
  </form>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { SiteForm } from '@/api/admin/upstreamSites'
import type { Proxy } from '@/types'
const form = defineModel<SiteForm>({ required: true })
defineProps<{ busy: boolean; isNew?: boolean; proxies: Proxy[] }>()
const emit = defineEmits<{ save: [] }>()
const { t } = useI18n()
const tr = (key: string) => t(`upstreamSites.${key}`)
</script>
