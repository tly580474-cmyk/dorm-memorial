# 宿舍电子纪念社区

面向 5～6 名宿舍成员的私有电子纪念社区。应用采用 Go 单体服务、静态前端和 SQLite，原始媒体通过 AList 存入第三方网盘。

项目已完成 AList/夸克技术验证和基础平台，当前处于 **阶段 2：纪念册核心**，并先行交付了消息与站内通知切片。应用已具备邀请注册、内容投稿与审核、媒体上传与缓存、照片墙、时间线、留言册、群聊、私信和通知。

近期新增能力：

- **账号自注销**：设置中自助注销账号，历史文章、媒体、留言与聊天记录原样保留，管理员可在后台恢复；已注销成员在内容与成员列表中标注「已注销」。
- **媒体上传限制**：图片单文件 ≤ 15 MiB、视频 ≤ 300 MiB（默认配置）；保留上传原文件。视频由服务器检测后选择原文件复用、Fast Start 封装或生成 H.264/AAC 播放版，网页默认使用选定的播放资源。
- **图片查看器**：首页、时间线、照片墙、留言册、帖子详情、私信与群聊中的图片均可点击打开全屏查看器，支持滚轮/双指缩放（1x–5x）、拖拽平移、双击切换与多图翻页。
- **语音消息**：群聊与私信支持录制并发送语音（最长 60 秒，过短自动丢弃），播放与图片、视频同卡片化展示。

- 开发计划：[`DEVELOPMENT_PLAN.md`](./DEVELOPMENT_PLAN.md)
- 前端布局：[`FRONTEND_LAYOUT.md`](./FRONTEND_LAYOUT.md)
- 阶段 0 存储验证：[`docs/PHASE_0_STORAGE_VALIDATION.md`](./docs/PHASE_0_STORAGE_VALIDATION.md)
- 阶段 1 基础平台：[`docs/PHASE_1_PLATFORM.md`](./docs/PHASE_1_PLATFORM.md)
- 阶段 2 纪念册核心：[`docs/PHASE_2_MEMORIAL_CORE.md`](./docs/PHASE_2_MEMORIAL_CORE.md)
- 消息与通知先行切片：[`docs/MESSAGES_NOTIFICATIONS.md`](./docs/MESSAGES_NOTIFICATIONS.md)
- 注销·媒体上限·图片查看器·语音·消息美化：[`docs/FEATURES_ACCOUNT_MEDIA_MESSAGING.md`](./docs/FEATURES_ACCOUNT_MEDIA_MESSAGING.md)
- 视频处理优化清单与验证：[`docs/VIDEO_OPTIMIZATION_CHECKLIST.md`](./docs/VIDEO_OPTIMIZATION_CHECKLIST.md)

视频处理需要 FFmpeg；建议同时安装 ffprobe（优先查找 FFmpeg 同目录，其次查找 PATH）。缺少 ffprobe 时保守回退到转码，不直接复用未经检测的原文件。服务器必须为暂存及编码输出预留磁盘空间；原文件与播放资源完成远端校验后才标记可用。

本机更新使用 `start-local.ps1`：先确认上传任务已结束，再停止旧应用进程，然后运行脚本。脚本会在 13048 端口仍被占用时提前退出，不再覆盖前端后继续复用旧后端；AList 和 Caddy 仍可复用。仅访问已运行的服务时直接打开页面即可，无需重新执行构建。不要在旧应用运行期间单独覆盖 `web/dist`，前端与后端应一起更新。

## 快速检查

```powershell
go test ./...
go vet ./...
npm run build --prefix web
```
