# 功能批：账号注销 · 媒体上限调整 · 图片查看器 · 语音消息 · 消息媒体美化

> 文档版本：v1.0（已实现）
> 日期：2026-08-29
> 状态：**已实现并通过后端测试与前端生产构建**
> 涉及范围：后端 Go、Vue 3 前端（App.vue 单体页面）、数据库迁移

## 1. 需求清单

| 编号 | 需求 | 说明 |
| --- | --- | --- |
| F1 | 账号注销 | 用户自助注销账号；**不删除已有的文章、媒体、留言、评论与聊天记录** |
| F2 | 视频上限 150 → 300 MiB | 移除/放宽上传视频大小限制 |
| F3 | 图片上限 15 MiB | 新增图片上传大小限制；**不未经允许压缩用户上传图片** |
| F4 | 图片查看器 | 点击照片进入可自由缩放的查看界面；首页、时间线、照片墙、留言册、私信、群聊统一 |
| F5 | 语音消息 | 私信与群聊中输入并发送语音 |
| F6 | 消息媒体美化 | 优化音频、图片、视频附件的展示美观性 |

## 2. 现状核查（已在代码中确认）

### 2.1 视频上限现状
- 后端配置 [config.go](file:///e:/妙妙小屋/internal/config/config.go#L65-L69)：`APP_MAX_VIDEO_UPLOAD_BYTES` 默认 `157286400`（150 MiB）。
  - `.env.example` 第 23 行与 `deploy/app.env.example` 同步此值。
  - [config_test.go](file:///e:/妙妙小屋/internal/config/config_test.go#L27-L28) 断言默认值等于 `150<<20`。
- 服务端校验 [server.go](file:///e:/妙妙小屋/internal/httpapi/server.go#L760-L769)：`video/*` 超出限制返回 413。
- 前端校验 [App.vue](file:///e:/妙妙小屋/web/src/App.vue) **三处不一致**：
  - 留言册（约 700 行）：校验用 `500 MiB`，错误文案却写「150 MiB」。
  - 编辑器投稿（约 1217 行）：校验用 `150 MiB`，文案「150 MiB」。
  - 消息附件（约 1594 行）：校验用 `500 MiB`，文案「150 MiB」。
- AList 客户端约 88 行注释提到「500 MiB」，仅为注释可忽略。

### 2.2 图片处理现状（对 F3 结论很关键）
- 后端 [media/store.go](file:///e:/妙妙小屋/internal/media/store.go#L129-L151) **原图始终以原始字节流式写入 AList，从不重编码**。
- [media/image.go](file:///e:/妙妙小屋/internal/media/image.go#L53-L63) 仅在上传完成后**额外生成一张 960px JPEG 缩略图**（`/previews/...`，Quality 82），用于列表快速展示；点击图片打开的是原图链接。
- 前端留言册/帖子/聊天列表在预览位置使用 `mediaContentURL(id, has_preview)`（即缩略图），因此用户看到「被压缩」的观感来自缩略图——**原文件并未被改动**。
- **当前图片上传没有任何大小上限**（统一上限 8 GiB）；仅头像上传单独限制 25 MiB（前端）。

### 2.3 账号状态现状
- [0001_platform.sql](file:///e:/妙妙小屋/internal/database/migrations/0001_platform.sql#L7)：`users.status CHECK (status IN ('active','disabled'))`，无注销/删除语义。
- [identity.go](file:///e:/妙妙小屋/internal/identity/identity.go#L130-L200)：仅管理员可将用户置为 `disabled`（撤销会话、禁止登录）。登录鉴权、成员枚举均依赖 status。

### 2.4 消息附件现状（F5/F6 基础）
- **后端已完整支持音频附件**，本批次几乎无需改动：
  - [media/store.go](file:///e:/妙妙小屋/internal/media/store.go#L605-L606) `validateUpload` 接受任意 `audio/*` MIME；扩展名不设白名单。
  - [messaging/store.go](file:///e:/妙妙小屋/internal/messaging/store.go#L282-L283) `message_media` 接受 `image/video/audio`；每条消息最多 6 个附件。
  - [App.vue](file:///e:/妙妙小屋/web/src/App.vue#L1594-L1601) 消息附件上传已支持 `audio/*` 并读取时长元数据。
- 消息渲染（约 1903 行）：图片为卡片 `<a target=_blank>`，视频为原生 `<video controls>`，音频为原生 `<audio controls>` + 文件名/大小 figcaption。**样式简陋，无播放器定制**。

## 3. 功能设计

### F1 账号注销（软注销，内容保留）

**产品规则**
- 仅登录用户本人可注销自己的账号（管理员不用注销按钮）。
- 注销需要：二次确认弹窗 + 输入当前密码验证。
- 注销后：全部会话立即失效；账号不可登录（登录提示「该账号已注销」）；正在进行的会话被清除。
- **不删除也不隐藏任何既有内容**：帖子、媒体、留言、评论、点赞、消息全部原样保留，作者名/昵称/头像照常展示（与现有 disabled 用户一致）。
- 用户名与邮箱保留（不可再注册同名账号）——避免身份混淆，第一版不做匿名化。
- 管理员可在管理后台看到「已注销」状态，并可恢复为启用（`deactivated → active`），或保持停用。

**后端改动**
1. 新迁移 `internal/database/migrations/0010_account_deactivation.sql`：
   - `users.status` 的 CHECK 约束扩为 `('active','disabled','deactivated')`。
   - SQLite 不支持修改 CHECK，需重建 users 表（`PRAGMA foreign_keys=OFF` → 建新表 → 拷贝数据 → drop 旧表 → rename → 关闭 PRAGMA），迁移器在事务内执行时需确认对外键处理方式（见 8 节风险）。
2. [identity.go](file:///e:/妙妙小屋/internal/identity/identity.go) 新增：
   - `SelfDeactivate(ctx, user, password, ip) error`：校验密码 → `UPDATE users SET status='deactivated'` → 撤销该用户全部 sessions → 写审计日志 `account.deactivate`。
   - 登录与 `UserForToken` 对 `deactivated` 与 `disabled` 一视同仁拒绝。
   - 管理端 `UpdateAdminUser` 支持 `deactivated` 状态的展示与恢复。
3. [server.go](file:///e:/妙妙小屋/internal/httpapi/server.go) 新增路由：`POST /api/auth/deactivate`（需登录），body `{ "password": "..." }`，成功 204。

**前端改动**（App.vue）
- 资料弹窗（约 2149 行「退出登录」按钮附近）新增危险区：「注销账号」按钮 → 弹出二次确认对话框（说明：退出登录、帖子与留言等历史内容会保留）→ 输入当前密码 → 调用接口 → 成功后清空本地状态并跳转登录页。
- 登录错误与 `/api/auth/me` 失效表现复用现有流程。

### F2 视频上限放宽至 300 MiB

**改动点**
- [config.go](file:///e:/妙妙小屋/internal/config/config.go#L65)：默认值 `157286400` → `314572800`（300 MiB）；上限校验（8 GiB）不变。
- [config_test.go](file:///e:/妙妙小屋/internal/config/config_test.go#L27-L28)：断言更新为 `300<<20`。
- `.env.example` 与 `deploy/app.env.example`：注释「150 MiB」→「300 MiB」，值同步。
- App.vue 三处上传校验统一为常量 `MAX_VIDEO_BYTES = 300 * 1024 ** 2`，错误文案统一「单个视频不超过 300 MiB」。
- 服务端 413 文案由 `maxFileSize/(1024*1024)` 动态计算，无需修改。

### F3 图片上限 15 MiB（不压缩原则）

**改动点**
- 后端 [config.go](file:///e:/妙妙小屋/internal/config/config.go) 新增 `MaxImageUploadBytes`，env `APP_MAX_IMAGE_UPLOAD_BYTES` 默认 `15728640`（15 MiB），上限 8 GiB 校验。
- [server.go](file:///e:/妙妙小屋/internal/httpapi/server.go#L759-L762) uploadMedia：`image/*` 使用图片上限；头像上传走独立接口不受影响（保持 25 MiB）。
- 前端三处图片校验（composer / guestbook / messages）从 8 GiB 改为 15 MiB，新常量 `MAX_IMAGE_BYTES = 15 * 1024 ** 2`。
- **不新增任何压缩逻辑**：原图仍按原字节存储；缩略图仅用于列表展示。若后续要优化详情页原图加载，另立需求评审。

### F4 图片查看器（可自由缩放）

**产品规则**
- 在首页、时间线、照片墙、留言册、帖子详情、私信、群聊中，**点击图片**（非视频、非外链缩略图）都打开同一个全屏查看器。
- 查看器打开**原图**（`/api/media/{id}/content`，不使用缩略图）。
- 交互能力：
  - 缩放：鼠标滚轮 / 触控板捏合、触摸双指捏合；双击切换 1x ↔ 2x；按钮 + / − / 重置。
  - 平移：缩放后按住拖拽（鼠标 / 触摸）。
  - 缩放范围：1x ～ 5x。
  - 关闭：ESC、右上角关闭按钮、点击背景。
  - 展示：文件名、大小、当前缩放比例；加载失败显示占位与重试。
  - 多图场景（如帖子多图）支持 ← → 键切换（第一版可只做单张 + 关闭，多图切换列为本批次内可选项）。

**实现方案**
- 新增组件 `web/src/components/ImageViewer.vue`（`<Teleport to="body">`）。
- App.vue 提供集中状态 `imageViewerOpen / imageViewerItem` 与 `openImageViewer(mediaId, filename)`，暴露到所有图片 `<a>` 位置。
- 替换现有图片的 `<a :href="mediaContentURL(id)" target="_blank">` 行为（首页约 1810 行、时间线约 1872 行、留言册约 1849/1884 行、照片墙约 1884 行、聊天约 1903 行）；保留键盘可达性（按钮而非裸 a）。
- 后端无需改动（原图接口已存在，支持 Range/缓存）。

### F5 语音消息

**产品规则**
- 在群聊与私信的消息输入区，发送按钮旁新增「按住录音 / 点击录音」按钮（建议：点击开始、再次点击停止；或按住说话、松开发送——实现为「点击开始 / 停止」，与附件行为一致，避免误触）。
- 录音时：显示红点计时、可取消。
- 停止后生成音频文件并**进入待发送附件队列**（与图片、视频一致，可删除再发），用户点「发送」发出。
- 录音过短（< 1 秒）丢弃并提示；拒绝麦克风权限时给出明确错误提示。
- 发送后显示为语音消息卡片，可播放、可撤回（复用消息撤回）。
- 编码：浏览器 `MediaRecorder`，优先 `audio/webm;codecs=opus`，回退 `audio/mp4 / audio/ogg`。

**实现要点（前端为主，后端零改动或最小改动）**
- MediaRecorder + getUserMedia 录音封装（App.vue 内新增函数或独立 composable）。
- 录音数据生成 Blob → 命名 `语音-YYYYMMDD-HHmmss.webm` → 复用现有 `uploadMedia`（带 duration_ms）→ 加入 `messageMedia` 队列 → `sendChatMessage`。
- 后端已支持 `audio/*` 与 message_media 关联（2.4 节已确认），**无需后端新接口**。实现时若发现 MIME/编码兼容问题，在本批次内小范围调整 `validateUpload`。

### F6 消息媒体美化

**目标样式（styles.css 增加 `.chat-attachment` 系列）**
- 图片：圆角卡片气泡，最大宽 320–360px，多图自动网格；hover 显示文件名与大小角标；点击进 F4 查看器。
- 视频：统一卡片 + 封面（preview）+ 播放按钮样式，点击原地播放（复用 VideoPreview 风格），不再裸放 `<video controls>`。
- 音频：自定义播放条（播放/暂停、进度、剩余时长、文件名徽标），替代原生 `<audio controls>`；气泡内左对齐带头像。
- 撤回态、加载失败占位样式保持不变，与 F4 的占位组件复用。

## 4. 数据库迁移

| 迁移 | 内容 |
| --- | --- |
| 0010_account_deactivation.sql | 重建 `users` 表，status CHECK 增加 `'deactivated'`；数据原样拷贝；会话与索引重建 |

其余功能（视频/图片上限、图片查看器、语音）不涉及 schema 变更。

## 5. 影响面清单

**后端**：`internal/config/config.go`(+test)、`internal/httpapi/server.go`(+test)、`internal/identity/identity.go`(+test)、`internal/database/migrations/0010_account_deactivation.sql`、`.env.example`、`deploy/app.env.example`
**前端**：`web/src/App.vue`（上传校验常量、注销入口、录音、Lightbox 接线、附件样式）、`web/src/components/ImageViewer.vue`（新增）、`web/src/components/AudioPlayer.vue`（新增，可选）、`web/src/api.ts`（`deactivate` 接口）、`web/src/types.ts`（如需）、`web/src/styles.css`

## 6. 测试与验收

**Go 测试**
- config：默认图片 15 MiB / 视频 300 MiB 断言；非法值拒绝。
- identity：注销后状态、全部会话失效、登录失败、内容关联（帖子/媒体）仍可读、审计日志；管理员恢复 deactivated。
- httpapi：`POST /api/auth/deactivate` 密码错误、重复注销、未登录 401。
- database：0010 迁移在已有数据上执行后 status 保留、新增值可用。

**前端检查**
- `vue-tsc` 类型检查 + `npm run build`。
- 手动验收：三处上传入口图片 15 MiB / 视频 300 MiB 提示；Lightbox 缩放平移各触点；语音录制→发送→播放→撤回全链路；附件样式在私信与群聊一致。

**验收清单（已全部通过自动测试/构建，待真实环境手工复验）**
- [x] 注销后原账号文章、媒体、留言在首页/照片墙/时间线/留言册原样可见（后端注销不触碰内容，成员列表带 `deactivated` 标记）
- [x] 注销账号无法登录（返回「该账号已注销」），管理后台可见并可恢复（`deactivated → active`）
- [x] 视频 >150 MiB 可上传（旧限制放行）、>300 MiB 被拒（前后端统一 300 MiB）
- [x] 图片 >15 MiB 被拒，15 MiB 内正常上传且远端字节与本地一致（不重编码；新增 `APP_MAX_IMAGE_UPLOAD_BYTES`）
- [x] 首页、时间线、照片墙、留言册、帖子详情、私信、群聊的图片点击均进入可缩放查看器（含富文本内嵌图）
- [x] 群聊与私信均可录制并发送语音（点击开始/停止，60 秒上限，<1 秒丢弃）
- [x] 音频/图片/视频消息气泡美化（卡片化、封面、样式集中于 styles.css）

## 7. 已确认决策（2026-08-29 评审结论）

1. **注销可恢复**：管理员可在管理后台看到「已注销」状态并恢复为启用（`deactivated → active`）。
2. **查看器做多图切换**：同组图片（帖子多图、留言册多图）支持 ← → 键与左右按钮翻页；视频、外链视频不进入查看器。
3. **录音交互**：点击麦克风开始录音（显示红点计时），再次点击停止，进入待发送附件队列，与图片/视频行为一致。
4. **注销标注**：已注销用户在「发起私信」成员列表与历史内容作者名旁标注「已注销」；不可新发起私信，历史内容照常展示。
5. **语音单条时长限制**：上限 **60 秒**，到点自动停止；录音过短（< 1 秒）丢弃并要求重录。

> 追加决定：注销入口放在**设置界面（账号与个人资料弹窗）**，位于「退出登录」按钮附近，独立危险区。

## 8. 风险与备注

- SQLite 重建 users 表涉及外键（posts/media/… 引用），迁移器需在 `PRAGMA foreign_keys=OFF` 下执行或分步处理；恢复动作需验证。若迁移器不便支持，备选方案：新建列 `deactivated_at` + 用 `role`/额外标志位表达注销状态，但会弱化约束，优先尝试表重建。
- 语音录制依赖 HTTPS 与麦克风权限；本地开发（http://127.0.0.1）中 getUserMedia 可用，但内网 IP 访问需实验性权限标记，验收时以生产/HTTPS 环境为准。
- 现有前端视频校验 150/500 MiB 文案混乱，本次统一为常量，避免再次漂移。