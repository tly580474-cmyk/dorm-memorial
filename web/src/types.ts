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

export type PostStatus = 'draft' | 'pending' | 'published' | 'hidden' | 'deleted'

export interface Media {
  id: string
  owner_id?: string
  original_filename: string
  media_type: 'image' | 'video'
  mime_type: string
  size_bytes: number
  sha256?: string
  status: 'uploading' | 'ready' | 'unavailable' | 'deleted'
  created_at?: string
  width?: number | null
  height?: number | null
  duration_ms?: number | null
  has_preview: boolean
}

export interface MediaUsage {
  used_bytes: number
  reserved_bytes: number
  quota_bytes: number
}

export interface Post {
  id: string
  author: Pick<User, 'id' | 'username' | 'nickname' | 'avatar_path'>
  body: string
  status: PostStatus
  visibility: 'members' | 'private'
  content_date: string | null
  moderation_note: string
  submitted_at: string | null
  published_at: string | null
  created_at: string
  updated_at: string
  tags: string[]
  comment_count: number
  like_count: number
  liked_by_me: boolean
  media: Media[]
}

export interface PostPage {
  posts: Post[]
  next_cursor?: string
}

export interface Comment {
  id: string
  post_id: string
  author: Pick<User, 'id' | 'username' | 'nickname' | 'avatar_path'>
  body: string
  created_at: string
}

export type Member = Pick<User, 'id' | 'username' | 'nickname' | 'avatar_path' | 'bio' | 'bed_no' | 'memorial_note'>

export interface GuestbookEntry {
  id: string
  author: Pick<User, 'id' | 'username' | 'nickname' | 'avatar_path'>
  recipient: Pick<User, 'id' | 'username' | 'nickname' | 'avatar_path'> | null
  body: string
  status: 'visible' | 'hidden' | 'deleted'
  created_at: string
  updated_at: string
  media: Media[]
}

export interface GuestbookPage {
  entries: GuestbookEntry[]
  next_cursor?: string
}
