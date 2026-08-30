import type { AdminMedia, AdminMessage, AdminUser, ChatMessage, Comment, Conversation, GuestbookEntry, GuestbookPage, Media, MediaUsage, Member, MessagePage, NotificationPage, Post, PostPage, Session, User } from './types'

type ApiErrorBody = { error?: { message?: string } }
export type MediaProcessingPhase = 'staged' | 'transcoding' | 'uploading' | 'verifying' | 'completed' | 'failed'
export type MediaProcessingStep = 'probing' | 'encoding' | 'finalizing' | ''
export type MediaProcessingJob = { id: string; media_id: string; phase: MediaProcessingPhase; step: MediaProcessingStep; encoder: string; error_code: string; media?: Media }

export class ApiError extends Error {
  constructor(message: string, public readonly status: number) {
    super(message)
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as ApiErrorBody
    throw new ApiError(body.error?.message ?? `请求失败（${response.status}）`, response.status)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

async function download(path: string, options: RequestInit = {}) {
  const response = await fetch(path, { ...options, credentials: 'same-origin' })
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as ApiErrorBody
    throw new ApiError(body.error?.message ?? `下载失败（${response.status}）`, response.status)
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  const filename = disposition.match(/filename="([^"]+)"/)?.[1] ?? 'dorm-memorial-backup.db'
  return { blob: await response.blob(), filename }
}

const processingErrorLabels: Record<string, string> = {
  staging_missing: '服务器暂存文件丢失，请重新上传',
  transcode_failed: '视频转码失败，请检查文件是否完整',
  transcode_output_invalid: '转码结果无效，请重新上传',
  original_upload_failed: '原文件保存到媒体存储失败',
  playback_upload_failed: '兼容播放版本保存失败',
  storage_verify_failed: '媒体存储校验失败',
  database_failed: '处理结果保存失败',
}

export async function waitForMediaProcessing(jobID: string, onPhase: (job: MediaProcessingJob) => void) {
  let consecutiveErrors = 0
  let pollDelay = 1500
  let includeUsage = false
  // The server's job deadline is two hours; allow a short margin without polling forever.
  const deadline = Date.now() + 130 * 60 * 1000
  while (Date.now() < deadline) {
    let response: { job: MediaProcessingJob; usage?: MediaUsage }
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 15000)
    try {
      response = await request<{ job: MediaProcessingJob; usage?: MediaUsage }>(`/api/media-upload-jobs/${encodeURIComponent(jobID)}${includeUsage ? '' : '?include_usage=0'}`, { signal: controller.signal })
      consecutiveErrors = 0
    } catch (error) {
      if (error instanceof ApiError && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429) throw error
      consecutiveErrors += 1
      if (consecutiveErrors >= 20) throw error
      window.clearTimeout(timeout)
      await new Promise((resolve) => window.setTimeout(resolve, pollDelay))
      pollDelay = Math.min(5000, Math.round(pollDelay * 1.5))
      continue
    } finally {
      window.clearTimeout(timeout)
    }
    onPhase(response.job)
    if (response.job.phase === 'completed') {
      if (!response.job.media) throw new ApiError('视频处理完成，但服务器未返回媒体记录，请刷新页面', 502)
      if (response.usage) return { media: response.job.media, usage: response.usage }
      if (includeUsage) throw new ApiError('视频处理完成，但无法获取存储用量，请刷新页面', 502)
      // Older servers may include usage on every response. New servers calculate it only once here.
      includeUsage = true
      continue
    }
    if (response.job.phase === 'failed') throw new ApiError(processingErrorLabels[response.job.error_code] ?? '视频处理失败，请重新上传', 500)
    await new Promise((resolve) => window.setTimeout(resolve, pollDelay))
    pollDelay = Math.min(5000, Math.round(pollDelay * 1.5))
  }
  throw new ApiError('视频处理等待超时，请刷新页面检查处理结果', 408)
}

