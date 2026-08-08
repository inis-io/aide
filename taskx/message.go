package taskx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Message - 任务消息
type Message struct {
	// Id - 任务 ID
	Id string `json:"id"`
	// Type - 任务类型名
	Type string `json:"type"`
	// Payload - 任务载荷
	Payload json.RawMessage `json:"payload"`
	// Queue - 所属队列
	Queue string `json:"queue"`
	// Group - 聚合组（预留）
	Group string `json:"group"`
	// MaxRetry - 最大重试次数
	MaxRetry int `json:"maxRetry"`
	// Timeout - 单次执行超时
	Timeout time.Duration `json:"timeout"`
	// Deadline - 执行截止时间点
	Deadline time.Time `json:"deadline"`
	// Retention - 完成后保留时长
	Retention time.Duration `json:"retention"`
	// UniqueTTL - 内容去重窗口
	UniqueTTL time.Duration `json:"uniqueTtl"`
	// Attempts - 已重试次数
	Attempts int `json:"attempts"`
	// ProcessAt - 期望执行时间
	ProcessAt time.Time `json:"processAt"`
	// RetryAt - 下次重试时间
	RetryAt time.Time `json:"retryAt"`
	// LastError - 最近一次失败原因
	LastError string `json:"lastError"`
	// CreatedAt - 入队时间
	CreatedAt time.Time `json:"createdAt"`

	taskIDSet   bool
	idPrepared  bool
	lockKeys    []lockRef
	leaseUntil  time.Time
	completedAt time.Time
	archivedAt  time.Time
	encodeErr   error
}

// Option - 入队选项
type Option func(*Message)

// NewMessage - 创建任务消息
func NewMessage(taskType string, payload any) *Message {
	msg := &Message{Type: taskType}
	switch value := payload.(type) {
	case json.RawMessage:
		msg.Payload = append(json.RawMessage(nil), value...)
	case []byte:
		msg.Payload = append(json.RawMessage(nil), value...)
	default:
		msg.Payload, msg.encodeErr = json.Marshal(payload)
	}
	if len(msg.Payload) == 0 && msg.encodeErr == nil {
		msg.Payload = json.RawMessage("null")
	}
	return msg
}

// ProcessIn - 设置延迟执行时间
func ProcessIn(duration time.Duration) Option {
	return func(msg *Message) { msg.ProcessAt = time.Now().Add(duration) }
}

// ProcessAt - 设置定时执行时间
func ProcessAt(at time.Time) Option {
	return func(msg *Message) { msg.ProcessAt = at }
}

// queueOption - 设置队列名
func queueOption(name string) Option {
	return func(msg *Message) { msg.Queue = name }
}

// MaxRetry - 设置最大重试次数
func MaxRetry(count int) Option {
	return func(msg *Message) { msg.MaxRetry = count }
}

// Timeout - 设置单次执行超时
func Timeout(duration time.Duration) Option {
	return func(msg *Message) { msg.Timeout = duration }
}

// Deadline - 设置执行截止时间点
func Deadline(at time.Time) Option {
	return func(msg *Message) { msg.Deadline = at }
}

// Retention - 设置完成保留时间
func Retention(duration time.Duration) Option {
	return func(msg *Message) { msg.Retention = duration }
}

// Unique - 设置内容去重窗口
func Unique(ttl time.Duration) Option {
	return func(msg *Message) { msg.UniqueTTL = ttl }
}

// TaskID - 设置确定性任务 ID
func TaskID(id string) Option {
	return func(msg *Message) {
		msg.Id = id
		msg.taskIDSet = true
	}
}

