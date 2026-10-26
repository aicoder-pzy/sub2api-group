<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <IPRiskSettings expanded />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import IPRiskSettings from './IPRiskSettings.vue'
</script>
