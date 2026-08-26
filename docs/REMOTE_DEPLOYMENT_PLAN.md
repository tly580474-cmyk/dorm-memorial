# 远程生产部署计划

> 适用版本：提交 `a6752c9` 及其后续部署修订  
> 目标规模：5～6 名成员、原始媒体不超过 100 GiB  
> 计划状态：域名、人工备份方式与维护窗口已确认；待完成部署模板修订后执行

## 1. 已知生产环境

| 项目 | 当前情况 |
| --- | --- |
| 系统 | Ubuntu 24.04.2 LTS，KVM |
| CPU | 2 vCPU，Intel Xeon Platinum |
| 内存 | 1.6 GiB RAM + 2 GiB Swap |
| 磁盘 | 40 GiB ext4，约 24 GiB 可用 |
| 入口 | Nginx 1.24，UFW 仅开放 22/80/443 |
| 既有服务 | Nginx、FRPS、New API、阿里云相关服务 |
| 容器 | 未安装 Docker，也不计划引入 |

部署原则：Go 应用和 AList 只监听回环地址；Nginx 是唯一公网入口；原始媒体只写入夸克网盘；主机仅保存 SQLite、日志和最多 2 GiB 的媒体缓存。

## 2. 已确认事项与剩余准备

以下信息不应在仓库或工单中明文保存：

1. 正式域名为 `3048.clical.xin`，DNS 已解析；部署当天仍需从公网复核解析结果。
2. TLS 默认使用现有 Certbot/Let's Encrypt 流程。
3. AList 在生产机上使用的固定版本和数据目录。
4. AList 专用用户凭据。该用户在 AList 中看到的 `/` 必须已经限制为夸克 `3048` 目录，应用配置使用 `ALIST_ROOT=/`。
5. 数据库由管理员在管理中心手动导出，并保存在管理员设备；每次发布、迁移和批量管理前必须执行。
6. 可以安排维护窗口；正式执行前确定具体起止时间，并提前通知成员。
7. 首次管理员账号信息仍需在部署当天通过安全渠道注入，不写入仓库或工单。

本地优先项已于 2026-08-26 跑通：管理员通过 HTTP 接口下载在线快照，文件具有标准 SQLite 签名；随后用 `dbtool restore` 恢复到全新路径，恢复成功且 SHA-256 与下载文件一致。生产上线时仍需再执行同样的端到端验收。

## 3. 上线门槛与部署前修订

正式安装前先创建一个仅包含部署修订的提交，并再次运行完整 CI。至少需要完成：

- 将 Nginx 的 `client_max_body_size 16m` 调整为应用允许的上限（建议 `8g`）。
- 上传接口配置 `proxy_request_buffering off`，避免 Nginx 把完整视频写入系统盘临时文件。
- 为上传和媒体读取配置足够的超时，建议 `proxy_read_timeout`、`proxy_send_timeout` 为 `3600s`。
- 为上传接口设置每 IP 最多 2 个并发连接，避免小内存主机被并行大文件占满。
- 在 `deploy/app.env.example` 中补齐：

  ```dotenv
  APP_MEDIA_CACHE_DIR=/var/cache/dorm-memorial/media
  APP_MEDIA_CACHE_MAX_BYTES=2147483648
  ```

- 让 systemd 模板和实际发布目录一致，并设置明确资源上限。建议应用使用 `MemoryHigh=384M`、`MemoryMax=640M`；AList 单独设置限制，不能与应用共用额度。
- 确认静态前端路径指向本次发布的 `web` 目录，而不是开发机路径。
- 确认生产构建不包含 `.env`、数据库、AList 数据目录或本地缓存。

上线门槛检查：

```bash
go test -race ./...
go vet ./...
npm ci --prefix web
npm run build --prefix web
git diff --check
```

## 4. 目录与账号规划

创建两个互相隔离、禁止交互登录的系统账号：

- `dorm-memorial`：运行 Go 应用，只能写应用数据库、临时导出文件和媒体缓存。
- `alist`：运行 AList，只能写自己的配置和运行数据。

建议目录：

```text
/opt/dorm-memorial/
  releases/<git-sha>/
    bin/dorm-memorial
    bin/dbtool
    web/
  current -> releases/<git-sha>

/etc/dorm-memorial/app.env
/var/lib/dorm-memorial/app.db
/var/cache/dorm-memorial/media/

/opt/alist/alist
/var/lib/alist/
```

权限建议：

- `/etc/dorm-memorial/app.env`：`root:dorm-memorial`，`0640`。
- `/var/lib/dorm-memorial`、`/var/cache/dorm-memorial`：`dorm-memorial:dorm-memorial`，目录 `0750`。
- 发布目录归 `root:root` 所有，运行账号只读。
- AList 配置、数据库和凭据不得由 `dorm-memorial` 账号读取。

## 5. 构建发布包

生产机不安装 Node.js。前端和 Linux 二进制在开发机或 CI 构建：