export const api = {
  me: () => request<{ user: User }>('/api/auth/me'),
  login: (body: { identifier: string; password: string }) =>
    request<{ user: User }>('/api/auth/login', { method: 'POST', body: JSON.stringify(body) }),
  register: (body: { invite_code: string; username: string; email: string; password: string; nickname: string }) =>
    request<{ user: User }>('/api/auth/register', { method: 'POST', body: JSON.stringify(body) }),
  logout: () => request<void>('/api/auth/logout', { method: 'POST', body: '{}' }),
  deactivate: (password: string) => request<void>('/api/auth/deactivate', { method: 'POST', body: JSON.stringify({ password }) }),
  updateProfile: (body: { nickname: string; bio: string; bed_no: string; memorial_note: string }) =>
    request<{ user: User }>('/api/profile', { method: 'PATCH', body: JSON.stringify(body) }),
  updateAccount: (body: { username: string; email: string; nickname: string; current_password: string; new_password: string }) =>
    request<{ user: User }>('/api/account', { method: 'PATCH', body: JSON.stringify(body) }),
  sessions: () => request<{ sessions: Session[] }>('/api/auth/sessions'),
  members: () => request<{ members: Member[] }>('/api/members'),
  conversations: () => request<{ conversations: Conversation[] }>('/api/messages/conversations'),
  startDirectConversation: (recipientID: string) => request<{ conversation: Conversation }>('/api/messages/conversations/direct', { method: 'POST', body: JSON.stringify({ recipient_id: recipientID }) }),
  messages: (conversationID: string, query: { cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<MessagePage>(`/api/messages/conversations/${encodeURIComponent(conversationID)}${params.size ? `?${params}` : ''}`)
  },
  sendMessage: (conversationID: string, body: string, mediaIDs: string[] = []) => request<{ message: ChatMessage }>(`/api/messages/conversations/${encodeURIComponent(conversationID)}`, { method: 'POST', body: JSON.stringify({ body, media_ids: mediaIDs }) }),
  message: (id: string) => request<{ message: ChatMessage }>(`/api/messages/items/${encodeURIComponent(id)}`),
  markConversationRead: (conversationID: string) => request<void>(`/api/messages/conversations/${encodeURIComponent(conversationID)}/read`, { method: 'POST', body: '{}' }),
  recallMessage: (id: string) => request<void>(`/api/messages/items/${encodeURIComponent(id)}/recall`, { method: 'POST', body: '{}' }),
  notifications: (query: { cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<NotificationPage>(`/api/notifications${params.size ? `?${params}` : ''}`)
  },
  markNotificationRead: (id: string) => request<void>(`/api/notifications/${encodeURIComponent(id)}/read`, { method: 'POST', body: '{}' }),
  markAllNotificationsRead: () => request<void>('/api/notifications/read-all', { method: 'POST', body: '{}' }),
  clearNotifications: () => request<void>('/api/notifications', { method: 'DELETE', body: '{}' }),
  revokeSession: (id: string) => request<void>(`/api/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE', body: '{}' }),
  createInvites: (body: { max_uses: number; expires_in_hours: number; count: number }) =>
    request<{ invites: Array<{ code: string; expires_at: string; max_uses: number }>; count: number }>('/api/admin/invites', { method: 'POST', body: JSON.stringify(body) }),
  adminUsers: (query: { search?: string; role?: string; status?: string } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value) params.set(key, value) })
    return request<{ users: AdminUser[]; count: number }>(`/api/admin/users${params.size ? `?${params}` : ''}`)
  },
  updateAdminUser: (id: string, body: { role: 'admin' | 'member'; status: 'active' | 'disabled' | 'deactivated' }) =>
    request<{ user: AdminUser }>(`/api/admin/users/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  adminMessages: (query: { search?: string; status?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<{ messages: AdminMessage[]; count: number }>(`/api/admin/messages${params.size ? `?${params}` : ''}`)
  },
  removeAdminMessage: (id: string) => request<void>(`/api/admin/messages/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  adminMedia: (query: { search?: string; type?: string; status?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<{ media: AdminMedia[]; count: number }>(`/api/admin/media${params.size ? `?${params}` : ''}`)
  },
  purgeAdminMedia: (id: string, force = false) => request<void>(`/api/admin/media/${encodeURIComponent(id)}`, { method: 'DELETE', body: JSON.stringify({ force }) }),
  exportBackup: () => download('/api/admin/backup', { method: 'POST' }),
  posts: (query: { scope?: 'feed' | 'mine' | 'pending' | 'admin'; status?: string; cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<PostPage>(`/api/posts${params.size ? `?${params}` : ''}`)
  },
  post: (id: string) => request<{ post: Post }>(`/api/posts/${encodeURIComponent(id)}`),
  createPost: (body: { title: string; body: string; body_html: string; content_date: string; visibility: 'members' | 'private'; tags: string[]; media_ids: string[]; submit: boolean; external_video_url: string }) =>
    request<{ post: Post }>('/api/posts', { method: 'POST', body: JSON.stringify(body) }),
  updatePost: (id: string, body: { title: string; body: string; body_html: string; content_date: string; visibility: 'members' | 'private'; tags: string[]; media_ids: string[]; submit: boolean; external_video_url: string }) =>
    request<{ post: Post }>(`/api/posts/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  submitPost: (id: string) => request<{ post: Post }>(`/api/posts/${encodeURIComponent(id)}/submit`, { method: 'POST', body: '{}' }),
  moderatePost: (id: string, action: 'approve' | 'hide', note = '') =>
    request<{ post: Post }>(`/api/admin/posts/${encodeURIComponent(id)}/moderate`, { method: 'POST', body: JSON.stringify({ action, note }) }),
  deletePost: (id: string) => request<void>(`/api/posts/${encodeURIComponent(id)}`, { method: 'DELETE', body: '{}' }),
  comments: (postID: string) => request<{ comments: Comment[] }>(`/api/posts/${encodeURIComponent(postID)}/comments`),
  addComment: (postID: string, body: string) => request<{ comment: Comment }>(`/api/posts/${encodeURIComponent(postID)}/comments`, { method: 'POST', body: JSON.stringify({ body }) }),
  deleteComment: (id: string) => request<void>(`/api/comments/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  toggleLike: (postID: string) => request<{ liked: boolean; like_count: number }>(`/api/posts/${encodeURIComponent(postID)}/like`, { method: 'POST', body: '{}' }),
  guestbook: (query: { recipient_id?: string; status?: 'visible' | 'hidden'; cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<GuestbookPage>(`/api/guestbook${params.size ? `?${params}` : ''}`)
  },
  createGuestbookEntry: (body: { recipient_id: string; body: string; media_ids: string[]; external_video_url: string }) => request<{ entry: GuestbookEntry }>('/api/guestbook', { method: 'POST', body: JSON.stringify(body) }),
  hideGuestbookEntry: (id: string) => request<void>(`/api/guestbook/${encodeURIComponent(id)}/hide`, { method: 'POST', body: '{}' }),
  restoreGuestbookEntry: (id: string) => request<void>(`/api/guestbook/${encodeURIComponent(id)}/restore`, { method: 'POST', body: '{}' }),
  deleteGuestbookEntry: (id: string) => request<void>(`/api/guestbook/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  setAvatar: (mediaID: string) => request<{ user: User }>('/api/profile/avatar', { method: 'POST', body: JSON.stringify({ media_id: mediaID }) }),
  clearAvatar: () => request<{ user: User }>('/api/profile/avatar', { method: 'DELETE' }),
  mediaUsage: () => request<{ usage: MediaUsage }>('/api/media/usage'),
  deleteMedia: (id: string) => request<void>(`/api/media/${encodeURIComponent(id)}`, { method: 'DELETE', body: '{}' }),
  uploadMedia: (file: File, uploadID: string, metadata: { width?: number; height?: number; duration_ms?: number }, onProgress: (percent: number) => void, onProcessing: (job: MediaProcessingJob) => void = () => undefined) => new Promise<{ media: Media; usage: MediaUsage }>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/media/uploads')
    xhr.withCredentials = true
    xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream')
    xhr.setRequestHeader('X-File-Name', encodeURIComponent(file.name))
    xhr.setRequestHeader('X-Upload-ID', uploadID)
    if (metadata.width) xhr.setRequestHeader('X-Media-Width', String(metadata.width))
    if (metadata.height) xhr.setRequestHeader('X-Media-Height', String(metadata.height))
    if (metadata.duration_ms) xhr.setRequestHeader('X-Media-Duration-MS', String(metadata.duration_ms))
    xhr.upload.addEventListener('progress', (event) => {
      if (event.lengthComputable) onProgress(Math.round((event.loaded / event.total) * 100))
    })
    xhr.addEventListener('load', () => {
      const body = (() => { try { return JSON.parse(xhr.responseText) as ApiErrorBody & { media?: Media; usage: MediaUsage; job?: MediaProcessingJob } } catch { return {} as ApiErrorBody & { media?: Media; usage: MediaUsage; job?: MediaProcessingJob } } })()
      if (xhr.status === 202 && body.job) {
        onProcessing(body.job)
        void waitForMediaProcessing(body.job.id, onProcessing).then(resolve, reject)
      } else if (xhr.status >= 200 && xhr.status < 300 && body.media) resolve({ media: body.media, usage: body.usage })
      else reject(new ApiError(body.error?.message ?? `上传失败（${xhr.status}）`, xhr.status))
    })
    xhr.addEventListener('error', () => reject(new ApiError('网络中断，文件尚未上传完成', 0)))
    xhr.send(file)
  }),
}