// messageWire - 消息持久化结构，所有时间统一编码为毫秒
type messageWire struct {
	Id            string          `json:"id"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
	Queue         string          `json:"queue"`
	Group         string          `json:"group,omitempty"`
	MaxRetry      int             `json:"maxRetry"`
	TimeoutMs     int64           `json:"timeoutMs,omitempty"`
	DeadlineMs    int64           `json:"deadlineMs,omitempty"`
	RetentionMs   int64           `json:"retentionMs,omitempty"`
	UniqueTTLms   int64           `json:"uniqueTtlMs,omitempty"`
	Attempts      int             `json:"attempts"`
	ProcessAtMs   int64           `json:"processAtMs,omitempty"`
	RetryAtMs     int64           `json:"retryAtMs,omitempty"`
	LastError     string          `json:"lastError,omitempty"`
	CreatedAtMs   int64           `json:"createdAtMs"`
	TaskIDSet     bool            `json:"taskIdSet,omitempty"`
	IDPrepared    bool            `json:"idPrepared,omitempty"`
	LockKeys      []lockRef       `json:"lockKeys,omitempty"`
	LeaseUntilMs  int64           `json:"leaseUntilMs,omitempty"`
	CompletedAtMs int64           `json:"completedAtMs,omitempty"`
	ArchivedAtMs  int64           `json:"archivedAtMs,omitempty"`
}

type lockRef struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

func millis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

func fromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}

func encodeMessage(msg *Message) ([]byte, error) {
	wire := messageWire{
		Id: msg.Id, Type: msg.Type, Payload: msg.Payload, Queue: msg.Queue, Group: msg.Group,
		MaxRetry: msg.MaxRetry, TimeoutMs: msg.Timeout.Milliseconds(), DeadlineMs: millis(msg.Deadline),
		RetentionMs: msg.Retention.Milliseconds(), UniqueTTLms: msg.UniqueTTL.Milliseconds(),
		Attempts: msg.Attempts, ProcessAtMs: millis(msg.ProcessAt), RetryAtMs: millis(msg.RetryAt),
		LastError: msg.LastError, CreatedAtMs: millis(msg.CreatedAt), TaskIDSet: msg.taskIDSet, IDPrepared: msg.idPrepared,
		LockKeys: append([]lockRef(nil), msg.lockKeys...), LeaseUntilMs: millis(msg.leaseUntil),
		CompletedAtMs: millis(msg.completedAt), ArchivedAtMs: millis(msg.archivedAt),
	}
	return json.Marshal(wire)
}

func decodeMessage(data []byte) (*Message, error) {
	var wire messageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	return &Message{
		Id: wire.Id, Type: wire.Type, Payload: wire.Payload, Queue: wire.Queue, Group: wire.Group,
		MaxRetry: wire.MaxRetry, Timeout: time.Duration(wire.TimeoutMs) * time.Millisecond,
		Deadline: fromMillis(wire.DeadlineMs), Retention: time.Duration(wire.RetentionMs) * time.Millisecond,
		UniqueTTL: time.Duration(wire.UniqueTTLms) * time.Millisecond, Attempts: wire.Attempts,
		ProcessAt: fromMillis(wire.ProcessAtMs), RetryAt: fromMillis(wire.RetryAtMs),
		LastError: wire.LastError, CreatedAt: fromMillis(wire.CreatedAtMs), taskIDSet: wire.TaskIDSet, idPrepared: wire.IDPrepared,
		lockKeys: append([]lockRef(nil), wire.LockKeys...), leaseUntil: fromMillis(wire.LeaseUntilMs),
		completedAt: fromMillis(wire.CompletedAtMs), archivedAt: fromMillis(wire.ArchivedAtMs),
	}, nil
}

func cloneMessage(msg *Message) *Message {
	if msg == nil {
		return nil
	}
	copyMsg := *msg
	copyMsg.Payload = append(json.RawMessage(nil), msg.Payload...)
	copyMsg.lockKeys = append([]lockRef(nil), msg.lockKeys...)
	return &copyMsg
}

func prepareMessage(msg *Message) error {
	if msg == nil {
		return fmt.Errorf("taskx: 任务消息不能为空")
	}
	if msg.encodeErr != nil {
		return fmt.Errorf("taskx: 任务载荷编码失败: %w", msg.encodeErr)
	}
	msg.Type = strings.TrimSpace(msg.Type)
	if msg.Type == "" {
		return fmt.Errorf("taskx: 任务类型不能为空")
	}
	if len(msg.Payload) == 0 {
		msg.Payload = json.RawMessage("null")
	}
	if !json.Valid(msg.Payload) {
		return fmt.Errorf("taskx: 任务载荷不是有效 JSON")
	}
	msg.Queue = strings.TrimSpace(msg.Queue)
	if msg.Queue == "" {
		msg.Queue = "default"
	}
	queue, err := cleanQueue(msg.Queue)
	if err != nil {
		return err
	}
	msg.Queue = queue
	if !msg.idPrepared {
		if strings.TrimSpace(msg.Id) == "" {
			msg.Id = uuid.NewString()
		} else if !msg.taskIDSet {
			msg.taskIDSet = true
		}
		msg.idPrepared = true
	}
	if msg.MaxRetry < 0 {
		msg.MaxRetry = 0
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	return nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
