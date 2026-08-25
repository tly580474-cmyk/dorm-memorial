export interface User {
  id: string
  username: string
  email: string
  role: 'admin' | 'member'
  status: 'active' | 'disabled'
  nickname: string
  bio: string
  bed_no: string
  memorial_note: string
  avatar_path: string
}

export interface Session {
  id: string
  user_agent: string
  ip_address: string
  created_at: string
  last_seen_at: string
  expires_at: string
  current: boolean
}
