// @vitest-environment happy-dom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'
import { api } from './api'
import type { Conversation, Post, User } from './types'

let wrapper: VueWrapper | undefined

const user: User = {
  id: 'tester', username: 'tester', email: 'tester@example.test', role: 'member', status: 'active',
  nickname: '测试用户', bio: '', bed_no: '', memorial_note: '', avatar_path: '',
}

function makePost(id: string, title: string): Post {
  return {
    id, author: { id: user.id, username: user.username, nickname: user.nickname, avatar_path: '' }, title,
    body: title, body_html: `<p>${title}</p>`, status: 'published', visibility: 'members', content_date: null,
    moderation_note: '', submitted_at: null, published_at: '2026-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    tags: [], comment_count: 0, like_count: 0, liked_by_me: false, media: [], external_video_url: '',
  }
}

const firstPost = makePost('11111111111111111111111111111111', '第一篇')
const secondPost = makePost('22222222222222222222222222222222', '第二篇')
const conversation: Conversation = { id: '33333333333333333333333333333333', type: 'group', title: '宿舍群聊', peer: null, last_message: null, unread_count: 0 }

beforeEach(() => {
  vi.spyOn(api, 'me').mockResolvedValue({ user })
  vi.spyOn(api, 'members').mockResolvedValue({ members: [] })
  vi.spyOn(api, 'posts').mockImplementation(async (query = {}) => ({ posts: query.scope === 'mine' ? [] : [firstPost, secondPost] }))
  vi.spyOn(api, 'mediaUsage').mockResolvedValue({ usage: { used_bytes: 0, reserved_bytes: 0, quota_bytes: 1024 } })
  vi.spyOn(api, 'mediaLimits').mockResolvedValue({ max_image_upload_bytes: 1024 ** 2, supported_image_mime_types: ['image/jpeg'] })
  vi.spyOn(api, 'notifications').mockResolvedValue({ notifications: [], unread_count: 0 })
  vi.spyOn(api, 'conversations').mockResolvedValue({ conversations: [] })
  vi.spyOn(api, 'messages').mockResolvedValue({ messages: [] })
  vi.spyOn(api, 'markConversationRead').mockResolvedValue(undefined)
  vi.spyOn(api, 'sessions').mockResolvedValue({ sessions: [] })
  vi.spyOn(api, 'logout').mockResolvedValue(undefined)
  vi.spyOn(api, 'post')
  vi.spyOn(api, 'comments')
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('async lifecycle guards', () => {
  it('ignores a detail response after leaving it and opening another post', async () => {
    const postResponses = new Map<string, { promise: Promise<{ post: Post }>; resolve: (value: { post: Post }) => void }>()
    const commentResponses = new Map<string, { promise: Promise<{ comments: never[] }>; resolve: (value: { comments: never[] }) => void }>()
    for (const post of [firstPost, secondPost]) {
      let resolvePost!: (value: { post: Post }) => void
      let resolveComments!: (value: { comments: never[] }) => void
      postResponses.set(post.id, { promise: new Promise((resolve) => { resolvePost = resolve }), resolve: resolvePost })
      commentResponses.set(post.id, { promise: new Promise((resolve) => { resolveComments = resolve }), resolve: resolveComments })
    }
    vi.mocked(api.post).mockImplementation((id) => postResponses.get(id)!.promise)
    vi.mocked(api.comments).mockImplementation((id) => commentResponses.get(id)!.promise)

    wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
    await flushPromises()
    const detailLinks = wrapper.findAll('.detail-link')
    void detailLinks[0]!.trigger('click')
    await flushPromises()
    await wrapper.get('.detail-back').trigger('click')
    const secondLink = wrapper.findAll('.detail-link')[1]
    void secondLink!.trigger('click')
    await flushPromises()
    expect(wrapper.get('#detail-title').text()).toBe('第二篇')

    postResponses.get(firstPost.id)!.resolve({ post: firstPost })
    commentResponses.get(firstPost.id)!.resolve({ comments: [] })
    await flushPromises()
    expect(wrapper.get('#detail-title').text()).toBe('第二篇')

    postResponses.get(secondPost.id)!.resolve({ post: secondPost })
    commentResponses.get(secondPost.id)!.resolve({ comments: [] })
    await flushPromises()
    expect(wrapper.get('#detail-title').text()).toBe('第二篇')
  })

  it('stops a late microphone stream when the app unmounts during permission request', async () => {
    let resolveStream!: (stream: MediaStream) => void
    const pendingStream = new Promise<MediaStream>((resolve) => { resolveStream = resolve })
    const mediaDevices = { getUserMedia: vi.fn(() => pendingStream) }
    Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: mediaDevices })
    vi.stubGlobal('MediaRecorder', class {})
    vi.mocked(api.conversations).mockResolvedValue({ conversations: [conversation] })

    wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
    await flushPromises()
    await wrapper.findAll('.sidebar nav button')[4]!.trigger('click')
    await flushPromises()
    await wrapper.get('.voice-button').trigger('click')
    wrapper.unmount()
    wrapper = undefined

    const track = { stop: vi.fn() }
    resolveStream({ getTracks: () => [track] } as unknown as MediaStream)
    await flushPromises()
    expect(track.stop).toHaveBeenCalledOnce()
  })

  it('keeps the session visible and restores polling when logout fails', async () => {
    vi.useFakeTimers()
    vi.mocked(api.logout).mockRejectedValue(new Error('服务暂时不可用'))
    wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
    await flushPromises()
    const callsBefore = vi.mocked(api.notifications).mock.calls.length
    await wrapper.get('.avatar-button').trigger('click')
    await flushPromises()
    await wrapper.get('.logout-button').trigger('click')
    await flushPromises()

    expect(wrapper.find('.app-shell').exists()).toBe(true)
    expect(wrapper.get('.top-notice').text()).toContain('退出登录失败')
    expect(vi.mocked(api.notifications).mock.calls.length).toBeGreaterThan(callsBefore)
    const callsAfterRestore = vi.mocked(api.notifications).mock.calls.length
    await vi.advanceTimersByTimeAsync(4000)
    expect(vi.mocked(api.notifications).mock.calls.length).toBeGreaterThan(callsAfterRestore)
  })
})
