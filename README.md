# 宿舍电子纪念社区

面向 5～6 名宿舍成员的私有电子纪念社区。应用采用 Go 单体服务、静态前端和 SQLite，原始媒体通过 AList 存入第三方网盘。

项目已完成 AList/夸克技术验证和基础平台，当前处于 **阶段 2：纪念册核心**，并先行交付了消息与站内通知切片。应用已具备邀请注册、内容投稿与审核、媒体上传与缓存、照片墙、时间线、留言册、群聊、私信和通知。

- 开发计划：[`DEVELOPMENT_PLAN.md`](./DEVELOPMENT_PLAN.md)
- 前端布局：[`FRONTEND_LAYOUT.md`](./FRONTEND_LAYOUT.md)
- 阶段 0 存储验证：[`docs/PHASE_0_STORAGE_VALIDATION.md`](./docs/PHASE_0_STORAGE_VALIDATION.md)
- 阶段 1 基础平台：[`docs/PHASE_1_PLATFORM.md`](./docs/PHASE_1_PLATFORM.md)
- 阶段 2 纪念册核心：[`docs/PHASE_2_MEMORIAL_CORE.md`](./docs/PHASE_2_MEMORIAL_CORE.md)
- 消息与通知先行切片：[`docs/MESSAGES_NOTIFICATIONS.md`](./docs/MESSAGES_NOTIFICATIONS.md)

## 快速检查

```powershell
go test ./...
go vet ./...
npm run build --prefix web
```
