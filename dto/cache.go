package dto

// CacheConfig - 缓存总配置（由调用方传入）
type CacheConfig struct {
	// 配置哈希（可选，不传会自动计算）
	Hash   string `json:"hash"`
	// 引擎：redis / file
	Engine string `json:"engine"`
	// Redis 配置
	Redis  CacheRedisConfig `json:"redis"`
	// 文件缓存配置
	File   CacheFileConfig  `json:"file"`
}

// CacheRedisConfig - Redis 配置
type CacheRedisConfig struct {
	// Host     - 主机地址 - 不限制 host，因为 docker 的地址不正常
	Host     string `json:"host" comment:"主机地址" default:"localhost"`
	// Port     - 端口号
	Port     int    `json:"port" comment:"端口号" validate:"numeric" default:"6379"`
	// Password - 密码
	Password string `json:"password" comment:"密码"`
	// Expired  - 过期时间
	Expired  int    `json:"expired"  comment:"过期时间" validate:"numeric" default:"7200"`
	// Prefix   - 前缀
	Prefix   string `json:"prefix"   comment:"前缀" validate:"alphaDash,max=12" default:"AIDE"`
	// Database - 数据库
	Database int    `json:"database" comment:"数据库索引" validate:"numeric"`
}

// CacheFileConfig - 文件缓存配置
type CacheFileConfig struct {
	// Expired - 过期时间
	Expired int    `json:"expired" comment:"过期时间" validate:"numeric" default:"7200"`
	// Prefix  - 前缀
	Prefix  string `json:"prefix"  comment:"前缀" validate:"alphaDash,max=12" default:"AIDE"`
	// Root    - 文件缓存根目录
	Root    string `json:"root"    comment:"文件缓存根目录" default:"./runtime/cache"`
	// Suffix  - 文件后缀
	Suffix  string `json:"suffix"  comment:"文件后缀" default:"json"`
}
