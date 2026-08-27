<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { AlertCircle, ArchiveRestore, Bell, BookHeart, CalendarDays, Camera, Check, CheckCheck, ChevronLeft, Copy, DatabaseBackup, Download, Eye, EyeOff, FileEdit, Film, Heart, Home, Image, LogOut, MailPlus, Menu, MessageCircle, Music, Paperclip, Plus, RefreshCw, RotateCcw, Search, Send, Settings, ShieldCheck, Sparkles, Trash2, Undo2, UploadCloud, UserRound, Users, X } from 'lucide-vue-next'
import { api, ApiError } from './api'
import type { AdminMedia, AdminMessage, AdminUser, ChatMessage, Comment, Conversation, GuestbookEntry, Media, MediaUsage, Member, NotificationItem, Post, Session, User } from './types'
import VideoPreview from './components/VideoPreview.vue'

type EditorMedia = {
  key: string
  file?: File
  media?: Media
  name: string
  size: number
  kind: 'image' | 'video' | 'audio'
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
const accountBusy = ref(false)
const accountMessage = ref('')
const account = reactive({ username: '', email: '', nickname: '', current_password: '', new_password: '', confirm_password: '' })
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
const sessionsExpanded = ref(false)
const visibleSessions = computed(() => sessionsExpanded.value ? sessions.value : sessions.value.slice(0, 3))
const inviteCodes = ref<string[]>([])
const inviteCount = ref(5)
const inviteCountOptions = Array.from({ length: 20 }, (_, index) => index + 1)
const inviteCopyStatus = ref<'idle' | 'copied' | 'error'>('idle')
const inviteBusy = ref(false)
const managementMessage = ref('')
const managementTab = ref<'invites' | 'users' | 'messages' | 'media' | 'backup'>('users')
const adminUsers = ref<AdminUser[]>([])
const adminMessages = ref<AdminMessage[]>([])
const adminMedia = ref<AdminMedia[]>([])
const adminLoading = ref(false)
const adminActionID = ref('')
const backupBusy = ref(false)
const adminFeedback = ref('')
const adminUserFilters = reactive({ search: '', role: '', status: '' })
const adminMessageFilters = reactive({ search: '', status: 'sent' })
const adminMediaFilters = reactive({ search: '', type: '', status: '' })
const profile = reactive({ nickname: '', bio: '', bed_no: '', memorial_note: '' })
const feedPosts = ref<Post[]>([])
const myPosts = ref<Post[]>([])
const pendingPosts = ref<Post[]>([])
const contentLoading = ref(false)
const contentError = ref('')
const feedNextCursor = ref('')
const feedLoadingMore = ref(false)
const contentLoadSentinel = ref<HTMLElement | null>(null)
const timelineVisibleCount = ref(8)
const wallVisibleCount = ref(12)
let contentObserver: IntersectionObserver | null = null
const topNotice = ref('')
let topNoticeTimer = 0
const composerOpen = ref(false)
const composerDialog = ref<HTMLElement | null>(null)
let composerTrigger: HTMLElement | null = null
const composerBusy = ref(false)
const composerError = ref('')
const editingPostID = ref('')
let composerInitialState = ''
let dialogBackdropArmed = false
const editor = reactive({ body: '', content_date: '', visibility: 'members' as 'members' | 'private', tags: '', external_video_url: '' })
const editorMedia = ref<EditorMedia[]>([])
const removedMediaIDs = ref<string[]>([])
const mediaInput = ref<HTMLInputElement | null>(null)
const mediaUsage = ref<MediaUsage | null>(null)
const mediaSelectionError = ref('')
const mediaLoadErrors = ref(new Set<string>())
const publicProfile = ref<Member | null>(null)
const mediaUploading = computed(() => editorMedia.value.some((item) => item.status === 'uploading'))
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
const guestbookExternalVideoURL = ref('')
const guestbookMedia = ref<EditorMedia[]>([])
const guestbookMediaInput = ref<HTMLInputElement | null>(null)
const guestbookLoading = ref(false)
const guestbookBusy = ref(false)
const guestbookError = ref('')
const guestbookNextCursor = ref('')
const guestbookStatus = ref<'visible' | 'hidden'>('visible')
let guestbookRequest = 0
const guestbookMediaUploading = computed(() => guestbookMedia.value.some((item) => item.status === 'uploading'))
const selectedGuestbookMember = computed(() => guestbookMembers.value.find((member) => member.id === guestbookRecipientID.value) ?? null)
const canViewHiddenGuestbook = computed(() => user.value?.role === 'admin' || Boolean(user.value && guestbookRecipientID.value === user.value.id))
const notificationsOpen = ref(false)
const notifications = ref<NotificationItem[]>([])
const notificationUnread = ref(0)
const notificationLoading = ref(false)
const notificationClearing = ref(false)
const notificationFeedback = ref('')
const notificationPanel = ref<HTMLElement | null>(null)
const notificationTrigger = ref<HTMLElement | null>(null)
const conversations = ref<Conversation[]>([])
const selectedConversationID = ref('')
const chatMessages = ref<ChatMessage[]>([])
const messageMembers = ref<Member[]>([])
const messageBody = ref('')
const messageMedia = ref<EditorMedia[]>([])
const messageMediaInput = ref<HTMLInputElement | null>(null)
const messageLoading = ref(false)
const messageBusy = ref(false)
const messageError = ref('')
const messageNextCursor = ref('')
const messageThreadOpen = ref(false)
const messageScroll = ref<HTMLElement | null>(null)
const messageMediaUploading = computed(() => messageMedia.value.some((item) => item.status === 'uploading'))
let activityTimer = 0
let messageRequest = 0
let messageLoadingRequest = 0
let messageHighlightTimer = 0
const selectedConversation = computed(() => conversations.value.find((item) => item.id === selectedConversationID.value) ?? null)
const totalMessageUnread = computed(() => conversations.value.reduce((sum, item) => sum + item.unread_count, 0))
type ActiveView = 'home' | 'timeline' | 'wall' | 'guestbook' | 'messages' | 'management' | 'detail'
type NavigationView = Exclude<ActiveView, 'detail'>

const activeView = ref<ActiveView>('home')
let detailReturnView: NavigationView = 'home'
let detailReturnScrollY = 0
const wallItems = computed(() => feedPosts.value.flatMap((post) => [
  ...post.media.map((media) => ({ key: `media-${media.id}`, post, media, externalVideoURL: '' })),
  ...(post.external_video_url ? [{ key: `external-${post.id}`, post, media: null, externalVideoURL: post.external_video_url }] : []),
]))
const visibleWallItems = computed(() => wallItems.value.slice(0, wallVisibleCount.value))

function showPublicProfile(id: string) {
  const member = [...guestbookMembers.value, ...messageMembers.value].find((item) => item.id === id)
  if (member) publicProfile.value = member
}

const timelineGroups = computed(() => {
  const sorted = [...feedPosts.value].sort((left, right) => timelineDate(right).getTime() - timelineDate(left).getTime()).slice(0, timelineVisibleCount.value)
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
  { id: 'messages' as const, label: '消息', icon: MessageCircle, available: true },
]

async function setView(view: NavigationView) {
  if (view === 'management' && user.value?.role !== 'admin') return
  activeView.value = view
  mobileMenuOpen.value = false
  if (view === 'guestbook') await loadGuestbook(true)
  if (view === 'messages') await loadMessageCenter()
  if (view === 'management') await loadManagementSection()
  await nextTick(() => document.querySelector<HTMLElement>('#main-content h1')?.focus())
}

function timelineDate(post: Post) {
  return new Date(post.content_date || post.published_at || post.created_at)
}

onMounted(async () => {
  document.addEventListener('pointerdown', handleNotificationOutsidePointer)
  document.addEventListener('keydown', handleNotificationKeydown)
  document.addEventListener('visibilitychange', handleVisibilityRefresh)
  contentObserver = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) void revealMoreContent()
  }, { rootMargin: '500px 0px' })
  if (contentLoadSentinel.value) contentObserver.observe(contentLoadSentinel.value)
  try {
    applyUser((await api.me()).user)
    await loadContent()
    await initializeActivity()
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 401) console.error(error)
  } finally {
    booting.value = false
  }
})

onBeforeUnmount(() => {
  stopActivityPolling()
  document.removeEventListener('pointerdown', handleNotificationOutsidePointer)
  document.removeEventListener('keydown', handleNotificationKeydown)
  document.removeEventListener('visibilitychange', handleVisibilityRefresh)
  contentObserver?.disconnect()
  if (topNoticeTimer) window.clearTimeout(topNoticeTimer)
  if (messageHighlightTimer) window.clearTimeout(messageHighlightTimer)
})

function applyUser(next: User) {
  user.value = next
  profile.nickname = next.nickname
  profile.bio = next.bio
  profile.bed_no = next.bed_no
  profile.memorial_note = next.memorial_note
  account.username = next.username
  account.email = next.email
  account.nickname = next.nickname
  const author = { id: next.id, username: next.username, nickname: next.nickname, avatar_path: next.avatar_path }
  feedPosts.value = feedPosts.value.map((post) => post.author.id === next.id ? { ...post, author } : post)
  myPosts.value = myPosts.value.map((post) => post.author.id === next.id ? { ...post, author } : post)
  guestbookMembers.value = guestbookMembers.value.map((member) => member.id === next.id ? { ...member, username: next.username, nickname: next.nickname, avatar_path: next.avatar_path, bio: next.bio, bed_no: next.bed_no, memorial_note: next.memorial_note } : member)
  messageMembers.value = messageMembers.value.map((member) => member.id === next.id ? { ...member, username: next.username, nickname: next.nickname, avatar_path: next.avatar_path, bio: next.bio, bed_no: next.bed_no, memorial_note: next.memorial_note } : member)
}

watch(contentLoadSentinel, (next, previous) => {
  if (previous) contentObserver?.unobserve(previous)
  if (next) contentObserver?.observe(next)
})

async function submitAuth() {
  authBusy.value = true
  authError.value = ''
  try {
    const response = authMode.value === 'login'
      ? await api.login({ identifier: auth.identifier, password: auth.password })
      : await api.register({ invite_code: auth.inviteCode, username: auth.username, email: auth.email, password: auth.password, nickname: auth.nickname })
    applyUser(response.user)
    await loadContent()
    await initializeActivity()
    auth.password = ''
  } catch (error) {
    authError.value = error instanceof Error ? error.message : '暂时无法完成请求'
  } finally {
    authBusy.value = false
  }
}

async function logout() {
  stopActivityPolling()
  await api.logout()
  user.value = null
  feedPosts.value = []
  myPosts.value = []
  pendingPosts.value = []
  notifications.value = []
  conversations.value = []
  profileOpen.value = false
}

