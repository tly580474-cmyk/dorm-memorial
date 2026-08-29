# 宿舍电子纪念社区

面向 5～6 名宿舍成员的私有电子纪念社区。应用采用 Go 单体服务、静态前端和 SQLite，原始媒体通过 AList 存入第三方网盘。

项目已完成 AList/夸克技术验证和基础平台，当前处于 **阶段 2：纪念册核心**，并先行交付了消息与站内通知切片。应用已具备邀请注册、内容投稿与审核、媒体上传与缓存、照片墙、时间线、留言册、群聊、私信和通知。

近期新增能力：

- **账号自注销**：设置中自助注销账号，历史文章、媒体、留言与聊天记录原样保留，管理员可在后台恢复；已注销成员在内容与成员列表中标注「已注销」。
- **媒体上传限制**：图片单文件 ≤ 15 MiB、视频 ≤ 300 MiB（前后端一致）；上传文件始终以原始字节存储，不做任何重编码。
- **图片查看器**：首页、时间线、照片墙、留言册、帖子详情、私信与群聊中的图片均可点击打开全屏查看器，支持滚轮/双指缩放（1x–5x）、拖拽平移、双击切换与多图翻页。
- **语音消息**：群聊与私信支持录制并发送语音（最长 60 秒，过短自动丢弃），播放与图片、视频同卡片化展示。

- 开发计划：[`DEVELOPMENT_PLAN.md`](./DEVELOPMENT_PLAN.md)
- 前端布局：[`FRONTEND_LAYOUT.md`](./FRONTEND_LAYOUT.md)
- 阶段 0 存储验证：[`docs/PHASE_0_STORAGE_VALIDATION.md`](./docs/PHASE_0_STORAGE_VALIDATION.md)
- 阶段 1 基础平台：[`docs/PHASE_1_PLATFORM.md`](./docs/PHASE_1_PLATFORM.md)
- 阶段 2 纪念册核心：[`docs/PHASE_2_MEMORIAL_CORE.md`](./docs/PHASE_2_MEMORIAL_CORE.md)
- 消息与通知先行切片：[`docs/MESSAGES_NOTIFICATIONS.md`](./docs/MESSAGES_NOTIFICATIONS.md)
- 注销·媒体上限·图片查看器·语音·消息美化：[`docs/FEATURES_ACCOUNT_MEDIA_MESSAGING.md`](./docs/FEATURES_ACCOUNT_MEDIA_MESSAGING.md)

## 快速检查

```powershell
go test ./...
go vet ./...
npm run build --prefix web
```
