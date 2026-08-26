<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { AlertCircle, Bell, BookHeart, BookOpenText, CalendarDays, Camera, Check, Copy, Eye, EyeOff, FileEdit, Film, Heart, Home, Image, LogOut, Menu, MessageCircle, Plus, RotateCcw, Send, Settings, ShieldCheck, Sparkles, Trash2, UploadCloud, UserRound, X } from 'lucide-vue-next'
import { api, ApiError } from './api'
import type { Comment, GuestbookEntry, Media, MediaUsage, Member, Post, Session, User } from './types'

type EditorMedia = {
  key: string
  file?: File
  media?: Media
  name: string
  size: number
  kind: 'image' | 'video'
  status: 'pending' | 'uploading' | 'ready' | 'error'
  progress: number
  error: string
  persisted: boolean
  width?: number
  height?: number
  duration_ms?: number
  metadataPromise?: Promise<void>
}

const user = ref<User | null>(null)
const booting = ref(true)
const authMode = ref<'login' | 'register'>('login')
const authBusy = ref(false)
const authError = ref('')
const passwordVisible = ref(false)
const auth = reactive({ identifier: '', password: '', inviteCode: '', username: '', email: '', nickname: '' })
const profileOpen = ref(false)
const profileDialog = ref<HTMLElement | null>(null)
let profileTrigger: HTMLElement | null = null
const mobileMenuOpen = ref(false)
const profileBusy = ref(false)
const profileMessage = ref('')
const avatarInput = ref<HTMLInputElement | null>(null)
const avatarBusy = ref(false)
const avatarProgress = ref(0)
const brokenAvatarIDs = ref(new Set<string>())
const avatarCropFrame = ref<HTMLElement | null>(null)
const avatarCropFrameSize = ref(300)
const avatarCrop = reactive({ sourceURL: '', filename: '', imageWidth: 0, imageHeight: 0, zoom: 1, x: 50, y: 50, outputSize: 512 })
const avatarCropStyle = computed(() => {
  if (!avatarCrop.imageWidth || !avatarCrop.imageHeight) return {}
  const frame = avatarCropFrameSize.value
  const baseScale = Math.max(frame / avatarCrop.imageWidth, frame / avatarCrop.imageHeight)
  const width = avatarCrop.imageWidth * baseScale * avatarCrop.zoom
  const height = avatarCrop.imageHeight * baseScale * avatarCrop.zoom
  return {
    width: `${width}px`,
    height: `${height}px`,
    left: `${-(width - frame) * avatarCrop.x / 100}px`,
    top: `${-(height - frame) * avatarCrop.y / 100}px`,
  }
})
const sessions = ref<Session[]>([])
const inviteCodes = ref<string[]>([])
const inviteCount = ref(5)
const inviteCountOptions = Array.from({ length: 20 }, (_, index) => index + 1)
const inviteCopyStatus = ref<'idle' | 'copied' | 'error'>('idle')
const inviteBusy = ref(false)
const profile = reactive({ nickname: '', bio: '', bed_no: '', memorial_note: '' })
const feedPosts = ref<Post[]>([])
const myPosts = ref<Post[]>([])
const pendingPosts = ref<Post[]>([])
const contentLoading = ref(false)
const contentError = ref('')
const composerOpen = ref(false)
const composerDialog = ref<HTMLElement | null>(null)
let composerTrigger: HTMLElement | null = null
const composerBusy = ref(false)
const composerError = ref('')
const editingPostID = ref('')
const editor = reactive({ body: '', content_date: '', visibility: 'members' as 'members' | 'private', tags: '' })
const editorMedia = ref<EditorMedia[]>([])
const removedMediaIDs = ref<string[]>([])
const mediaInput = ref<HTMLInputElement | null>(null)
const mediaUsage = ref<MediaUsage | null>(null)
const mediaSelectionError = ref('')
const mediaLoadErrors = ref(new Set<string>())
const mediaUploading = computed(() => editorMedia.value.some((item) => item.status === 'uploading'))
const detailOpen = ref(false)
const detailDialog = ref<HTMLElement | null>(null)
let detailTrigger: HTMLElement | null = null
const detailPost = ref<Post | null>(null)
const detailComments = ref<Comment[]>([])
const detailLoading = ref(false)
const detailError = ref('')
const commentBody = ref('')
const commentBusy = ref(false)
const guestbookEntries = ref<GuestbookEntry[]>([])
const guestbookMembers = ref<Member[]>([])
const guestbookRecipientID = ref('')
const guestbookBody = ref('')
const guestbookMedia = ref<EditorMedia[]>([])
const guestbookMediaInput = ref<HTMLInputElement | null>(null)
const guestbookLoading = ref(false)
const guestbookBusy = ref(false)
const guestbookError = ref('')
const guestbookNextCursor = ref('')
let guestbookRequest = 0
const guestbookMediaUploading = computed(() => guestbookMedia.value.some((item) => item.status === 'uploading'))
const selectedGuestbookMember = computed(() => guestbookMembers.value.find((member) => member.id === guestbookRecipientID.value) ?? null)
const activeView = ref<'home' | 'timeline' | 'wall' | 'guestbook'>('home')
const wallItems = computed(() => feedPosts.value.flatMap((post) => post.media.map((media) => ({ post, media }))))
const timelineGroups = computed(() => {
  const sorted = [...feedPosts.value].sort((left, right) => timelineDate(right).getTime() - timelineDate(left).getTime())
  const groups = new Map<string, { label: string; posts: Post[] }>()
  for (const post of sorted) {
    const date = timelineDate(post)
    const key = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
    const group = groups.get(key) ?? { label: date.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long' }), posts: [] }
    group.posts.push(post)
    groups.set(key, group)
  }
  return [...groups.entries()].map(([key, group]) => ({ key, ...group }))
})

const greeting = computed(() => {
  const hour = new Date().getHours()
  return hour < 11 ? '早上好' : hour < 18 ? '下午好' : '晚上好'
})

const nav = [
  { id: 'home' as const, label: '首页', icon: Home, available: true },
  { id: 'timeline' as const, label: '时间线', icon: Sparkles, available: true },
  { id: 'wall' as const, label: '照片墙', icon: Camera, available: true },
  { id: 'guestbook' as const, label: '留言册', icon: BookHeart, available: true },
  { label: '论坛', icon: BookOpenText, available: false },
  { label: '消息', icon: MessageCircle, available: false },
]

function setView(view: 'home' | 'timeline' | 'wall' | 'guestbook') {
  activeView.value = view
  mobileMenuOpen.value = false
  if (view === 'guestbook') void loadGuestbook(true)
  nextTick(() => document.querySelector<HTMLElement>('#main-content h1')?.focus())
}

function timelineDate(post: Post) {
  return new Date(post.content_date || post.published_at || post.created_at)
}

onMounted(async () => {
  try {
    applyUser((await api.me()).user)
    await loadContent()
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 401) console.error(error)
  } finally {
    booting.value = false
  }
})

function applyUser(next: User) {
  user.value = next
  profile.nickname = next.nickname
  profile.bio = next.bio
  profile.bed_no = next.bed_no
  profile.memorial_note = next.memorial_note
}

async function submitAuth() {
  authBusy.value = true
  authError.value = ''
  try {
    const response = authMode.value === 'login'
      ? await api.login({ identifier: auth.identifier, password: auth.password })
      : await api.register({ invite_code: auth.inviteCode, username: auth.username, email: auth.email, password: auth.password, nickname: auth.nickname })
    applyUser(response.user)
    await loadContent()
    auth.password = ''
  } catch (error) {
    authError.value = error instanceof Error ? error.message : '暂时无法完成请求'
  } finally {
    authBusy.value = false
  }
}

async function logout() {
  await api.logout()
  user.value = null
  feedPosts.value = []
  myPosts.value = []
  pendingPosts.value = []
  profileOpen.value = false
}

async function openProfile() {
	profileTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  profileOpen.value = true
  profileMessage.value = ''
  try {
    sessions.value = (await api.sessions()).sessions
  } catch (error) {
    profileMessage.value = error instanceof Error ? error.message : '无法读取会话'
  }
  await nextTick()
  profileDialog.value?.querySelector<HTMLElement>('input, button, textarea')?.focus()
}

