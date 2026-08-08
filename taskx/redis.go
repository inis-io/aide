package taskx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	redislib "github.com/redis/go-redis/v9"
)

type redisBroker struct {
	client   *redislib.Client
	prefix   string
	leaseTTL time.Duration
}

func newRedisBroker(config Config) (Broker, error) {
	client := redislib.NewClient(&redislib.Options{
		Addr: config.Redis.Addr, Username: config.Redis.Username, Password: config.Redis.Password,
		DB: config.Redis.DB, PoolSize: config.Redis.PoolSize,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("taskx: Redis 连接失败: %w", err)
	}
	return &redisBroker{client: client, prefix: config.Redis.Prefix, leaseTTL: config.LeaseTTL}, nil
}

func (this *redisBroker) tag(queue string) (string, error) {
	queue, err := cleanQueue(queue)
	if err != nil {
		return "", err
	}
	return this.prefix + "{" + queue + "}:", nil
}

func (this *redisBroker) key(queue, suffix string) (string, error) {
	tag, err := this.tag(queue)
	if err != nil {
		return "", err
	}
	return tag + suffix, nil
}

func (this *redisBroker) keys(queue string, suffixes ...string) ([]string, error) {
	keys := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		key, err := this.key(queue, suffix)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (this *redisBroker) queuesKey() string { return this.prefix + "queues" }

func (this *redisBroker) acquireLocks(ctx context.Context, msg *Message) error {
	msg.lockKeys = prepareLockRefs(msg)
	acquired := make([]lockRef, 0, len(msg.lockKeys))
	for _, ref := range msg.lockKeys {
		key, err := this.key(msg.Queue, "lock:"+ref.Key)
		if err != nil {
			this.removeLockRefs(ctx, msg.Queue, acquired, "", msg.Id)
			return err
		}
		ok, err := this.client.SetNX(ctx, key, msg.Id, lockTTL(msg, ref.Kind)).Result()
		if err != nil {
			this.removeLockRefs(ctx, msg.Queue, acquired, "", msg.Id)
			return err
		}
		if !ok {
			this.removeLockRefs(ctx, msg.Queue, acquired, "", msg.Id)
			return lockConflict(ref.Kind)
		}
		acquired = append(acquired, ref)
	}
	return nil
}

var redisUnlockScript = redislib.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)

func (this *redisBroker) removeLockRefs(ctx context.Context, queue string, refs []lockRef, kind, owner string) {
	for _, ref := range refs {
		if kind != "" && ref.Kind != kind {
			continue
		}
		key, err := this.key(queue, "lock:"+ref.Key)
		if err == nil {
			_ = redisUnlockScript.Run(ctx, this.client, []string{key}, owner).Err()
		}
	}
}

var redisEnqueueScript = redislib.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
if tonumber(ARGV[3]) > tonumber(ARGV[4]) then
  redis.call('ZADD', KEYS[3], ARGV[3], ARGV[1])
else
  redis.call('LPUSH', KEYS[2], ARGV[1])
end
return 1`)

func (this *redisBroker) Enqueue(ctx context.Context, msg *Message) error {
	if err := prepareMessage(msg); err != nil {
		return err
	}
	if err := this.client.SAdd(ctx, this.queuesKey(), msg.Queue).Err(); err != nil {
		return fmt.Errorf("taskx: 登记 Redis 队列失败: %w", err)
	}
	if err := this.acquireLocks(ctx, msg); err != nil {
		return err
	}
	data, err := encodeMessage(msg)
	if err != nil {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "", msg.Id)
		return err
	}
	keys, err := this.keys(msg.Queue, "msgs", statePending, stateScheduled)
	if err != nil {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "", msg.Id)
		return err
	}
	result, err := redisEnqueueScript.Run(ctx, this.client, keys, msg.Id, data, millis(msg.ProcessAt), time.Now().UnixMilli()).Int()
	if err != nil {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "", msg.Id)
		return err
	}
	if result == 0 {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "", msg.Id)
		return ErrTaskIdConflict
	}
	return nil
}

var redisClaimScript = redislib.NewScript(`
while true do
  local id = redis.call('RPOP', KEYS[1])
  if not id then return nil end
  local raw = redis.call('HGET', KEYS[4], id)
  if raw then
    redis.call('HSET', KEYS[2], id, raw)
    redis.call('ZADD', KEYS[3], ARGV[1], id)
    return raw
  end
end`)

func (this *redisBroker) Claim(ctx context.Context, queues []string) (*Message, error) {
	leaseUntil := time.Now().Add(this.leaseTTL)
	for _, queue := range queues {
		keys, err := this.keys(queue, statePending, stateActive, "lease", "msgs")
		if err != nil {
			return nil, err
		}
		text, err := redisClaimScript.Run(ctx, this.client, keys, leaseUntil.UnixMilli()).Text()
		if errors.Is(err, redislib.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		msg, err := decodeMessage([]byte(text))
		if err != nil {
			return nil, err
		}
		msg.leaseUntil = leaseUntil
		return msg, nil
	}
	return nil, nil
}

var redisPromoteScript = redislib.NewScript(`
local moved = 0
local scheduled = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1])
for _, id in ipairs(scheduled) do
  if redis.call('ZREM', KEYS[2], id) == 1 and redis.call('HEXISTS', KEYS[1], id) == 1 then
    redis.call('LPUSH', KEYS[5], id); moved = moved + 1
  end
