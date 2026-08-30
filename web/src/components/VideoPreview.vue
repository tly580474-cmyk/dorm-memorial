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
const selectedVariant = ref<'playback' | 'original' | ''>('')
type Failure = 'checking' | 'preparing' | 'network' | 'permission' | 'missing' | 'unsupported' | 'unavailable' | 'external'
const failure = ref<Failure>('checking')
const failureStatus = ref(0)
const automaticRetry = ref(false)
let automaticAttempts = 0
let videoRetryTimer = 0
let diagnosticTimeout = 0
let diagnostic: AbortController | undefined
let disposed = false
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
	window.clearTimeout(posterRetryTimer)
	if (posterRetry.value >= 5) return
	const delay = Math.min(8000, 1000 * 2 ** posterRetry.value)
	posterRetryTimer = window.setTimeout(() => {
		posterRetry.value += 1
		posterFailed.value = false
	}, delay)
}
const internalSource = computed(() => {
	if (props.external) return false
	try {
		const url = new URL(props.src, window.location.href)
		return url.origin === window.location.origin && /^\/api\/media\/[^/]+\/content$/.test(url.pathname)
	} catch { return false }
})
const safeExternalSource = computed(() => {
  if (!props.external) return true
  try {
    const url = new URL(props.src, window.location.href)
    return ['http:', 'https:'].includes(url.protocol) && !(window.location.protocol === 'https:' && url.protocol !== 'https:')
  } catch { return false }
})
const embeddedSource = computed(() => {
  if (!props.embedded || !safeExternalSource.value) return false
  try {
    const url = new URL(props.src, window.location.href)
    const host = url.hostname.toLowerCase().replace(/\.$/, '')
    return ['player.bilibili.com', 'youtube.com', 'www.youtube.com', 'www.youtube-nocookie.com'].includes(host)
  } catch { return false }
})
const validExternalPlayback = computed(() => safeExternalSource.value && (!props.embedded || embeddedSource.value))
function scheduleRetry(kind: 'preparing' | 'network', retryAfter = 0) {
	failure.value = kind
	const limit = kind === 'preparing' ? 18 : 3
	if (automaticAttempts >= limit) return
	const delay = Math.min(10000, Math.max(retryAfter, 1500 * 2 ** Math.min(automaticAttempts, 3)))
	automaticAttempts += 1
	automaticRetry.value = true
	videoRetryTimer = window.setTimeout(() => {
		automaticRetry.value = false
		videoRetry.value += 1
		videoFailed.value = false
	}, delay)
}
async function onVideoError(event: Event) {
	if (diagnostic || disposed) return
	window.clearTimeout(videoRetryTimer)
	automaticRetry.value = false
	videoFailed.value = true
	if (!internalSource.value) { failure.value = 'external'; return }
	failure.value = 'checking'
	failureStatus.value = 0
	const errorCode = (event.currentTarget as HTMLVideoElement).error?.code
	const controller = new AbortController()
	diagnostic = controller
	diagnosticTimeout = window.setTimeout(() => controller.abort(), 8000)
	try {
		// Read only one byte, then cancel even when storage ignores Range. Never download a video to diagnose it.
		const response = await fetch(playbackSrc.value, {
			headers: { Range: 'bytes=0-0' }, credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
		})
		void response.body?.cancel().catch(() => undefined)
		if (disposed || diagnostic !== controller) return
		failureStatus.value = response.status
		if (response.status === 401 || response.status === 403) { failure.value = 'permission'; return }
		if (response.status === 404 || response.status === 410) { failure.value = 'missing'; return }
		// A cached/new frontend may meet a server predating `watch`. Use its existing
		// compatible rendition endpoint once; never retry a rejected variant forever.
		if (response.status === 400 && new URL(playbackSrc.value, window.location.href).searchParams.get('variant') === 'watch' && !selectedVariant.value) {
			selectedVariant.value = 'playback'
			videoFailed.value = false
			return
		}
		if (response.status === 503 && response.headers.get('X-Media-State') === 'preparing') {
			scheduleRetry('preparing', Math.max(0, Number(response.headers.get('Retry-After')) || 0) * 1000)
			return
		}
		if (response.ok && (errorCode === 3 || errorCode === 4)) {
			if (response.headers.get('X-Media-Variant') === 'original' && selectedVariant.value !== 'original' && selectedVariant.value !== 'playback') {
				selectedVariant.value = 'playback'
				videoFailed.value = false
			} else failure.value = 'unsupported'
			return
		}
		if (response.ok || response.status >= 500 || response.status === 408 || response.status === 429) scheduleRetry('network')
		else failure.value = 'unavailable'
	} catch {
		if (!disposed && diagnostic === controller) scheduleRetry('network')
	} finally {
		if (diagnostic === controller) {
			window.clearTimeout(diagnosticTimeout)
			diagnostic = undefined
		}
		controller.abort()
	}
}
onBeforeUnmount(() => {
	disposed = true
	window.clearTimeout(posterRetryTimer)
	window.clearTimeout(videoRetryTimer)
	window.clearTimeout(diagnosticTimeout)
	diagnostic?.abort()
	diagnostic = undefined
})
const sourceLabel = computed(() => props.external ? '外链视频' : '上传视频')
const playbackSrc = computed(() => {
	if (!internalSource.value) return props.src
	const url = new URL(props.src, window.location.href)
	if (selectedVariant.value) url.searchParams.set('variant', selectedVariant.value)
	else if (!url.searchParams.has('variant')) url.searchParams.set('variant', 'watch')
	if (videoRetry.value) url.searchParams.set('playback_retry', String(videoRetry.value))
	return props.src.startsWith('/') ? `${url.pathname}${url.search}${url.hash}` : url.href
})
const failureTitle = computed(() => ({
	checking: '正在检查视频状态…',
	preparing: automaticRetry.value ? '正在准备兼容播放版本…' : '兼容播放版本仍在准备中',
	network: '视频暂时无法加载', permission: '无权访问此视频', missing: '视频不存在或已被删除',
	unsupported: '浏览器无法解码此视频', unavailable: '视频暂时不可用', external: '外链视频暂时无法播放',
})[failure.value])
const failureDetail = computed(() => {
	if (failure.value === 'checking') return '正在确认访问权限和文件状态'
	if (failure.value === 'preparing') return automaticRetry.value ? '服务器已确认正在处理，请稍候' : '已停止自动重试；请稍后重试，或尝试原文件'
	if (failure.value === 'permission') return '请登录有访问权限的账号后重试'
	if (failure.value === 'missing') return '请联系上传者确认文件是否仍可访问'
	if (failure.value === 'external') return '请检查原网站后重新尝试'
	if (failure.value === 'unsupported') return '可以重试，或尝试播放原文件'
	if (failure.value === 'unavailable') return `服务器拒绝了播放请求（HTTP ${failureStatus.value}），请刷新页面后重试`
	return automaticRetry.value ? '网络或媒体存储暂时不可用，稍后自动重试' : '已停止自动重试，请检查网络后重试'
})
function retryVideo() {
	window.clearTimeout(videoRetryTimer)
	window.clearTimeout(diagnosticTimeout)
	diagnostic?.abort()
	diagnostic = undefined
	automaticAttempts = 0
	automaticRetry.value = false
	videoRetry.value += 1
	videoFailed.value = false
}
function retryOriginal() {
	selectedVariant.value = 'original'
	retryVideo()
}
</script>

