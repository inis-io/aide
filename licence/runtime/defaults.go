package runtime

var (
	// DefaultServerURL - 官方授权平台地址；自定义平台请使用 AutoStartWithOptions 显式配置。
	DefaultServerURL = "https://apis.inis.cn"
	// DefaultBootstrapPublicKeys - 官方 bootstrap 根公钥表（只含公钥，不包含任何签名私钥）。
	// 多根并存的语义：验 keyset 时按 keyset 载荷的 keyVersion 精确选根，同一客户端可同时信任多个代际，
	// 存量旧部署用旧根签发、新部署用新根签发都能通过验签，换根不需要客户端改代码或改配置。
	// bootstrap-key-2026-01 - 原根，私钥已在生产丢失且无法恢复，随灾难恢复换根作废；
	// 条目保留只为让仍以旧根签名的存量部署继续可用，平台新签发一律不再使用它。
	// bootstrap-key-2026-02 - 2026-02 灾难恢复换根后的官方根，此后新签发以它为准。
	DefaultBootstrapPublicKeys = map[string]string{
		"bootstrap-key-2026-01": "cbc89be1e4fc10ed0411ac1fc4f26a6e24aec42a815fe8c09decfd405586ae6a",
		"bootstrap-key-2026-02": "82774729dbda2abb0f8ef5d5f05200700c80d127562136a80d3ee849b325763a",
	}
)