```powershell
npm ci --prefix web
npm run build --prefix web
$env:CGO_ENABLED='0'
$env:GOOS='linux'
$env:GOARCH='amd64'
go build -trimpath -ldflags='-s -w' -o build/linux-amd64/bin/dorm-memorial ./cmd/server
go build -trimpath -ldflags='-s -w' -o build/linux-amd64/bin/dbtool ./cmd/dbtool
```

将 `web/dist` 复制为发布包中的 `web`，生成 SHA-256 清单。发布包只应包含两个二进制、前端静态文件、校验清单和版本信息。

上传到生产机的临时目录后先校验哈希，再移动到 `/opt/dorm-memorial/releases/<git-sha>`。不要直接覆盖当前运行目录。

## 6. AList 安装和隔离

1. 备份现有 AList 数据（若有），安装并固定已验证版本；应用部署当天不同时升级 AList。
2. 配置 AList 仅监听 `127.0.0.1:5244`，不得开放 UFW 端口。
3. 在 AList 中挂载夸克 `3048` 目录，并创建专用用户 `dorm-memorial`。
4. 将该用户基础路径限制到 `3048`，授予应用实际需要的读取、创建目录、上传、重命名和删除权限。
5. 使用专用账号登录验证其 `/` 只能看到 `3048` 内容；不能看到夸克网盘其他目录。
6. 应用优先配置 `ALIST_USERNAME` 和 `ALIST_PASSWORD`，以便自动刷新令牌；临时复制的 Token 只用于诊断。
7. 在服务器本机完成一次小文件上传、Range 读取、SHA-256 校验和删除测试。

## 7. 应用配置

生产环境文件 `/etc/dorm-memorial/app.env` 至少包含：

```dotenv
APP_ENV=production
APP_ADDRESS=127.0.0.1:8080
APP_DATABASE_PATH=/var/lib/dorm-memorial/app.db
APP_FRONTEND_DIR=/opt/dorm-memorial/current/web
APP_PUBLIC_URL=https://3048.clical.xin
APP_COOKIE_SECURE=true
APP_SESSION_TTL=720h
APP_MEDIA_CACHE_DIR=/var/cache/dorm-memorial/media
APP_MEDIA_CACHE_MAX_BYTES=2147483648

ALIST_BASE_URL=http://127.0.0.1:5244
ALIST_USERNAME=dorm-memorial
ALIST_PASSWORD=<保存在服务器上的专用密码>
ALIST_TOKEN=
ALIST_ROOT=/
```

只有空数据库首次启动时临时加入 `APP_BOOTSTRAP_ADMIN_*`。确认管理员可以登录后立即从配置中删除这些值并重启应用；不要依赖 bootstrap 变量作为日常密码管理机制。

## 8. Nginx 与 TLS

在不影响现有站点的前提下为 `3048.clical.xin` 新增独立 `server_name`。关键要求：

- HTTP 永久跳转 HTTPS。
- TLS 终止在 Nginx，Go 应用仍只监听 `127.0.0.1:8080`。
- 登录和注册接口保留速率限制。
- 上传路径关闭请求缓冲，设置大文件超时和并发限制。
- 媒体响应不由 Nginx 长期缓存；应用自己的 2 GiB LRU 缓存是唯一受控缓存。
- 不代理 AList 管理页，也不开放 `5244`。
- `nginx -t` 成功后使用 reload，不停止同机其他站点。

签发证书前先确认 DNS；证书完成后再启用 `APP_COOKIE_SECURE=true` 的生产应用。

## 9. 首次发布步骤

1. 记录当前 CPU、内存、Swap、磁盘、现有监听端口和服务状态，留作基线。
2. 创建系统账号、目录和权限。
3. 安装并验证 AList 回环服务及专用目录权限。
4. 上传并校验应用发布包。
5. 安装 `/etc/dorm-memorial/app.env`、systemd unit 和 Nginx 配置。
6. 若数据库为空，设置一次性 bootstrap 管理员；若数据库从本地迁移，先执行完整性检查再上传。
7. 启动 AList，确认 `127.0.0.1:5244` 健康。
8. 启动应用。启动过程自动执行 SQLite 迁移；首次启动必须保留完整日志。
9. 检查 `/health/live`、`/health/ready`，再检查管理员健康接口。
10. 移除 bootstrap 凭据并重启应用。
11. 配置并 reload Nginx，最后从公网 HTTPS 地址验收。
12. 在管理中心执行一次手动导出，将下载文件保存到管理员设备，并恢复到临时路径完成完整性演练。

推荐发布命令采用原子软链接切换：

```bash
ln -sfn /opt/dorm-memorial/releases/<git-sha> /opt/dorm-memorial/current.next
mv -Tf /opt/dorm-memorial/current.next /opt/dorm-memorial/current
systemctl restart dorm-memorial
```

## 10. 验收清单

### 基础与安全

- 公网只能访问 80/443；8080 和 5244 从公网不可达。
- HTTPS 证书链正常，登录 Cookie 带 `Secure`、`HttpOnly`、`SameSite=Lax`。
- 普通成员访问管理 API 返回 403。
- AList 专用账号只能访问 `3048`。
- 日志不出现密码、Token、邀请码、Cookie 或私信正文。

