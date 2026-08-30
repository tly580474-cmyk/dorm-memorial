<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

export interface ViewerItem {
  id: string
  filename?: string
  size_bytes?: number
  has_preview?: boolean
}

const props = defineProps<{
  open: boolean
  items: ViewerItem[]
  initialIndex: number
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'update:index', index: number): void
}>()

const index = ref(props.initialIndex)

function formatBytes(value?: number) {
  if (!value || value <= 0) return ''
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let size = value / 1024
  let unit = units[0]
  for (let i = 1; size >= 1024 && i < units.length; i++) { size /= 1024; unit = units[i] }
  return `${size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${unit}`
}

const useOriginal = ref(false)
const retryNonce = ref(0)
const retryAttempt = ref(0)
let retryTimer = 0
const src = computed(() => {
  const item = props.items[index.value]
  if (!item) return ''
  const params = new URLSearchParams()
  if (!useOriginal.value) params.set('variant', 'display')
  if (retryNonce.value) params.set('retry', String(retryNonce.value))
  const query = params.toString()
  return `/api/media/${encodeURIComponent(item.id)}/content${query ? `?${query}` : ''}`
})
const current = computed(() => props.items[index.value] ?? null)
const previousQualitySrc = ref('')
const previewSrc = computed(() => previousQualitySrc.value || (current.value?.has_preview ? `/api/media/${encodeURIComponent(current.value.id)}/content?variant=preview` : ''))

const loaded = ref(false)
const loadFailed = ref(false)
function resetLoadState(resetQuality = true) {
	window.clearTimeout(retryTimer)
	if (resetQuality) { useOriginal.value = false; previousQualitySrc.value = '' }
	retryAttempt.value = 0
	retryNonce.value = resetQuality ? 0 : retryNonce.value + 1
  loaded.value = false
  loadFailed.value = false
  resetTransform()
}

function isCurrentImage(event: Event) {
	return props.open && (event.currentTarget as HTMLImageElement).getAttribute('src') === src.value
}

function onImageError(event: Event) {
	if (!isCurrentImage(event)) return
	window.clearTimeout(retryTimer)
	loaded.value = false
	if (!useOriginal.value && retryAttempt.value < 5) {
		retryAttempt.value += 1
		const delay = Math.min(8000, 1000 * 2 ** (retryAttempt.value - 1))
		retryTimer = window.setTimeout(() => { retryNonce.value += 1 }, delay)
		return
	}
	loadFailed.value = true
}

function loadOriginal() {
	if (useOriginal.value) return
	window.clearTimeout(retryTimer)
	if (loaded.value) previousQualitySrc.value = src.value
	useOriginal.value = true
	loaded.value = false
	loadFailed.value = false
}

function onImageLoaded(event: Event) {
	if (!isCurrentImage(event)) return
	window.clearTimeout(retryTimer)
	loaded.value = true
	loadFailed.value = false
	if (useOriginal.value) return
	for (const nextIndex of [index.value - 1, index.value + 1]) {
		const item = props.items[nextIndex]
		if (!item) continue
		const image = new Image()
		image.fetchPriority = 'low'
		image.src = `/api/media/${encodeURIComponent(item.id)}/content?variant=display`
	}
}

const scale = ref(1)
const translateX = ref(0)
const translateY = ref(0)
const MIN_SCALE = 1
const MAX_SCALE = 5

function resetTransform() {
  scale.value = 1
  translateX.value = 0
  translateY.value = 0
}

function zoomBy(factor: number, centerX = 0, centerY = 0) {
  const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, scale.value * factor))
  if (next === scale.value) return
  const ratio = next / scale.value
  if (ratio !== 1) {
    translateX.value = centerX - ratio * (centerX - translateX.value)
    translateY.value = centerY - ratio * (centerY - translateY.value)
  }
  scale.value = next
}

function onWheel(event: WheelEvent) {
  if (!props.open) return
  event.preventDefault()
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  const centerX = event.clientX - rect.left - rect.width / 2
  const centerY = event.clientY - rect.top - rect.height / 2
  zoomBy(event.deltaY < 0 ? 1.15 : 1 / 1.15, centerX, centerY)
}

function onDoubleClick(event: MouseEvent) {
  if (scale.value > 1) resetTransform()
  else zoomBy(2, 0, 0)
}

// 拖拽平移（仅在放大后可用）
const dragging = ref(false)
let lastX = 0
let lastY = 0
function onPointerDown(event: PointerEvent) {
  if (scale.value <= 1) return
  dragging.value = true
  lastX = event.clientX
  lastY = event.clientY
  ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
}
function onPointerMove(event: PointerEvent) {
  if (!dragging.value) return
  translateX.value += event.clientX - lastX
  translateY.value += event.clientY - lastY
  lastX = event.clientX
  lastY = event.clientY
}
function onPointerUp(event: PointerEvent) {
  dragging.value = false
  ;(event.currentTarget as HTMLElement).releasePointerCapture(event.pointerId)
}

