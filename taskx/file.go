package taskx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/afero"
)

type fileBroker struct {
	fs         afero.Fs
	root       string
	leaseTTL   time.Duration
	syncWrites bool
	mutex      sync.Mutex
	sequence   atomic.Uint64
}

type lockFile struct {
	Id          string `json:"id"`
	Kind        string `json:"kind"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
}

var queueNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func newFileBroker(config Config) (Broker, error) {
	return newFileBrokerWithFs(config, afero.NewOsFs())
}

func newFileBrokerWithFs(config Config, fs afero.Fs) (Broker, error) {
	if fs == nil {
		return nil, errors.New("taskx: file 文件系统不能为空")
	}
	return &fileBroker{
		fs: fs, root: filepath.Clean(config.File.Root), leaseTTL: config.LeaseTTL,
		syncWrites: config.File.SyncWrites,
	}, nil
}

func cleanQueue(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	if !queueNamePattern.MatchString(name) || name == "." || name == ".." {
		return "", fmt.Errorf("taskx: 非法队列名[%s]", name)
	}
	return name, nil
}

func (this *fileBroker) queueDir(queue string) (string, error) {
	name, err := cleanQueue(queue)
	if err != nil {
		return "", err
	}
	return filepath.Join(this.root, name), nil
}

func (this *fileBroker) stateDir(queue, state string) (string, error) {
	root, err := this.queueDir(queue)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, state), nil
}

func (this *fileBroker) ensureQueue(queue string) error {
	for _, state := range append(append([]string(nil), allStates...), "locks") {
		dir, err := this.stateDir(queue, state)
		if err != nil {
			return err
		}
		if err = this.fs.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (this *fileBroker) idName(id string) string { return digest(id) + ".json" }

func (this *fileBroker) pendingName(msg *Message) string {
	sequence := this.sequence.Add(1) % 1_000_000
	return fmt.Sprintf("%013d-%06d-%s.json", time.Now().UnixMilli(), sequence, digest(msg.Id))
}

func (this *fileBroker) writeAtomic(path string, data []byte) error {
	if err := this.fs.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp-" + uuid.NewString()
	file, err := this.fs.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil && this.syncWrites {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = this.fs.Remove(temporary)
		return writeErr
	}
	if closeErr != nil {
		_ = this.fs.Remove(temporary)
		return closeErr
	}
	if err = this.fs.Rename(temporary, path); err == nil {
		return nil
	}
	// Windows 不允许 Rename 覆盖目标，删除目标后重试。
	if _, statErr := this.fs.Stat(path); statErr != nil {
		_ = this.fs.Remove(temporary)
		return err
	}
	if removeErr := this.fs.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		_ = this.fs.Remove(temporary)
		return err
	}
	if retryErr := this.fs.Rename(temporary, path); retryErr != nil {
		_ = this.fs.Remove(temporary)
		return retryErr
	}
	return nil
}

func (this *fileBroker) writeMessage(path string, msg *Message) error {
	data, err := encodeMessage(msg)
	if err != nil {
		return err
	}
	return this.writeAtomic(path, data)
}

func (this *fileBroker) readMessage(path string) (*Message, error) {
	data, err := afero.ReadFile(this.fs, path)
	if err != nil {
		return nil, err
	}
	return decodeMessage(data)
}

func prepareLockRefs(msg *Message) []lockRef {
	refs := make([]lockRef, 0, 2)
	if msg.taskIDSet {
		refs = append(refs, lockRef{Key: digest("task:" + msg.Id), Kind: "task"})
	}
	if msg.UniqueTTL > 0 {
		refs = append(refs, lockRef{Key: digest(msg.Type + "\x00" + string(msg.Payload)), Kind: "unique"})
	}
	return refs
}

func lockTTL(msg *Message, kind string) time.Duration {
	if kind == "unique" {
		return msg.UniqueTTL
	}
	return 24 * time.Hour
}

func lockConflict(kind string) error {
	if kind == "task" {
		return ErrTaskIdConflict
	}
	return ErrDuplicateTask
}

func (this *fileBroker) acquireLocks(msg *Message) error {
	dir, err := this.stateDir(msg.Queue, "locks")
	if err != nil {
		return err
	}
	msg.lockKeys = prepareLockRefs(msg)
	acquired := make([]lockRef, 0, len(msg.lockKeys))
	for _, ref := range msg.lockKeys {
		path := filepath.Join(dir, ref.Key+".lock")
		record := lockFile{Id: msg.Id, Kind: ref.Kind, ExpiresAtMs: time.Now().Add(lockTTL(msg, ref.Kind)).UnixMilli()}
		data, _ := json.Marshal(record)
		for attempt := 0; attempt < 2; attempt++ {
			file, openErr := this.fs.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if openErr == nil {
				_, writeErr := file.Write(data)
				if writeErr == nil && this.syncWrites {
					writeErr = file.Sync()
				}
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					_ = this.fs.Remove(path)
					this.removeLockRefs(msg.Queue, acquired, "", msg.Id)
					if writeErr != nil {
						return writeErr
					}
					return closeErr
				}
				acquired = append(acquired, ref)
				break
			}
			if !errors.Is(openErr, os.ErrExist) {
				this.removeLockRefs(msg.Queue, acquired, "", msg.Id)
				return openErr
			}
			existing, readErr := afero.ReadFile(this.fs, path)
			var old lockFile
			if readErr == nil {
				_ = json.Unmarshal(existing, &old)
			}
			if old.ExpiresAtMs > time.Now().UnixMilli() {
				this.removeLockRefs(msg.Queue, acquired, "", msg.Id)
				return lockConflict(ref.Kind)
			}
			_ = this.fs.Remove(path)
		}
	}
	return nil
}

func (this *fileBroker) removeLockRefs(queue string, refs []lockRef, kind, owner string) {
	dir, err := this.stateDir(queue, "locks")
	if err != nil {
		return
	}
	for _, ref := range refs {
		if kind == "" || ref.Kind == kind {
			path := filepath.Join(dir, ref.Key+".lock")
			data, readErr := afero.ReadFile(this.fs, path)
			var record lockFile
			if readErr == nil && json.Unmarshal(data, &record) == nil && (owner == "" || record.Id == owner) {
				_ = this.fs.Remove(path)
			}
		}
	}
}

func (this *fileBroker) Enqueue(ctx context.Context, msg *Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prepareMessage(msg); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if _, err := cleanQueue(msg.Queue); err != nil {
		return err
	}
	if err := this.ensureQueue(msg.Queue); err != nil {
		return err
	}
	if err := this.acquireLocks(msg); err != nil {
		return err
	}
	state := statePending
	name := this.pendingName(msg)
	if msg.ProcessAt.After(time.Now()) {
		state = stateScheduled
		name = this.idName(msg.Id)
	}
	dir, _ := this.stateDir(msg.Queue, state)
	if err := this.writeMessage(filepath.Join(dir, name), msg); err != nil {
		this.removeLockRefs(msg.Queue, msg.lockKeys, "", msg.Id)
		return err
	}
	return nil
}

func (this *fileBroker) Claim(ctx context.Context, queues []string) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, queue := range queues {
		if _, err := cleanQueue(queue); err != nil {
			return nil, err
		}
		dir, _ := this.stateDir(queue, statePending)
		entries, err := afero.ReadDir(this.fs, dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			source := filepath.Join(dir, entry.Name())
			msg, readErr := this.readMessage(source)
			if readErr != nil {
				continue
			}
			activeDir, _ := this.stateDir(queue, stateActive)
			target := filepath.Join(activeDir, this.idName(msg.Id))
			if renameErr := this.fs.Rename(source, target); renameErr != nil {
				continue
			}
			msg.leaseUntil = time.Now().Add(this.leaseTTL)
			if writeErr := this.writeMessage(target, msg); writeErr != nil {
				_ = this.fs.Rename(target, source)
				return nil, writeErr
			}
			return msg, nil
		}
	}
	return nil, nil
}

func (this *fileBroker) Promote(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	queues, err := this.queues()
	if err != nil {
		return 0, err
	}
	now := time.Now()
	moved := 0
	for _, queue := range queues {
		for _, state := range []string{stateScheduled, stateRetry, stateActive} {
			dir, _ := this.stateDir(queue, state)
			entries, readErr := afero.ReadDir(this.fs, dir)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return moved, readErr
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				path := filepath.Join(dir, entry.Name())
				msg, loadErr := this.readMessage(path)
				if loadErr != nil {
					continue
				}
				due := state == stateScheduled && !msg.ProcessAt.After(now)
				due = due || state == stateRetry && !msg.RetryAt.After(now)
				due = due || state == stateActive && !msg.leaseUntil.After(now)
				if !due {
					continue
				}
				msg.leaseUntil = time.Time{}
				pendingDir, _ := this.stateDir(queue, statePending)
				target := filepath.Join(pendingDir, this.pendingName(msg))
				if renameErr := this.fs.Rename(path, target); renameErr != nil {
					continue
				}
				_ = this.writeMessage(target, msg)
				moved++
			}
		}
		this.cleanup(queue, now)
	}
	return moved, nil
}

func (this *fileBroker) transition(msg *Message, target string, cause error) error {
	activeDir, err := this.stateDir(msg.Queue, stateActive)
	if err != nil {
		return err
	}
	source := filepath.Join(activeDir, this.idName(msg.Id))
	stored, err := this.readMessage(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stored.leaseUntil = time.Time{}
	if cause != nil {
		stored.LastError = cause.Error()
	}
	var path string
	switch target {
	case stateRetry:
		stored.Attempts++
		stored.RetryAt = msg.RetryAt
		dir, _ := this.stateDir(msg.Queue, stateRetry)
		path = filepath.Join(dir, this.idName(msg.Id))
	case stateArchived:
		stored.archivedAt = time.Now()
		dir, _ := this.stateDir(msg.Queue, stateArchived)
		path = filepath.Join(dir, this.idName(msg.Id))
	case stateCompleted:
		stored.completedAt = time.Now()
		dir, _ := this.stateDir(msg.Queue, stateCompleted)
		path = filepath.Join(dir, this.idName(msg.Id))
	case statePending:
		dir, _ := this.stateDir(msg.Queue, statePending)
		path = filepath.Join(dir, this.pendingName(stored))
	default:
		return fmt.Errorf("taskx: 不支持的目标状态[%s]", target)
	}
	if err = this.fs.Rename(source, path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return this.writeMessage(path, stored)
}

func (this *fileBroker) Ack(_ context.Context, msg *Message) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if msg.Retention <= 0 {
		dir, err := this.stateDir(msg.Queue, stateActive)
		if err != nil {
			return err
		}
		err = this.fs.Remove(filepath.Join(dir, this.idName(msg.Id)))
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		this.removeLockRefs(msg.Queue, msg.lockKeys, "", msg.Id)
		return err
	}
	err := this.transition(msg, stateCompleted, nil)
	this.removeLockRefs(msg.Queue, msg.lockKeys, "", msg.Id)
	return err
}

func (this *fileBroker) Retry(_ context.Context, msg *Message, cause error) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	err := this.transition(msg, stateRetry, cause)
	this.removeLockRefs(msg.Queue, msg.lockKeys, "unique", msg.Id)
	return err
}

func (this *fileBroker) Archive(_ context.Context, msg *Message, cause error) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	err := this.transition(msg, stateArchived, cause)
	this.removeLockRefs(msg.Queue, msg.lockKeys, "", msg.Id)
	return err
}

func (this *fileBroker) Release(_ context.Context, msg *Message) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.transition(msg, statePending, nil)
}

func (this *fileBroker) Extend(_ context.Context, msg *Message, leaseUntil time.Time) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	dir, err := this.stateDir(msg.Queue, stateActive)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, this.idName(msg.Id))
	stored, err := this.readMessage(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stored.leaseUntil = leaseUntil
	msg.leaseUntil = leaseUntil
	return this.writeMessage(path, stored)
}

func (this *fileBroker) queues() ([]string, error) {
	entries, err := afero.ReadDir(this.fs, this.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	queues := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			queues = append(queues, entry.Name())
		}
	}
	sort.Strings(queues)
	return queues, nil
}

func (this *fileBroker) cleanup(queue string, now time.Time) {
	completedDir, _ := this.stateDir(queue, stateCompleted)
	entries, _ := afero.ReadDir(this.fs, completedDir)
	for _, entry := range entries {
		path := filepath.Join(completedDir, entry.Name())
		msg, err := this.readMessage(path)
		if err == nil && msg.Retention > 0 && !msg.completedAt.Add(msg.Retention).After(now) {
			_ = this.fs.Remove(path)
		}
	}
	locksDir, _ := this.stateDir(queue, "locks")
	locks, _ := afero.ReadDir(this.fs, locksDir)
	for _, entry := range locks {
		path := filepath.Join(locksDir, entry.Name())
		data, err := afero.ReadFile(this.fs, path)
		var record lockFile
		if err != nil || json.Unmarshal(data, &record) != nil || record.ExpiresAtMs <= now.UnixMilli() {
			_ = this.fs.Remove(path)
		}
	}
	this.cleanupTemps(queue)
}

func (this *fileBroker) cleanupTemps(queue string) {
	for _, state := range append(append([]string(nil), allStates...), "locks") {
		dir, _ := this.stateDir(queue, state)
		entries, _ := afero.ReadDir(this.fs, dir)
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".tmp-") {
				_ = this.fs.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
}

func normalizeInspect(query InspectQuery) (InspectQuery, error) {
	query.Queue = strings.TrimSpace(query.Queue)
	query.State = strings.ToLower(strings.TrimSpace(query.State))
	if query.State != "" && query.Queue == "" {
		return query, errors.New("taskx: 列表模式必须指定队列")
	}
	if query.State != "" && !validState(query.State) {
		return query, fmt.Errorf("taskx: 非法任务状态[%s]", query.State)
	}
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.Size <= 0 {
		query.Size = 20
	}
	if query.Size > 100 {
		query.Size = 100
	}
	return query, nil
}

func validState(state string) bool {
	for _, item := range allStates {
		if state == item {
			return true
		}
	}
	return false
}

func (this *fileBroker) Inspect(_ context.Context, query InspectQuery) (*InspectResult, error) {
	query, err := normalizeInspect(query)
	if err != nil {
		return nil, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	queues, err := this.queues()
	if err != nil {
		return nil, err
	}
	if query.Queue != "" {
		if _, err = cleanQueue(query.Queue); err != nil {
			return nil, err
		}
		queues = []string{query.Queue}
	}
	result := &InspectResult{Counts: make(map[string]map[string]int)}
	if query.State == "" {
		for _, queue := range queues {
			result.Counts[queue] = make(map[string]int)
			for _, state := range allStates {
				dir, _ := this.stateDir(queue, state)
				entries, _ := afero.ReadDir(this.fs, dir)
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
						result.Counts[queue][state]++
					}
				}
			}
		}
		return result, nil
	}
	dir, _ := this.stateDir(query.Queue, query.State)
	entries, err := afero.ReadDir(this.fs, dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	result.Total = len(paths)
	start := (query.Page - 1) * query.Size
	end := start + query.Size
	if start > len(paths) {
		start = len(paths)
	}
	if end > len(paths) {
		end = len(paths)
	}
	for _, path := range paths[start:end] {
		msg, loadErr := this.readMessage(path)
		if loadErr == nil {
			result.Tasks = append(result.Tasks, *msg)
		}
	}
	return result, nil
}

func (this *fileBroker) findTaskPath(queue, state, id string) (string, *Message, error) {
	dir, err := this.stateDir(queue, state)
	if err != nil {
		return "", nil, err
	}
	if state != statePending {
		path := filepath.Join(dir, this.idName(id))
		msg, loadErr := this.readMessage(path)
		return path, msg, loadErr
	}
	entries, err := afero.ReadDir(this.fs, dir)
	if err != nil {
		return "", nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		msg, loadErr := this.readMessage(path)
		if loadErr == nil && msg.Id == id {
			return path, msg, nil
		}
	}
	return "", nil, os.ErrNotExist
}

func validateManage(op ManageOp) (ManageOp, error) {
	op.Action = strings.ToLower(strings.TrimSpace(op.Action))
	op.State = strings.ToLower(strings.TrimSpace(op.State))
	op.Queue = strings.TrimSpace(op.Queue)
	if _, err := cleanQueue(op.Queue); err != nil {
		return op, err
	}
	if !validState(op.State) {
		return op, fmt.Errorf("taskx: 非法任务状态[%s]", op.State)
	}
	switch op.Action {
	case "run":
		if op.State != stateScheduled && op.State != stateRetry && op.State != stateArchived {
			return op, errors.New("taskx: 仅 scheduled/retry/archived 任务可重跑")
		}
		if strings.TrimSpace(op.Id) == "" {
			return op, errors.New("taskx: run 操作必须指定任务 ID")
		}
	case "delete":
		if op.State == stateActive || strings.TrimSpace(op.Id) == "" {
			return op, errors.New("taskx: 不可删除 active 任务且必须指定任务 ID")
		}
	case "purge":
		if op.State != stateCompleted && op.State != stateArchived {
			return op, errors.New("taskx: 仅 completed/archived 状态可清空")
		}
	default:
		return op, fmt.Errorf("taskx: 非法管理操作[%s]", op.Action)
	}
	return op, nil
}

func (this *fileBroker) Manage(_ context.Context, op ManageOp) error {
	op, err := validateManage(op)
	if err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if op.Action == "purge" {
		dir, _ := this.stateDir(op.Queue, op.State)
		entries, _ := afero.ReadDir(this.fs, dir)
		for _, entry := range entries {
			msg, loadErr := this.readMessage(filepath.Join(dir, entry.Name()))
			if loadErr == nil {
				this.removeLockRefs(op.Queue, msg.lockKeys, "", msg.Id)
			}
		}
		if err = this.fs.RemoveAll(dir); err != nil {
			return err
		}
		return this.fs.MkdirAll(dir, 0o755)
	}
	path, msg, err := this.findTaskPath(op.Queue, op.State, op.Id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if op.Action == "delete" {
		this.removeLockRefs(op.Queue, msg.lockKeys, "", msg.Id)
		return this.fs.Remove(path)
	}
	if op.State == stateArchived {
		if err = this.acquireLocks(msg); err != nil {
			return err
		}
	}
	msg.Attempts = 0
	msg.LastError = ""
	msg.ProcessAt = time.Time{}
	msg.RetryAt = time.Time{}
	msg.archivedAt = time.Time{}
	pendingDir, _ := this.stateDir(op.Queue, statePending)
	target := filepath.Join(pendingDir, this.pendingName(msg))
	if err = this.fs.Rename(path, target); err != nil {
		if op.State == stateArchived {
			this.removeLockRefs(op.Queue, msg.lockKeys, "", msg.Id)
		}
		return err
	}
	return this.writeMessage(target, msg)
}

func (this *fileBroker) Close() error { return nil }

var _ Broker = (*fileBroker)(nil)