function closeProfile() {
  resetAvatarCrop()
  profileOpen.value = false
  nextTick(() => profileTrigger?.focus())
}

function trapProfileFocus(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeProfile()
    return
  }
  if (event.key !== 'Tab' || !profileDialog.value) return
  const items = [...profileDialog.value.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), [href]')]
  if (items.length === 0) return
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first?.focus()
  }
}

async function saveProfile() {
  profileBusy.value = true
  profileMessage.value = ''
  try {
    applyUser((await api.updateProfile(profile)).user)
    profileMessage.value = '资料已保存'
  } catch (error) {
    profileMessage.value = error instanceof Error ? error.message : '保存失败'
  } finally {
    profileBusy.value = false
  }
}

function avatarURL(value: string) {
  return value ? mediaContentURL(value, true) : ''
}

function avatarVisible(value: string) {
  return Boolean(value) && !brokenAvatarIDs.value.has(value)
}

function markAvatarBroken(value: string) {
  brokenAvatarIDs.value = new Set([...brokenAvatarIDs.value, value])
}

function resetAvatarCrop() {
  if (avatarCrop.sourceURL) URL.revokeObjectURL(avatarCrop.sourceURL)
  Object.assign(avatarCrop, { sourceURL: '', filename: '', imageWidth: 0, imageHeight: 0, zoom: 1, x: 50, y: 50, outputSize: 512 })
}

async function handleAvatarInput(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !user.value) return
  if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type) || file.size <= 0 || file.size > 25 * 1024 * 1024) {
    profileMessage.value = '头像请选择 25 MiB 以内的 JPEG、PNG 或 WebP 图片。'
    return
  }
  resetAvatarCrop()
  const sourceURL = URL.createObjectURL(file)
  const image = new window.Image()
  image.onload = () => {
    avatarCrop.sourceURL = sourceURL
    avatarCrop.filename = file.name
    avatarCrop.imageWidth = image.naturalWidth
    avatarCrop.imageHeight = image.naturalHeight
    profileMessage.value = file.size > 2 * 1024 * 1024 ? '原图大于 2 MiB，确认裁剪后会压缩上传。' : ''
    nextTick(() => { avatarCropFrameSize.value = avatarCropFrame.value?.clientWidth || 300 })
  }
  image.onerror = () => {
    URL.revokeObjectURL(sourceURL)
    profileMessage.value = '无法读取这张图片，请换一张重试。'
  }
  image.src = sourceURL
}

function canvasBlob(canvas: HTMLCanvasElement, quality: number) {
  return new Promise<Blob>((resolve, reject) => canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error('无法生成头像图片')), 'image/jpeg', quality))
}

async function buildCroppedAvatar() {
  const image = new window.Image()
  image.src = avatarCrop.sourceURL
  await image.decode()
  const visibleSize = Math.min(avatarCrop.imageWidth, avatarCrop.imageHeight) / avatarCrop.zoom
  const sourceX = (avatarCrop.imageWidth - visibleSize) * avatarCrop.x / 100
  const sourceY = (avatarCrop.imageHeight - visibleSize) * avatarCrop.y / 100
  const canvas = document.createElement('canvas')
  canvas.width = avatarCrop.outputSize
  canvas.height = avatarCrop.outputSize
  const context = canvas.getContext('2d')
  if (!context) throw new Error('当前浏览器无法处理图片')
  context.fillStyle = '#fff'
  context.fillRect(0, 0, canvas.width, canvas.height)
  context.drawImage(image, sourceX, sourceY, visibleSize, visibleSize, 0, 0, canvas.width, canvas.height)
  let quality = .9
  let blob = await canvasBlob(canvas, quality)
  while (blob.size > 2 * 1024 * 1024 && quality > .55) {
    quality -= .08
    blob = await canvasBlob(canvas, quality)
  }
  if (blob.size > 2 * 1024 * 1024) throw new Error('压缩后仍超过 2 MiB，请选择较小的输出尺寸')
  const baseName = avatarCrop.filename.replace(/\.[^.]+$/, '') || 'avatar'
  return new File([blob], `${baseName}-${avatarCrop.outputSize}.jpg`, { type: 'image/jpeg' })
}

async function uploadCroppedAvatar() {
  if (!avatarCrop.sourceURL || !user.value || avatarBusy.value) return
  avatarBusy.value = true
  avatarProgress.value = 0
  profileMessage.value = ''
  let uploadedID = ''
  try {
    const file = await buildCroppedAvatar()
    const uploaded = await api.uploadMedia(file, crypto.randomUUID(), { width: avatarCrop.outputSize, height: avatarCrop.outputSize }, (progress) => { avatarProgress.value = progress })
    uploadedID = uploaded.media.id
    applyUser((await api.setAvatar(uploaded.media.id)).user)
    brokenAvatarIDs.value = new Set([...brokenAvatarIDs.value].filter((id) => id !== uploaded.media.id))
    resetAvatarCrop()
    profileMessage.value = '头像已更新'
  } catch (error) {
    if (uploadedID) await api.deleteMedia(uploadedID).catch(() => undefined)
    profileMessage.value = error instanceof Error ? error.message : '头像上传失败'
  } finally {
    avatarBusy.value = false
  }
}

async function clearAvatar() {
  if (!user.value?.avatar_path || avatarBusy.value) return
  avatarBusy.value = true
  profileMessage.value = ''
  try {
    applyUser((await api.clearAvatar()).user)
    profileMessage.value = '头像已移除'
  } catch (error) {
    profileMessage.value = error instanceof Error ? error.message : '无法移除头像'
  } finally {
    avatarBusy.value = false
  }
}

async function openDetail(post: Post) {
  detailTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  detailPost.value = post
  detailComments.value = []
  detailError.value = ''
  commentBody.value = ''
  detailOpen.value = true
  detailLoading.value = true
  await nextTick()
  detailDialog.value?.querySelector<HTMLElement>('button, textarea, [href]')?.focus()
  try {
    const [fresh, comments] = await Promise.all([api.post(post.id), api.comments(post.id)])
    detailPost.value = fresh.post
    detailComments.value = comments.comments
  } catch (error) {
    detailError.value = error instanceof Error ? error.message : '无法读取详情'
  } finally {
    detailLoading.value = false
  }
}

function closeDetail() {
  detailOpen.value = false
  nextTick(() => detailTrigger?.focus())
}

function trapDetailFocus(event: KeyboardEvent) {
  if (event.key === 'Escape') { event.preventDefault(); closeDetail(); return }
  if (event.key !== 'Tab' || !detailDialog.value) return
  const items = [...detailDialog.value.querySelectorAll<HTMLElement>('button:not(:disabled), textarea:not(:disabled), [href]')]
  if (!items.length) return
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}

function replacePost(next: Post) {
  feedPosts.value = feedPosts.value.map((post) => post.id === next.id ? next : post)
  myPosts.value = myPosts.value.map((post) => post.id === next.id ? next : post)
  if (detailPost.value?.id === next.id) detailPost.value = next
}

async function togglePostLike(post: Post) {
  try {
    const response = await api.toggleLike(post.id)
    replacePost({ ...post, liked_by_me: response.liked, like_count: response.like_count })
  } catch (error) {
    detailError.value = error instanceof Error ? error.message : '点赞失败'
  }
}

async function submitComment() {
  if (!detailPost.value || !commentBody.value.trim()) return
  commentBusy.value = true
  detailError.value = ''
  try {
    const response = await api.addComment(detailPost.value.id, commentBody.value)
    detailComments.value.push(response.comment)
    replacePost({ ...detailPost.value, comment_count: detailPost.value.comment_count + 1 })
    commentBody.value = ''
  } catch (error) {
    detailError.value = error instanceof Error ? error.message : '评论失败'
  } finally {
    commentBusy.value = false
  }
}

async function removeComment(comment: Comment) {
  try {
    await api.deleteComment(comment.id)
    detailComments.value = detailComments.value.filter((item) => item.id !== comment.id)
    if (detailPost.value) replacePost({ ...detailPost.value, comment_count: Math.max(0, detailPost.value.comment_count - 1) })
  } catch (error) {
    detailError.value = error instanceof Error ? error.message : '删除评论失败'
  }
}

