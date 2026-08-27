<script setup lang="ts">
import { computed, ref } from 'vue'
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
const posterFailed = ref(false)
const sourceLabel = computed(() => props.external ? '外链视频' : '上传视频')
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
      <video v-else :src="src" :poster="poster || undefined" controls autoplay preload="metadata" playsinline :aria-label="title"></video>
    </template>
    <button v-else type="button" :aria-label="`播放${sourceLabel}：${title}`" @click="active = true">
      <img v-if="poster && !posterFailed" :src="poster" alt="" loading="lazy" @error="posterFailed = true" />
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
.shade { position: absolute; inset: 0; background: linear-gradient(180deg, rgba(13,16,14,.08) 30%, rgba(13,16,14,.82) 100%); }
.source-badge { position: absolute; top: 10px; left: 10px; display: inline-flex; align-items: center; gap: 5px; padding: 5px 8px; border: 1px solid rgba(255,255,255,.32); border-radius: 999px; background: rgba(23,27,24,.68); font-size: 12px; backdrop-filter: blur(7px); }
.play-mark { position: absolute; inset: 50% auto auto 50%; width: 58px; height: 58px; display: grid; place-items: center; border: 1px solid rgba(255,255,255,.55); border-radius: 50%; background: rgba(255,255,255,.92); color: #354139; transform: translate(-50%, -50%); box-shadow: 0 8px 24px rgba(0,0,0,.22); transition: transform 160ms ease; }
button:hover .play-mark { transform: translate(-50%, -50%) scale(1.06); }
.caption { position: absolute; right: 13px; bottom: 12px; left: 13px; display: grid; gap: 2px; }
.caption strong { overflow: hidden; font-size: 14px; text-overflow: ellipsis; white-space: nowrap; }
.caption small { color: rgba(255,255,255,.76); font-size: 12px; }
@media (prefers-reduced-motion: reduce) { .play-mark { transition: none; } }
</style>
