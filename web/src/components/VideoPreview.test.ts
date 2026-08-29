// @vitest-environment happy-dom

import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import VideoPreview from './VideoPreview.vue'

describe('VideoPreview', () => {
  it('tries the original upload before requesting a compatible rendition', async () => {
    vi.useFakeTimers()
    const wrapper = mount(VideoPreview, {
      props: {
        src: '/api/media/0123456789abcdef0123456789abcdef/content',
        title: '测试视频',
      },
    })

    await wrapper.get('button').trigger('click')
    expect(wrapper.get('video').attributes('src')).toBe('/api/media/0123456789abcdef0123456789abcdef/content')

    await wrapper.get('video').trigger('error')
    expect(wrapper.get('video').attributes('src')).toContain('variant=playback')

    await wrapper.get('video').trigger('error')
    expect(wrapper.get('[role="status"]').text()).toContain('正在准备兼容播放版本')

    wrapper.unmount()
    vi.useRealTimers()
  })
})
