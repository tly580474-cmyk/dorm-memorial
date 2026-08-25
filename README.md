# 宿舍电子纪念社区

面向 5～6 名宿舍成员的私有电子纪念社区。应用采用 Go 单体服务、静态前端和 SQLite，原始媒体通过 AList 存入第三方网盘。

项目当前处于开发计划的 **阶段 0：AList/夸克技术验证**。

- 开发计划：[`DEVELOPMENT_PLAN.md`](./DEVELOPMENT_PLAN.md)
- 前端布局：[`FRONTEND_LAYOUT.md`](./FRONTEND_LAYOUT.md)
- 当前验证说明：[`docs/PHASE_0_STORAGE_VALIDATION.md`](./docs/PHASE_0_STORAGE_VALIDATION.md)

## 快速检查

```powershell
go test ./...
go vet ./...
```
