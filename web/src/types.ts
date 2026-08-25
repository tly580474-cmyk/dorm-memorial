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
}

export interface PostPage {
  posts: Post[]
  next_cursor?: string
}
