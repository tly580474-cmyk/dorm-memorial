// @vitest-environment happy-dom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'
import { api } from './api'
import type { Post, User } from './types'

let wrapper: VueWrapper | undefined

const user: User = {
  id: 'tester', username: 'tester', email: 'tester@example.test', role: 'member', status: 'active',
  nickname: '测试用户', bio: '', bed_no: '', memorial_note: '', avatar_path: '',
}
const post: Post = {
  id: '0123456789abcdef0123456789abcdef',
  author: { id: user.id, username: user.username, nickname: user.nickname, avatar_path: '' },
  title: '测试内容',
  body: '安全正文',
  body_html: '<p>安全正文</p><script>alert(1)</script><a href="javascript:alert(1)" onclick="alert(1)">危险链接</a><img src="/api/media/0123456789abcdef0123456789abcdef/content" onerror="alert(1)" />',
  status: 'published', visibility: 'members', content_date: null, moderation_note: '',
  submitted_at: null, published_at: '2026-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
  tags: [], comment_count: 0, like_count: 0, liked_by_me: false, media: [], external_video_url: '',
}

beforeEach(() => {
  vi.spyOn(api, 'me').mockResolvedValue({ user })
  vi.spyOn(api, 'members').mockResolvedValue({ members: [] })
  vi.spyOn(api, 'posts').mockResolvedValue({ posts: [post] })
  vi.spyOn(api, 'mediaUsage').mockResolvedValue({ usage: { used_bytes: 0, reserved_bytes: 0, quota_bytes: 1024 } })
  vi.spyOn(api, 'mediaLimits').mockResolvedValue({ max_image_upload_bytes: 1024 ** 2, supported_image_mime_types: ['image/jpeg'] })
  vi.spyOn(api, 'notifications').mockResolvedValue({ notifications: [], unread_count: 0 })
  vi.spyOn(api, 'conversations').mockResolvedValue({ conversations: [] })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
})

describe('rich text rendering', () => {
  it('removes executable markup and unsafe attributes before v-html', async () => {
    wrapper = mount(App, { global: { stubs: { RichTextEditor: true } } })
    await flushPromises()

    const body = wrapper.get('.post-body')
    expect(body.find('script').exists()).toBe(false)
    expect(body.find('[onclick]').exists()).toBe(false)
    expect(body.find('[onerror]').exists()).toBe(false)
    expect(body.find('a').attributes('href')).toBeUndefined()
    expect(body.find('img').attributes('src')).toContain('/api/media/')
  })
})
