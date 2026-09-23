// Package licence - Licence SDK 门面（facade）包。
//
// 本包不承载任何实现，仅以类型别名（type X = ...）、常量别名（const X = ...）与
// 函数/变量别名（var X = ...）形式镜像全部 86 个导出符号，保证下游
// `import licence "github.com/inis-io/aide/licence"` 零改动。
//
// 实现下沉到两个子包：
//
//   - protocol/  平台契约镜像层：信封/载荷/签发验签（licence.go、sign.go、
//     envelope.go、signature.go）、运行面状态码与本地判定（status.go）、
//     版本范围表达式（version-range.go）。与 licen-hub 平台
//     backend/app/common/sign、license-status.go、version-range.go 字节级镜像。
//   - runtime/   运行面客户端层：Client、传输（HTTP/gRPC）、设备指纹、本地加密
//     存储、SaaS 租户与菜单、平台配置同步、在线更新、自助发放/兑换、配置回推。
//
// 依赖方向（编译期保证无环）：
//
//	admin/callback/updater → runtime + protocol（直连子包，不经门面）
//	runtime → protocol（runtime 只做限定引用 LicenceProtocol.X，不再导出）
//	licence（本包）→ runtime + protocol（纯别名，禁止新增实现）
//
// 新增能力的归属规则：契约镜像语义进 protocol/，客户端行为进 runtime/，
// 本包 facade.go 只做别名登记；子包之间禁止反向依赖本包。
package licence
