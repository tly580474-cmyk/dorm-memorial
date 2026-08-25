# 阶段 1：基础平台

阶段 1 建立可登录、可部署、可备份的应用骨架。正式前端选择 Vue 3，后端保持 Go 单体服务，数据库使用 SQLite WAL。

## 本地启动

1. 复制 `.env.example` 中的 `APP_*` 配置到本地 `.env` 或当前终端环境。
2. 第一次启动时设置四个 `APP_BOOTSTRAP_ADMIN_*` 变量。数据库已有用户后，这些变量不会再创建账号，应从环境中删除。
3. 构建前端并启动服务：

```powershell
Set-Location web
npm install
npm run build
Set-Location ..
go run ./cmd/server
```

开发前端时可以分别运行 `go run ./cmd/server` 与 `npm run dev --prefix web`。Vite 会把 `/api` 和 `/health` 代理到 `127.0.0.1:8080`。

## 已实现的身份闭环

- 首次管理员安全引导。
- 管理员创建有次数和有效期限制的邀请码。
- 邀请注册、用户名或邮箱登录、退出登录。
- 随机服务端 Session，浏览器只保存 `HttpOnly`、`SameSite=Lax` Cookie。
- 会话持久化、查看登录设备和注销指定设备。
- 管理员与普通成员的接口权限分离。
- 昵称、简介、床号和纪念寄语编辑。
- 注册、邀请、资料修改等基础审计记录。

## 健康检查

- `GET /health/live`：进程存活。
- `GET /health/ready`：SQLite 可访问且可写。
- `GET /api/admin/health`：管理员查看活跃用户和会话数量。

所有日志采用 JSON 结构化输出，不记录密码、Session Token、邀请码或私信正文。

## 备份与恢复

在线备份使用 SQLite `VACUUM INTO`，生成后立即执行完整性检查：

```powershell
go run ./cmd/dbtool backup -source data/dorm-memorial.db -destination backups/manual.db
```

恢复命令拒绝覆盖已有目标文件，并在复制前后执行完整性检查：

```powershell
go run ./cmd/dbtool restore -source backups/manual.db -destination data/restored.db
```

生产模板位于 `deploy/systemd` 和 `deploy/nginx`。每日备份当前先写入独立本地备份目录；上传到异机或远端并执行保留策略属于部署配置步骤。

## 安全边界

- 生产环境强制启用 Secure Cookie。
- 修改请求拒绝浏览器标记为 `cross-site` 的请求，JSON API 不接受表单 Content-Type。
- 用户名、邮箱、邀请码和密码在服务端重新校验。
- 登录失败使用统一错误信息，不暴露账号是否存在。
- 本地 AList 数据、应用数据库、备份和 `.env` 均被 Git 忽略。