async function loadGuestbook(reset = false) {
  if (!user.value) return
  const requestID = ++guestbookRequest
  guestbookLoading.value = true
  guestbookError.value = ''
  const recipientID = guestbookRecipientID.value
  try {
    if (guestbookMembers.value.length === 0) guestbookMembers.value = (await api.members()).members
    const response = await api.guestbook({ recipient_id: recipientID || undefined, cursor: reset ? undefined : guestbookNextCursor.value || undefined, limit: 20 })
    if (requestID !== guestbookRequest || recipientID !== guestbookRecipientID.value) return
    guestbookEntries.value = reset ? response.entries : [...guestbookEntries.value, ...response.entries]
    guestbookNextCursor.value = response.next_cursor ?? ''
  } catch (error) {
    if (requestID === guestbookRequest) guestbookError.value = error instanceof Error ? error.message : '暂时无法读取留言'
  } finally {
    if (requestID === guestbookRequest) guestbookLoading.value = false
  }
}

function selectGuestbookRecipient(id: string) {
  if (guestbookRecipientID.value === id) return
  guestbookRecipientID.value = id
  guestbookEntries.value = []
  guestbookNextCursor.value = ''
  void loadGuestbook(true)
}

function handleGuestbookMediaInput(event: Event) {
  const input = event.target as HTMLInputElement
  addGuestbookMediaFiles(input.files ? [...input.files] : [])
  input.value = ''
}

function addGuestbookMediaFiles(files: File[]) {
  guestbookError.value = ''
  const available = 6 - guestbookMedia.value.length
  if (files.length > available) guestbookError.value = `每条留言最多附带 6 个文件，本次只加入前 ${Math.max(available, 0)} 个。`
  for (const file of files.slice(0, Math.max(available, 0))) {
    const kind = file.type.startsWith('image/') ? 'image' : file.type.startsWith('video/') ? 'video' : null
    if (!kind || file.size <= 0 || file.size > 8 * 1024 ** 3) {
      guestbookError.value = `${file.name} 不是有效的图片或视频，或文件超过 8 GiB。`
      continue
    }
    const item: EditorMedia = { key: crypto.randomUUID(), file, name: file.name, size: file.size, kind, status: 'pending', progress: 0, error: '', persisted: false }
    guestbookMedia.value.push(item)
    if (kind === 'video') item.metadataPromise = readVideoMetadata(item)
  }
}

async function removeGuestbookMedia(item: EditorMedia) {
  if (item.status === 'uploading') return
  guestbookMedia.value = guestbookMedia.value.filter((candidate) => candidate.key !== item.key)
  if (item.media) await api.deleteMedia(item.media.id).catch(() => undefined)
}

async function submitGuestbookEntry() {
  if ((!guestbookBody.value.trim() && guestbookMedia.value.length === 0) || guestbookBusy.value) return
  guestbookBusy.value = true
  guestbookError.value = ''
  try {
    for (const item of guestbookMedia.value) {
      if (item.status === 'pending' || item.status === 'error') await uploadEditorMedia(item)
    }
    const response = await api.createGuestbookEntry({
      recipient_id: guestbookRecipientID.value,
      body: guestbookBody.value,
      media_ids: guestbookMedia.value.flatMap((item) => item.media ? [item.media.id] : []),
    })
    guestbookEntries.value.unshift(response.entry)
    guestbookBody.value = ''
    guestbookMedia.value = []
  } catch (error) {
    guestbookError.value = error instanceof Error ? error.message : '留言发布失败'
  } finally {
    guestbookBusy.value = false
  }
}

async function hideGuestbookEntry(entry: GuestbookEntry) {
  try {
    await api.hideGuestbookEntry(entry.id)
    guestbookEntries.value = guestbookEntries.value.filter((item) => item.id !== entry.id)
  } catch (error) {
    guestbookError.value = error instanceof Error ? error.message : '隐藏留言失败'
  }
}

async function deleteGuestbookEntry(entry: GuestbookEntry) {
  try {
    await api.deleteGuestbookEntry(entry.id)
    guestbookEntries.value = guestbookEntries.value.filter((item) => item.id !== entry.id)
  } catch (error) {
    guestbookError.value = error instanceof Error ? error.message : '删除留言失败'
  }
}

async function revoke(session: Session) {
  await api.revokeSession(session.id)
  if (session.current) {
    user.value = null
    profileOpen.value = false
  } else {
    sessions.value = sessions.value.filter((item) => item.id !== session.id)
  }
}

async function createInvite() {
  inviteBusy.value = true
  inviteCopyStatus.value = 'idle'
  try {
    const response = await api.createInvites({ max_uses: 1, expires_in_hours: 168, count: inviteCount.value })
    inviteCodes.value = response.invites.map((item) => item.code)
  } finally {
    inviteBusy.value = false
  }
}

async function loadContent() {
  if (!user.value) return
  contentLoading.value = true
  contentError.value = ''
  try {
    const requests: [Promise<{ posts: Post[] }>, Promise<{ posts: Post[] }>, Promise<{ posts: Post[] }> | null] = [
      api.posts({ scope: 'feed', limit: 20 }),
      api.posts({ scope: 'mine', limit: 20 }),
      user.value.role === 'admin' ? api.posts({ scope: 'pending', limit: 20 }) : null,
    ]
    const [feed, mine, pending] = await Promise.all([requests[0], requests[1], requests[2] ?? Promise.resolve({ posts: [] })])
    feedPosts.value = feed.posts
    myPosts.value = mine.posts
    pendingPosts.value = pending.posts
    mediaUsage.value = (await api.mediaUsage()).usage
  } catch (error) {
    contentError.value = error instanceof Error ? error.message : '暂时无法读取纪念内容'
  } finally {
    contentLoading.value = false
  }
}

async function openComposer(post?: Post) {
  composerTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  editingPostID.value = post?.id ?? ''
  editor.body = post?.body ?? ''
  editor.content_date = post?.content_date?.slice(0, 10) ?? ''
  editor.visibility = post?.visibility ?? 'members'
  editor.tags = post?.tags.join('、') ?? ''
  editorMedia.value = (post?.media ?? []).map((item) => ({ key: item.id, media: item, name: item.original_filename, size: item.size_bytes, kind: item.media_type, status: 'ready', progress: 100, error: '', persisted: true, width: item.width ?? undefined, height: item.height ?? undefined, duration_ms: item.duration_ms ?? undefined }))
  removedMediaIDs.value = []
  mediaSelectionError.value = ''
  composerError.value = ''
  composerOpen.value = true
  await nextTick()
  composerDialog.value?.querySelector<HTMLElement>('textarea, input, button')?.focus()
}

function closeComposer() {
  if (composerBusy.value) return
  composerOpen.value = false
  nextTick(() => composerTrigger?.focus())
}

function trapComposerFocus(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeComposer()
    return
  }
  if (event.key !== 'Tab' || !composerDialog.value) return
  const items = [...composerDialog.value.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled)')]
  if (items.length === 0) return
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first?.focus()
  }
}

async function savePost(submit: boolean) {
  composerBusy.value = true
  composerError.value = ''
  try {
    for (const item of editorMedia.value) {
      if (item.status === 'pending' || item.status === 'error') await uploadEditorMedia(item)
    }
    const body = {
      body: editor.body,
      content_date: editor.content_date,
      visibility: editor.visibility,
      tags: editor.tags.split(/[、,，]/).map((tag) => tag.trim()).filter(Boolean),
      media_ids: editorMedia.value.flatMap((item) => item.media ? [item.media.id] : []),
      submit,
    }
    if (editingPostID.value) await api.updatePost(editingPostID.value, body)
    else await api.createPost(body)
    await Promise.allSettled(removedMediaIDs.value.map((id) => api.deleteMedia(id)))
    composerOpen.value = false
    await loadContent()
  } catch (error) {
    composerError.value = error instanceof Error ? error.message : '保存失败'
  } finally {
    composerBusy.value = false
  }
}

function chooseMedia() {
  mediaInput.value?.click()
}

function handleMediaInput(event: Event) {
  const input = event.target as HTMLInputElement
  addMediaFiles(input.files ? [...input.files] : [])
  input.value = ''
}

function handleMediaDrop(event: DragEvent) {
  addMediaFiles(event.dataTransfer?.files ? [...event.dataTransfer.files] : [])
}

