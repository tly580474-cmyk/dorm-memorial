<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { Bell, BookHeart, BookOpenText, Camera, Check, Copy, Eye, EyeOff, Home, LogOut, Menu, MessageCircle, Plus, Settings, ShieldCheck, Sparkles, Users, X } from 'lucide-vue-next'
import { api, ApiError } from './api'
import type { Session, User } from './types'

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
const sessions = ref<Session[]>([])
const inviteCodes = ref<string[]>([])
const inviteCount = ref(5)
const inviteCountOptions = Array.from({ length: 20 }, (_, index) => index + 1)
const inviteCopyStatus = ref<'idle' | 'copied' | 'error'>('idle')
const inviteBusy = ref(false)
const profile = reactive({ nickname: '', bio: '', bed_no: '', memorial_note: '' })

const greeting = computed(() => {
  const hour = new Date().getHours()
  return hour < 11 ? '早上好' : hour < 18 ? '下午好' : '晚上好'
})

const nav = [
  { label: '首页', icon: Home, active: true },
  { label: '时间线', icon: Sparkles },
  { label: '照片墙', icon: Camera },
  { label: '留言册', icon: BookHeart },
  { label: '论坛', icon: BookOpenText },
  { label: '消息', icon: MessageCircle },
]

onMounted(async () => {
  try {
    applyUser((await api.me()).user)
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
      <div class="top-actions"><button class="icon-button" type="button" aria-label="查看通知"><Bell /></button><button class="avatar-button" type="button" aria-label="打开个人设置" @click="openProfile">{{ user.nickname.slice(0, 1) }}</button></div>
    </header>

    <aside class="sidebar" :class="{ open: mobileMenuOpen }" aria-label="主要导航">
      <div class="mobile-menu-head"><span>浏览纪念册</span><button class="icon-button" type="button" aria-label="关闭菜单" @click="mobileMenuOpen = false"><X /></button></div>
      <nav><a v-for="item in nav" :key="item.label" href="#" :class="{ active: item.active }"><component :is="item.icon" :size="20" aria-hidden="true" /><span>{{ item.label }}</span></a></nav>
      <div class="sidebar-bottom"><button v-if="user.role === 'admin'" type="button"><ShieldCheck :size="20" />管理</button><button type="button" @click="openProfile"><Settings :size="20" />设置</button></div>
    </aside>
    <button v-if="mobileMenuOpen" class="scrim" type="button" aria-label="关闭菜单" @click="mobileMenuOpen = false"></button>

    <main id="main-content" class="main-content">
      <section class="welcome-card">
        <div><p class="eyebrow">{{ greeting }}，{{ user.nickname }}</p><h1>欢迎回到我们的妙妙小屋</h1><p>基础平台已经就绪。下一阶段，照片、故事和留言会从这里慢慢长出来。</p></div>
        <button class="primary-button compact" type="button"><Plus :size="19" />分享回忆</button>
      </section>
      <section class="dashboard-grid" aria-label="社区概览">
        <article class="dashboard-card"><Users aria-hidden="true" /><h2>室友成员</h2><p>邀请注册已启用，只有拿到邀请码的室友可以加入。</p></article>
        <article class="dashboard-card"><ShieldCheck aria-hidden="true" /><h2>私密空间</h2><p>登录状态由服务端安全会话维护，页面不会保存长期令牌。</p></article>
        <article class="dashboard-card"><BookOpenText aria-hidden="true" /><h2>下一步</h2><p>阶段 2 将接入纪念内容、照片墙和留言册。</p></article>
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
    </main>

    <nav class="bottom-nav" aria-label="移动端导航"><a href="#" class="active"><Home /><span>首页</span></a><a href="#"><Camera /><span>照片</span></a><button type="button" aria-label="发布回忆"><Plus /></button><a href="#"><BookOpenText /><span>论坛</span></a><a href="#"><MessageCircle /><span>消息</span></a></nav>

    <div v-if="profileOpen" class="dialog-layer" role="presentation" @click.self="closeProfile">
      <section ref="profileDialog" class="profile-dialog" role="dialog" aria-modal="true" aria-labelledby="profile-title" @keydown="trapProfileFocus">
        <header><div><p class="eyebrow">账号与个人资料</p><h2 id="profile-title">{{ user.nickname }}</h2></div><button class="icon-button" type="button" aria-label="关闭" @click="closeProfile"><X /></button></header>
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
  </div>
</template>