// 触摸双指缩放
let touchPinch: { distance: number; scale: number } | null = null
function onTouchStart(event: TouchEvent) {
  if (event.touches.length === 2) {
    touchPinch = { distance: touchDistance(event.touches), scale: scale.value }
  }
}
function onTouchMove(event: TouchEvent) {
  if (event.touches.length === 2 && touchPinch) {
    const distance = touchDistance(event.touches)
    const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, touchPinch.scale * (distance / touchPinch.distance)))
    scale.value = next
  }
}
function onTouchEnd() {
  touchPinch = null
}
function touchDistance(touches: TouchList) {
  const first = touches[0]
  const second = touches[1]
  if (!first || !second) return 0
  const dx = first.clientX - second.clientX
  const dy = first.clientY - second.clientY
  return Math.sqrt(dx * dx + dy * dy)
}

function step(delta: number) {
  const next = index.value + delta
  if (next < 0 || next >= props.items.length) return
  emit('update:index', next)
  index.value = next
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open) return
  switch (event.key) {
    case 'Escape': emit('close'); break
    case 'ArrowLeft': step(-1); break
    case 'ArrowRight': step(1); break
    case '+':
    case '=': zoomBy(1.25); break
    case '-': zoomBy(1 / 1.25); break
    case '0': resetTransform(); break
  }
}

