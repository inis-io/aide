# dto/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `dto` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 dto 专属约定。

## 模块定位

- 模块路径 `github.com/inis-io/aide/dto`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，无任何第三方依赖，仅依赖标准库。
- 定位：**数据传输对象**——各服务的配置与响应结构体（如 `JwtBody`），纯 struct、无行为、无方法（除 JSON tag 外）。
- 被父模块 `utils`（`jwt.go` / `cipher.go`）与各业务子包引用，改动结构体字段前必须确认调用方语义不受影响。

## 核心约定

- 结构体字段逐行写中文注释；JSON tag 与现有序列化语义保持兼容。
- 只允许新增字段，删除或调整字段语义属于 Breaking Change，需同步修改所有引用方。
- 不引入任何依赖：新增能力若需要第三方库，应放在调用方（utils 或业务包），而非 dto。

## 构建与测试命令

```bash
cd dto && go build ./... && go vet ./... && go test ./...   # 本模块独立执行（无测试文件）
```

## 文档

- 父模块使用教程见 [`../README.md`](../README.md)。
