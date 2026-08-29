<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { ExternalLink, Film, Play } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  src: string
  poster?: string
  title?: string
  external?: boolean
  embedded?: boolean
  square?: boolean
}>(), {
  poster: '',
  title: '视频',
  external: false,
  embedded: false,
  square: false,
})

const active = ref(false)
const videoFailed = ref(false)
const videoRetry = ref(0)
const useCompatible = ref(false)
let videoRetryTimer = 0
const posterFailed = ref(false)
const posterRetry = ref(0)
let posterRetryTimer = 0
const posterSrc = computed(() => {
	if (!props.poster) return ''
	if (!posterRetry.value) return props.poster
	return `${props.poster}${props.poster.includes('?') ? '&' : '?'}poster_retry=${posterRetry.value}`
})
function onPosterError() {
	posterFailed.value = true
	if (posterRetry.value >= 5) return
	const delay = Math.min(8000, 1000 * 2 ** posterRetry.value)
	posterRetryTimer = window.setTimeout(() => {
		posterRetry.value += 1
		posterFailed.value = false
	}, delay)
}
function onVideoError() {
	if (!props.external && !useCompatible.value) {
		useCompatible.value = true
		videoFailed.value = false
		videoRetry.value = 0
		return
	}
	videoFailed.value = true
	if (props.external || videoRetry.value >= 36) return
	const delay = Math.min(10000, 1500 * 2 ** Math.min(videoRetry.value, 3))
	videoRetryTimer = window.setTimeout(() => {
		videoRetry.value += 1
		videoFailed.value = false
	}, delay)
}
onBeforeUnmount(() => {
	window.clearTimeout(posterRetryTimer)
	window.clearTimeout(videoRetryTimer)
})
const sourceLabel = computed(() => props.external ? '外链视频' : '上传视频')
const playbackSrc = computed(() => {
	const base = props.external || !useCompatible.value || props.src.includes('variant=') ? props.src : `${props.src}${props.src.includes('?') ? '&' : '?'}variant=playback`
	return videoRetry.value ? `${base}${base.includes('?') ? '&' : '?'}playback_retry=${videoRetry.value}` : base
})
function retryVideo() {
	window.clearTimeout(videoRetryTimer)
	videoRetry.value += 1
	videoFailed.value = false
}
function retryOriginal() {
	window.clearTimeout(videoRetryTimer)
	useCompatible.value = false
	videoRetry.value = 0
	videoFailed.value = false
}
</script>

<template>
  <div class="video-preview" :class="{ square, active }">
    <template v-if="active">
      <iframe
        v-if="embedded"
        :src="src"
        :title="`播放：${title}`"
        sandbox="allow-scripts allow-same-origin allow-presentation"
        allow="accelerometer; autoplay; encrypted-media; gyroscope; picture-in-picture; web-share"
        allowfullscreen
      ></iframe>
	  <template v-else>
		<video v-show="!videoFailed" :key="playbackSrc" :src="playbackSrc" :poster="posterSrc || undefined" controls autoplay preload="metadata" playsinline :aria-label="title" @error="onVideoError"></video>
		<div v-if="videoFailed" class="video-preparing" :role="external || videoRetry >= 36 ? 'alert' : 'status'" aria-live="polite"><span v-if="!external && videoRetry < 36"></span><strong>{{ external || videoRetry >= 36 ? '视频暂时无法播放' : '正在准备兼容播放版本…' }}</strong><small>{{ external ? '请检查原网站后重新尝试' : videoRetry >= 36 ? '可以重试，或尝试播放原文件' : '原文件不兼容，首次处理可能需要一些时间' }}</small><div v-if="external || videoRetry >= 36" class="video-recovery"><button type="button" @click="retryVideo">重新尝试</button><button v-if="!external" type="button" @click="retryOriginal">尝试原文件</button></div></div>
	  </template>
    </template>
    <button v-else type="button" :aria-label="`播放${sourceLabel}：${title}`" @click="active = true">
      <img v-if="posterSrc && !posterFailed" :src="posterSrc" alt="" loading="lazy" @error="onPosterError" />
      <span v-else class="fallback-art" aria-hidden="true"><Film :size="48" /></span>
      <span class="shade" aria-hidden="true"></span>
      <span class="source-badge"><ExternalLink v-if="external" :size="13" /><Film v-else :size="13" />{{ sourceLabel }}</span>
      <span class="play-mark" aria-hidden="true"><Play :size="28" fill="currentColor" /></span>
      <span class="caption"><strong>{{ title }}</strong><small>点击后才加载并播放</small></span>
    </button>
  </div>