async function openProfile() {
	profileTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  profileOpen.value = true
  sessionsExpanded.value = false
  profileMessage.value = ''
  accountMessage.value = ''
  account.current_password = ''
  account.new_password = ''
  account.confirm_password = ''
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

function closeProfileFromBackdrop(event: MouseEvent) {
  if (consumeDialogBackdrop(event)) closeProfile()
}

function closePublicProfileFromBackdrop(event: MouseEvent) {
  if (consumeDialogBackdrop(event)) publicProfile.value = null
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

async function saveAccount() {
  if (accountBusy.value) return
  accountMessage.value = ''
  if (account.new_password !== account.confirm_password) {
    accountMessage.value = '两次输入的新密码不一致'
    return
  }
  accountBusy.value = true
  try {
    const passwordChanged = Boolean(account.new_password)
    applyUser((await api.updateAccount({
      username: account.username,
      email: account.email,
      nickname: account.nickname,
      current_password: account.current_password,
      new_password: account.new_password,
    })).user)
    account.current_password = ''
    account.new_password = ''
    account.confirm_password = ''
    if (passwordChanged) {
      sessions.value = (await api.sessions()).sessions
      sessionsExpanded.value = false
    }
    accountMessage.value = passwordChanged ? '账号与密码已更新，其他设备已退出登录' : '账号信息已更新'
    showTopNotice(accountMessage.value)
  } catch (error) {
    accountMessage.value = error instanceof ApiError && error.status === 401 ? '当前密码不正确' : error instanceof Error ? error.message : '账号信息保存失败'
  } finally {
    accountBusy.value = false
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
  if (activeView.value !== 'detail') {
    detailReturnView = activeView.value
    detailReturnScrollY = window.scrollY
  }
  detailPost.value = post
  detailComments.value = []
  detailError.value = ''
  commentBody.value = ''
  activeView.value = 'detail'
  detailLoading.value = true
  await nextTick()
  window.scrollTo({ top: 0 })
  document.querySelector<HTMLElement>('#detail-title')?.focus()
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
  activeView.value = detailReturnView
  nextTick(() => {
    window.scrollTo({ top: detailReturnScrollY })
    detailTrigger?.focus({ preventScroll: true })
  })
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
  const status = guestbookStatus.value
  try {
    if (guestbookMembers.value.length === 0) guestbookMembers.value = (await api.members()).members
    const response = await api.guestbook({ recipient_id: recipientID || undefined, status, cursor: reset ? undefined : guestbookNextCursor.value || undefined, limit: 20 })
    if (requestID !== guestbookRequest || recipientID !== guestbookRecipientID.value || status !== guestbookStatus.value) return
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
  guestbookStatus.value = 'visible'
  guestbookEntries.value = []
  guestbookNextCursor.value = ''
  void loadGuestbook(true)
}

function toggleHiddenGuestbook() {
  if (!canViewHiddenGuestbook.value) return
  guestbookStatus.value = guestbookStatus.value === 'visible' ? 'hidden' : 'visible'
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
    if (!kind) {
      guestbookError.value = `${file.name} 不是有效的图片或视频。`
      continue
    }
    if (file.size <= 0 || file.size > (kind === 'video' ? 500 * 1024 ** 2 : 8 * 1024 ** 3)) {
      guestbookError.value = kind === 'video' ? `${file.name} 为空或超过 150 MiB，请先压缩视频后再上传。` : `${file.name} 为空或超过 8 GiB。`
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
  if ((!guestbookBody.value.trim() && !guestbookExternalVideoURL.value.trim() && guestbookMedia.value.length === 0) || guestbookBusy.value) return
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
      external_video_url: guestbookExternalVideoURL.value,
    })
    guestbookEntries.value.unshift(response.entry)
    guestbookBody.value = ''
    guestbookExternalVideoURL.value = ''
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
  if (!window.confirm('确定删除这条留言吗？删除后无法在界面中恢复。')) return
  try {
    await api.deleteGuestbookEntry(entry.id)
    guestbookEntries.value = guestbookEntries.value.filter((item) => item.id !== entry.id)
  } catch (error) {
    guestbookError.value = error instanceof Error ? error.message : '删除留言失败'
  }
}

async function restoreGuestbookEntry(entry: GuestbookEntry) {
  try {
    await api.restoreGuestbookEntry(entry.id)
    guestbookEntries.value = guestbookEntries.value.filter((item) => item.id !== entry.id)
  } catch (error) {
    guestbookError.value = error instanceof Error ? error.message : '恢复留言失败'
  }
}

async function revoke(session: Session) {
  await api.revokeSession(session.id)
  if (session.current) {
    user.value = null
    profileOpen.value = false
  } else {
    sessions.value = sessions.value.filter((item) => item.id !== session.id)
    if (sessions.value.length <= 3) sessionsExpanded.value = false
  }
}

async function createInvite() {
  inviteBusy.value = true
  inviteCopyStatus.value = 'idle'
  managementMessage.value = ''
  try {
    const response = await api.createInvites({ max_uses: 1, expires_in_hours: 168, count: inviteCount.value })
    inviteCodes.value = response.invites.map((item) => item.code)
    managementMessage.value = `已生成 ${response.count} 个邀请码`
  } catch (error) {
    managementMessage.value = error instanceof Error ? error.message : '邀请码生成失败'
  } finally {
    inviteBusy.value = false
  }
}

async function selectManagementTab(tab: typeof managementTab.value) {
  managementTab.value = tab
  adminFeedback.value = ''
  await loadManagementSection()
}

async function loadManagementSection() {
  if (user.value?.role !== 'admin' || managementTab.value === 'invites' || managementTab.value === 'backup') return
  adminLoading.value = true
  adminFeedback.value = ''
  try {
    if (managementTab.value === 'users') {
      adminUsers.value = (await api.adminUsers(adminUserFilters)).users
    } else if (managementTab.value === 'messages') {
      adminMessages.value = (await api.adminMessages({ ...adminMessageFilters, limit: 100 })).messages
    } else {
      adminMedia.value = (await api.adminMedia({ ...adminMediaFilters, limit: 100 })).media
    }
  } catch (error) {
    adminFeedback.value = error instanceof Error ? error.message : '管理数据读取失败'
  } finally {
    adminLoading.value = false
  }
}

async function exportAdminBackup() {
  backupBusy.value = true
  adminFeedback.value = ''
  try {
    const { blob, filename } = await api.exportBackup()
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = filename
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    adminFeedback.value = `备份 ${filename} 已通过完整性校验并开始下载`
  } catch (error) {
    adminFeedback.value = error instanceof Error ? error.message : '备份导出失败'
  } finally {
    backupBusy.value = false
  }
}

async function saveAdminUser(item: AdminUser) {
  if (item.id === user.value?.id) return
  const action = item.status === 'disabled' ? '停用后该成员会立即退出所有设备。' : ''
  if (!window.confirm(`确定将 ${item.nickname} 设置为“${item.role === 'admin' ? '管理员' : '成员'} / ${item.status === 'active' ? '启用' : '停用'}”吗？${action}`)) {
    await loadManagementSection()
    return
  }
  adminActionID.value = item.id
  adminFeedback.value = ''
  try {
    const updated = (await api.updateAdminUser(item.id, { role: item.role, status: item.status })).user
    adminUsers.value = adminUsers.value.map((candidate) => candidate.id === updated.id ? updated : candidate)
    adminFeedback.value = `已更新 ${updated.nickname} 的账号权限`
  } catch (error) {
    adminFeedback.value = error instanceof Error ? error.message : '用户更新失败'
    await loadManagementSection()
  } finally {
    adminActionID.value = ''
  }
}

async function removeAdminMessage(item: AdminMessage) {
  if (!window.confirm(`确定从宿舍群聊中移除 ${item.sender.nickname} 的这条消息吗？成员界面将显示为已撤回。`)) return
  adminActionID.value = item.id
  adminFeedback.value = ''
  try {
    await api.removeAdminMessage(item.id)
    item.status = 'recalled'
    item.body = ''
    adminFeedback.value = '群聊消息已移除'
  } catch (error) {
    adminFeedback.value = error instanceof Error ? error.message : '消息移除失败'
  } finally {
    adminActionID.value = ''
  }
}

async function removeAdminMedia(item: AdminMedia) {
  if (item.reference_count > 0) return
  if (!window.confirm(`确定永久删除“${item.original_filename}”吗？远端存储中的文件也会被删除，且无法恢复。`)) return
  adminActionID.value = item.id
  adminFeedback.value = ''
  try {
    await api.deleteMedia(item.id)
    adminMedia.value = adminMedia.value.filter((candidate) => candidate.id !== item.id)
    adminFeedback.value = '未被引用的媒体已删除'
  } catch (error) {
    adminFeedback.value = error instanceof Error ? error.message : '媒体删除失败'
  } finally {
    adminActionID.value = ''
  }
}

function adminMediaTypeLabel(type: AdminMedia['media_type']) {
  return { image: '图片', video: '视频', audio: '音频' }[type]
}

async function loadContent() {
  if (!user.value) return
  contentLoading.value = true
  contentError.value = ''
  try {
    if (guestbookMembers.value.length === 0) guestbookMembers.value = (await api.members()).members
    const requests = [
      api.posts({ scope: 'feed', limit: 8 }),
      api.posts({ scope: 'mine', limit: 20 }),
    ] as const
    const [feed, mine] = await Promise.all(requests)
    feedPosts.value = feed.posts
    feedNextCursor.value = feed.next_cursor ?? ''
    myPosts.value = mine.posts
    pendingPosts.value = []
    timelineVisibleCount.value = 8
    wallVisibleCount.value = 12
    mediaUsage.value = (await api.mediaUsage()).usage
  } catch (error) {
    contentError.value = error instanceof Error ? error.message : '暂时无法读取纪念内容'
  } finally {
    contentLoading.value = false
  }
}

async function loadMoreFeed() {
  if (!user.value || !feedNextCursor.value || feedLoadingMore.value) return
  feedLoadingMore.value = true
  try {
    const page = await api.posts({ scope: 'feed', cursor: feedNextCursor.value, limit: 8 })
    const known = new Set(feedPosts.value.map((post) => post.id))
    feedPosts.value.push(...page.posts.filter((post) => !known.has(post.id)))
    feedNextCursor.value = page.next_cursor ?? ''
  } catch (error) {
    contentError.value = error instanceof Error ? error.message : '无法继续读取回忆'
  } finally {
    feedLoadingMore.value = false
  }
}

async function revealMoreContent() {
  if (contentLoading.value || feedLoadingMore.value) return
  if (activeView.value === 'home') {
    await loadMoreFeed()
    return
  }
  if (activeView.value === 'timeline') {
    if (timelineVisibleCount.value < feedPosts.value.length) {
      timelineVisibleCount.value += 8
      return
    }
    const before = feedPosts.value.length
    await loadMoreFeed()
    if (feedPosts.value.length > before) timelineVisibleCount.value += 8
    return
  }
  if (activeView.value === 'wall') {
    if (wallVisibleCount.value < wallItems.value.length) {
      wallVisibleCount.value += 12
      return
    }
    const before = feedPosts.value.length
    await loadMoreFeed()
    if (feedPosts.value.length > before) wallVisibleCount.value += 12
  }
}

function showTopNotice(message: string) {
  topNotice.value = message
  if (topNoticeTimer) window.clearTimeout(topNoticeTimer)
  topNoticeTimer = window.setTimeout(() => { topNotice.value = '' }, 3500)
}

function armDialogBackdrop(event: PointerEvent) {
  dialogBackdropArmed = event.target === event.currentTarget
}

function consumeDialogBackdrop(event: MouseEvent) {
  const shouldClose = dialogBackdropArmed && event.target === event.currentTarget
  dialogBackdropArmed = false
  return shouldClose
}

function composerState() {
  return JSON.stringify({
    body: editor.body,
    contentDate: editor.content_date,
    visibility: editor.visibility,
    tags: editor.tags,
    externalVideoURL: editor.external_video_url,
    media: editorMedia.value.map((item) => item.media?.id ?? item.key),
    removed: removedMediaIDs.value,
  })
}

function composerHasContent() {
  return Boolean(editor.body.trim() || editor.external_video_url.trim() || editorMedia.value.length)
}

async function openComposer(post?: Post) {
  composerTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  editingPostID.value = post?.id ?? ''
  editor.body = post?.body ?? ''
  editor.content_date = post?.content_date?.slice(0, 10) ?? ''
  editor.visibility = post?.visibility ?? 'members'
  editor.tags = post?.tags.join('、') ?? ''
  editor.external_video_url = post?.external_video_url ?? ''
  editorMedia.value = (post?.media ?? []).map((item) => ({ key: item.id, media: item, name: item.original_filename, size: item.size_bytes, kind: item.media_type, status: 'ready', progress: 100, error: '', persisted: true, width: item.width ?? undefined, height: item.height ?? undefined, duration_ms: item.duration_ms ?? undefined }))
  removedMediaIDs.value = []
  mediaSelectionError.value = ''
  composerError.value = ''
  composerInitialState = composerState()
  composerOpen.value = true
  await nextTick()
  composerDialog.value?.querySelector<HTMLElement>('textarea, input, button')?.focus()
}

async function closeComposer() {
  if (composerBusy.value) return
  if (composerState() !== composerInitialState && (composerHasContent() || editingPostID.value)) {
    await savePost(false, true)
    return
  }
  composerOpen.value = false
  await nextTick(() => composerTrigger?.focus())
}

function closeComposerFromBackdrop(event: MouseEvent) {
  if (consumeDialogBackdrop(event)) void closeComposer()
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

async function savePost(submit: boolean, automatic = false) {
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
      external_video_url: editor.external_video_url,
    }
    const wasEditing = Boolean(editingPostID.value)
    if (wasEditing) await api.updatePost(editingPostID.value, body)
    else await api.createPost(body)
    await Promise.allSettled(removedMediaIDs.value.map((id) => api.deleteMedia(id)))
    composerOpen.value = false
    await loadContent()
    showTopNotice(automatic ? (wasEditing ? '编辑内容已自动保存' : '已自动保存到草稿') : (submit ? '回忆已发布' : '草稿已保存'))
    await nextTick(() => composerTrigger?.focus())
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
    if (file.size <= 0 || file.size > (kind === 'video' ? 150 * 1024 ** 2 : 8 * 1024 ** 3)) {
      mediaSelectionError.value = kind === 'video' ? `${file.name} 为空或超过 150 MiB，请先压缩视频后再上传。` : `${file.name} 为空或超过 8 GiB。`
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

async function readAudioMetadata(item: EditorMedia) {
  if (!item.file) return
  const objectURL = URL.createObjectURL(item.file)
  try {
    await new Promise<void>((resolve) => {
      const audio = document.createElement('audio')
      const finish = () => resolve()
      const timeout = window.setTimeout(finish, 5000)
      audio.preload = 'metadata'
      audio.onloadedmetadata = () => {
        window.clearTimeout(timeout)
        if (Number.isFinite(audio.duration)) item.duration_ms = Math.round(audio.duration * 1000)
        finish()
      }
      audio.onerror = () => { window.clearTimeout(timeout); finish() }
      audio.src = objectURL
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

function isEmbeddedPlayer(value: string) {
  try {
    const host = new URL(value).hostname.toLowerCase()
    return ['player.bilibili.com', 'youtube.com', 'www.youtube.com', 'www.youtube-nocookie.com'].includes(host)
  } catch {
    return false
  }
}

function externalVideoThumbnail(value: string) {
  try {
    const url = new URL(value)
    const host = url.hostname.toLowerCase()
    let videoID = ''
    if (host === 'youtu.be') videoID = url.pathname.split('/').filter(Boolean)[0] ?? ''
    if (host.includes('youtube.com')) {
      videoID = url.searchParams.get('v') ?? ''
      if (!videoID) {
        const parts = url.pathname.split('/').filter(Boolean)
        const marker = parts.findIndex((part) => part === 'embed' || part === 'shorts')
        if (marker >= 0) videoID = parts[marker + 1] ?? ''
      }
    }
    return /^[\w-]{6,20}$/.test(videoID) ? `https://i.ytimg.com/vi/${videoID}/hqdefault.jpg` : ''
  } catch {
    return ''
  }
}

function markMediaLoadError(id: string) {
  mediaLoadErrors.value.add(id)
}

function retryMediaLoad(id: string) {
  mediaLoadErrors.value.delete(id)
}

function messagePreview(item: ChatMessage | null) {
  if (!item) return '还没有消息'
  if (item.status === 'recalled') return '一条消息已撤回'
  if (item.body) return item.body
  const attachments = item.attachments ?? []
  if (attachments.some((attachment) => attachment.media_type === 'image')) return '[图片]'
  if (attachments.some((attachment) => attachment.media_type === 'video')) return '[视频]'
  if (attachments.some((attachment) => attachment.media_type === 'audio')) return '[音频]'
  return '还没有消息'
}

async function moderate(post: Post, action: 'approve' | 'hide') {
  try {
    await api.moderatePost(post.id, action)
    await loadContent()
  } catch (error) {
    contentError.value = error instanceof Error ? error.message : '审核失败'
  }
}

async function deletePost(post: Post) {
  if (!window.confirm(`确定删除${post.author.id === user.value?.id ? '这条回忆' : `${post.author.nickname}的这条回忆`}吗？删除后不会再出现在首页。`)) return
  try {
    await api.deletePost(post.id)
    feedPosts.value = feedPosts.value.filter((item) => item.id !== post.id)
    myPosts.value = myPosts.value.filter((item) => item.id !== post.id)
    pendingPosts.value = pendingPosts.value.filter((item) => item.id !== post.id)
    if (detailPost.value?.id === post.id) closeDetail()
  } catch (error) {
    contentError.value = error instanceof Error ? error.message : '删除回忆失败'
  }
}

async function initializeActivity() {
  stopActivityPolling()
  await Promise.allSettled([loadNotifications(), loadConversations()])
  activityTimer = window.setInterval(() => {
    if (!user.value || document.hidden) return
    void loadNotifications(true)
    void loadConversations(true)
    if (activeView.value === 'messages' && selectedConversationID.value) void loadMessages(true, true)
  }, 4000)
}

function handleVisibilityRefresh() {
  if (document.hidden || !user.value) return
  void loadNotifications(true)
  void loadConversations(true)
  if (activeView.value === 'messages' && selectedConversationID.value) void loadMessages(true, true)
}

function stopActivityPolling() {
  if (activityTimer) window.clearInterval(activityTimer)
  activityTimer = 0
}

async function loadNotifications(quiet = false) {
  if (!user.value) return
  if (!quiet) notificationLoading.value = true
  try {
    const page = await api.notifications({ limit: 30 })
    notifications.value = page.notifications
    notificationUnread.value = page.unread_count
  } catch (error) {
    if (!quiet) console.error(error)
  } finally {
    if (!quiet) notificationLoading.value = false
  }
}

async function toggleNotifications() {
  notificationsOpen.value = !notificationsOpen.value
  notificationFeedback.value = ''
  if (notificationsOpen.value) await loadNotifications()
}

function closeNotifications(restoreFocus = false) {
  if (!notificationsOpen.value) return
  notificationsOpen.value = false
  if (restoreFocus) nextTick(() => notificationTrigger.value?.focus())
}

function handleNotificationOutsidePointer(event: PointerEvent) {
  if (!notificationsOpen.value || !(event.target instanceof Node)) return
  if (notificationPanel.value?.contains(event.target) || notificationTrigger.value?.contains(event.target)) return
  closeNotifications()
}

function handleNotificationKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && notificationsOpen.value) closeNotifications(true)
}

async function markAllNotificationsRead() {
  await api.markAllNotificationsRead()
  notifications.value = notifications.value.map((item) => ({ ...item, read_at: item.read_at || new Date().toISOString() }))
  notificationUnread.value = 0
}

async function clearAllNotifications() {
  if (notifications.value.length === 0 || notificationClearing.value) return
  if (!window.confirm('确定清空全部通知吗？清空后无法恢复。')) return
  notificationClearing.value = true
  notificationFeedback.value = ''
  try {
    await api.clearNotifications()
    notifications.value = []
    notificationUnread.value = 0
    notificationFeedback.value = '通知已清空'
  } catch (error) {
    notificationFeedback.value = error instanceof Error ? error.message : '清空通知失败'
  } finally {
    notificationClearing.value = false
  }
}

async function revealMessage(message: ChatMessage) {
  await setView('messages')
  if (selectedConversationID.value !== message.conversation_id) await openConversation(message.conversation_id)
  else messageThreadOpen.value = true
  if (!chatMessages.value.some((candidate) => candidate.id === message.id)) {
    chatMessages.value = [...chatMessages.value, message].sort((left, right) => left.created_at.localeCompare(right.created_at) || left.id.localeCompare(right.id))
  }
  await nextTick()
  const messageIndex = chatMessages.value.findIndex((candidate) => candidate.id === message.id)
  const element = messageScroll.value?.querySelectorAll<HTMLElement>('.chat-message').item(messageIndex)
  element?.classList.add('notification-target')
  element?.setAttribute('tabindex', '-1')
  element?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  element?.focus({ preventScroll: true })
  if (messageHighlightTimer) window.clearTimeout(messageHighlightTimer)
  messageHighlightTimer = window.setTimeout(() => {
    element?.classList.remove('notification-target')
    element?.removeAttribute('tabindex')
  }, 2600)
}

async function openNotification(item: NotificationItem) {
  if (!item.read_at) {
    await api.markNotificationRead(item.id).catch(() => undefined)
    item.read_at = new Date().toISOString()
    notificationUnread.value = Math.max(0, notificationUnread.value - 1)
  }
  closeNotifications()
  if (item.target_type === 'message') {
    try {
      const response = await api.message(item.target_id)
      await revealMessage(response.message)
    } catch (error) {
      await setView('messages')
      messageError.value = error instanceof Error ? error.message : '无法定位这条消息'
    }
  } else if (item.target_type === 'conversation') {
    await setView('messages')
    await openConversation(item.target_id)
  } else if (item.target_type === 'post') {
    await setView('home')
    const post = feedPosts.value.find((candidate) => candidate.id === item.target_id)
    if (post) await openDetail(post)
  } else if (item.target_type === 'guestbook') {
    await setView('guestbook')
    if (item.target_id) selectGuestbookRecipient(item.target_id)
  }
}

async function loadConversations(quiet = false) {
  if (!user.value) return
  try {
    conversations.value = (await api.conversations()).conversations
  } catch (error) {
    if (!quiet) messageError.value = error instanceof Error ? error.message : '无法读取会话'
  }
}

async function loadMessageCenter() {
  messageError.value = ''
  if (messageMembers.value.length === 0) {
    try {
      messageMembers.value = (await api.members()).members.filter((member) => member.id !== user.value?.id)
    } catch (error) {
      messageError.value = error instanceof Error ? error.message : '无法读取成员'
    }
  }
  await loadConversations()
  if (!selectedConversationID.value && conversations.value.length) selectedConversationID.value = conversations.value[0]?.id ?? ''
  if (selectedConversationID.value) await loadMessages(true)
}

async function startDirectConversation(member: Member) {
  messageError.value = ''
  try {
    const response = await api.startDirectConversation(member.id)
    await loadConversations()
    if (!conversations.value.some((item) => item.id === response.conversation.id)) conversations.value.unshift(response.conversation)
    await openConversation(response.conversation.id)
  } catch (error) {
    messageError.value = error instanceof Error ? error.message : '无法开始私信'
  }
}

function chooseMessageMedia() {
  messageMediaInput.value?.click()
}

function handleMessageMediaInput(event: Event) {
  const input = event.target as HTMLInputElement
  addMessageMediaFiles(input.files ? [...input.files] : [])
  input.value = ''
}

function addMessageMediaFiles(files: File[]) {
  messageError.value = ''
  const available = 6 - messageMedia.value.length
  if (files.length > available) messageError.value = `每条消息最多添加 6 个附件，本次只加入前 ${Math.max(available, 0)} 个。`
  for (const file of files.slice(0, Math.max(available, 0))) {
    const kind = file.type.startsWith('image/') ? 'image' : file.type.startsWith('video/') ? 'video' : file.type.startsWith('audio/') ? 'audio' : null
    if (!kind) {
      messageError.value = `${file.name} 不是支持的图片、视频或音频格式。`
      continue
    }
    if (file.size <= 0 || file.size > (kind === 'video' ? 500 * 1024 ** 2 : 8 * 1024 ** 3)) {
      messageError.value = kind === 'video' ? `${file.name} 为空或超过 150 MiB，请先压缩视频后再上传。` : `${file.name} 为空或超过 8 GiB。`
      continue
    }
    const item: EditorMedia = { key: crypto.randomUUID(), file, name: file.name, size: file.size, kind, status: 'pending', progress: 0, error: '', persisted: false }
    messageMedia.value.push(item)
    if (kind === 'video') item.metadataPromise = readVideoMetadata(item)
    if (kind === 'audio') item.metadataPromise = readAudioMetadata(item)
  }
}

async function removeMessageMedia(item: EditorMedia) {
  if (item.status === 'uploading') return
  messageMedia.value = messageMedia.value.filter((candidate) => candidate.key !== item.key)
  if (!item.media) return
  try {
    await api.deleteMedia(item.media.id)
    mediaUsage.value = (await api.mediaUsage()).usage
  } catch (error) {
    messageError.value = error instanceof Error ? error.message : '附件已移除，但远端清理失败'
  }
}

async function discardMessageMedia() {
  const uploaded = messageMedia.value.flatMap((item) => item.media ? [item.media.id] : [])
  messageMedia.value = []
  if (uploaded.length) await Promise.allSettled(uploaded.map((id) => api.deleteMedia(id)))
}

async function openConversation(id: string) {
  if (messageBusy.value && selectedConversationID.value !== id) return
  if (selectedConversationID.value && selectedConversationID.value !== id) await discardMessageMedia()
  selectedConversationID.value = id
  messageThreadOpen.value = true
  messageBody.value = ''
  messageError.value = ''
  messageNextCursor.value = ''
  chatMessages.value = []
  await loadMessages(true)
}

async function loadMessages(reset = true, quiet = false) {
  const conversationID = selectedConversationID.value
  if (!conversationID) return
  const requestID = ++messageRequest
  if (!quiet) {
    messageLoadingRequest = requestID
    messageLoading.value = true
  }
  try {
    const previousLastID = chatMessages.value[chatMessages.value.length - 1]?.id
    const page = await api.messages(conversationID, { cursor: reset ? undefined : messageNextCursor.value || undefined, limit: 50 })
    if (requestID !== messageRequest || conversationID !== selectedConversationID.value) return
    const nextMessages = Array.isArray(page.messages) ? page.messages : []
    chatMessages.value = reset ? nextMessages : [...nextMessages, ...chatMessages.value]
    messageNextCursor.value = page.next_cursor ?? ''
    await api.markConversationRead(conversationID)
    conversations.value = conversations.value.map((item) => item.id === conversationID ? { ...item, unread_count: 0 } : item)
    if (reset && previousLastID !== chatMessages.value[chatMessages.value.length - 1]?.id) await nextTick(() => { if (messageScroll.value) messageScroll.value.scrollTop = messageScroll.value.scrollHeight })
  } catch (error) {
    if (!quiet && requestID === messageRequest) messageError.value = error instanceof Error ? error.message : '无法读取消息'
  } finally {
    if (!quiet && messageLoadingRequest === requestID) messageLoading.value = false
  }
}

async function sendChatMessage() {
  if (!selectedConversationID.value || (!messageBody.value.trim() && messageMedia.value.length === 0) || messageBusy.value) return
  const conversationID = selectedConversationID.value
  messageBusy.value = true
  messageError.value = ''
  try {
    for (const item of messageMedia.value) {
      if (item.status !== 'ready') await uploadEditorMedia(item)
    }
    const response = await api.sendMessage(conversationID, messageBody.value, messageMedia.value.flatMap((item) => item.media ? [item.media.id] : []))
    chatMessages.value.push(response.message)
    messageBody.value = ''
    messageMedia.value = []
    await loadConversations(true)
    await nextTick(() => { if (messageScroll.value) messageScroll.value.scrollTop = messageScroll.value.scrollHeight })
  } catch (error) {
    messageError.value = error instanceof Error ? error.message : '消息发送失败'
  } finally {
    messageBusy.value = false
  }
}

function canRecallMessage(item: ChatMessage) {
  return item.status === 'sent' && item.sender.id === user.value?.id && Date.now() - new Date(item.created_at).getTime() <= 2 * 60 * 1000
}

async function recallChatMessage(item: ChatMessage) {
  if (!window.confirm('确定撤回这条消息吗？')) return
  try {
    await api.recallMessage(item.id)
    chatMessages.value = chatMessages.value.map((candidate) => candidate.id === item.id ? { ...candidate, body: '', attachments: [], status: 'recalled', recalled_at: new Date().toISOString() } : candidate)
  } catch (error) {
    messageError.value = error instanceof Error ? error.message : '消息撤回失败'
  }
}

function closeMobileThread() {
  messageThreadOpen.value = false
}

function formatMessageTime(value: string) {
  const date = new Date(value)
  const today = new Date()
  return date.toDateString() === today.toDateString()
    ? date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
    : date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
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
    <div v-if="topNotice" class="top-notice" role="status" aria-live="polite"><Check :size="18" aria-hidden="true" />{{ topNotice }}</div>
    <header class="topbar">
      <button class="icon-button mobile-only" type="button" aria-label="打开菜单" @click="mobileMenuOpen = true"><Menu /></button>
      <a class="brand" href="#"><BookHeart :size="25" aria-hidden="true" /><span>妙妙小屋</span></a>
      <div class="top-actions">
        <button ref="notificationTrigger" class="icon-button notification-trigger" type="button" aria-label="查看通知" :aria-expanded="notificationsOpen" aria-controls="notification-panel" @click="toggleNotifications"><Bell /><span v-if="notificationUnread" class="notification-badge">{{ notificationUnread > 99 ? '99+' : notificationUnread }}</span></button>
        <section v-if="notificationsOpen" id="notification-panel" ref="notificationPanel" class="notification-panel" aria-label="站内通知">
          <header><div><strong>通知</strong><span>{{ notificationUnread }} 条未读</span></div><div class="notification-actions"><button v-if="notificationUnread" type="button" @click="markAllNotificationsRead"><CheckCheck :size="17" />全部已读</button><button v-if="notifications.length" class="clear-notifications" type="button" :disabled="notificationClearing" @click="clearAllNotifications"><Trash2 :size="17" />{{ notificationClearing ? '清空中…' : '清空' }}</button></div></header>
          <p v-if="notificationFeedback" class="notification-feedback" role="status">{{ notificationFeedback }}</p>
          <div v-if="notificationLoading" class="notification-empty" role="status">正在读取通知…</div><div v-else-if="notifications.length === 0" class="notification-empty">暂时没有新通知</div><button v-for="item in notifications" v-else :key="item.id" class="notification-row" :class="{ unread: !item.read_at }" type="button" @click="openNotification(item)"><span class="mini-avatar"><img v-if="item.actor && avatarVisible(item.actor.avatar_path)" :src="avatarURL(item.actor.avatar_path)" alt="" @error="item.actor && markAvatarBroken(item.actor.avatar_path)" /><span v-else>{{ item.actor?.nickname.slice(0, 1) || '妙' }}</span></span><span><strong>{{ item.title }}</strong><small v-if="item.body">{{ item.body }}</small><time :datetime="item.created_at">{{ formatMessageTime(item.created_at) }}</time></span></button>
        </section>
        <button class="avatar-button" type="button" aria-label="打开个人设置" @click="openProfile"><img v-if="avatarVisible(user.avatar_path)" :src="avatarURL(user.avatar_path)" alt="" @error="markAvatarBroken(user.avatar_path)" /><span v-else>{{ user.nickname.slice(0, 1) }}</span></button>
      </div>
    </header>

    <aside class="sidebar" :class="{ open: mobileMenuOpen }" aria-label="主要导航">
      <div class="mobile-menu-head"><span>浏览纪念册</span><button class="icon-button" type="button" aria-label="关闭菜单" @click="mobileMenuOpen = false"><X /></button></div>
      <nav><button v-for="item in nav" :key="item.label" type="button" :class="{ active: item.id === activeView }" :disabled="!item.available" :aria-current="item.id === activeView ? 'page' : undefined" :title="item.available ? item.label : `${item.label}正在开发`" @click="item.id && setView(item.id)"><component :is="item.icon" :size="20" aria-hidden="true" /><span>{{ item.label }}</span><small v-if="!item.available">开发中</small><small v-else-if="item.id === 'messages' && totalMessageUnread" class="nav-unread">{{ totalMessageUnread > 99 ? '99+' : totalMessageUnread }}</small></button></nav>
      <div class="sidebar-bottom"><button v-if="user.role === 'admin'" type="button" :class="{ active: activeView === 'management' }" :aria-current="activeView === 'management' ? 'page' : undefined" @click="setView('management')"><ShieldCheck :size="20" />管理</button><button type="button" @click="openProfile"><Settings :size="20" />设置</button></div>
    </aside>
    <button v-if="mobileMenuOpen" class="scrim" type="button" aria-label="关闭菜单" @click="mobileMenuOpen = false"></button>

    <main id="main-content" class="main-content" :class="{ 'messages-active': activeView === 'messages', 'message-thread-active': activeView === 'messages' && messageThreadOpen }">
      <template v-if="activeView === 'home'">
      <header class="page-heading"><div><p class="eyebrow">{{ greeting }}，{{ user.nickname }}</p><h1 tabindex="-1">最近的回忆</h1><p>记录一句话，也可以先存进草稿箱慢慢写。</p></div><button class="primary-button compact" type="button" @click="openComposer()"><Plus :size="19" />分享回忆</button></header>

      <p v-if="contentError" class="form-error content-alert" role="alert">{{ contentError }}</p>
      <section class="content-columns">
        <div class="feed-column">
          <button class="quick-composer" type="button" @click="openComposer()"><span class="mini-avatar"><img v-if="avatarVisible(user.avatar_path)" :src="avatarURL(user.avatar_path)" alt="" @error="markAvatarBroken(user.avatar_path)" /><span v-else>{{ user.nickname.slice(0, 1) }}</span></span><span>想记录今天的什么？</span><FileEdit :size="20" aria-hidden="true" /></button>
          <div v-if="contentLoading" class="content-empty" role="status"><span class="loader"></span><span>正在读取回忆…</span></div>
          <div v-else-if="feedPosts.length === 0" class="content-empty"><BookHeart :size="34" aria-hidden="true" /><h2>第一段回忆，等你来写</h2><p>发布后，会立即出现在所有室友的首页。</p><button class="secondary-button" type="button" @click="openComposer()">开始记录</button></div>
          <article v-for="post in feedPosts" v-else :key="post.id" class="post-card">
            <header><button class="profile-summary" type="button" @click="showPublicProfile(post.author.id)"><span class="mini-avatar"><img v-if="avatarVisible(post.author.avatar_path)" :src="avatarURL(post.author.avatar_path)" alt="" @error="markAvatarBroken(post.author.avatar_path)" /><span v-else>{{ post.author.nickname.slice(0, 1) }}</span></span><span><strong>{{ post.author.nickname }}</strong><small>{{ displayDate(post.published_at) }}</small></span></button></header>
            <p class="post-body">{{ post.body }}</p>
            <div v-if="post.media.length" class="post-media-grid">
              <figure v-for="(item, mediaIndex) in post.media.slice(0, 4)" :key="item.id">
                <div v-if="mediaLoadErrors.has(item.id)" class="media-unavailable"><AlertCircle :size="24" aria-hidden="true" /><strong>远端媒体暂时不可用</strong><button type="button" @click="retryMediaLoad(item.id)"><RotateCcw :size="17" />重新加载</button></div>
                <img v-else-if="item.media_type === 'image'" :src="mediaContentURL(item.id, item.has_preview)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" />
                <VideoPreview v-else :src="mediaContentURL(item.id)" :poster="mediaContentURL(item.id, true)" :title="item.original_filename" />
                <button v-if="mediaIndex === 3 && post.media.length > 4" class="remaining-media" type="button" :aria-label="`还有 ${post.media.length - 4} 张媒体，打开详情查看`" @click="openDetail(post)"><span>剩余 +{{ post.media.length - 4 }} 张</span><small>进入详情查看</small></button>
                <figcaption><span>{{ item.original_filename }}</span><small>{{ formatBytes(item.size_bytes) }}</small></figcaption>
              </figure>
            </div>
            <VideoPreview v-if="post.external_video_url" class="external-video-frame" :src="post.external_video_url" :poster="externalVideoThumbnail(post.external_video_url)" :title="post.body || '分享的外链视频'" external :embedded="isEmbeddedPlayer(post.external_video_url)" />
            <div v-if="post.tags.length" class="tag-row"><span v-for="tag in post.tags" :key="tag">#{{ tag }}</span></div>
            <footer><span v-if="post.content_date"><CalendarDays :size="17" />记录于 {{ displayDate(post.content_date) }}</span><button type="button" :class="{ liked: post.liked_by_me }" :aria-label="post.liked_by_me ? '取消点赞' : '点赞'" @click="togglePostLike(post)"><Heart :size="17" :fill="post.liked_by_me ? 'currentColor' : 'none'" />{{ post.like_count }}</button><button type="button" @click="openDetail(post)"><MessageCircle :size="17" />{{ post.comment_count }} 条评论</button><button v-if="post.author.id === user.id || user.role === 'admin'" type="button" @click="openComposer(post)"><FileEdit :size="17" />编辑</button><button v-if="post.author.id === user.id || user.role === 'admin'" class="post-delete-link" type="button" :aria-label="`删除${post.author.nickname}的回忆`" @click="deletePost(post)"><Trash2 :size="17" />删除</button><button type="button" class="detail-link" @click="openDetail(post)">查看详情</button></footer>
          </article>
          <div v-if="feedNextCursor || feedLoadingMore" ref="contentLoadSentinel" class="scroll-load-sentinel" role="status"><span v-if="feedLoadingMore" class="loader"></span><span>{{ feedLoadingMore ? '正在加载更多回忆…' : '继续向下滑动加载更多' }}</span></div>
        </div>

        <aside class="content-rail" aria-label="我的投稿">
          <section class="rail-card"><header><div><p class="eyebrow">我的内容</p><h2>草稿与回忆</h2></div><span>{{ myPosts.length }}</span></header><div v-if="myPosts.length === 0" class="rail-empty">还没有内容</div><button v-for="post in myPosts.slice(0, 6)" :key="post.id" class="draft-row" type="button" :disabled="post.status !== 'draft' && post.status !== 'published'" @click="(post.status === 'draft' || post.status === 'published') && openComposer(post)"><span>{{ post.body || '无标题回忆' }}</span><small :data-status="post.status">{{ statusLabel(post.status) }}</small></button></section>
        </aside>
      </section>
      </template>

      <template v-else-if="activeView === 'detail' && detailPost">
        <article class="detail-page" aria-labelledby="detail-title">
          <header class="detail-page-header">
            <button class="detail-back" type="button" @click="closeDetail"><ChevronLeft :size="20" aria-hidden="true" />返回上一页</button>
            <div class="detail-heading-copy">
              <p class="eyebrow">{{ displayDate(detailPost.content_date || detailPost.published_at) }}</p>
              <h1 id="detail-title" tabindex="-1">{{ detailPost.author.nickname }}的回忆</h1>
              <button class="detail-author" type="button" @click="showPublicProfile(detailPost.author.id)"><span class="mini-avatar"><img v-if="avatarVisible(detailPost.author.avatar_path)" :src="avatarURL(detailPost.author.avatar_path)" alt="" @error="markAvatarBroken(detailPost.author.avatar_path)" /><span v-else>{{ detailPost.author.nickname.slice(0, 1) }}</span></span><span><strong>{{ detailPost.author.nickname }}</strong><small>发布于 {{ displayDate(detailPost.published_at) }}</small></span></button>
            </div>
            <div class="detail-page-tools"><button type="button" :class="{ liked: detailPost.liked_by_me }" :aria-label="detailPost.liked_by_me ? '取消点赞' : '点赞'" @click="togglePostLike(detailPost)"><Heart :size="19" :fill="detailPost.liked_by_me ? 'currentColor' : 'none'" />{{ detailPost.like_count }}</button><button v-if="detailPost.author.id === user.id || user.role === 'admin'" type="button" @click="openComposer(detailPost)"><FileEdit :size="18" />编辑</button></div>
          </header>

          <p v-if="detailPost.body" class="detail-body">{{ detailPost.body }}</p>
          <div v-if="detailPost.tags.length" class="tag-row detail-tags"><span v-for="tag in detailPost.tags" :key="tag">#{{ tag }}</span></div>
          <p v-if="detailError" class="form-error content-alert" role="alert">{{ detailError }}</p>

          <section v-if="detailPost.media.length || detailPost.external_video_url" class="detail-media-section" aria-labelledby="detail-media-title">
            <header><div><p class="eyebrow">照片与视频</p><h2 id="detail-media-title">这段回忆的媒体</h2></div><span>{{ detailPost.media.length + (detailPost.external_video_url ? 1 : 0) }} 项</span></header>
            <div class="detail-gallery">
              <figure v-for="item in detailPost.media" :key="item.id">
                <div v-if="mediaLoadErrors.has(item.id)" class="media-unavailable"><AlertCircle :size="24" /><strong>暂时无法读取</strong><button type="button" @click="retryMediaLoad(item.id)"><RotateCcw :size="17" />重试</button></div>
                <a v-else-if="item.media_type === 'image'" :href="mediaContentURL(item.id)" target="_blank" rel="noopener" :aria-label="`查看原图：${item.original_filename}`"><img :src="mediaContentURL(item.id, item.has_preview)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" /></a>
                <VideoPreview v-else :src="mediaContentURL(item.id)" :poster="mediaContentURL(item.id, true)" :title="item.original_filename" />
                <figcaption><span :title="item.original_filename">{{ item.original_filename }}</span><small>{{ formatBytes(item.size_bytes) }}</small></figcaption>
              </figure>
              <figure v-if="detailPost.external_video_url" class="detail-external-video"><VideoPreview :src="detailPost.external_video_url" :poster="externalVideoThumbnail(detailPost.external_video_url)" :title="detailPost.body || '分享的外链视频'" external :embedded="isEmbeddedPlayer(detailPost.external_video_url)" /><figcaption><span>外链视频</span><small>播放时从原网站加载</small></figcaption></figure>
            </div>
          </section>
          <section v-else class="detail-media-empty"><Camera :size="30" aria-hidden="true" /><div><strong>这段回忆没有媒体</strong><span>文字也值得被完整保存。</span></div></section>

          <section class="comments-section detail-comments" aria-labelledby="comments-title">
            <header><div><p class="eyebrow">室友回应</p><h2 id="comments-title">评论</h2></div><span><MessageCircle :size="18" />{{ detailPost.comment_count }} 条</span></header>
            <div v-if="detailLoading" class="comment-empty" role="status"><span class="loader"></span><span>正在读取评论…</span></div><div v-else-if="detailComments.length === 0" class="comment-empty">还没有评论，来留下第一句话吧。</div>
            <article v-for="comment in detailComments" :key="comment.id" class="comment-row"><span class="mini-avatar"><img v-if="avatarVisible(comment.author.avatar_path)" :src="avatarURL(comment.author.avatar_path)" alt="" @error="markAvatarBroken(comment.author.avatar_path)" /><span v-else>{{ comment.author.nickname.slice(0, 1) }}</span></span><div><header><strong>{{ comment.author.nickname }}</strong><time :datetime="comment.created_at">{{ new Date(comment.created_at).toLocaleString('zh-CN') }}</time></header><p>{{ comment.body }}</p></div><button v-if="comment.author.id === user.id || user.role === 'admin'" type="button" :aria-label="`删除${comment.author.nickname}的评论`" @click="removeComment(comment)"><Trash2 :size="17" /></button></article>
            <form class="comment-form" @submit.prevent="submitComment"><label for="comment-body">写评论</label><textarea id="comment-body" v-model="commentBody" rows="4" maxlength="2000" required placeholder="说点什么…"></textarea><div><small>{{ commentBody.length }} / 2000</small><button class="primary-button compact" type="submit" :disabled="commentBusy || !commentBody.trim()"><Send :size="17" />{{ commentBusy ? '发送中…' : '发表评论' }}</button></div></form>
          </section>
        </article>
      </template>

      <template v-else-if="activeView === 'timeline'">
        <header class="page-heading"><div><p class="eyebrow">按发生日期整理</p><h1 tabindex="-1">我们的时间线</h1><p>没有填写内容日期的回忆，会按照发布日期归档。</p></div><button class="primary-button compact" type="button" @click="openComposer()"><Plus :size="19" />补一段回忆</button></header>
        <div v-if="timelineGroups.length === 0" class="content-empty"><Sparkles :size="34" aria-hidden="true" /><h2>时间线还是空的</h2><p>发布第一段回忆后，它会出现在这里。</p></div>
        <div v-else class="timeline-list">
          <section v-for="group in timelineGroups" :key="group.key" class="timeline-group"><header><span></span><h2>{{ group.label }}</h2><small>{{ group.posts.length }} 条</small></header><div>
            <article v-for="post in group.posts" :key="post.id" class="timeline-entry"><time :datetime="(post.content_date || post.published_at || post.created_at)">{{ displayDate(post.content_date || post.published_at || post.created_at) }}</time><div><button class="text-profile-link" type="button" @click="showPublicProfile(post.author.id)">{{ post.author.nickname }}</button><p>{{ post.body || '分享了媒体回忆' }}</p><div v-if="post.media.length || post.external_video_url" class="timeline-media"><template v-for="item in post.media.slice(0, post.external_video_url ? 3 : 4)" :key="item.id"><img v-if="item.media_type === 'image'" :src="mediaContentURL(item.id, item.has_preview)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" /><VideoPreview v-else :src="mediaContentURL(item.id)" :poster="mediaContentURL(item.id, true)" :title="item.original_filename" square /></template><VideoPreview v-if="post.external_video_url" :src="post.external_video_url" :poster="externalVideoThumbnail(post.external_video_url)" :title="post.body || '分享的外链视频'" external :embedded="isEmbeddedPlayer(post.external_video_url)" square /></div><div v-if="post.tags.length" class="tag-row"><span v-for="tag in post.tags" :key="tag">#{{ tag }}</span></div><button v-if="post.author.id === user.id || user.role === 'admin'" class="inline-edit" type="button" @click="openComposer(post)"><FileEdit :size="15" />编辑这条回忆</button></div></article>
          </div></section>
        </div>
        <div v-if="timelineVisibleCount < feedPosts.length || feedNextCursor || feedLoadingMore" ref="contentLoadSentinel" class="scroll-load-sentinel" role="status"><span v-if="feedLoadingMore" class="loader"></span><span>{{ feedLoadingMore ? '正在展开时间线…' : '继续向下滑动查看更多' }}</span></div>
      </template>

      <template v-else-if="activeView === 'wall'">
        <header class="page-heading"><div><p class="eyebrow">照片与视频</p><h1 tabindex="-1">宿舍照片墙</h1><p>按最近发布排序，当前已加载 {{ wallItems.length }} 个媒体文件。</p></div><button class="primary-button compact" type="button" @click="openComposer()"><Plus :size="19" />添加照片</button></header>
        <div v-if="wallItems.length === 0" class="content-empty"><Camera :size="34" aria-hidden="true" /><h2>照片墙还没有内容</h2><p>发布带照片或视频的回忆后，就会陈列在这里。</p></div>
        <div v-else class="photo-wall">
          <figure v-for="item in visibleWallItems" :key="item.key">
            <div v-if="item.media && mediaLoadErrors.has(item.media.id)" class="media-unavailable"><AlertCircle :size="24" /><strong>暂时无法读取</strong><button type="button" @click="retryMediaLoad(item.media.id)"><RotateCcw :size="17" />重试</button></div>
            <a v-else-if="item.media?.media_type === 'image'" :href="mediaContentURL(item.media.id)" target="_blank" rel="noopener" :aria-label="`查看原图：${item.media.original_filename}`"><img :src="mediaContentURL(item.media.id, item.media.has_preview)" :alt="item.media.original_filename" loading="lazy" @error="markMediaLoadError(item.media.id)" /></a>
            <VideoPreview v-else-if="item.media" :src="mediaContentURL(item.media.id)" :poster="mediaContentURL(item.media.id, true)" :title="item.media.original_filename" />
            <VideoPreview v-else :src="item.externalVideoURL" :poster="externalVideoThumbnail(item.externalVideoURL)" :title="item.post.body || '分享的外链视频'" external :embedded="isEmbeddedPlayer(item.externalVideoURL)" />
            <figcaption><div><button class="text-profile-link" type="button" @click="showPublicProfile(item.post.author.id)">{{ item.post.author.nickname }}</button><span>{{ item.post.body || item.media?.original_filename || '外链视频' }}</span><button v-if="item.post.author.id === user.id || user.role === 'admin'" class="inline-edit" type="button" @click="openComposer(item.post)"><FileEdit :size="14" />编辑</button></div><time :datetime="item.post.content_date || item.post.published_at || item.post.created_at">{{ displayDate(item.post.content_date || item.post.published_at || item.post.created_at) }}</time></figcaption>
          </figure>
        </div>
        <div v-if="wallVisibleCount < wallItems.length || feedNextCursor || feedLoadingMore" ref="contentLoadSentinel" class="scroll-load-sentinel" role="status"><span v-if="feedLoadingMore" class="loader"></span><span>{{ feedLoadingMore ? '正在加载更多照片…' : '继续向下滑动查看更多' }}</span></div>
      </template>

      <template v-else-if="activeView === 'messages'">
        <header class="page-heading"><div><p class="eyebrow">只在室友之间</p><h1 tabindex="-1">消息</h1><p>宿舍群聊和一对一私信都会保存在这里。</p></div></header>
        <p v-if="messageError" class="form-error content-alert" role="alert">{{ messageError }}</p>
        <div class="message-layout" :class="{ 'thread-open': messageThreadOpen }">
          <aside class="conversation-panel" aria-label="会话列表">
            <section class="direct-starters" aria-labelledby="direct-starters-title"><header><strong id="direct-starters-title">发起私信</strong><MailPlus :size="18" /></header><div><button v-for="member in messageMembers" :key="member.id" type="button" :title="`给${member.nickname}发私信`" @click="startDirectConversation(member)"><span class="mini-avatar"><img v-if="avatarVisible(member.avatar_path)" :src="avatarURL(member.avatar_path)" alt="" @error="markAvatarBroken(member.avatar_path)" /><span v-else>{{ member.nickname.slice(0, 1) }}</span></span><span>{{ member.nickname }}</span></button></div></section>
            <nav class="conversation-list" aria-label="已有会话"><button v-for="conversation in conversations" :key="conversation.id" type="button" :disabled="messageBusy && selectedConversationID !== conversation.id" :class="{ active: selectedConversationID === conversation.id }" :aria-current="selectedConversationID === conversation.id ? 'true' : undefined" @click="openConversation(conversation.id)"><span class="conversation-avatar"><span v-if="conversation.type === 'group'"><Users :size="21" /></span><template v-else><img v-if="conversation.peer && avatarVisible(conversation.peer.avatar_path)" :src="avatarURL(conversation.peer.avatar_path)" alt="" @error="conversation.peer && markAvatarBroken(conversation.peer.avatar_path)" /><span v-else>{{ conversation.peer?.nickname.slice(0, 1) }}</span></template></span><span class="conversation-copy"><strong>{{ conversation.title }}</strong><small>{{ messagePreview(conversation.last_message) }}</small></span><span v-if="conversation.unread_count" class="conversation-unread">{{ conversation.unread_count > 99 ? '99+' : conversation.unread_count }}</span></button></nav>
          </aside>

          <section v-if="selectedConversation" class="message-thread" :aria-labelledby="`conversation-${selectedConversation.id}`">
            <header><button class="icon-button thread-back" type="button" aria-label="返回会话列表" @click="closeMobileThread"><ChevronLeft /></button><span class="conversation-avatar"><Users v-if="selectedConversation.type === 'group'" :size="21" /><template v-else><img v-if="selectedConversation.peer && avatarVisible(selectedConversation.peer.avatar_path)" :src="avatarURL(selectedConversation.peer.avatar_path)" alt="" @error="selectedConversation.peer && markAvatarBroken(selectedConversation.peer.avatar_path)" /><span v-else>{{ selectedConversation.peer?.nickname.slice(0, 1) }}</span></template></span><div><strong :id="`conversation-${selectedConversation.id}`">{{ selectedConversation.title }}</strong><small>{{ selectedConversation.type === 'group' ? '所有室友可见' : '仅会话双方可见' }}</small></div></header>
            <div ref="messageScroll" class="message-scroll" role="log" aria-live="polite" aria-relevant="additions"><button v-if="messageNextCursor" class="message-more" type="button" :disabled="messageLoading" @click="loadMessages(false)">{{ messageLoading ? '读取中…' : '查看更早消息' }}</button><div v-if="messageLoading && chatMessages.length === 0" class="message-placeholder" role="status"><span class="loader"></span><span>正在读取消息…</span></div><div v-else-if="chatMessages.length === 0" class="message-placeholder"><MessageCircle :size="32" /><strong>从第一句话开始</strong><span>消息只对这个会话中的成员可见。</span></div><article v-for="item in chatMessages" v-else :key="item.id" class="chat-message" :class="{ mine: item.sender.id === user.id, recalled: item.status === 'recalled' }"><button v-if="item.sender.id !== user.id" class="mini-avatar" type="button" :aria-label="`查看${item.sender.nickname}的资料`" @click="showPublicProfile(item.sender.id)"><img v-if="avatarVisible(item.sender.avatar_path)" :src="avatarURL(item.sender.avatar_path)" alt="" @error="markAvatarBroken(item.sender.avatar_path)" /><span v-else>{{ item.sender.nickname.slice(0, 1) }}</span></button><div><header><button v-if="item.sender.id !== user.id" class="message-author-link" type="button" @click="showPublicProfile(item.sender.id)">{{ item.sender.nickname }}</button><time :datetime="item.created_at">{{ formatMessageTime(item.created_at) }}</time></header><div v-if="item.status === 'sent' && (item.attachments ?? []).length" class="chat-attachments"><figure v-for="attachment in (item.attachments ?? [])" :key="attachment.id" class="chat-attachment" :class="attachment.media_type"><div v-if="mediaLoadErrors.has(attachment.id)" class="message-media-unavailable"><AlertCircle :size="21" /><span>附件暂时无法读取</span><button type="button" @click="retryMediaLoad(attachment.id)"><RotateCcw :size="15" />重试</button></div><a v-else-if="attachment.media_type === 'image'" :href="mediaContentURL(attachment.id)" target="_blank" rel="noopener"><img :src="mediaContentURL(attachment.id, attachment.has_preview)" :alt="attachment.original_filename" loading="lazy" @error="markMediaLoadError(attachment.id)" /></a><video v-else-if="attachment.media_type === 'video'" :src="mediaContentURL(attachment.id)" controls preload="none" playsinline :aria-label="attachment.original_filename" @error="markMediaLoadError(attachment.id)"></video><audio v-else :src="mediaContentURL(attachment.id)" controls preload="metadata" :aria-label="attachment.original_filename" @error="markMediaLoadError(attachment.id)"></audio><figcaption><span :title="attachment.original_filename">{{ attachment.original_filename }}</span><small>{{ formatBytes(attachment.size_bytes) }}</small></figcaption></figure></div><p v-if="item.status === 'recalled' || item.body">{{ item.status === 'recalled' ? `${item.sender.id === user.id ? '你' : item.sender.nickname}撤回了一条消息` : item.body }}</p><button v-if="canRecallMessage(item)" type="button" @click="recallChatMessage(item)"><Undo2 :size="15" />撤回</button></div></article></div>
            <form class="message-composer" @submit.prevent="sendChatMessage"><input ref="messageMediaInput" class="visually-hidden" type="file" accept="image/*,video/*,audio/*" multiple @change="handleMessageMediaInput" /><div v-if="messageMedia.length" class="message-attachment-queue" aria-label="待发送附件"><article v-for="item in messageMedia" :key="item.key"><span class="message-file-icon"><Image v-if="item.kind === 'image'" :size="20" /><Film v-else-if="item.kind === 'video'" :size="20" /><Music v-else :size="20" /></span><span><strong :title="item.name">{{ item.name }}</strong><small v-if="item.status === 'uploading'">上传中 {{ item.progress }}%</small><small v-else-if="item.status === 'error'" class="field-error">{{ item.error }}</small><small v-else>{{ formatBytes(item.size) }}</small></span><button type="button" :disabled="item.status === 'uploading'" :aria-label="`移除 ${item.name}`" @click="removeMessageMedia(item)"><X :size="17" /></button><progress v-if="item.status === 'uploading'" :value="item.progress" max="100">{{ item.progress }}%</progress></article></div><label class="visually-hidden" for="message-body">输入消息</label><textarea id="message-body" v-model="messageBody" rows="2" maxlength="4000" placeholder="输入消息，Ctrl + Enter 发送" @keydown.ctrl.enter.prevent="sendChatMessage"></textarea><div class="message-composer-actions"><div><button class="attachment-button" type="button" :disabled="messageBusy || messageMedia.length >= 6" @click="chooseMessageMedia"><Paperclip :size="18" />添加附件</button><small>{{ messageMedia.length }} / 6 个附件 · {{ messageBody.length }} / 4000 字</small></div><button class="primary-button compact" type="submit" :disabled="messageBusy || messageMediaUploading || (!messageBody.trim() && messageMedia.length === 0)"><Send :size="17" />{{ messageBusy ? '发送中…' : '发送' }}</button></div></form>
          </section>
          <section v-else class="message-welcome"><MessageCircle :size="38" /><h2>选择一个会话</h2><p>可以进入宿舍群聊，或从左侧选择一位室友开始私信。</p></section>
        </div>
      </template>

      <template v-else-if="activeView === 'management'">
        <header class="page-heading"><div><p class="eyebrow">仅管理员可见</p><h1 tabindex="-1">管理中心</h1><p>管理室友账号、公共群聊与远端媒体；私信始终只对会话双方可见。</p></div></header>
        <nav class="management-tabs" aria-label="管理功能">
          <button type="button" :class="{ active: managementTab === 'users' }" :aria-current="managementTab === 'users' ? 'page' : undefined" @click="selectManagementTab('users')"><Users :size="19" />用户 <span>{{ adminUsers.length || '' }}</span></button>
          <button type="button" :class="{ active: managementTab === 'messages' }" :aria-current="managementTab === 'messages' ? 'page' : undefined" @click="selectManagementTab('messages')"><MessageCircle :size="19" />群聊消息 <span>{{ adminMessages.length || '' }}</span></button>
          <button type="button" :class="{ active: managementTab === 'media' }" :aria-current="managementTab === 'media' ? 'page' : undefined" @click="selectManagementTab('media')"><Image :size="19" />媒体 <span>{{ adminMedia.length || '' }}</span></button>
          <button type="button" :class="{ active: managementTab === 'invites' }" :aria-current="managementTab === 'invites' ? 'page' : undefined" @click="selectManagementTab('invites')"><MailPlus :size="19" />邀请码</button>
          <button type="button" :class="{ active: managementTab === 'backup' }" :aria-current="managementTab === 'backup' ? 'page' : undefined" @click="selectManagementTab('backup')"><DatabaseBackup :size="19" />备份</button>
        </nav>

        <p v-if="adminFeedback" class="management-feedback" :class="{ error: adminFeedback.includes('失败') || adminFeedback.includes('无法') || adminFeedback.includes('必须') || adminFeedback.includes('不能') }" role="status" aria-live="polite">{{ adminFeedback }}</p>

        <section v-if="managementTab === 'invites'" class="admin-card management-card" aria-labelledby="invite-manager-title">
          <div><p class="eyebrow">成员邀请</p><h2 id="invite-manager-title">批量邀请室友</h2><p class="muted">每个邀请码 7 天内有效，只能使用一次；一次最多生成 20 个。</p></div>
          <div class="invite-actions">
            <div class="invite-count"><label for="invite-count">生成数量</label><select id="invite-count" v-model.number="inviteCount"><option v-for="count in inviteCountOptions" :key="count" :value="count">{{ count }} 个</option></select></div>
            <textarea v-if="inviteCodes.length" class="invite-codes" :value="inviteCodes.join('\n')" readonly rows="5" aria-label="生成的邀请码" @focus="($event.target as HTMLTextAreaElement).select()"></textarea>
            <div class="invite-buttons"><button class="secondary-button" type="button" :disabled="inviteBusy" @click="createInvite">{{ inviteBusy ? '生成中…' : `生成 ${inviteCount} 个邀请码` }}</button><button v-if="inviteCodes.length" class="secondary-button" type="button" @click="copyInvites"><Check v-if="inviteCopyStatus === 'copied'" :size="18" /><Copy v-else :size="18" />{{ inviteCopyStatus === 'copied' ? '已复制' : inviteCopyStatus === 'error' ? '复制失败，请手动选择' : '复制全部' }}</button></div>
            <p class="copy-status" aria-live="polite">{{ inviteCopyStatus === 'copied' ? `已复制 ${inviteCodes.length} 个邀请码` : managementMessage }}</p>
          </div>
        </section>

        <section v-else class="management-panel" :aria-labelledby="`management-${managementTab}-title`">
          <header class="management-panel-head">
            <div><p class="eyebrow">{{ managementTab === 'users' ? '账号与权限' : managementTab === 'messages' ? '仅公共群聊' : managementTab === 'media' ? '存储与引用' : '数据安全' }}</p><h2 :id="`management-${managementTab}-title`">{{ managementTab === 'users' ? '用户管理' : managementTab === 'messages' ? '消息管理' : managementTab === 'media' ? '媒体管理' : '导出备份' }}</h2><p>{{ managementTab === 'users' ? '调整角色与账号状态；停用账号会撤销其全部登录会话。' : managementTab === 'messages' ? '只显示 3048 宿舍群聊，管理员无法读取任何私信。' : managementTab === 'media' ? '显示媒体元数据与引用状态；只有未被引用的文件可以永久删除。' : '生成经过 SQLite 完整性校验的业务数据快照，并下载到管理员设备。' }}</p></div>
            <button v-if="managementTab !== 'backup'" class="icon-button" type="button" :disabled="adminLoading" aria-label="刷新当前管理数据" @click="loadManagementSection"><RefreshCw :size="20" /></button>
          </header>

          <form v-if="managementTab === 'users'" class="management-filters" role="search" @submit.prevent="loadManagementSection">
            <label><span>搜索用户</span><span class="search-field"><Search :size="18" /><input v-model.trim="adminUserFilters.search" type="search" placeholder="昵称、用户名或邮箱" /></span></label>
            <label><span>角色</span><select v-model="adminUserFilters.role"><option value="">全部角色</option><option value="admin">管理员</option><option value="member">成员</option></select></label>
            <label><span>状态</span><select v-model="adminUserFilters.status"><option value="">全部状态</option><option value="active">启用</option><option value="disabled">已停用</option></select></label>
            <button class="secondary-button" type="submit" :disabled="adminLoading"><Search :size="17" />查询</button>
          </form>
          <form v-else-if="managementTab === 'messages'" class="management-filters compact-filters" role="search" @submit.prevent="loadManagementSection">
            <label><span>搜索群聊</span><span class="search-field"><Search :size="18" /><input v-model.trim="adminMessageFilters.search" type="search" placeholder="消息内容或发送者" /></span></label>
            <label><span>状态</span><select v-model="adminMessageFilters.status"><option value="">全部状态</option><option value="sent">正常</option><option value="recalled">已撤回</option></select></label>
            <button class="secondary-button" type="submit" :disabled="adminLoading"><Search :size="17" />查询</button>
          </form>
          <form v-else-if="managementTab === 'media'" class="management-filters" role="search" @submit.prevent="loadManagementSection">
            <label><span>搜索媒体</span><span class="search-field"><Search :size="18" /><input v-model.trim="adminMediaFilters.search" type="search" placeholder="文件名或上传者" /></span></label>
            <label><span>类型</span><select v-model="adminMediaFilters.type"><option value="">全部类型</option><option value="image">图片</option><option value="video">视频</option><option value="audio">音频</option></select></label>
            <label><span>状态</span><select v-model="adminMediaFilters.status"><option value="">全部状态</option><option value="ready">可用</option><option value="uploading">上传中</option><option value="unavailable">不可用</option></select></label>
            <button class="secondary-button" type="submit" :disabled="adminLoading"><Search :size="17" />查询</button>
          </form>

          <div v-if="adminLoading" class="management-empty" role="status"><span class="loader"></span><span>正在读取管理数据…</span></div>
          <div v-else-if="managementTab === 'users'" class="management-list">
            <p v-if="adminUsers.length === 0" class="management-empty">没有符合条件的用户。</p>
            <article v-for="item in adminUsers" v-else :key="item.id" class="management-row user-management-row">
              <span class="mini-avatar"><img v-if="avatarVisible(item.avatar_path)" :src="avatarURL(item.avatar_path)" alt="" @error="markAvatarBroken(item.avatar_path)" /><span v-else>{{ item.nickname.slice(0, 1) }}</span></span>
              <div class="management-row-copy"><strong>{{ item.nickname }} <span v-if="item.id === user.id" class="self-badge">当前账号</span></strong><small>@{{ item.username }} · {{ item.email }}</small><small>加入于 {{ displayDate(item.created_at) }} · {{ item.active_session_count }} 个活跃会话</small></div>
              <label><span>角色</span><select v-model="item.role" :disabled="item.id === user.id || adminActionID === item.id"><option value="admin">管理员</option><option value="member">成员</option></select></label>
              <label><span>状态</span><select v-model="item.status" :disabled="item.id === user.id || adminActionID === item.id"><option value="active">启用</option><option value="disabled">停用</option></select></label>
              <button class="secondary-button row-action" type="button" :disabled="item.id === user.id || adminActionID === item.id" @click="saveAdminUser(item)">{{ adminActionID === item.id ? '保存中…' : '保存' }}</button>
            </article>
          </div>
          <div v-else-if="managementTab === 'messages'" class="management-list">
            <p v-if="adminMessages.length === 0" class="management-empty">没有符合条件的群聊消息。</p>
            <article v-for="item in adminMessages" v-else :key="item.id" class="management-row message-management-row">
              <span class="mini-avatar"><img v-if="avatarVisible(item.sender.avatar_path)" :src="avatarURL(item.sender.avatar_path)" alt="" @error="markAvatarBroken(item.sender.avatar_path)" /><span v-else>{{ item.sender.nickname.slice(0, 1) }}</span></span>
              <div class="management-row-copy"><strong>{{ item.sender.nickname }} <small>@{{ item.sender.username }}</small></strong><p>{{ item.status === 'recalled' ? '这条消息已撤回' : (item.body || `包含 ${item.attachment_count} 个附件`) }}</p><small>{{ item.conversation_title }} · {{ new Date(item.created_at).toLocaleString('zh-CN') }}<template v-if="item.attachment_count"> · {{ item.attachment_count }} 个附件</template></small></div>
              <span class="status-badge" :data-status="item.status">{{ item.status === 'sent' ? '正常' : '已撤回' }}</span>
              <button class="danger-action" type="button" :disabled="item.status !== 'sent' || adminActionID === item.id" @click="removeAdminMessage(item)"><Trash2 :size="17" />{{ adminActionID === item.id ? '移除中…' : '移除' }}</button>
            </article>
          </div>
          <div v-else-if="managementTab === 'media'" class="management-list">
            <p v-if="adminMedia.length === 0" class="management-empty">没有符合条件的媒体。</p>
            <article v-for="item in adminMedia" v-else :key="item.id" class="management-row media-management-row">
              <span class="media-admin-icon"><Image v-if="item.media_type === 'image'" :size="21" /><Film v-else-if="item.media_type === 'video'" :size="21" /><Music v-else :size="21" /></span>
              <div class="management-row-copy"><strong :title="item.original_filename">{{ item.original_filename }}</strong><small>{{ adminMediaTypeLabel(item.media_type) }} · {{ formatBytes(item.size_bytes) }} · {{ item.owner_nickname }} (@{{ item.owner_username }})</small><small>上传于 {{ new Date(item.created_at).toLocaleString('zh-CN') }} · {{ item.reference_count ? `被 ${item.reference_count} 处内容引用` : '未被引用' }}</small></div>
              <span class="status-badge" :data-status="item.status">{{ item.status === 'ready' ? '可用' : item.status === 'uploading' ? '上传中' : '不可用' }}</span>
              <button class="danger-action" type="button" :disabled="item.reference_count > 0 || adminActionID === item.id" :title="item.reference_count > 0 ? '请先移除引用该文件的内容' : '永久删除未被引用的媒体'" @click="removeAdminMedia(item)"><Trash2 :size="17" />{{ adminActionID === item.id ? '删除中…' : '删除' }}</button>
            </article>
          </div>
          <div v-else class="backup-section">
            <article class="backup-export-card">
              <span class="backup-export-icon"><DatabaseBackup :size="30" /></span>
              <div>
                <h3>下载业务数据备份</h3>
                <p>包含账号、个人资料、帖子、留言、评论、群聊与私信、通知、媒体索引和审计记录。</p>
                <p class="backup-warning"><AlertCircle :size="18" />不包含 AList / 夸克网盘中的原始图片、视频和音频；这些文件仍需通过网盘侧单独保障。</p>
              </div>
              <button class="primary-button backup-download-button" type="button" :disabled="backupBusy" @click="exportAdminBackup"><Download :size="18" />{{ backupBusy ? '正在生成并校验…' : '生成并下载备份' }}</button>
            </article>
            <div class="backup-guidance" aria-label="备份操作说明">
              <article><strong>1. 下载并保存</strong><p>浏览器会下载一个 SQLite 数据库文件。请确认文件已落盘，不要只保留在下载临时目录。</p></article>
              <article><strong>2. 妥善保管</strong><p>备份含邮箱、密码哈希、会话及私信等敏感信息。请勿分享，建议放入加密磁盘或加密压缩包。</p></article>
              <article><strong>3. 维护前执行</strong><p>每次部署、升级或批量管理数据前先导出一份；恢复操作需在维护窗口内停止服务后进行。</p></article>
            </div>
          </div>
        </section>
      </template>

      <template v-else>
        <header class="page-heading"><div><p class="eyebrow">写给我们，也写给某个人</p><h1 tabindex="-1">宿舍留言册</h1><p>每句话都只在室友之间可见，接收者可以隐藏并随时恢复留言。</p></div><button v-if="canViewHiddenGuestbook" class="secondary-button" type="button" :aria-pressed="guestbookStatus === 'hidden'" @click="toggleHiddenGuestbook"><ArchiveRestore :size="18" />{{ guestbookStatus === 'hidden' ? '返回公开留言' : '查看已隐藏' }}</button></header>
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

            <form v-if="guestbookStatus === 'visible'" class="guestbook-composer" @submit.prevent="submitGuestbookEntry">
              <label for="guestbook-body">{{ selectedGuestbookMember ? `给${selectedGuestbookMember.nickname}留言` : '给宿舍留言' }}</label>
              <textarea id="guestbook-body" v-model="guestbookBody" rows="4" maxlength="2000" placeholder="写一句以后再看到还会想起今天的话…"></textarea>
              <div class="guestbook-composer-meta"><small>{{ guestbookBody.length }} / 2000</small><span>最多 6 个附件</span></div>
              <div class="field guestbook-video-link"><label for="guestbook-video-url">外链视频（可选）</label><input id="guestbook-video-url" v-model.trim="guestbookExternalVideoURL" type="url" inputmode="url" maxlength="2000" placeholder="粘贴视频链接或播放器 iframe 代码" /><small>外链只在点击播放后从原网站加载，不占用本站视频流量。</small></div>
              <input ref="guestbookMediaInput" class="visually-hidden" type="file" accept="image/*,video/*" multiple @change="handleGuestbookMediaInput" />
              <div v-if="guestbookMedia.length" class="media-queue guestbook-media-queue">
                <article v-for="item in guestbookMedia" :key="item.key" :data-status="item.status"><span class="media-kind"><Image v-if="item.kind === 'image'" :size="19" /><Film v-else :size="19" /></span><div><strong>{{ item.name }}</strong><small>{{ formatBytes(item.size) }} · {{ item.status === 'pending' ? '等待发布' : item.status === 'uploading' ? `上传 ${item.progress}%` : item.status === 'ready' ? '已就绪' : item.error }}</small><progress v-if="item.status === 'uploading'" :value="item.progress" max="100"></progress></div><button type="button" :disabled="item.status === 'uploading'" :aria-label="`移除 ${item.name}`" @click="removeGuestbookMedia(item)"><Trash2 :size="17" /></button></article>
              </div>
              <p v-if="guestbookError" class="form-error" role="alert">{{ guestbookError }}</p>
              <footer><button class="secondary-button" type="button" :disabled="guestbookBusy || guestbookMedia.length >= 6" @click="guestbookMediaInput?.click()"><UploadCloud :size="18" />添加照片或视频</button><button class="primary-button compact" type="submit" :disabled="guestbookBusy || guestbookMediaUploading || (!guestbookBody.trim() && !guestbookExternalVideoURL.trim() && guestbookMedia.length === 0)"><Send :size="18" />{{ guestbookBusy ? '发布中…' : '留下这句话' }}</button></footer>
            </form>

            <div v-if="guestbookLoading && guestbookEntries.length === 0" class="content-empty" role="status"><span class="loader"></span><span>正在翻开留言册…</span></div>
            <div v-else-if="guestbookEntries.length === 0" class="content-empty guestbook-empty"><component :is="guestbookStatus === 'hidden' ? ArchiveRestore : BookHeart" :size="34" aria-hidden="true" /><h2>{{ guestbookStatus === 'hidden' ? '没有已隐藏留言' : '这一页还没有留言' }}</h2><p>{{ guestbookStatus === 'hidden' ? '隐藏的留言会集中出现在这里，并可随时恢复。' : '成为第一个在这里留下字迹的人吧。' }}</p></div>
            <section v-else class="guestbook-entries" aria-label="留言列表">
              <article v-for="entry in guestbookEntries" :key="entry.id" class="guestbook-entry">
                <header><span class="mini-avatar"><img v-if="avatarVisible(entry.author.avatar_path)" :src="avatarURL(entry.author.avatar_path)" alt="" @error="markAvatarBroken(entry.author.avatar_path)" /><span v-else>{{ entry.author.nickname.slice(0, 1) }}</span></span><div><strong>{{ entry.author.nickname }}</strong><span>{{ entry.recipient ? `写给 ${entry.recipient.nickname}` : '写给整个宿舍' }}</span></div></header>
                <p v-if="entry.body">{{ entry.body }}</p>
                <div v-if="entry.media.length || entry.external_video_url" class="guestbook-entry-media"><template v-for="item in entry.media" :key="item.id"><div v-if="mediaLoadErrors.has(item.id)" class="media-unavailable"><AlertCircle :size="24" /><strong>暂时无法读取</strong><button type="button" @click="retryMediaLoad(item.id)"><RotateCcw :size="17" />重试</button></div><img v-else-if="item.media_type === 'image'" :src="mediaContentURL(item.id, item.has_preview)" :alt="item.original_filename" loading="lazy" @error="markMediaLoadError(item.id)" /><VideoPreview v-else :src="mediaContentURL(item.id)" :poster="mediaContentURL(item.id, true)" :title="item.original_filename" /></template><VideoPreview v-if="entry.external_video_url" :src="entry.external_video_url" :poster="externalVideoThumbnail(entry.external_video_url)" :title="entry.body || '留言中的外链视频'" external :embedded="isEmbeddedPlayer(entry.external_video_url)" /></div>
                <footer><time :datetime="entry.created_at">{{ new Date(entry.created_at).toLocaleString('zh-CN') }}</time><div><button v-if="guestbookStatus === 'hidden'" type="button" @click="restoreGuestbookEntry(entry)"><ArchiveRestore :size="17" />恢复显示</button><button v-else-if="user.role === 'admin' || entry.recipient?.id === user.id" type="button" @click="hideGuestbookEntry(entry)">隐藏</button><button v-if="entry.author.id === user.id || user.role === 'admin'" class="danger-link" type="button" @click="deleteGuestbookEntry(entry)">删除</button></div></footer>
              </article>
            </section>
            <button v-if="guestbookNextCursor" class="secondary-button guestbook-more" type="button" :disabled="guestbookLoading" @click="loadGuestbook(false)">{{ guestbookLoading ? '读取中…' : '翻看更早的留言' }}</button>
          </div>
        </div>
      </template>
    </main>

    <nav class="bottom-nav" aria-label="移动端导航"><button type="button" class="nav-item" :class="{ active: activeView === 'home' }" @click="setView('home')"><Home /><span>首页</span></button><button type="button" class="nav-item" :class="{ active: activeView === 'wall' }" @click="setView('wall')"><Camera /><span>照片</span></button><button type="button" class="create-nav" aria-label="发布回忆" @click="openComposer()"><Plus /></button><button type="button" class="nav-item" :class="{ active: activeView === 'guestbook' }" @click="setView('guestbook')"><BookHeart /><span>留言</span></button><button type="button" class="nav-item" :class="{ active: activeView === 'messages' }" @click="setView('messages')"><MessageCircle /><span>消息</span><small v-if="totalMessageUnread">{{ totalMessageUnread > 9 ? '9+' : totalMessageUnread }}</small></button></nav>

    <div v-if="composerOpen" class="dialog-layer" role="presentation" @pointerdown="armDialogBackdrop" @click.self="closeComposerFromBackdrop">
      <section ref="composerDialog" class="composer-dialog" role="dialog" aria-modal="true" aria-labelledby="composer-title" @keydown="trapComposerFocus">
        <header><div><p class="eyebrow">{{ editingPostID ? '编辑回忆' : '新的纪念' }}</p><h2 id="composer-title">写下一段回忆</h2></div><button class="icon-button" type="button" aria-label="关闭发布器" :disabled="composerBusy" @click="closeComposer"><X /></button></header>
        <form @submit.prevent="savePost(true)">
          <div class="field"><label for="post-body">正文</label><textarea id="post-body" v-model="editor.body" rows="8" maxlength="10000" autofocus placeholder="那天发生了什么？也可以只上传照片或视频。"></textarea><small>{{ editor.body.length }} / 10000</small></div>
          <section class="media-editor" aria-labelledby="media-editor-title">
            <header><div><strong id="media-editor-title">照片与视频</strong><small>选择后会在保存投稿时上传，不会暂存在生产服务器。</small></div><span>{{ editorMedia.length }} / 20</span></header>
            <input ref="mediaInput" class="visually-hidden" type="file" accept="image/*,video/*" multiple @change="handleMediaInput" />
            <button class="media-dropzone" type="button" :disabled="composerBusy || editorMedia.length >= 20" @click="chooseMedia" @dragover.prevent @drop.prevent="handleMediaDrop"><UploadCloud :size="25" aria-hidden="true" /><span><strong>选择照片或视频</strong><small>单个视频不超过 150 MiB；建议先压缩视频，可明显缩短上传时间</small></span></button>
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
          <div class="field"><label for="external-video-url">视频外链或嵌入代码（可选）</label><textarea id="external-video-url" v-model.trim="editor.external_video_url" rows="3" maxlength="5000" placeholder="粘贴视频直链，或 Bilibili / YouTube 提供的 &lt;iframe&gt; 嵌入代码"></textarea><small>嵌入代码只会提取播放器地址；目前允许 Bilibili、YouTube 和 YouTube NoCookie。</small></div>
          <div class="field"><label for="post-tags">标签</label><input id="post-tags" v-model="editor.tags" maxlength="320" placeholder="用逗号或顿号分隔，最多 10 个" /></div>
          <p v-if="composerError" class="form-error" role="alert">{{ composerError }}</p>
          <footer><button class="secondary-button" type="button" :disabled="composerBusy" @click="savePost(false)">保存草稿</button><button class="primary-button" type="submit" :disabled="composerBusy || mediaUploading || (!editor.body.trim() && editorMedia.length === 0 && !editor.external_video_url)"><Send :size="18" />{{ composerBusy ? '上传并保存中…' : editingPostID ? '保存并发布' : '直接发布' }}</button></footer>
        </form>
      </section>
    </div>

    <div v-if="profileOpen" class="dialog-layer" role="presentation" @pointerdown="armDialogBackdrop" @click.self="closeProfileFromBackdrop">
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
        <form class="account-form" @submit.prevent="saveAccount">
          <div><p class="eyebrow">登录信息</p><h3>修改账号</h3><small>用户名、邮箱、昵称或密码发生变化时，需要输入当前密码确认。</small></div>
          <div class="field-grid"><div class="field"><label for="account-username">用户名</label><input id="account-username" v-model.trim="account.username" autocomplete="username" minlength="3" maxlength="24" required /></div><div class="field"><label for="account-email">邮箱</label><input id="account-email" v-model.trim="account.email" type="email" autocomplete="email" required /></div></div>
          <div class="field"><label for="account-nickname">昵称</label><input id="account-nickname" v-model.trim="account.nickname" maxlength="40" required /></div>
          <div class="field"><label for="account-current-password">当前密码</label><input id="account-current-password" v-model="account.current_password" type="password" autocomplete="current-password" minlength="10" maxlength="128" required /></div>
          <div class="field-grid"><div class="field"><label for="account-new-password">新密码（可选）</label><input id="account-new-password" v-model="account.new_password" type="password" autocomplete="new-password" minlength="10" maxlength="128" placeholder="至少 10 个字符" /></div><div class="field"><label for="account-confirm-password">确认新密码</label><input id="account-confirm-password" v-model="account.confirm_password" type="password" autocomplete="new-password" :required="Boolean(account.new_password)" /></div></div>
          <p v-if="accountMessage" class="form-message" role="status">{{ accountMessage }}</p><button class="primary-button" type="submit" :disabled="accountBusy || !account.current_password">{{ accountBusy ? '保存中…' : '保存账号信息' }}</button>
        </form>
        <form @submit.prevent="saveProfile">
          <div><p class="eyebrow">公开资料</p><h3>纪念册资料</h3></div>
          <div class="field"><label for="bed-no">床号或位置</label><input id="bed-no" v-model.trim="profile.bed_no" maxlength="30" placeholder="例如 2 号床" /></div>
          <div class="field"><label for="bio">个人简介</label><textarea id="bio" v-model="profile.bio" maxlength="500" rows="3"></textarea></div>
          <div class="field"><label for="memorial-note">纪念寄语</label><textarea id="memorial-note" v-model="profile.memorial_note" maxlength="500" rows="3"></textarea></div>
          <p v-if="profileMessage" class="form-message" role="status">{{ profileMessage }}</p><button class="primary-button" type="submit" :disabled="profileBusy">{{ profileBusy ? '保存中…' : '保存资料' }}</button>
        </form>
        <div class="session-section">
          <div class="session-heading"><div><h3>登录设备</h3><p>默认显示最近活跃的 3 条记录。</p></div><span>{{ sessions.length }} 条</span></div>
          <div v-for="session in visibleSessions" :key="session.id" class="session-row"><div><strong>{{ session.current ? '当前设备' : '其他设备' }}</strong><span>{{ session.user_agent || '未知浏览器' }}</span><small>{{ session.ip_address }} · {{ new Date(session.last_seen_at).toLocaleString('zh-CN') }}</small></div><button class="text-danger" type="button" @click="revoke(session)">{{ session.current ? '退出此设备' : '注销' }}</button></div>
          <button v-if="sessions.length > 3" class="session-toggle" type="button" :aria-expanded="sessionsExpanded" @click="sessionsExpanded = !sessionsExpanded"><span>{{ sessionsExpanded ? '收起登录记录' : `展开其余 ${sessions.length - 3} 条记录` }}</span><ChevronLeft :size="18" aria-hidden="true" /></button>
        </div>
        <button class="logout-button" type="button" @click="logout"><LogOut :size="18" />退出登录</button>
      </section>
    </div>

    <div v-if="publicProfile" class="dialog-layer" role="presentation" @pointerdown="armDialogBackdrop" @click.self="closePublicProfileFromBackdrop">
      <section class="profile-dialog public-profile-dialog" role="dialog" aria-modal="true" aria-labelledby="public-profile-title">
        <header><div><p class="eyebrow">室友公开资料</p><h2 id="public-profile-title">{{ publicProfile.nickname }}</h2></div><button class="icon-button" type="button" aria-label="关闭公开资料" @click="publicProfile = null"><X /></button></header>
        <div class="public-profile-hero"><span class="public-profile-avatar"><img v-if="avatarVisible(publicProfile.avatar_path)" :src="avatarURL(publicProfile.avatar_path)" alt="" @error="markAvatarBroken(publicProfile.avatar_path)" /><span v-else>{{ publicProfile.nickname.slice(0, 1) }}</span></span><div><strong>{{ publicProfile.nickname }}</strong><span>@{{ publicProfile.username }}</span><small v-if="publicProfile.bed_no">床号或位置：{{ publicProfile.bed_no }}</small></div></div>
        <dl class="public-profile-fields"><div><dt>个人简介</dt><dd>{{ publicProfile.bio || '这位室友还没有填写简介。' }}</dd></div><div><dt>纪念寄语</dt><dd>{{ publicProfile.memorial_note || '暂时没有留下寄语。' }}</dd></div></dl>
        <p class="privacy-note">邮箱、账号状态、登录会话等敏感信息不会在这里显示。</p>
      </section>
    </div>

  </div>
</template>
