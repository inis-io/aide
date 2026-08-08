package cachex

import (
	"context"
	"fmt"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// ================================== Redis 缓存 - 开始 ==================================

// RedisStore - Redis 缓存驱动
type RedisStore struct {
	// Redis 客户端
	Client *redis.Client
	// 配置
	Config RedisConfig
}

// newRedisStore - Redis 缓存驱动工厂
func newRedisStore(config Config) (Store, error) {
	return &RedisStore{
		Client: redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%v", config.Redis.Host, config.Redis.Port),
			DB:       config.Redis.Database,
			Password: config.Redis.Password,
			// 明确禁用维护通知
			// 这可防止客户端发送“CLIENT MAINT_NOTIFICATIONS ON”
			MaintNotificationsConfig: &maintnotifications.Config{
				Mode: maintnotifications.ModeDisabled,
			},
		}),
		Config: config.Redis,
	}, nil
}

// Has - 判断缓存是否存在
func (this *RedisStore) Has(key string) (ok bool) {
	ctx := context.Background()
	result, err := this.Client.Exists(ctx, key).Result()
	return utils.Ternary[bool](err != nil, false, result == 1)
}

// Get - 获取缓存
func (this *RedisStore) Get(key string) (value any) {
	ctx := context.Background()
	result, err := this.Client.Get(ctx, key).Result()
	return utils.Ternary[any](err != nil, nil, utils.Json.Decode(result))
}

// Set - 设置缓存（expired <= 0 表示永不过期）
func (this *RedisStore) Set(key string, value any, expired time.Duration) (ok bool) {
	ctx := context.Background()
	// go-redis 以 0 表示永不过期
	if expired < 0 {
		expired = 0
	}
	err := this.Client.Set(ctx, key, utils.Json.Encode(value), expired).Err()
	return utils.Ternary[bool](err != nil, false, true)
}

// Delete - 删除缓存
func (this *RedisStore) Delete(key ...string) (ok bool) {
	if len(key) == 0 {
		return true
	}
	ctx := context.Background()
	err := this.Client.Del(ctx, key...).Err()
	return utils.Ternary[bool](err != nil, false, true)
}

// Clear - 清空缓存（有前缀时按前缀扫描删除，避免清空共享库中其他应用的数据）
func (this *RedisStore) Clear() (ok bool) {

	ctx := context.Background()

	// 前缀为空时保持兼容 - 回退为清空整个库
	if utils.Is.Empty(this.Config.Prefix) {
		err := this.Client.FlushDB(ctx).Err()
		return utils.Ternary[bool](err != nil, false, true)
	}

	// 按前缀扫描匹配 Driver 层命名的键
	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := this.Client.Scan(ctx, cursor, this.Config.Prefix+"-*", 100).Result()
		if err != nil {
			return false
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}

	// 分批删除，避免单次 DEL 携带过多键
	const batchSize = 100
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		if err := this.Client.Del(ctx, keys[start:end]...).Err(); err != nil {
			return false
		}
	}

	return true
}

// 编译期接口校验
var _ Store = (*RedisStore)(nil)

// ================================== Redis 缓存 - 结束 ==================================