</template>

<style scoped>
.video-preview { position: relative; width: 100%; aspect-ratio: 16 / 9; overflow: hidden; border-radius: 11px; background: #343732; color: #fff; }
.video-preview.square { aspect-ratio: 1; }
.video-preview > button { position: relative; width: 100%; height: 100%; display: block; overflow: hidden; padding: 0; border: 0; border-radius: inherit; background: transparent; color: inherit; cursor: pointer; text-align: left; }
.video-preview > button:focus-visible { outline: 3px solid #b85838; outline-offset: -3px; }
.video-preview img, .video-preview video, .video-preview iframe { width: 100%; height: 100%; display: block; border: 0; object-fit: cover; }
.video-preview.active video { object-fit: contain; }
.fallback-art { position: absolute; inset: 0; display: grid; place-items: center; background: radial-gradient(circle at 72% 22%, #6e776e, transparent 36%), linear-gradient(145deg, #4b514b, #292d29); color: rgba(255,255,255,.82); }
.video-preparing { position: absolute; inset: 0; display: grid; place-content: center; justify-items: center; gap: 7px; padding: 20px; background: #292d29; color: rgba(255,255,255,.9); text-align: center; }
.video-preparing span { width: 24px; height: 24px; border: 3px solid rgba(255,255,255,.3); border-top-color: #fff; border-radius: 50%; animation: video-spin .9s linear infinite; }
.video-preparing small { color: rgba(255,255,255,.68); }
.video-recovery { display: flex; flex-wrap: wrap; justify-content: center; gap: 8px; margin-top: 8px; }
.video-recovery button { min-height: 44px; padding: 8px 14px; border: 1px solid rgba(255,255,255,.42); border-radius: 999px; background: rgba(255,255,255,.12); color: #fff; cursor: pointer; }
.video-recovery button:hover { background: rgba(255,255,255,.2); }
.video-recovery button:focus-visible { outline: 3px solid #fff; outline-offset: 2px; }
.shade { position: absolute; inset: 0; background: linear-gradient(180deg, rgba(13,16,14,.08) 30%, rgba(13,16,14,.82) 100%); }
.source-badge { position: absolute; top: 10px; left: 10px; display: inline-flex; align-items: center; gap: 5px; padding: 5px 8px; border: 1px solid rgba(255,255,255,.32); border-radius: 999px; background: rgba(23,27,24,.68); font-size: 12px; backdrop-filter: blur(7px); }
.play-mark { position: absolute; inset: 50% auto auto 50%; width: 58px; height: 58px; display: grid; place-items: center; border: 1px solid rgba(255,255,255,.55); border-radius: 50%; background: rgba(255,255,255,.92); color: #354139; transform: translate(-50%, -50%); box-shadow: 0 8px 24px rgba(0,0,0,.22); transition: transform 160ms ease; }
button:hover .play-mark { transform: translate(-50%, -50%) scale(1.06); }
.caption { position: absolute; right: 13px; bottom: 12px; left: 13px; display: grid; gap: 2px; }
.caption strong { overflow: hidden; font-size: 14px; text-overflow: ellipsis; white-space: nowrap; }
.caption small { color: rgba(255,255,255,.76); font-size: 12px; }
@keyframes video-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .play-mark { transition: none; } .video-preparing span { animation: none; } }
</style>
