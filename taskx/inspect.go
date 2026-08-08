package taskx

// InspectQuery - 检视查询
type InspectQuery struct {
	// Queue - 队列名（空表示全部队列）
	Queue string `json:"queue"`
	// State - 任务状态（空表示计数模式）
	State string `json:"state"`
	// Page - 页码（从 1 开始）
	Page int `json:"page"`
	// Size - 每页条数
	Size int `json:"size"`
}

// InspectResult - 检视结果
type InspectResult struct {
	// Counts - 队列到各状态数量的映射
	Counts map[string]map[string]int `json:"counts"`
	// Tasks - 当前页任务
	Tasks []Message `json:"tasks"`
	// Total - 列表总数
	Total int `json:"total"`
}

// ManageOp - 管理操作
type ManageOp struct {
	// Action - run / delete / purge
	Action string `json:"action"`
	// Queue - 队列名
	Queue string `json:"queue"`
	// State - 当前或目标状态
	State string `json:"state"`
	// Id - 任务 ID
	Id string `json:"id"`
}

const (
	statePending   = "pending"
	stateActive    = "active"
	stateScheduled = "scheduled"
	stateRetry     = "retry"
	stateCompleted = "completed"
	stateArchived  = "archived"
)

var allStates = []string{statePending, stateActive, stateScheduled, stateRetry, stateCompleted, stateArchived}
