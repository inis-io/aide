// Package llm - API 商城「AI 大模型统一网关」客户端子包（OpenAI 兼容协议，sk-key Bearer 认证）。
//
// 本包是面向 Licen Hub LLM 网关的独立轻客户端，与 apis 根包的能力子包（iplocate/email）
// 形态不同：不挂在 lic.Apis 上、不 import runtime/protocol/传输实现，只依赖标准库。
// 决策理由（licen-hub docs/plan/apis/10 §4.1）：
//
//   - 凭证体系不同：apis 根 Client 由 licence runtime 注入许可证签名头族；LLM 网关只认
//     Authorization: Bearer sk-…（商城 API 密钥），两套凭证链路语义互不污染。
//   - 目标客户可能没有许可证：LLM 网关调用方是「注册即买套餐/充余额」的商城用户，
//     不一定有交付项目与许可证，独立构造不依赖 licence.New(...).Start() 激活流程。
//
// 与 lic.Apis 并存关系：
//
//   - 交付项目内调用 IP 定位/邮件代发 → licence.New(...) → lic.Apis.IPLocate/Email（许可证计量）
//   - 任意服务调用大模型 → llm.NewClient(...)（sk-key 计量）；同一份钱包/订阅/免费额度体系扣费
//
// 错误处理范式：非 2xx 一律归一为 *llm.Error（errors.As 断言），Code 取值见 ErrorCode*
// 常量（与服务端 writeServiceError 映射逐字一致）；IsRetryable() 仅对 upstream_error /
// server_error / model_unavailable 为 true。本包不做客户端自动重试（服务端已做渠道
// failover；quota_exceeded/insufficient_balance 重试无意义；rate_limited 的退避节奏由
// 调用方按自身业务决定）。
package llm
