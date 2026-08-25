import type { Session, User } from './types'

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
  revokeSession: (id: string) => request<void>(`/api/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE', body: '{}' }),
  createInvites: (body: { max_uses: number; expires_in_hours: number; count: number }) =>
    request<{ invites: Array<{ code: string; expires_at: string; max_uses: number }>; count: number }>('/api/admin/invites', { method: 'POST', body: JSON.stringify(body) }),
}
