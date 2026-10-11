<template>
  <div ref="containerRef" class="relative h-full min-h-0 w-full min-w-0" data-testid="pelican-artwork-preview" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanAssessmentQuality } from '@/utils/pelicanHtml'
import { mountPelicanArtwork } from '@/utils/pelicanRenderer'
import type { PelicanPreviewMode } from '@/utils/pelicanPreview'

const props = withDefaults(defineProps<{ html: string; title: string; mode?: PelicanPreviewMode; interactive?: boolean; quality?: 'normal' | 'degraded' | 'unknown'; reason?: string }>(), { mode: 'fit', interactive: true })
const { t } = useI18n()
const containerRef = ref<HTMLElement | null>(null)
let preview: ReturnType<typeof mountPelicanArtwork> | undefined
function options() {
  const quality = props.quality ?? getPelicanAssessmentQuality(props.html)
  return { ...props, badgeLabel: quality ? `${t('pelicanTests.assessment.provider')} · ${t(`pelicanTests.assessment.${quality}`)}` : '' }
}
onMounted(() => { if (containerRef.value) preview = mountPelicanArtwork(containerRef.value, options()) })
watch(() => [props.html, props.title, props.mode, props.interactive, props.quality, props.reason], () => preview?.update(options()))
onBeforeUnmount(() => preview?.destroy())
</script>