function addMediaFiles(files: File[]) {
  mediaSelectionError.value = ''
  const available = 20 - editorMedia.value.length
  if (files.length > available) mediaSelectionError.value = `每篇回忆最多添加 20 个文件，本次只加入前 ${Math.max(available, 0)} 个。`
  for (const file of files.slice(0, Math.max(available, 0))) {
    const kind = file.type.startsWith('image/') ? 'image' : file.type.startsWith('video/') ? 'video' : null
    if (!kind) {
      mediaSelectionError.value = `${file.name} 不是支持的图片或视频格式。`
      continue
    }
    if (file.size <= 0 || file.size > 8 * 1024 ** 3) {
      mediaSelectionError.value = `${file.name} 为空或超过 8 GiB。`
      continue
    }
    const item: EditorMedia = { key: crypto.randomUUID(), file, name: file.name, size: file.size, kind, status: 'pending', progress: 0, error: '', persisted: false }
    editorMedia.value.push(item)
    if (kind === 'video') item.metadataPromise = readVideoMetadata(item)
  }
}

async function uploadEditorMedia(item: EditorMedia) {
  if (!item.file) throw new Error(`${item.name} 无法重新读取，请移除后再次选择。`)
  await item.metadataPromise
  item.status = 'uploading'
  item.error = ''
  item.progress = 0
  try {
    const response = await api.uploadMedia(item.file, crypto.randomUUID(), { width: item.width, height: item.height, duration_ms: item.duration_ms }, (percent) => { item.progress = percent })
    item.media = response.media
    item.status = 'ready'
    item.progress = 100
    mediaUsage.value = response.usage
  } catch (error) {
    item.status = 'error'
    item.error = error instanceof Error ? error.message : '上传失败'
    throw error
  }
}

async function readVideoMetadata(item: EditorMedia) {
  if (!item.file) return
  const objectURL = URL.createObjectURL(item.file)
  try {
    await new Promise<void>((resolve) => {
      const video = document.createElement('video')
      const finish = () => resolve()
      const timeout = window.setTimeout(finish, 5000)
      video.preload = 'metadata'
      video.onloadedmetadata = () => {
        window.clearTimeout(timeout)
        item.width = video.videoWidth
        item.height = video.videoHeight
        if (Number.isFinite(video.duration)) item.duration_ms = Math.round(video.duration * 1000)
        finish()
      }
      video.onerror = () => { window.clearTimeout(timeout); finish() }
      video.src = objectURL
    })
  } finally {
    URL.revokeObjectURL(objectURL)
  }
}

async function removeEditorMedia(item: EditorMedia) {
  if (item.status === 'uploading') return
  editorMedia.value = editorMedia.value.filter((candidate) => candidate.key !== item.key)
  if (!item.media) return
  if (item.persisted) {
    removedMediaIDs.value.push(item.media.id)
    return
  }
  try {
    await api.deleteMedia(item.media.id)
    mediaUsage.value = (await api.mediaUsage()).usage
  } catch (error) {
    mediaSelectionError.value = error instanceof Error ? error.message : '已从本次投稿移除，但远端清理失败'
  }
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let size = value / 1024
  let unit = units[0]
  for (let index = 1; size >= 1024 && index < units.length; index++) {
    size /= 1024
    unit = units[index]
  }
  return `${size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${unit}`
}

function usagePercent() {
  if (!mediaUsage.value?.quota_bytes) return 0
  return Math.min(100, Math.round(((mediaUsage.value.used_bytes + mediaUsage.value.reserved_bytes) / mediaUsage.value.quota_bytes) * 100))
}

function retryEditorMedia(item: EditorMedia) {
  item.status = 'pending'
  item.error = ''
  item.progress = 0
}

function mediaContentURL(id: string, preview = false) {
  return `/api/media/${encodeURIComponent(id)}/content${preview ? '?variant=preview' : ''}`
}

function markMediaLoadError(id: string) {
  mediaLoadErrors.value.add(id)
}

function retryMediaLoad(id: string) {
  mediaLoadErrors.value.delete(id)
}

async function moderate(post: Post, action: 'approve' | 'hide') {
  try {
    await api.moderatePost(post.id, action)
    await loadContent()
  } catch (error) {
    contentError.value = error instanceof Error ? error.message : '审核失败'
  }
}

function displayDate(value: string | null | undefined) {
  if (!value) return ''
  return new Date(value).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
}

function statusLabel(status: Post['status']) {
  return { draft: '草稿', pending: '待审核', published: '已发布', hidden: '已隐藏', deleted: '已删除' }[status]
}

async function copyInvites() {
  if (inviteCodes.value.length === 0) return
  try {
    await navigator.clipboard.writeText(inviteCodes.value.join('\n'))
    inviteCopyStatus.value = 'copied'
    window.setTimeout(() => { inviteCopyStatus.value = 'idle' }, 2000)
  } catch {
    inviteCopyStatus.value = 'error'
  }
}
</script>

