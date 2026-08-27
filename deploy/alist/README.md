# AList 超星流式上传补丁

目标版本：AList `v3.63.0`（commit `843d9dc8149126976b2625911e45a4d3ffd6f2f5`）。

官方 `ChaoXingGroupDrive` 驱动会先把完整文件写入 `bytes.Buffer`，再发起 multipart 上传。`chaoxing-streaming.patch` 将正文替换为 `io.Pipe` 流，并保留确定的 multipart boundary 与 `Content-Length`，使内存占用不随文件大小增长。

构建前在对应版本的 AList 源码根目录执行：

```sh
git apply /path/to/chaoxing-streaming.patch
go test -vet=off ./drivers/chaoxing
go build -trimpath -o alist .
```

补丁不解决超星或代理链路主动重置连接的问题。部署前仍需在目标出口上验证上传成功率、Range 和完整下载。