end
local retry = redis.call('ZRANGEBYSCORE', KEYS[3], '-inf', ARGV[1])
for _, id in ipairs(retry) do
  if redis.call('ZREM', KEYS[3], id) == 1 and redis.call('HEXISTS', KEYS[1], id) == 1 then
    redis.call('LPUSH', KEYS[5], id); moved = moved + 1
  end
end
local leases = redis.call('ZRANGEBYSCORE', KEYS[4], '-inf', ARGV[1])
for _, id in ipairs(leases) do
  local raw = redis.call('HGET', KEYS[6], id)
  if raw and redis.call('HDEL', KEYS[6], id) == 1 then
    redis.call('HSET', KEYS[1], id, raw)
    redis.call('LPUSH', KEYS[5], id)
    moved = moved + 1
  end
  redis.call('ZREM', KEYS[4], id)
end
return moved`)

var redisCleanupScript = redislib.NewScript(`
local ids = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
for _, id in ipairs(ids) do
  if redis.call('ZREM', KEYS[1], id) == 1 then redis.call('HDEL', KEYS[2], id) end
end
return #ids`)

func (this *redisBroker) Promote(ctx context.Context) (int, error) {
	queues, err := this.client.SMembers(ctx, this.queuesKey()).Result()
	if err != nil {
		return 0, err
	}
	moved := 0
	now := time.Now().UnixMilli()
	for _, queue := range queues {
		keys, keyErr := this.keys(queue, "msgs", stateScheduled, stateRetry, "lease", statePending, stateActive)
		if keyErr != nil {
			return moved, keyErr
		}
		count, runErr := redisPromoteScript.Run(ctx, this.client, keys, now).Int()
		if runErr != nil {
			return moved, runErr
		}
		moved += count
		cleanupKeys, _ := this.keys(queue, stateCompleted, "msgs")
		if _, runErr = redisCleanupScript.Run(ctx, this.client, cleanupKeys, now).Int(); runErr != nil {
			return moved, runErr
		}
	}
	return moved, nil
}

var redisAckScript = redislib.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 then return 0 end
redis.call('HDEL', KEYS[1], ARGV[1]); redis.call('ZREM', KEYS[2], ARGV[1])
if tonumber(ARGV[3]) > 0 then
  redis.call('HSET', KEYS[3], ARGV[1], ARGV[2]); redis.call('ZADD', KEYS[4], ARGV[3], ARGV[1])
else
  redis.call('HDEL', KEYS[3], ARGV[1])
end
return 1`)

func (this *redisBroker) Ack(ctx context.Context, msg *Message) error {
	stored := cloneMessage(msg)
	stored.leaseUntil = time.Time{}
	stored.completedAt = time.Now()
	data, err := encodeMessage(stored)
	if err != nil {
		return err
	}
	keys, err := this.keys(msg.Queue, stateActive, "lease", "msgs", stateCompleted)
	if err != nil {
		return err
	}
	expiresAt := int64(0)
	if msg.Retention > 0 {
		expiresAt = stored.completedAt.Add(msg.Retention).UnixMilli()
	}
	changed, err := redisAckScript.Run(ctx, this.client, keys, msg.Id, data, expiresAt).Int()
	if err == nil && changed == 1 {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "", msg.Id)
	}
	return err
}

var redisMoveActiveScript = redislib.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 then return 0 end
redis.call('HDEL', KEYS[1], ARGV[1]); redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('HSET', KEYS[3], ARGV[1], ARGV[2]); redis.call('ZADD', KEYS[4], ARGV[3], ARGV[1])
return 1`)

