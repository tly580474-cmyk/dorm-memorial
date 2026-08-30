// @vitest-environment happy-dom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'
import { api } from './api'
import type { MediaProcessingJob } from './api'

let wrapper: VueWrapper | undefined
let progress: (value: number) => void
let processing: (job: MediaProcessingJob) => void
let complete: (value: Awaited<ReturnType<typeof api.uploadMedia>>) => void
let fail: (error: Error) => void
let uploadIDs: string[]
const usage = { used_bytes: 0, reserved_bytes: 0, quota_bytes: 1024 ** 3 }

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test-video')
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
  vi.spyOn(api, 'me').mockResolvedValue({ user: { id: 'tester', username: 'tester', nickname: '测试用户', email: '', role: 'member', status: 'active', bio: '', bed_no: '', memorial_note: '', avatar_path: '' } })
  vi.spyOn(api, 'members').mockResolvedValue({ members: [] })
  vi.spyOn(api, 'posts').mockResolvedValue({ posts: [] })
  vi.spyOn(api, 'mediaUsage').mockResolvedValue({ usage })
  vi.spyOn(api, 'mediaLimits').mockResolvedValue({ max_image_upload_bytes: 15 * 1024 ** 2, supported_image_mime_types: ['image/jpeg', 'image/png', 'image/gif', 'image/webp'] })
  vi.spyOn(api, 'notifications').mockResolvedValue({ notifications: [], unread_count: 0 })
  vi.spyOn(api, 'conversations').mockResolvedValue({ conversations: [] })
  vi.spyOn(api, 'createPost')
  vi.spyOn(api, 'uploadMedia').mockImplementation((_file, _id, _metadata, onProgress, onProcessing) => {
    uploadIDs.push(_id)
    progress = onProgress!
    processing = onProcessing!
    return new Promise((resolve, reject) => { complete = resolve; fail = reject })
  })
  uploadIDs = []
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  vi.useRealTimers()
})

async function selectVideo() {
  wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
  await flushPromises()
  await wrapper.get('.page-heading button').trigger('click')
  const input = wrapper.get('.media-editor input[type=file]')
  Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['fixture'], 'progress.mp4', { type: 'video/mp4' })] })
  await input.trigger('change')
  // A browser may fail to read local video metadata; uploading still starts after the timeout.
  await vi.advanceTimersByTimeAsync(1800)
  expect(wrapper.getComponent({ name: 'RichTextEditor' }).props('disabled')).toBe(false)
  expect(wrapper.get('.composer-dialog button[type=submit]').attributes('disabled')).toBeDefined()
  expect(api.uploadMedia).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(3200)
  await flushPromises()
  expect(api.uploadMedia).toHaveBeenCalledOnce()
}

describe('background video upload UI', () => {
  it('updates progress and processing without typing, and keeps the editor available', async () => {
    await selectVideo()
    progress(100)
    await nextTick()
    expect(wrapper!.get('.media-queue').text()).toContain('已发送，服务器正在保存/校验')
    progress(3)
    // Reproduce the incidental render that previously left the progress frozen at 3%.
    await wrapper!.get('#post-title').setValue('仍可编辑')
    expect(wrapper!.get('.media-queue progress').attributes('value')).toBe('3')
    progress(42)
    await nextTick()
    expect(wrapper!.get('.media-queue progress').attributes('value')).toBe('42')
    expect(wrapper!.get('.media-queue').text()).toContain('正在后台上传 42%')
    processing({ id: 'job', media_id: 'media', phase: 'transcoding', step: 'probing', encoder: '', error_code: '' })
    await nextTick()
    expect(wrapper!.get('.media-queue article').attributes('data-status')).toBe('processing')
    expect(wrapper!.get('.media-queue').text()).toContain('正在检测视频格式')
    expect(wrapper!.getComponent({ name: 'RichTextEditor' }).props('disabled')).toBe(false)
    await vi.advanceTimersByTimeAsync(1800)
    expect(api.createPost).not.toHaveBeenCalled()
    complete({ media: { id: 'media', original_filename: 'progress.mp4', media_type: 'video', mime_type: 'video/mp4', size_bytes: 7, status: 'ready', has_preview: true }, usage })
    await flushPromises()
    expect(wrapper!.get('.media-queue article').attributes('data-status')).toBe('ready')
    expect(wrapper!.get('.media-queue').text()).toContain('已就绪')
    expect(wrapper!.get('.composer-dialog button[type=submit]').attributes('disabled')).toBeUndefined()
  })

  it('shows an asynchronous failure and makes retry available without another interaction', async () => {
    await selectVideo()
    fail(new Error('测试上传中断'))
    await flushPromises()
    expect(wrapper!.get('.media-queue article').attributes('data-status')).toBe('error')
    expect(wrapper!.get('.media-queue').text()).toContain('测试上传中断')
    expect(wrapper!.find('button[aria-label="重新上传"]').exists()).toBe(true)
    const firstUploadID = uploadIDs[0]
    await wrapper!.get('button[aria-label="重新上传"]').trigger('click')
    await flushPromises()
    expect(uploadIDs).toHaveLength(2)
    expect(uploadIDs[1]).not.toBe(firstUploadID)
  })

  it('uses the server image limit and MIME allowlist before starting an upload', async () => {
    vi.mocked(api.mediaLimits).mockResolvedValue({ max_image_upload_bytes: 1024 ** 2, supported_image_mime_types: ['image/jpeg', 'image/png'] })
    wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
    await flushPromises()
    await wrapper.get('.page-heading button').trigger('click')
    const input = wrapper.get('.media-editor input[type=file]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File([new Uint8Array(1024 ** 2 + 1)], 'too-large.webp', { type: 'image/webp' })] })
    await input.trigger('change')
    expect(wrapper.get('.media-editor [role="alert"]').text()).toContain('不是支持的图片格式')
    expect(api.uploadMedia).not.toHaveBeenCalled()
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File([new Uint8Array(1024 ** 2 + 1)], 'too-large.jpg', { type: 'image/jpeg' })] })
    await input.trigger('change')
    expect(wrapper.get('.media-editor [role="alert"]').text()).toContain('超过 1 MiB')
    expect(api.uploadMedia).not.toHaveBeenCalled()
  })

  it('reuses an image upload ID when a failed upload is retried', async () => {
    wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
    await flushPromises()
    await wrapper.get('.page-heading button').trigger('click')
    const input = wrapper.get('.media-editor input[type=file]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['fixture'], 'memory.jpg', { type: 'image/jpeg' })] })
    await input.trigger('change')
    await flushPromises()
    expect(uploadIDs).toHaveLength(1)
    const firstUploadID = uploadIDs[0]
    fail(new Error('图片上传失败'))
    await flushPromises()
    await wrapper.get('button[aria-label="重新上传"]').trigger('click')
    await flushPromises()
    expect(uploadIDs).toHaveLength(2)
    expect(uploadIDs[1]).toBe(firstUploadID)
  })
})
