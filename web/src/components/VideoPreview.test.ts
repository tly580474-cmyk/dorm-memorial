// @vitest-environment happy-dom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import VideoPreview from './VideoPreview.vue'

const src = '/api/media/0123456789abcdef0123456789abcdef/content'
let wrapper: VueWrapper | undefined
const fetchMock = vi.fn()

function response(status: number, headers: Record<string, string> = {}) {
  const cancel = vi.fn().mockResolvedValue(undefined)
  return { status, ok: status >= 200 && status < 300, headers: new Headers(headers), body: { cancel } }
}
async function error(code = 4) {
  Object.defineProperty(wrapper!.get('video').element, 'error', { configurable: true, value: { code } })
  await wrapper!.get('video').trigger('error')
  await flushPromises()
}
async function play(props: Record<string, unknown> = {}) {
  wrapper = mount(VideoPreview, { props: { src, ...props } })
  await wrapper.get('button').trigger('click')
}

describe('VideoPreview', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('does not load video before clicking and asks the server for the default watch resource', async () => {
    wrapper = mount(VideoPreview, { props: { src: `${src}?token=hello`, title: '测试视频' } })
    expect(wrapper.find('video').exists()).toBe(false)
    expect(fetchMock).not.toHaveBeenCalled()
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('video').attributes('src')).toBe(`${src}?token=hello&variant=watch`)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('switches a confirmed legacy original decode failure to compatible playback', async () => {
    const result = response(206, { 'X-Media-Variant': 'original' })
    fetchMock.mockResolvedValue(result)
    await play()
    await error()
    expect(fetchMock).toHaveBeenCalledWith(`${src}?variant=watch`, expect.objectContaining({ headers: { Range: 'bytes=0-0' }, credentials: 'same-origin', cache: 'no-store' }))
    expect(result.body.cancel).toHaveBeenCalledOnce()
    expect(wrapper!.get('video').attributes('src')).toBe(`${src}?variant=playback`)
    expect(wrapper!.find('.video-preparing').exists()).toBe(false)
  })

  it('falls back once when an older server rejects watch, without misreporting a network failure', async () => {
    fetchMock.mockResolvedValue(response(400))
    await play()
    await error()
    expect(wrapper!.get('video').attributes('src')).toBe(`${src}?variant=playback`)
    expect(wrapper!.find('.video-preparing').exists()).toBe(false)
    await error()
    expect(wrapper!.get('[role="alert"]').text()).toContain('HTTP 400')
    expect(wrapper!.text()).not.toContain('检查网络')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not request another rendition when a prepared playback file cannot decode', async () => {
    fetchMock.mockResolvedValue(response(206, { 'X-Media-Variant': 'playback' }))
    await play()
    await error()
    expect(wrapper!.get('video').attributes('src')).toBe(`${src}?variant=watch`)
    expect(wrapper!.get('[role="alert"]').text()).toContain('浏览器无法解码')
    expect(wrapper!.text()).not.toContain('正在准备兼容')
    expect(vi.getTimerCount()).toBe(0)
    await wrapper!.get('.video-recovery button:last-child').trigger('click')
    expect(wrapper!.get('video').attributes('src')).toContain('variant=original')
    await error()
    expect(wrapper!.get('video').attributes('src')).toContain('variant=original')
  })

  it.each([401, 403, 404, 410])('classifies HTTP %i without retries or misleading preparation text', async (status) => {
    fetchMock.mockResolvedValue(response(status))
    await play()
    await error()
    expect(wrapper!.get('[role="alert"]').text()).toContain(status < 404 ? '无权访问' : '视频不存在')
    expect(wrapper!.text()).not.toContain('兼容播放版本')
    expect(wrapper!.findAll('.video-recovery button')).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('shows preparation only for a marked response and stops automatic retries after a finite limit', async () => {
    fetchMock.mockResolvedValue(response(503, { 'X-Media-State': 'preparing', 'Retry-After': '2' }))
    await play()
    await error()
    expect(wrapper!.get('[role="status"]').text()).toContain('正在准备兼容播放版本')
    await vi.advanceTimersByTimeAsync(1999)
    expect(wrapper!.get('video').attributes('src')).not.toContain('playback_retry')
    await vi.advanceTimersByTimeAsync(1)
    expect(wrapper!.get('video').attributes('src')).toContain('playback_retry=1')
    for (let attempt = 1; attempt < 19; attempt++) {
      await error()
      await vi.runOnlyPendingTimersAsync()
    }
    expect(wrapper!.get('[role="alert"]').text()).toContain('已停止自动重试')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('treats unmarked 503 as a storage/network error with bounded retries', async () => {
    fetchMock.mockResolvedValue(response(503))
    await play()
    for (let attempt = 0; attempt < 4; attempt++) {
      await error()
      expect(wrapper!.text()).not.toContain('兼容播放版本')
      await vi.runOnlyPendingTimersAsync()
    }
    expect(wrapper!.get('[role="alert"]').text()).toContain('已停止自动重试')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not interpret a network media error as original codec incompatibility', async () => {
    fetchMock.mockResolvedValue(response(206, { 'X-Media-Variant': 'original' }))
    await play()
    await error(2)
    expect(wrapper!.get('video').attributes('src')).toContain('variant=watch')
    expect(wrapper!.text()).toContain('网络或媒体存储')
  })

  it.each([{ external: true }, { src: 'https://other.example/video.mp4' }])('never fetches external video to diagnose it (%j)', async (props) => {
    await play(props)
    await error()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(wrapper!.get('[role="alert"]').text()).toContain('外链视频暂时无法播放')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not load an external video with an unsafe URL scheme', async () => {
    await play({ external: true, src: 'javascript:alert(1)' })
    expect(wrapper!.find('video').exists()).toBe(false)
    expect(wrapper!.find('iframe').exists()).toBe(false)
    expect(wrapper!.get('[role="alert"]').text()).toContain('地址无效')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('does not turn an unapproved embedded host into an iframe', async () => {
    await play({ external: true, embedded: true, src: 'https://evil.example/embed/video' })
    expect(wrapper!.find('iframe').exists()).toBe(false)
    expect(wrapper!.find('video').exists()).toBe(false)
    expect(wrapper!.get('[role="alert"]').text()).toContain('地址无效')
  })

  it('aborts an outstanding diagnostic and cancels timers on unmount', async () => {
    let signal: AbortSignal | undefined
    fetchMock.mockImplementation((_url: string, options: RequestInit) => {
      signal = options.signal as AbortSignal
      return new Promise(() => undefined)
    })
    await play()
    await wrapper!.get('video').trigger('error')
    expect(signal!.aborted).toBe(false)
    wrapper!.unmount()
    wrapper = undefined
    expect(signal!.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })
})
