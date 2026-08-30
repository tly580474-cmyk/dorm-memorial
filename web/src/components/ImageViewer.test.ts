// @vitest-environment happy-dom
import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import ImageViewer from './ImageViewer.vue'

const items = [
  { id: 'first', filename: 'first.jpg', has_preview: true },
  { id: 'second', filename: 'second.png', has_preview: true },
]
let wrapper: VueWrapper | undefined
beforeEach(() => vi.useFakeTimers())
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.useRealTimers() })
function open() {
  wrapper = mount(ImageViewer, { props: { open: true, items, initialIndex: 0 }, global: { stubs: { teleport: true } } })
  return wrapper
}

describe('ImageViewer image replacement', () => {
  it('removes the preview completely when the display image is ready', async () => {
    const view = open()
    expect(view.find('.image-viewer-preview').exists()).toBe(true)
    await view.get('.image-viewer-active').trigger('load')
    expect(view.find('.image-viewer-preview').exists()).toBe(false)
    expect(view.find('.image-viewer-loading').exists()).toBe(false)
    expect(view.findAll('.image-viewer-stack img')).toHaveLength(1)
  })

  it('keeps the display as a placeholder for the original, then shows only the original', async () => {
    const view = open()
    await view.get('.image-viewer-active').trigger('load')
    await view.get('.image-viewer-quality').trigger('click')
    expect(view.get('.image-viewer-preview').attributes('src')).toBe('/api/media/first/content?variant=display')
    await view.get('.image-viewer-active').trigger('load')
    expect(view.findAll('.image-viewer-stack img')).toHaveLength(1)
    expect(view.get('.image-viewer-active').attributes('src')).toBe('/api/media/first/content')
  })

  it('resets quality and loading when the parent changes the selected image', async () => {
    const view = open()
    await view.get('.image-viewer-active').trigger('load')
    await view.get('.image-viewer-quality').trigger('click')
    await view.setProps({ initialIndex: 1 })
    expect(view.get('.image-viewer-active').attributes('src')).toBe('/api/media/second/content?variant=display')
    expect(view.get('.image-viewer-preview').attributes('src')).toBe('/api/media/second/content?variant=preview')
    expect(view.get('.image-viewer-active').classes()).not.toContain('is-loaded')
  })

  it('cancels a scheduled retry after success or closing', async () => {
    const view = open()
    await view.get('.image-viewer-active').trigger('error')
    expect(vi.getTimerCount()).toBe(1)
    await view.get('.image-viewer-active').trigger('load')
    expect(vi.getTimerCount()).toBe(0)
    await view.get('.image-viewer-active').trigger('error')
    await view.setProps({ open: false })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores a late load event from the image that was just replaced', async () => {
    const view = open()
    const previousImage = view.get('.image-viewer-active')
    await view.setProps({ initialIndex: 1 })
    await previousImage.trigger('load')
    expect(view.get('.image-viewer-active').classes()).not.toContain('is-loaded')
    expect(view.find('.image-viewer-loading').exists()).toBe(true)
    await view.get('.image-viewer-active').trigger('load')
    expect(view.find('.image-viewer-preview').exists()).toBe(false)
  })

  it('falls back to the original once after display retries, then exposes a manual original retry', async () => {
    const view = open()
    const retryDelays = [1000, 2000, 4000, 8000, 8000]
    for (const delay of retryDelays) {
      await view.get('.image-viewer-active').trigger('error')
      await vi.advanceTimersByTimeAsync(delay)
      await nextTick()
    }
    await view.get('.image-viewer-active').trigger('error')
    await nextTick()

    expect(view.get('.image-viewer-active').attributes('src')).toBe('/api/media/first/content')
    expect(view.find('.image-viewer-loading').exists()).toBe(true)
    await view.get('.image-viewer-active').trigger('error')
    expect(view.get('.image-viewer-error').text()).toContain('原图暂时无法读取')
    expect(vi.getTimerCount()).toBe(0)

    await view.get('.image-viewer-error button').trigger('click')
    expect(view.get('.image-viewer-active').attributes('src')).toBe('/api/media/first/content?retry=1')
  })

  it('opens GIFs as original files so animation is retained', () => {
    wrapper = mount(ImageViewer, { props: { open: true, items: [{ id: 'gif', filename: 'memory.gif', mime_type: 'image/gif', has_preview: true }], initialIndex: 0 }, global: { stubs: { teleport: true } } })
    expect(wrapper.get('.image-viewer-active').attributes('src')).toBe('/api/media/gif/content')
    expect(wrapper.get('.image-viewer-quality').attributes('disabled')).toBeDefined()
  })
})