### 功能

- 管理员登录、批量邀请码和邀请注册正常。
- 文字帖子、评论、点赞、留言、隐藏/恢复正常。
- 图片上传、预览、头像裁剪和照片墙正常。
- 群聊、私信、图片/视频/音频消息、通知跳转和清空正常。
- 管理中心可读取用户、公共群聊消息和媒体列表；管理员不能读取私信正文。
- 管理员可以在管理中心生成并下载 SQLite 业务数据备份；普通成员调用备份接口返回 403。
- 删除未引用媒体会同步删除远端对象；删除帖子仍按当前设计执行软删除。

### 媒体链路

- 先用小图片和短视频完成上传、读取、Range 和删除。
- 再用约 200 MiB 视频验证连续播放，逐秒记录吞吐与停顿。
- 最后在维护窗口使用 2 GiB 级文件验证长传输；同时观察 Nginx 临时目录、应用 RSS、Swap 和 AList 日志。
- 缓存命中后再次访问同一媒体，不应重复请求 AList；缓存目录总量不得持续超过 2 GiB。

### 资源预算

- 应用稳定 RSS 目标低于 256 MiB，压力场景不超过 systemd `MemoryHigh`。
- AList 与应用合计不能造成持续 Swap 抖动。
- 部署后磁盘至少保留 15 GiB 可用空间。
- journald、备份和缓存均有明确上限，不允许无限增长。

## 11. 备份和恢复

采用管理员手动导出，不在本阶段启用定时备份。管理中心的“备份”页调用在线快照接口：先执行 WAL 检查点和 SQLite `VACUUM INTO`，再通过完整性检查，最后把数据库文件直接下载到管理员设备；生产服务器不长期保留导出副本。

人工备份规则：

- 每次应用发布、数据库迁移、批量删除或账号管理操作前必须导出。
- 有重要新增内容时及时导出；即使没有维护操作，也建议每周至少保留一份。
- 文件包含邮箱、密码哈希、会话、私信、通知等敏感数据，必须放在受控设备的加密磁盘或加密归档中，不得通过公共链接分享。
- 至少保留最近 4 个可用版本；每月选择一份恢复到临时路径，记录 `PRAGMA integrity_check` 结果。
- 管理界面导出只包含 SQLite 业务数据和媒体索引，不包含 AList/夸克中的原始媒体。
- AList 配置和挂载信息单独备份，敏感凭据必须加密；原始媒体以夸克为主副本，后续仍建议增加离线副本。

恢复必须在维护窗口中进行：先停止应用，保留故障数据库，使用 `dbtool restore` 恢复到一个全新的文件路径，验证完整性后再切换 `APP_DATABASE_PATH`。禁止直接覆盖正在使用的数据库。

## 12. 回滚方案

每次发布前：

1. 生成并校验数据库备份。
2. 记录当前 `/opt/dorm-memorial/current` 指向的提交。
3. 保留至少前两个发布目录。
4. 保存当前 systemd 和 Nginx 配置副本。

应用异常但数据库迁移兼容时，切回旧发布软链接并重启服务。若新版本已经执行不兼容迁移，则停止应用，将当前数据库移出运行路径，使用发布前备份恢复到新文件，再切回旧版本。不得直接覆盖或删除故障数据库。

AList 故障时不回滚应用数据库；先停止媒体写入并保留文字功能，修复 AList 凭据或挂载后再恢复上传。

## 13. 部署后观察期

上线后前 24 小时重点监控：

- `systemctl status` 和应用/AList 重启次数。
- 应用与 AList RSS、Swap、CPU 和打开文件数。
- `/var/lib`、`/var/cache`、Nginx 临时目录和 journald 用量。
- AList 认证失败、夸克 Cookie 失效、媒体读写超时和 Range 中断。
- 首次人工备份是否已下载到管理员设备，并能恢复到临时数据库。

观察期内不同时升级系统、Nginx、AList 和应用。24 小时稳定后，再开放给全部 5～6 名成员。

## 14. 建议执行批次

| 批次 | 内容 | 预计时间 | 完成标志 |
| --- | --- | ---: | --- |
| D0 | 复核域名解析、确定维护时间；修订部署模板 | 30～60 分钟 | 上线门槛全部通过 |
| D1 | 系统账号、目录、AList、TLS 和 Nginx 预配置 | 45～90 分钟 | 回环服务健康，公网仅 HTTPS |
| D2 | 首次应用发布、迁移、管理员登录和小文件验收 | 30～45 分钟 | 核心功能通过 |
| D3 | 中/大媒体连续传输与资源监控 | 60～120 分钟 | 无磁盘缓冲失控和持续停顿 |
| D4 | 备份恢复、回滚演练和 24 小时观察 | 24 小时 | 可恢复、资源稳定 |

所有批次都应逐项记录命令结果、服务状态和异常；出现数据完整性、权限越界、磁盘低于安全线或持续 Swap 抖动时立即停止进入下一批次。