func (this *redisBroker) Retry(ctx context.Context, msg *Message, cause error) error {
	stored := cloneMessage(msg)
	stored.Attempts++
	stored.LastError = cause.Error()
	stored.leaseUntil = time.Time{}
	data, err := encodeMessage(stored)
	if err != nil {
		return err
	}
	keys, err := this.keys(msg.Queue, stateActive, "lease", "msgs", stateRetry)
	if err != nil {
		return err
	}
	changed, err := redisMoveActiveScript.Run(ctx, this.client, keys, msg.Id, data, millis(msg.RetryAt)).Int()
	if err == nil && changed == 1 {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "unique", msg.Id)
	}
	return err
}

func (this *redisBroker) Archive(ctx context.Context, msg *Message, cause error) error {
	stored := cloneMessage(msg)
	stored.LastError = cause.Error()
	stored.leaseUntil = time.Time{}
	stored.archivedAt = time.Now()
	data, err := encodeMessage(stored)
	if err != nil {
		return err
	}
	keys, err := this.keys(msg.Queue, stateActive, "lease", "msgs", stateArchived)
	if err != nil {
		return err
	}
	changed, err := redisMoveActiveScript.Run(ctx, this.client, keys, msg.Id, data, stored.archivedAt.UnixMilli()).Int()
	if err == nil && changed == 1 {
		this.removeLockRefs(ctx, msg.Queue, msg.lockKeys, "", msg.Id)
	}
	return err
}

var redisReleaseScript = redislib.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if not raw then return 0 end
redis.call('HDEL', KEYS[1], ARGV[1]); redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('HSET', KEYS[3], ARGV[1], raw); redis.call('LPUSH', KEYS[4], ARGV[1])
return 1`)

func (this *redisBroker) Release(ctx context.Context, msg *Message) error {
	keys, err := this.keys(msg.Queue, stateActive, "lease", "msgs", statePending)
	if err != nil {
		return err
	}
	return redisReleaseScript.Run(ctx, this.client, keys, msg.Id).Err()
}

var redisExtendScript = redislib.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 then return 0 end
redis.call('ZADD', KEYS[2], ARGV[2], ARGV[1]); return 1`)

func (this *redisBroker) Extend(ctx context.Context, msg *Message, leaseUntil time.Time) error {
	keys, err := this.keys(msg.Queue, stateActive, "lease")
	if err != nil {
		return err
	}
	err = redisExtendScript.Run(ctx, this.client, keys, msg.Id, leaseUntil.UnixMilli()).Err()
	if err == nil {
		msg.leaseUntil = leaseUntil
	}
	return err
}

func (this *redisBroker) queueNames(ctx context.Context, queue string) ([]string, error) {
	if queue != "" {
		if _, err := cleanQueue(queue); err != nil {
			return nil, err
		}
		return []string{queue}, nil
	}
	queues, err := this.client.SMembers(ctx, this.queuesKey()).Result()
	sort.Strings(queues)
	return queues, err
}

func (this *redisBroker) stateIDs(ctx context.Context, queue, state string) ([]string, error) {
	key, err := this.key(queue, state)
	if err != nil {
		return nil, err
	}
	switch state {
	case statePending:
		return this.client.LRange(ctx, key, 0, -1).Result()
	case stateActive:
		return this.client.HKeys(ctx, key).Result()
	default:
		return this.client.ZRange(ctx, key, 0, -1).Result()
	}
}

