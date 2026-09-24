$ErrorActionPreference = "Stop"

$protoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

# protobuf 生成（生成代码禁止手改）。工作目录 = proto/，输出路径与 buf.gen.yaml 的 out=. 一致。
#
# 方式一（首选，需已装 buf）：Push-Location proto; buf generate; Pop-Location
# 方式二（protoc 直调，本机生成环境：protoc 33.0 + protoc-gen-go 1.36.10 + protoc-gen-go-grpc 1.5.1）：
#   apis/v1/runtime.proto import google/protobuf/struct.proto（Well-Known Types），protoc 必须能解析它：
#     · 官方 protoc 发行包自带 include/ 目录 → 追加 --proto_path="<protoc 安装目录>\include"；
#     · 仅有 protoc.exe 时用 --descriptor_set_in 指向 WKT 描述符集（本脚本采用，离线可复现）：
#         go run wkt.go "$env:TEMP\licence-wkt.pb"   # 从 google.golang.org/protobuf 运行时导出，见 wkt.go
# 两种方式生成结果逐字节一致（已用 include 目录 + --go_opt=M... 路线交叉验证）。
Push-Location $protoRoot
try {
    $wktSet = Join-Path $env:TEMP "licence-wkt.pb"
    go run wkt.go $wktSet
    protoc `
        --descriptor_set_in=$wktSet `
        --proto_path=. `
        --go_out=. --go_opt=paths=source_relative `
        --go-grpc_out=. --go-grpc_opt=paths=source_relative `
        licence/v1/runtime.proto `
        licence/v1/admin.proto `
        apis/v1/runtime.proto
} finally {
    Pop-Location
}