<template>
  <div class="video-preview" :class="{ square, active }">
    <template v-if="active">
      <iframe
        v-if="embeddedSource"
        :src="src"
        :title="`播放：${title}`"
        referrerpolicy="no-referrer"
        sandbox="allow-scripts allow-same-origin allow-presentation"
        allow="accelerometer; autoplay; encrypted-media; gyroscope; picture-in-picture; web-share"
        allowfullscreen
      ></iframe>
      <template v-else-if="validExternalPlayback">
		<video v-show="!videoFailed" :key="`${playbackSrc}:${videoRetry}`" :src="playbackSrc" :poster="posterSrc || undefined" controls autoplay preload="metadata" playsinline referrerpolicy="no-referrer" :aria-label="title" @error="onVideoError"></video>
		<div v-if="videoFailed" class="video-preparing" :role="failure === 'checking' || automaticRetry ? 'status' : 'alert'" aria-live="polite"><span v-if="failure === 'checking' || automaticRetry"></span><strong>{{ failureTitle }}</strong><small>{{ failureDetail }}</small><div v-if="failure !== 'checking' && !automaticRetry" class="video-recovery"><button type="button" @click="retryVideo">重新尝试</button><button v-if="internalSource && failure !== 'permission' && failure !== 'missing' && selectedVariant !== 'original'" type="button" @click="retryOriginal">尝试原文件</button></div></div>
	  </template>
      <div v-else class="video-preparing" role="alert"><strong>外链视频地址无效</strong><small>请检查链接后重试</small></div>
    </template>
    <button v-else type="button" :aria-label="`播放${sourceLabel}：${title}`" @click="active = true">
      <img v-if="posterSrc && !posterFailed" :src="posterSrc" alt="" loading="lazy" referrerpolicy="no-referrer" @error="onPosterError" />
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