func (this *redisBroker) Inspect(ctx context.Context, query InspectQuery) (*InspectResult, error) {
	query, err := normalizeInspect(query)
	if err != nil {
		return nil, err
	}
	queues, err := this.queueNames(ctx, query.Queue)
	if err != nil {
		return nil, err
	}
	result := &InspectResult{Counts: make(map[string]map[string]int)}
	if query.State == "" {
		for _, queue := range queues {
			result.Counts[queue] = make(map[string]int)
			for _, state := range allStates {
				ids, stateErr := this.stateIDs(ctx, queue, state)
				if stateErr != nil {
					return nil, stateErr
				}
				result.Counts[queue][state] = len(ids)
			}
		}
		return result, nil
	}
	ids, err := this.stateIDs(ctx, query.Queue, query.State)
	if err != nil {
		return nil, err
	}
	result.Total = len(ids)
	start := (query.Page - 1) * query.Size
	end := start + query.Size
	if start > len(ids) {
		start = len(ids)
	}
	if end > len(ids) {
		end = len(ids)
	}
	dataKey, _ := this.key(query.Queue, "msgs")
	if query.State == stateActive {
		dataKey, _ = this.key(query.Queue, stateActive)
	}
	for _, id := range ids[start:end] {
		data, getErr := this.client.HGet(ctx, dataKey, id).Bytes()
		if getErr != nil {
			continue
		}
		msg, decodeErr := decodeMessage(data)
		if decodeErr == nil {
			result.Tasks = append(result.Tasks, *msg)
		}
	}
	return result, nil
}

var redisManageScript = redislib.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if not raw then return nil end
if ARGV[3] == 'scheduled' or ARGV[3] == 'retry' or ARGV[3] == 'completed' or ARGV[3] == 'archived' then
  redis.call('ZREM', KEYS[2], ARGV[1])
elseif ARGV[3] == 'pending' then
  redis.call('LREM', KEYS[2], 0, ARGV[1])
end
if ARGV[2] == 'run' then
  redis.call('HSET', KEYS[1], ARGV[1], ARGV[4]); redis.call('LPUSH', KEYS[3], ARGV[1])
else
  redis.call('HDEL', KEYS[1], ARGV[1])
end
return raw`)

func (this *redisBroker) Manage(ctx context.Context, op ManageOp) error {
	op, err := validateManage(op)
	if err != nil {
		return err
	}
	if op.Action == "purge" {
		ids, listErr := this.stateIDs(ctx, op.Queue, op.State)
		if listErr != nil {
			return listErr
		}
		for _, id := range ids {
			if err = this.manageOne(ctx, ManageOp{Action: "delete", Queue: op.Queue, State: op.State, Id: id}); err != nil {
				return err
			}
		}
		return nil
	}
	return this.manageOne(ctx, op)
}

func (this *redisBroker) manageOne(ctx context.Context, op ManageOp) error {
	dataKey, _ := this.key(op.Queue, "msgs")
	stateKey, _ := this.key(op.Queue, op.State)
	pendingKey, _ := this.key(op.Queue, statePending)
	data, err := this.client.HGet(ctx, dataKey, op.Id).Bytes()
	if errors.Is(err, redislib.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	msg, err := decodeMessage(data)
	if err != nil {
		return err
	}
	if op.Action == "run" {
		if op.State == stateArchived {
			if err = this.acquireLocks(ctx, msg); err != nil {
				return err
			}
		}
		msg.Attempts = 0
		msg.LastError = ""
		msg.ProcessAt = time.Time{}
		msg.RetryAt = time.Time{}
		msg.archivedAt = time.Time{}
		data, err = encodeMessage(msg)
		if err != nil {
			return err
		}
	}
	result, err := redisManageScript.Run(ctx, this.client, []string{dataKey, stateKey, pendingKey}, op.Id, op.Action, op.State, data).Result()
	if err != nil && !errors.Is(err, redislib.Nil) {
		if op.Action == "run" && op.State == stateArchived {
			this.removeLockRefs(ctx, op.Queue, msg.lockKeys, "", msg.Id)
		}
		return err
	}
	if result == nil && op.Action == "run" && op.State == stateArchived {
		this.removeLockRefs(ctx, op.Queue, msg.lockKeys, "", msg.Id)
	}
	if result != nil && op.Action == "delete" {
		this.removeLockRefs(ctx, op.Queue, msg.lockKeys, "", msg.Id)
	}
	return nil
}

func (this *redisBroker) Close() error { return this.client.Close() }

var _ Broker = (*redisBroker)(nil)
