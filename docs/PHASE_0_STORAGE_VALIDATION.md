# 阶段 0：AList 存储链路验证

当前切片提供一个不落盘缓存上传内容的 Go `ObjectStorage` 原型，以及可对真实 AList/夸克目录执行上传、校验、移动和清理的探针。

## 已实现

- `PUT /api/fs/put` 流式上传，显式设置 `Content-Length`。
- `POST /api/fs/get` 获取对象元数据与临时读取地址。
- 下载与 Range 请求透传。
- 移动、重命名和删除。
- 根目录隔离及 `..`、反斜杠和 NUL 路径拒绝。
- HTTP 状态与 AList 业务错误到统一存储错误的映射。
- 基于 `httptest` 的模拟集成测试，不依赖真实凭据。

## 本地测试

```powershell
go test ./...
go test -race ./...
```

## 真实 AList 探针

复制 `.env.example` 中的变量到当前终端环境，但不要提交真实 Token：

```powershell
$env:ALIST_BASE_URL = 'http://127.0.0.1:5244'
$env:ALIST_TOKEN = '<token>'
$env:ALIST_ROOT = '/dorm-memorial/probe'
go run ./cmd/storage-probe
```

使用真实大文件验证流式行为：

```powershell
go run ./cmd/storage-probe -file 'D:\media\large-video.mp4'
```

探针依次执行上传、远端大小检查、完整下载 SHA-256 校验、移动/重命名和删除。只有显式传入 `-keep` 时才保留测试对象。

## 尚待真实环境完成

- 连接专用夸克目录并记录上传速度与进程内存峰值。
- 验证 1～2 GiB 视频、Range 读取和网络中断行为。
- 验证夸克 Cookie 失效后的错误映射和恢复流程。
- 确认 AList 版本不低于已修复路径穿越问题的版本。
