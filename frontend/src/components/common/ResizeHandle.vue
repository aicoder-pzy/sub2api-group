<template>
  <div
    role="separator"
    tabindex="0"
    class="resize-handle"
    :class="['resize-' + orientation, { 'resize-active': active }]"
    :aria-label="label"
    :title="label"
    :aria-orientation="orientation"
    :aria-valuemin="min"
    :aria-valuemax="max"
    :aria-valuenow="modelValue"
    @pointerdown="start"
    @pointermove="move"
    @pointerup="active = false"
    @pointercancel="active = false"
    @lostpointercapture="active = false"
    @keydown="onKeydown"
  ><span aria-hidden="true"></span></div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: number
  min: number
  max: number
  label: string
  orientation?: 'vertical' | 'horizontal'
  reverse?: boolean
}>(), { orientation: 'vertical', reverse: false })
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const active = ref(false)
let origin = 0, initial = 0
const coordinate = (event: PointerEvent) => props.orientation === 'vertical' ? event.clientX : event.clientY
const update = (value: number) => emit('update:modelValue', Math.round(Math.max(props.min, Math.min(props.max, value))))

function start(event: PointerEvent) {
  if (event.button !== 0 || !event.isPrimary) return
  event.preventDefault()
  origin = coordinate(event); initial = props.modelValue; active.value = true
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
}
function move(event: PointerEvent) {
  if (active.value) update(initial + (coordinate(event) - origin) * (props.reverse ? -1 : 1))
}
function onKeydown(event: KeyboardEvent) {
  const keys = props.orientation === 'vertical' ? ['ArrowLeft', 'ArrowRight'] : ['ArrowUp', 'ArrowDown']
  if (!keys.includes(event.key) && event.key !== 'Home' && event.key !== 'End') return
  event.preventDefault()
  if (event.key === 'Home') update(props.min)
  else if (event.key === 'End') update(props.max)
  else update(props.modelValue + (event.key === keys[0] ? -1 : 1) * (props.reverse ? -1 : 1) * (event.shiftKey ? 50 : 10))
}
</script>

<style scoped>
.resize-handle { position: relative; flex-shrink: 0; touch-action: none; user-select: none; outline: none; }
.resize-handle > span { position: absolute; border-radius: 99px; background: #cbd5e1; transition: background-color 150ms; }
.resize-vertical { width: 8px; cursor: col-resize; }
.resize-vertical > span { width: 3px; height: 32px; left: 2px; top: calc(50% - 16px); }
.resize-horizontal { height: 10px; cursor: row-resize; }
.resize-horizontal > span { height: 3px; width: 40px; top: 3px; left: calc(50% - 20px); }
.resize-handle:hover, .resize-handle:focus-visible, .resize-active { background: rgb(16 185 129 / 10%); }
.resize-handle:hover > span, .resize-handle:focus-visible > span, .resize-active > span { background: #059669; }
.resize-handle:focus-visible { outline: 2px solid #059669; outline-offset: -2px; }
</style>