<template>
  <div v-if="booting" class="boot-screen" role="status" aria-live="polite">
    <span class="loader" aria-hidden="true"></span><span>正在打开妙妙小屋…</span>
  </div>

  <main v-else-if="!user" class="auth-page">
    <section class="auth-story" aria-labelledby="welcome-title">
      <div class="brand-mark"><BookHeart :size="26" aria-hidden="true" /><span>妙妙小屋</span></div>
      <div class="story-copy">
        <p class="eyebrow">我们的宿舍纪念册</p>
        <h1 id="welcome-title">把一起生活的日子，慢慢收藏起来。</h1>
        <p>只属于室友的照片、故事和留言。没有公开广场，也没有陌生人的打扰。</p>
      </div>
      <p class="story-footnote">仅受邀请的成员可以加入</p>
    </section>

    <section class="auth-panel" aria-labelledby="auth-title">
      <div class="mobile-brand"><BookHeart :size="24" aria-hidden="true" /> 妙妙小屋</div>
      <div class="auth-card">
        <p class="eyebrow">{{ authMode === 'login' ? '欢迎回来' : '加入我们的回忆' }}</p>
        <h2 id="auth-title">{{ authMode === 'login' ? '登录纪念册' : '使用邀请码注册' }}</h2>
        <p class="muted">{{ authMode === 'login' ? '继续看看室友们最近分享了什么。' : '邀请码由宿舍管理员创建，每个邀请码都有有效期。' }}</p>
        <form @submit.prevent="submitAuth" novalidate>
          <div v-if="authMode === 'register'" class="field"><label for="invite-code">邀请码</label><input id="invite-code" v-model.trim="auth.inviteCode" autocomplete="one-time-code" required placeholder="例如 ABCD1234" /></div>
          <div v-if="authMode === 'register'" class="field-grid">
            <div class="field"><label for="username">用户名</label><input id="username" v-model.trim="auth.username" autocomplete="username" minlength="3" maxlength="24" required placeholder="字母、数字或下划线" /></div>
            <div class="field"><label for="nickname">昵称</label><input id="nickname" v-model.trim="auth.nickname" autocomplete="nickname" maxlength="40" required placeholder="室友熟悉的名字" /></div>
          </div>
          <div v-if="authMode === 'register'" class="field"><label for="email">邮箱</label><input id="email" v-model.trim="auth.email" type="email" autocomplete="email" required placeholder="用于识别账号" /></div>
          <div v-else class="field"><label for="identifier">用户名或邮箱</label><input id="identifier" v-model.trim="auth.identifier" autocomplete="username" required autofocus placeholder="输入用户名或邮箱" /></div>
          <div class="field"><label for="password">密码</label><div class="password-input"><input id="password" v-model="auth.password" :type="passwordVisible ? 'text' : 'password'" :autocomplete="authMode === 'login' ? 'current-password' : 'new-password'" minlength="10" required placeholder="至少 10 个字符" /><button type="button" :aria-label="passwordVisible ? '隐藏密码' : '显示密码'" :title="passwordVisible ? '隐藏密码' : '显示密码'" @click="passwordVisible = !passwordVisible"><EyeOff v-if="passwordVisible" :size="20" aria-hidden="true" /><Eye v-else :size="20" aria-hidden="true" /></button></div></div>
          <p v-if="authError" class="form-error" role="alert">{{ authError }}</p>
          <button class="primary-button" type="submit" :disabled="authBusy"><span v-if="authBusy" class="button-loader" aria-hidden="true"></span>{{ authBusy ? '请稍候…' : authMode === 'login' ? '登录' : '完成注册' }}</button>
        </form>
        <button class="text-button" type="button" @click="authMode = authMode === 'login' ? 'register' : 'login'; authError = ''">{{ authMode === 'login' ? '第一次来？使用邀请码注册' : '已经注册？返回登录' }}</button>
      </div>
    </section>
  </main>

  <div v-else class="app-shell">
    <a class="skip-link" href="#main-content">跳到主要内容</a>
    <header class="topbar">
      <button class="icon-button mobile-only" type="button" aria-label="打开菜单" @click="mobileMenuOpen = true"><Menu /></button>
      <a class="brand" href="#"><BookHeart :size="25" aria-hidden="true" /><span>妙妙小屋</span></a>
      <div class="top-actions"><button class="icon-button" type="button" aria-label="查看通知"><Bell /></button><button class="avatar-button" type="button" aria-label="打开个人设置" @click="openProfile"><img v-if="avatarVisible(user.avatar_path)" :src="avatarURL(user.avatar_path)" alt="" @error="markAvatarBroken(user.avatar_path)" /><span v-else>{{ user.nickname.slice(0, 1) }}</span></button></div>
    </header>

    <aside class="sidebar" :class="{ open: mobileMenuOpen }" aria-label="主要导航">
      <div class="mobile-menu-head"><span>浏览纪念册</span><button class="icon-button" type="button" aria-label="关闭菜单" @click="mobileMenuOpen = false"><X /></button></div>
      <nav><button v-for="item in nav" :key="item.label" type="button" :class="{ active: item.id === activeView }" :disabled="!item.available" :aria-current="item.id === activeView ? 'page' : undefined" :title="item.available ? item.label : `${item.label}正在开发`" @click="item.id && setView(item.id)"><component :is="item.icon" :size="20" aria-hidden="true" /><span>{{ item.label }}</span><small v-if="!item.available">开发中</small></button></nav>
      <div class="sidebar-bottom"><button v-if="user.role === 'admin'" type="button"><ShieldCheck :size="20" />管理</button><button type="button" @click="openProfile"><Settings :size="20" />设置</button></div>
    </aside>
    <button v-if="mobileMenuOpen" class="scrim" type="button" aria-label="关闭菜单" @click="mobileMenuOpen = false"></button>

    <main id="main-content" class="main-content">
      <template v-if="activeView === 'home'">
      <header class="page-heading"><div><p class="eyebrow">{{ greeting }}，{{ user.nickname }}</p><h1 tabindex="-1">最近的回忆</h1><p>记录一句话，也可以先存进草稿箱慢慢写。</p></div><button class="primary-button compact" type="button" @click="openComposer()"><Plus :size="19" />分享回忆</button></header>

      <p v-if="contentError" class="form-error content-alert" role="alert">{{ contentError }}</p>
      <section class="content-columns">
        <div class="feed-column">
          <button class="quick-composer" type="button" @click="openComposer()"><span class="mini-avatar"><img v-if="avatarVisible(user.avatar_path)" :src="avatarURL(user.avatar_path)" alt="" @error="markAvatarBroken(user.avatar_path)" /><span v-else>{{ user.nickname.slice(0, 1) }}</span></span><span>想记录今天的什么？</span><FileEdit :size="20" aria-hidden="true" /></button>
          <div v-if="contentLoading" class="content-empty" role="status"><span class="loader"></span><span>正在读取回忆…</span></div>
          <div v-else-if="feedPosts.length === 0" class="content-empty"><BookHeart :size="34" aria-hidden="true" /><h2>第一段回忆，等你来写</h2><p>投稿通过审核后，会出现在所有室友的首页。</p><button class="secondary-button" type="button" @click="openComposer()">开始记录</button></div>
          <article v-for="post in feedPosts" v-else :key="post.id" class="post-card">
            <header><span class="mini-avatar"><img v-if="avatarVisible(post.author.avatar_path)" :src="avatarURL(post.author.avatar_path)" alt="" @error="markAvatarBroken(post.author.avatar_path)" /><span v-else>{{ post.author.nickname.slice(0, 1) }}</span></span><div><strong>{{ post.author.nickname }}</strong><span>{{ displayDate(post.published_at) }}</span></div></header>
            <p class="post-body">{{ post.body }}</p>
            <div v-if="post.media.length" class="post-media-grid">
              <figure v-for="item in post.media" :key="item.id">
                <div v-if="mediaLoadErrors.has(item.id)" class="media-unavailable"><AlertCircle :size="24" aria-hidden="true" /><strong>远端媒体暂时不可用</strong><button type="button" @click="retryMediaLoad(item.id)"><RotateCcw :size="17" />重新加载</button></div>
                <img v-else-if="item.media_type === 'image'" :src="mediaContentURL(item.id)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" />
                <video v-else :src="mediaContentURL(item.id)" controls preload="metadata" :aria-label="item.original_filename" @error="markMediaLoadError(item.id)"></video>
                <figcaption><span>{{ item.original_filename }}</span><small>{{ formatBytes(item.size_bytes) }}</small></figcaption>
              </figure>
            </div>
            <div v-if="post.tags.length" class="tag-row"><span v-for="tag in post.tags" :key="tag">#{{ tag }}</span></div>
            <footer><span v-if="post.content_date"><CalendarDays :size="17" />记录于 {{ displayDate(post.content_date) }}</span><button type="button" :class="{ liked: post.liked_by_me }" :aria-label="post.liked_by_me ? '取消点赞' : '点赞'" @click="togglePostLike(post)"><Heart :size="17" :fill="post.liked_by_me ? 'currentColor' : 'none'" />{{ post.like_count }}</button><button type="button" @click="openDetail(post)"><MessageCircle :size="17" />{{ post.comment_count }} 条评论</button><button type="button" class="detail-link" @click="openDetail(post)">查看详情</button></footer>
          </article>
        </div>

        <aside class="content-rail" aria-label="我的投稿">
          <section class="rail-card"><header><div><p class="eyebrow">我的内容</p><h2>草稿与投稿</h2></div><span>{{ myPosts.length }}</span></header><div v-if="myPosts.length === 0" class="rail-empty">还没有草稿</div><button v-for="post in myPosts.slice(0, 6)" :key="post.id" class="draft-row" type="button" :disabled="post.status !== 'draft'" @click="post.status === 'draft' && openComposer(post)"><span>{{ post.body || '无标题草稿' }}</span><small :data-status="post.status">{{ statusLabel(post.status) }}</small></button></section>
          <section v-if="user.role === 'admin'" class="rail-card review-card"><header><div><p class="eyebrow">管理员审核</p><h2>待审核</h2></div><span>{{ pendingPosts.length }}</span></header><div v-if="pendingPosts.length === 0" class="rail-empty">当前没有待审核内容</div><article v-for="post in pendingPosts" :key="post.id" class="review-row"><strong>{{ post.author.nickname }}</strong><p>{{ post.body }}</p><div><button type="button" @click="moderate(post, 'hide')">退回隐藏</button><button type="button" @click="moderate(post, 'approve')">通过发布</button></div></article></section>
        </aside>
      </section>
      <section v-if="user.role === 'admin'" class="admin-card">
        <div><p class="eyebrow">管理员工具</p><h2>批量邀请室友</h2><p class="muted">每个邀请码 7 天内有效，只能使用一次；一次最多生成 20 个。</p></div>
        <div class="invite-actions">
          <div class="invite-count"><label for="invite-count">生成数量</label><select id="invite-count" v-model.number="inviteCount"><option v-for="count in inviteCountOptions" :key="count" :value="count">{{ count }} 个</option></select></div>
          <textarea v-if="inviteCodes.length" class="invite-codes" :value="inviteCodes.join('\n')" readonly rows="5" aria-label="生成的邀请码" @focus="($event.target as HTMLTextAreaElement).select()"></textarea>
          <div class="invite-buttons"><button class="secondary-button" type="button" :disabled="inviteBusy" @click="createInvite">{{ inviteBusy ? '生成中…' : `生成 ${inviteCount} 个邀请码` }}</button><button v-if="inviteCodes.length" class="secondary-button" type="button" @click="copyInvites"><Check v-if="inviteCopyStatus === 'copied'" :size="18" /><Copy v-else :size="18" />{{ inviteCopyStatus === 'copied' ? '已复制' : inviteCopyStatus === 'error' ? '复制失败，请手动选择' : '复制全部' }}</button></div>
          <p class="copy-status" aria-live="polite">{{ inviteCopyStatus === 'copied' ? `已复制 ${inviteCodes.length} 个邀请码` : '' }}</p>
        </div>
      </section>
      </template>

      <template v-else-if="activeView === 'timeline'">
        <header class="page-heading"><div><p class="eyebrow">按发生日期整理</p><h1 tabindex="-1">我们的时间线</h1><p>没有填写内容日期的回忆，会按照发布日期归档。</p></div><button class="primary-button compact" type="button" @click="openComposer()"><Plus :size="19" />补一段回忆</button></header>
        <div v-if="timelineGroups.length === 0" class="content-empty"><Sparkles :size="34" aria-hidden="true" /><h2>时间线还是空的</h2><p>发布第一段回忆后，它会出现在这里。</p></div>
        <div v-else class="timeline-list">
          <section v-for="group in timelineGroups" :key="group.key" class="timeline-group"><header><span></span><h2>{{ group.label }}</h2><small>{{ group.posts.length }} 条</small></header><div>
            <article v-for="post in group.posts" :key="post.id" class="timeline-entry"><time :datetime="(post.content_date || post.published_at || post.created_at)">{{ displayDate(post.content_date || post.published_at || post.created_at) }}</time><div><strong>{{ post.author.nickname }}</strong><p>{{ post.body || '分享了媒体回忆' }}</p><div v-if="post.media.length" class="timeline-media"><img v-for="item in post.media.filter((media) => media.media_type === 'image').slice(0, 4)" :key="item.id" :src="mediaContentURL(item.id, item.has_preview)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" /></div><div v-if="post.tags.length" class="tag-row"><span v-for="tag in post.tags" :key="tag">#{{ tag }}</span></div></div></article>
          </div></section>
        </div>
      </template>

      <template v-else-if="activeView === 'wall'">
        <header class="page-heading"><div><p class="eyebrow">照片与视频</p><h1 tabindex="-1">宿舍照片墙</h1><p>按最近发布排序，共收藏 {{ wallItems.length }} 个媒体文件。</p></div><button class="primary-button compact" type="button" @click="openComposer()"><Plus :size="19" />添加照片</button></header>
        <div v-if="wallItems.length === 0" class="content-empty"><Camera :size="34" aria-hidden="true" /><h2>照片墙还没有内容</h2><p>发布带照片或视频的回忆后，就会陈列在这里。</p></div>
        <div v-else class="photo-wall">
          <figure v-for="item in wallItems" :key="item.media.id">
            <div v-if="mediaLoadErrors.has(item.media.id)" class="media-unavailable"><AlertCircle :size="24" /><strong>暂时无法读取</strong><button type="button" @click="retryMediaLoad(item.media.id)"><RotateCcw :size="17" />重试</button></div>
            <a v-else-if="item.media.media_type === 'image'" :href="mediaContentURL(item.media.id)" target="_blank" rel="noopener" :aria-label="`查看原图：${item.media.original_filename}`"><img :src="mediaContentURL(item.media.id, item.media.has_preview)" :alt="item.media.original_filename" loading="lazy" @error="markMediaLoadError(item.media.id)" /></a>
            <video v-else :src="mediaContentURL(item.media.id)" controls preload="metadata" :aria-label="item.media.original_filename" @error="markMediaLoadError(item.media.id)"></video>
            <figcaption><div><strong>{{ item.post.author.nickname }}</strong><span>{{ item.post.body || item.media.original_filename }}</span></div><time :datetime="item.post.content_date || item.post.published_at || item.post.created_at">{{ displayDate(item.post.content_date || item.post.published_at || item.post.created_at) }}</time></figcaption>
          </figure>
        </div>
      </template>

      <template v-else>
        <header class="page-heading"><div><p class="eyebrow">写给我们，也写给某个人</p><h1 tabindex="-1">宿舍留言册</h1><p>每句话都只在室友之间可见，接收者可以隐藏不合适的留言。</p></div></header>
        <div class="guestbook-layout">
          <aside class="guestbook-people" aria-label="选择留言页">
            <button type="button" :class="{ active: guestbookRecipientID === '' }" :aria-pressed="guestbookRecipientID === ''" @click="selectGuestbookRecipient('')"><span class="guestbook-dorm-icon"><BookHeart :size="21" /></span><span><strong>写给整个宿舍</strong><small>大家共同的留言页</small></span></button>
            <button v-for="member in guestbookMembers" :key="member.id" type="button" :class="{ active: guestbookRecipientID === member.id }" :aria-pressed="guestbookRecipientID === member.id" @click="selectGuestbookRecipient(member.id)"><span class="mini-avatar"><img v-if="avatarVisible(member.avatar_path)" :src="avatarURL(member.avatar_path)" alt="" @error="markAvatarBroken(member.avatar_path)" /><span v-else>{{ member.nickname.slice(0, 1) }}</span></span><span><strong>{{ member.nickname }}</strong><small>{{ member.bed_no || `@${member.username}` }}</small></span></button>
          </aside>

          <div class="guestbook-main">
            <section class="guestbook-intro">
              <span v-if="selectedGuestbookMember" class="guestbook-hero-avatar"><img v-if="avatarVisible(selectedGuestbookMember.avatar_path)" :src="avatarURL(selectedGuestbookMember.avatar_path)" :alt="`${selectedGuestbookMember.nickname}的头像`" @error="markAvatarBroken(selectedGuestbookMember.avatar_path)" /><span v-else>{{ selectedGuestbookMember.nickname.slice(0, 1) }}</span></span>
              <BookHeart v-else :size="34" aria-hidden="true" />
              <div><p class="eyebrow">{{ selectedGuestbookMember ? '个人留言页' : '公共留言页' }}</p><h2>{{ selectedGuestbookMember ? `写给${selectedGuestbookMember.nickname}` : '写给 3048 的我们' }}</h2><p>{{ selectedGuestbookMember?.memorial_note || selectedGuestbookMember?.bio || '把没来得及说的话、祝福和照片留在这里。' }}</p></div>
            </section>

            <form class="guestbook-composer" @submit.prevent="submitGuestbookEntry">
              <label for="guestbook-body">{{ selectedGuestbookMember ? `给${selectedGuestbookMember.nickname}留言` : '给宿舍留言' }}</label>
              <textarea id="guestbook-body" v-model="guestbookBody" rows="4" maxlength="2000" placeholder="写一句以后再看到还会想起今天的话…"></textarea>
              <div class="guestbook-composer-meta"><small>{{ guestbookBody.length }} / 2000</small><span>最多 6 个附件</span></div>
              <input ref="guestbookMediaInput" class="visually-hidden" type="file" accept="image/*,video/*" multiple @change="handleGuestbookMediaInput" />
              <div v-if="guestbookMedia.length" class="media-queue guestbook-media-queue">
                <article v-for="item in guestbookMedia" :key="item.key" :data-status="item.status"><span class="media-kind"><Image v-if="item.kind === 'image'" :size="19" /><Film v-else :size="19" /></span><div><strong>{{ item.name }}</strong><small>{{ formatBytes(item.size) }} · {{ item.status === 'pending' ? '等待发布' : item.status === 'uploading' ? `上传 ${item.progress}%` : item.status === 'ready' ? '已就绪' : item.error }}</small><progress v-if="item.status === 'uploading'" :value="item.progress" max="100"></progress></div><button type="button" :disabled="item.status === 'uploading'" :aria-label="`移除 ${item.name}`" @click="removeGuestbookMedia(item)"><Trash2 :size="17" /></button></article>
              </div>
              <p v-if="guestbookError" class="form-error" role="alert">{{ guestbookError }}</p>
              <footer><button class="secondary-button" type="button" :disabled="guestbookBusy || guestbookMedia.length >= 6" @click="guestbookMediaInput?.click()"><UploadCloud :size="18" />添加照片或视频</button><button class="primary-button compact" type="submit" :disabled="guestbookBusy || guestbookMediaUploading || (!guestbookBody.trim() && guestbookMedia.length === 0)"><Send :size="18" />{{ guestbookBusy ? '发布中…' : '留下这句话' }}</button></footer>
            </form>

            <div v-if="guestbookLoading && guestbookEntries.length === 0" class="content-empty" role="status"><span class="loader"></span><span>正在翻开留言册…</span></div>
            <div v-else-if="guestbookEntries.length === 0" class="content-empty guestbook-empty"><BookHeart :size="34" aria-hidden="true" /><h2>这一页还没有留言</h2><p>成为第一个在这里留下字迹的人吧。</p></div>
            <section v-else class="guestbook-entries" aria-label="留言列表">
              <article v-for="entry in guestbookEntries" :key="entry.id" class="guestbook-entry">
                <header><span class="mini-avatar"><img v-if="avatarVisible(entry.author.avatar_path)" :src="avatarURL(entry.author.avatar_path)" alt="" @error="markAvatarBroken(entry.author.avatar_path)" /><span v-else>{{ entry.author.nickname.slice(0, 1) }}</span></span><div><strong>{{ entry.author.nickname }}</strong><span>{{ entry.recipient ? `写给 ${entry.recipient.nickname}` : '写给整个宿舍' }}</span></div></header>
                <p v-if="entry.body">{{ entry.body }}</p>
                <div v-if="entry.media.length" class="guestbook-entry-media"><template v-for="item in entry.media" :key="item.id"><div v-if="mediaLoadErrors.has(item.id)" class="media-unavailable"><AlertCircle :size="24" /><strong>暂时无法读取</strong><button type="button" @click="retryMediaLoad(item.id)"><RotateCcw :size="17" />重试</button></div><img v-else-if="item.media_type === 'image'" :src="mediaContentURL(item.id, item.has_preview)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" /><video v-else :src="mediaContentURL(item.id)" controls preload="metadata" :aria-label="item.original_filename" @error="markMediaLoadError(item.id)"></video></template></div>
                <footer><time :datetime="entry.created_at">{{ new Date(entry.created_at).toLocaleString('zh-CN') }}</time><div><button v-if="user.role === 'admin' || entry.recipient?.id === user.id" type="button" @click="hideGuestbookEntry(entry)">隐藏</button><button v-if="entry.author.id === user.id || user.role === 'admin'" class="danger-link" type="button" @click="deleteGuestbookEntry(entry)">删除</button></div></footer>
              </article>
            </section>
            <button v-if="guestbookNextCursor" class="secondary-button guestbook-more" type="button" :disabled="guestbookLoading" @click="loadGuestbook(false)">{{ guestbookLoading ? '读取中…' : '翻看更早的留言' }}</button>
          </div>
        </div>
      </template>
    </main>

    <nav class="bottom-nav" aria-label="移动端导航"><button type="button" class="nav-item" :class="{ active: activeView === 'home' }" @click="setView('home')"><Home /><span>首页</span></button><button type="button" class="nav-item" :class="{ active: activeView === 'wall' }" @click="setView('wall')"><Camera /><span>照片</span></button><button type="button" class="create-nav" aria-label="发布回忆" @click="openComposer()"><Plus /></button><button type="button" class="nav-item" :class="{ active: activeView === 'timeline' }" @click="setView('timeline')"><Sparkles /><span>时间线</span></button><button type="button" class="nav-item" :class="{ active: activeView === 'guestbook' }" @click="setView('guestbook')"><BookHeart /><span>留言</span></button></nav>

    <div v-if="composerOpen" class="dialog-layer" role="presentation" @click.self="closeComposer">
      <section ref="composerDialog" class="composer-dialog" role="dialog" aria-modal="true" aria-labelledby="composer-title" @keydown="trapComposerFocus">
        <header><div><p class="eyebrow">{{ editingPostID ? '编辑草稿' : '新的纪念' }}</p><h2 id="composer-title">写下一段回忆</h2></div><button class="icon-button" type="button" aria-label="关闭发布器" :disabled="composerBusy" @click="closeComposer"><X /></button></header>
        <form @submit.prevent="savePost(true)">
          <div class="field"><label for="post-body">正文</label><textarea id="post-body" v-model="editor.body" rows="8" maxlength="10000" autofocus placeholder="那天发生了什么？也可以只上传照片或视频。"></textarea><small>{{ editor.body.length }} / 10000</small></div>
          <section class="media-editor" aria-labelledby="media-editor-title">
            <header><div><strong id="media-editor-title">照片与视频</strong><small>选择后会在保存投稿时上传，不会暂存在生产服务器。</small></div><span>{{ editorMedia.length }} / 20</span></header>
            <input ref="mediaInput" class="visually-hidden" type="file" accept="image/*,video/*" multiple @change="handleMediaInput" />
            <button class="media-dropzone" type="button" :disabled="composerBusy || editorMedia.length >= 20" @click="chooseMedia" @dragover.prevent @drop.prevent="handleMediaDrop"><UploadCloud :size="25" aria-hidden="true" /><span><strong>选择照片或视频</strong><small>也可以把文件拖到这里，单个不超过 8 GiB</small></span></button>
            <p v-if="mediaSelectionError" class="field-error" role="alert"><AlertCircle :size="17" aria-hidden="true" />{{ mediaSelectionError }}</p>
            <div v-if="editorMedia.length" class="media-queue">
              <article v-for="item in editorMedia" :key="item.key" :data-status="item.status">
                <span class="media-kind"><Image v-if="item.kind === 'image'" :size="20" aria-hidden="true" /><Film v-else :size="20" aria-hidden="true" /></span>
                <div><strong>{{ item.name }}</strong><small>{{ formatBytes(item.size) }} · {{ item.status === 'pending' ? '等待保存' : item.status === 'uploading' ? `正在上传 ${item.progress}%` : item.status === 'ready' ? '已就绪' : item.error }}</small><progress v-if="item.status === 'uploading'" :value="item.progress" max="100">{{ item.progress }}%</progress></div>
                <button v-if="item.status === 'error'" type="button" aria-label="下次保存时重试上传" title="下次保存时重试" @click="retryEditorMedia(item)"><RotateCcw :size="18" /></button>
                <button v-else type="button" :disabled="item.status === 'uploading'" :aria-label="`移除 ${item.name}`" title="移除" @click="removeEditorMedia(item)"><Trash2 :size="18" /></button>
              </article>
            </div>
            <div v-if="mediaUsage" class="media-usage"><span>我的媒体空间</span><span>{{ formatBytes(mediaUsage.used_bytes + mediaUsage.reserved_bytes) }} / {{ formatBytes(mediaUsage.quota_bytes) }}</span><progress :value="usagePercent()" max="100">{{ usagePercent() }}%</progress></div>
          </section>
          <div class="field-grid"><div class="field"><label for="content-date">内容日期</label><input id="content-date" v-model="editor.content_date" type="date" /></div><div class="field"><label for="post-visibility">可见范围</label><select id="post-visibility" v-model="editor.visibility"><option value="members">所有室友</option><option value="private">仅自己</option></select></div></div>
          <div class="field"><label for="post-tags">标签</label><input id="post-tags" v-model="editor.tags" maxlength="320" placeholder="用逗号或顿号分隔，最多 10 个" /></div>
          <p v-if="composerError" class="form-error" role="alert">{{ composerError }}</p>
          <footer><button class="secondary-button" type="button" :disabled="composerBusy" @click="savePost(false)">保存草稿</button><button class="primary-button" type="submit" :disabled="composerBusy || mediaUploading || (!editor.body.trim() && editorMedia.length === 0)"><Send :size="18" />{{ composerBusy ? '上传并保存中…' : '提交审核' }}</button></footer>
        </form>
      </section>
    </div>

    <div v-if="profileOpen" class="dialog-layer" role="presentation" @click.self="closeProfile">
      <section ref="profileDialog" class="profile-dialog" role="dialog" aria-modal="true" aria-labelledby="profile-title" @keydown="trapProfileFocus">
        <header><div><p class="eyebrow">账号与个人资料</p><h2 id="profile-title">{{ user.nickname }}</h2></div><button class="icon-button" type="button" aria-label="关闭" @click="closeProfile"><X /></button></header>
        <section class="avatar-editor" :class="{ 'is-cropping': avatarCrop.sourceURL }" aria-labelledby="avatar-editor-title">
          <template v-if="avatarCrop.sourceURL">
            <div class="avatar-crop-workspace">
              <div><strong id="avatar-editor-title">裁剪头像</strong><span>调整范围后生成正方形头像，大于 2 MiB 的原图会先压缩再上传。</span></div>
              <div ref="avatarCropFrame" class="avatar-crop-frame" aria-label="头像裁剪预览"><img :src="avatarCrop.sourceURL" alt="待裁剪头像预览" :style="avatarCropStyle" /><span aria-hidden="true"></span></div>
              <div class="avatar-crop-controls">
                <label for="avatar-zoom">缩放 <output>{{ avatarCrop.zoom.toFixed(1) }}×</output></label><input id="avatar-zoom" v-model.number="avatarCrop.zoom" type="range" min="1" max="3" step="0.1" />
                <label for="avatar-x">水平位置 <output>{{ avatarCrop.x }}%</output></label><input id="avatar-x" v-model.number="avatarCrop.x" type="range" min="0" max="100" step="1" />
                <label for="avatar-y">垂直位置 <output>{{ avatarCrop.y }}%</output></label><input id="avatar-y" v-model.number="avatarCrop.y" type="range" min="0" max="100" step="1" />
                <label for="avatar-size">输出尺寸</label><select id="avatar-size" v-model.number="avatarCrop.outputSize"><option :value="256">256 × 256</option><option :value="512">512 × 512</option><option :value="1024">1024 × 1024</option></select>
              </div>
              <div class="avatar-buttons"><button class="secondary-button" type="button" :disabled="avatarBusy" @click="resetAvatarCrop">重新选择</button><button class="primary-button compact" type="button" :disabled="avatarBusy" @click="uploadCroppedAvatar"><UploadCloud :size="18" />{{ avatarBusy ? `上传中 ${avatarProgress}%` : '应用头像' }}</button></div>
            </div>
          </template>
          <template v-else>
            <div class="avatar-preview"><img v-if="avatarVisible(user.avatar_path)" :src="avatarURL(user.avatar_path)" :alt="`${user.nickname}的头像`" @error="markAvatarBroken(user.avatar_path)" /><UserRound v-else :size="34" aria-hidden="true" /></div><div><strong id="avatar-editor-title">个人头像</strong><span>支持 JPEG、PNG 或 WebP，最大 25 MiB；上传前可裁剪。</span><input ref="avatarInput" class="visually-hidden" type="file" accept="image/jpeg,image/png,image/webp" @change="handleAvatarInput" /><div class="avatar-buttons"><button class="secondary-button" type="button" :disabled="avatarBusy" @click="avatarInput?.click()">选择新头像</button><button v-if="user.avatar_path" type="button" class="text-danger" :disabled="avatarBusy" @click="clearAvatar">移除头像</button></div></div>
          </template>
        </section>
        <form @submit.prevent="saveProfile">
          <div class="field-grid"><div class="field"><label for="profile-nickname">昵称</label><input id="profile-nickname" v-model.trim="profile.nickname" maxlength="40" required /></div><div class="field"><label for="bed-no">床号或位置</label><input id="bed-no" v-model.trim="profile.bed_no" maxlength="30" placeholder="例如 2 号床" /></div></div>
          <div class="field"><label for="bio">个人简介</label><textarea id="bio" v-model="profile.bio" maxlength="500" rows="3"></textarea></div>
          <div class="field"><label for="memorial-note">纪念寄语</label><textarea id="memorial-note" v-model="profile.memorial_note" maxlength="500" rows="3"></textarea></div>
          <p v-if="profileMessage" class="form-message" role="status">{{ profileMessage }}</p><button class="primary-button" type="submit" :disabled="profileBusy">{{ profileBusy ? '保存中…' : '保存资料' }}</button>
        </form>
        <div class="session-section"><h3>登录设备</h3><div v-for="session in sessions" :key="session.id" class="session-row"><div><strong>{{ session.current ? '当前设备' : '其他设备' }}</strong><span>{{ session.user_agent || '未知浏览器' }}</span><small>{{ session.ip_address }} · {{ new Date(session.last_seen_at).toLocaleString('zh-CN') }}</small></div><button class="text-danger" type="button" @click="revoke(session)">{{ session.current ? '退出此设备' : '注销' }}</button></div></div>
        <button class="logout-button" type="button" @click="logout"><LogOut :size="18" />退出登录</button>
      </section>
    </div>

    <div v-if="detailOpen && detailPost" class="dialog-layer" role="presentation" @click.self="closeDetail">
      <section ref="detailDialog" class="detail-dialog" role="dialog" aria-modal="true" aria-labelledby="detail-title" @keydown="trapDetailFocus">
        <header><div><p class="eyebrow">{{ displayDate(detailPost.content_date || detailPost.published_at) }}</p><h2 id="detail-title">{{ detailPost.author.nickname }}的回忆</h2></div><button class="icon-button" type="button" aria-label="关闭详情" @click="closeDetail"><X /></button></header>
        <div class="detail-author"><span class="mini-avatar"><img v-if="avatarVisible(detailPost.author.avatar_path)" :src="avatarURL(detailPost.author.avatar_path)" alt="" @error="markAvatarBroken(detailPost.author.avatar_path)" /><span v-else>{{ detailPost.author.nickname.slice(0, 1) }}</span></span><div><strong>{{ detailPost.author.nickname }}</strong><span>{{ displayDate(detailPost.published_at) }}</span></div></div>
        <p v-if="detailPost.body" class="detail-body">{{ detailPost.body }}</p>
        <div v-if="detailPost.media.length" class="detail-media"><template v-for="item in detailPost.media" :key="item.id"><img v-if="item.media_type === 'image'" :src="mediaContentURL(item.id)" :alt="item.original_filename" /><video v-else :src="mediaContentURL(item.id)" controls preload="metadata" :aria-label="item.original_filename"></video></template></div>
        <div class="detail-actions"><button type="button" :class="{ liked: detailPost.liked_by_me }" @click="togglePostLike(detailPost)"><Heart :size="19" :fill="detailPost.liked_by_me ? 'currentColor' : 'none'" />{{ detailPost.liked_by_me ? '已点赞' : '点赞' }} · {{ detailPost.like_count }}</button><span><MessageCircle :size="19" />{{ detailPost.comment_count }} 条评论</span></div>
        <section class="comments-section" aria-labelledby="comments-title"><h3 id="comments-title">评论</h3><p v-if="detailError" class="form-error" role="alert">{{ detailError }}</p><div v-if="detailLoading" class="comment-empty" role="status">正在读取评论…</div><div v-else-if="detailComments.length === 0" class="comment-empty">还没有评论，来留下第一句话吧。</div><article v-for="comment in detailComments" :key="comment.id" class="comment-row"><span class="mini-avatar"><img v-if="avatarVisible(comment.author.avatar_path)" :src="avatarURL(comment.author.avatar_path)" alt="" @error="markAvatarBroken(comment.author.avatar_path)" /><span v-else>{{ comment.author.nickname.slice(0, 1) }}</span></span><div><header><strong>{{ comment.author.nickname }}</strong><time :datetime="comment.created_at">{{ new Date(comment.created_at).toLocaleString('zh-CN') }}</time></header><p>{{ comment.body }}</p></div><button v-if="comment.author.id === user.id || user.role === 'admin'" type="button" :aria-label="`删除${comment.author.nickname}的评论`" @click="removeComment(comment)"><Trash2 :size="17" /></button></article><form class="comment-form" @submit.prevent="submitComment"><label for="comment-body">写评论</label><textarea id="comment-body" v-model="commentBody" rows="3" maxlength="2000" required placeholder="说点什么…"></textarea><div><small>{{ commentBody.length }} / 2000</small><button class="primary-button compact" type="submit" :disabled="commentBusy || !commentBody.trim()"><Send :size="17" />{{ commentBusy ? '发送中…' : '发表评论' }}</button></div></form></section>
      </section>
    </div>
  </div>
</template>