watch(
  [() => props.open, () => props.initialIndex],
  ([open, initialIndex]) => { if (open) index.value = initialIndex },
  { immediate: true },
)
watch(
  [() => props.open, () => current.value?.id],
  ([open]) => {
    resetLoadState()
    if (open) {
      window.addEventListener('keydown', onKeydown)
    } else {
      window.removeEventListener('keydown', onKeydown)
      dragging.value = false
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
	window.clearTimeout(retryTimer)
	window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="viewer-fade">
      <div v-if="open" class="image-viewer" role="dialog" aria-modal="true" aria-label="图片查看器" @keydown.esc="emit('close')">
        <div class="image-viewer-backdrop" @click="emit('close')"></div>
        <button class="image-viewer-close" type="button" aria-label="关闭" @click="emit('close')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12" /></svg></button>

        <div
          class="image-viewer-stage"
          @wheel.prevent="onWheel"
          @dblclick.prevent="onDoubleClick"
          @pointerdown="onPointerDown"
          @pointermove="onPointerMove"
          @pointerup="onPointerUp"
          @pointercancel="onPointerUp"
          @touchstart.stop="onTouchStart"
          @touchmove.stop.prevent="onTouchMove"
          @touchend.stop="onTouchEnd"
        >
          <figure class="image-viewer-figure" :style="{ transform: `translate(${translateX}px, ${translateY}px) scale(${scale})` }">
			<div class="image-viewer-stack">
			  <img v-if="previewSrc && !loaded" class="image-viewer-preview" :src="previewSrc" alt="" draggable="false" />
			  <img v-if="!loadFailed" :key="src" class="image-viewer-active" :src="src" :alt="current?.filename ?? ''" draggable="false"
				@load="onImageLoaded" @error="onImageError"
				:class="{ 'is-loaded': loaded }" />
			  <div v-if="!loaded && !loadFailed" class="image-viewer-loading" role="status" aria-live="polite"><span></span><small>{{ useOriginal ? '正在加载原图…' : '正在准备清晰图片…' }}</small></div>
			</div>
            <figcaption v-if="current" class="image-viewer-caption" @click.stop>
              <span :title="current.filename">{{ current.filename }}</span>
              <small v-if="current.size_bytes">{{ formatBytes(current.size_bytes) }}</small>
            </figcaption>
            <div v-if="loadFailed" class="image-viewer-error" role="status">
              <span>图片暂时无法读取</span>
			  <button type="button" @click.stop="resetLoadState(false)">重新加载</button>
            </div>
          </figure>
        </div>

        <button v-if="scale > 1" class="image-viewer-range-tip" type="button" @click="resetTransform">缩放 {{ scale.toFixed(1) }}× · 点击重置</button>

        <div class="image-viewer-toolbar" role="toolbar" @click.stop>
          <button type="button" :disabled="scale >= MAX_SCALE" aria-label="放大" @click="zoomBy(1.25)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3M11 8v6M8 11h6" /></svg></button>
          <button type="button" :disabled="scale <= MIN_SCALE" aria-label="缩小" @click="zoomBy(1 / 1.25)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3M8 11h6" /></svg></button>
          <button type="button" aria-label="重置缩放" @click="resetTransform"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 2.6-6.4L3 8" /><path d="M3 3v5h5" /></svg></button>
		  <button class="image-viewer-quality" type="button" :disabled="useOriginal" @click="loadOriginal">{{ useOriginal ? '原图' : '查看原图' }}</button>
          <template v-if="items.length > 1">
            <span class="image-viewer-separator" aria-hidden="true"></span>
            <button type="button" :disabled="index <= 0" aria-label="上一张" @click="step(-1)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m15 18-6-6 6-6" /></svg></button>
            <span class="image-viewer-position">{{ index + 1 }} / {{ items.length }}</span>
            <button type="button" :disabled="index >= items.length - 1" aria-label="下一张" @click="step(1)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m9 18 6-6-6-6" /></svg></button>
          </template>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.image-viewer {
  position: fixed;
  inset: 0;
  z-index: 1200;
  display: flex;
  align-items: center;
  justify-content: center;
  touch-action: none;
}
.image-viewer-backdrop {
  position: absolute;
  inset: 0;
  background: rgba(8, 12, 24, 0.92);
  backdrop-filter: blur(4px);
}
.image-viewer-stage {
  position: relative;
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: zoom-in;
  overflow: hidden;
}
.image-viewer-stage[data-defined] { cursor: grab; }
.image-viewer-figure {
  margin: 0;
  max-width: 92vw;
  max-height: 88vh;
  will-change: transform;
  user-select: none;
  transition: transform 0.15s ease-out;
}
.image-viewer-stack { display: grid; place-items: center; }
.image-viewer-stack img {
	grid-area: 1 / 1;
  display: block;
  max-width: 92vw;
  max-height: 84vh;
  object-fit: contain;
  border-radius: 6px;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.5);
}
.image-viewer-preview { opacity: .72; filter: blur(1px); }
.image-viewer-active { opacity: 0; }
.image-viewer-active.is-loaded { opacity: 1; }
.image-viewer-loading { grid-area: 1 / 1; z-index: 1; align-self: end; display: inline-flex; align-items: center; gap: 7px; margin-bottom: 18px; padding: 7px 11px; border-radius: 999px; background: rgba(0,0,0,.58); color: rgba(255,255,255,.9); pointer-events: none; }
.image-viewer-loading span { width: 14px; height: 14px; border: 2px solid rgba(255,255,255,.35); border-top-color: #fff; border-radius: 50%; animation: viewer-spin .8s linear infinite; }
.image-viewer-caption {
  position: absolute;
  inset-inline: 0;
  bottom: -2.5rem;
  left: 0;
  right: 0;
  display: flex;
  gap: 0.6rem;
  align-items: center;
  justify-content: center;
  color: rgba(255, 255, 255, 0.85);
  font-size: 0.8rem;
  white-space: nowrap;
}
.image-viewer-caption span { max-width: 50vw; overflow: hidden; text-overflow: ellipsis; }
.image-viewer-error {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  gap: 0.8rem;
  color: rgba(255, 255, 255, 0.85);
  text-align: center;
}
.image-viewer-error button {
  background: rgba(255, 255, 255, 0.14);
  color: #fff;
  border: 1px solid rgba(255, 255, 255, 0.3);
  border-radius: 999px;
  padding: 0.45rem 1rem;
  cursor: pointer;
}
.image-viewer-close {
  position: absolute;
  top: 1rem;
  right: 1rem;
  z-index: 2;
  width: 2.6rem;
  height: 2.6rem;
  display: grid;
  place-items: center;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.25);
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  cursor: pointer;
}
.image-viewer-close svg { width: 1.25rem; height: 1.25rem; }
.image-viewer-toolbar {
  position: absolute;
  bottom: 1.2rem;
  left: 50%;
  transform: translateX(-50%);
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem;
  border-radius: 999px;
  background: rgba(0, 0, 0, 0.55);
  border: 1px solid rgba(255, 255, 255, 0.18);
}
.image-viewer-toolbar button {
  width: 2.3rem;
  height: 2.3rem;
  display: grid;
  place-items: center;
  border-radius: 999px;
  border: 0;
  background: transparent;
  color: rgba(255, 255, 255, 0.9);
  cursor: pointer;
}
.image-viewer-toolbar .image-viewer-quality { width: auto; min-width: 74px; padding: 0 12px; font-size: 12px; }
.image-viewer-toolbar button:disabled { opacity: 0.35; cursor: default; }
.image-viewer-toolbar button:not(:disabled):hover { background: rgba(255, 255, 255, 0.15); }
.image-viewer-toolbar svg { width: 1.1rem; height: 1.1rem; }
.image-viewer-separator { width: 1px; height: 1.2rem; background: rgba(255, 255, 255, 0.25); }
.image-viewer-position { color: rgba(255, 255, 255, 0.85); font-size: 0.8rem; padding: 0 0.3rem; min-width: 3.4rem; text-align: center; }
.image-viewer-range-tip {
  position: absolute;
  bottom: 4.6rem;
  left: 50%;
  transform: translateX(-50%);
  z-index: 2;
  border: 0;
  background: rgba(255, 255, 255, 0.14);
  color: #fff;
  font-size: 0.75rem;
  padding: 0.35rem 0.9rem;
  border-radius: 999px;
  cursor: pointer;
}
.viewer-fade-enter-active, .viewer-fade-leave-active { transition: opacity 0.18s ease; }
.viewer-fade-enter-from, .viewer-fade-leave-to { opacity: 0; }
@keyframes viewer-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) {
	.image-viewer-active, .viewer-fade-enter-active, .viewer-fade-leave-active { transition: none; }
	.image-viewer-loading span { animation: none; border-color: rgba(255,255,255,.7); }
}
</style>
