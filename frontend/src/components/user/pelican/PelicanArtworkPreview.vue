<template>
  <div ref="containerRef" class="relative h-full min-h-0 w-full min-w-0" data-testid="pelican-artwork-preview" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanAssessmentQuality } from '@/utils/pelicanHtml'
import { mountPelicanArtwork } from '@/utils/pelicanRenderer'
import type { PelicanPreviewMode } from '@/utils/pelicanPreview'
import type { PelicanCreativeEvaluation } from '@/api/pelicanCreative'

const props = withDefaults(defineProps<{ html: string; title: string; mode?: PelicanPreviewMode; interactive?: boolean; quality?: 'normal' | 'degraded' | 'unknown'; reason?: string; creative?: boolean; evaluation?: PelicanCreativeEvaluation }>(), { mode: 'fit', interactive: true })
const { t } = useI18n()
const containerRef = ref<HTMLElement | null>(null)
let preview: ReturnType<typeof mountPelicanArtwork> | undefined
function options() {
  if (props.evaluation) {
    const evaluation = props.evaluation
    const quality = ({ passed: 'normal', failed: 'degraded', unknown: 'unknown' } as const)[evaluation.status]
    const score = evaluation.score === undefined ? '' : `${evaluation.score}/100 · `
    return { ...props, quality, reason: evaluation.reason, badgeLabel: `${t('pelicanTests.creative.visualProvider')} · ${score}${t(`pelicanTests.creative.${evaluation.status}`)}` }
  }
  const quality = props.creative && !props.reason ? null : props.quality ?? getPelicanAssessmentQuality(props.html)
  const provider = t(props.creative ? 'pelicanTests.creative.verificationProvider' : 'pelicanTests.assessment.provider')
  return { ...props, quality, badgeLabel: quality ? `${provider} · ${t(`pelicanTests.assessment.${quality}`)}` : '' }
}
onMounted(() => { if (containerRef.value) preview = mountPelicanArtwork(containerRef.value, options()) })
watch(() => [props.html, props.title, props.mode, props.interactive, props.quality, props.reason, props.creative, props.evaluation], () => preview?.update(options()))
onBeforeUnmount(() => preview?.destroy())
</script>
