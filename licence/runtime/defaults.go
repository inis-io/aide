package runtime

var (
	// DefaultServerURL - 官方授权平台地址；自定义平台请使用 AutoStartWithOptions 显式配置。
	DefaultServerURL = "https://licen-hub.inis.cn"
	// DefaultBootstrapPublicKeys - 官方 bootstrap 根公钥（只含公钥，不包含任何签名私钥）。
	DefaultBootstrapPublicKeys = map[string]string{
		"bootstrap-key-2026-01": "cbc89be1e4fc10ed0411ac1fc4f26a6e24aec42a815fe8c09decfd405586ae6a",
	}
)
