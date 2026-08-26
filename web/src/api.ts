import type { Comment, GuestbookEntry, GuestbookPage, Media, MediaUsage, Member, Post, PostPage, Session, User } from './types'

type ApiErrorBody = { error?: { message?: string } }

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

export const api = {
  me: () => request<{ user: User }>('/api/auth/me'),
  login: (body: { identifier: string; password: string }) =>
    request<{ user: User }>('/api/auth/login', { method: 'POST', body: JSON.stringify(body) }),
  register: (body: { invite_code: string; username: string; email: string; password: string; nickname: string }) =>
    request<{ user: User }>('/api/auth/register', { method: 'POST', body: JSON.stringify(body) }),
  logout: () => request<void>('/api/auth/logout', { method: 'POST', body: '{}' }),
  updateProfile: (body: { nickname: string; bio: string; bed_no: string; memorial_note: string }) =>
    request<{ user: User }>('/api/profile', { method: 'PATCH', body: JSON.stringify(body) }),
  sessions: () => request<{ sessions: Session[] }>('/api/auth/sessions'),
  members: () => request<{ members: Member[] }>('/api/members'),
  revokeSession: (id: string) => request<void>(`/api/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE', body: '{}' }),
  createInvites: (body: { max_uses: number; expires_in_hours: number; count: number }) =>
    request<{ invites: Array<{ code: string; expires_at: string; max_uses: number }>; count: number }>('/api/admin/invites', { method: 'POST', body: JSON.stringify(body) }),
  posts: (query: { scope?: 'feed' | 'mine' | 'pending'; status?: string; cursor?: string; limit?: number } = {}) => {
    const params = new URLSearchParams()
    Object.entries(query).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
    return request<PostPage>(`/api/posts${params.size ? `?${params}` : ''}`)
  },
  post: (id: string) => request<{ post: Post }>(`/api/posts/${encodeURIComponent(id)}`),
  createPost: (body: { body: string; content_date: string; visibility: 'members' | 'private'; tags: string[]; media_ids: string[]; submit: boolean }) =>
    request<{ post: Post }>('/api/posts', { method: 'POST', body: JSON.stringify(body) }),
  updatePost: (id: string, body: { body: string; content_date: string; visibility: 'members' | 'private'; tags: string[]; media_ids: string[]; submit: boolean }) =>
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
  createGuestbookEntry: (body: { recipient_id: string; body: string; media_ids: string[] }) => request<{ entry: GuestbookEntry }>('/api/guestbook', { method: 'POST', body: JSON.stringify(body) }),
  hideGuestbookEntry: (id: string) => request<void>(`/api/guestbook/${encodeURIComponent(id)}/hide`, { method: 'POST', body: '{}' }),
  restoreGuestbookEntry: (id: string) => request<void>(`/api/guestbook/${encodeURIComponent(id)}/restore`, { method: 'POST', body: '{}' }),
  deleteGuestbookEntry: (id: string) => request<void>(`/api/guestbook/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  setAvatar: (mediaID: string) => request<{ user: User }>('/api/profile/avatar', { method: 'POST', body: JSON.stringify({ media_id: mediaID }) }),
  clearAvatar: () => request<{ user: User }>('/api/profile/avatar', { method: 'DELETE' }),
  mediaUsage: () => request<{ usage: MediaUsage }>('/api/media/usage'),
  deleteMedia: (id: string) => request<void>(`/api/media/${encodeURIComponent(id)}`, { method: 'DELETE', body: '{}' }),
  uploadMedia: (file: File, uploadID: string, metadata: { width?: number; height?: number; duration_ms?: number }, onProgress: (percent: number) => void) => new Promise<{ media: Media; usage: MediaUsage }>((resolve, reject) => {
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
      const body = (() => { try { return JSON.parse(xhr.responseText) as ApiErrorBody & { media: Media; usage: MediaUsage } } catch { return {} as ApiErrorBody & { media: Media; usage: MediaUsage } } })()
      if (xhr.status >= 200 && xhr.status < 300) resolve(body)
      else reject(new ApiError(body.error?.message ?? `上传失败（${xhr.status}）`, xhr.status))
    })
    xhr.addEventListener('error', () => reject(new ApiError('网络中断，文件尚未上传完成', 0)))
    xhr.send(file)
  }),
}
