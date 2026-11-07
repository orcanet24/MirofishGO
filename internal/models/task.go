// Port of backend/app/models/task.py.
//
// TaskManager singleton thread-safe: tareas en memoria (no se persisten) para
// seguir build/prepare/report de larga duración. El formato de salida de
// Task.ToDict es idéntico al dict Python de to_dict().
package models

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"mirofish/internal/locale"
	"mirofish/internal/timex"
)

// TaskStatus replica el enum Python (valores string en JSON).
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"    // 等待中
	TaskProcessing TaskStatus = "processing" // 处理中
	TaskCompleted  TaskStatus = "completed"  // 已完成
	TaskFailed     TaskStatus = "failed"     // 失败
)

// Task — dataclass interna en memoria; no se serializa tal cual.
type Task struct {
	TaskID   string
	TaskType string
	Status   TaskStatus

	CreatedAt time.Time
	UpdatedAt time.Time

	Progress int    // 0-100
	Message  string // estado

	Result   map[string]any
	ErrorMsg *string

	Metadata       map[string]any
	ProgressDetail map[string]any
}

// isoformat replica datetime.now().isoformat(): microsegundos sin TZ.
func isoformat(t time.Time) string {
	return timex.ISO(t)
}

// TaskDict — misma clave/orden que Task.to_dict() de Python.
type TaskDict struct {
	TaskID    string `json:"task_id"`
	TaskType  string `json:"task_type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Progress  int    `json:"progress"`
	Message   string `json:"message"`

	ProgressDetail map[string]any `json:"progress_detail"`
	Result         map[string]any `json:"result"`
	Error          *string        `json:"error"`
	Metadata       map[string]any `json:"metadata"`
}

// ToDict convierte a la forma serializable, igual que Task.to_dict().
func (t *Task) ToDict() TaskDict {
	return TaskDict{
		TaskID:    t.TaskID,
		TaskType:  t.TaskType,
		Status:    string(t.Status),
		CreatedAt: isoformat(t.CreatedAt),
		UpdatedAt: isoformat(t.UpdatedAt),
		Progress:  t.Progress,
		Message:   t.Message,

		ProgressDetail: t.ProgressDetail,
		Result:         t.Result,
		Error:          t.ErrorMsg,
		Metadata:       t.Metadata,
	}
}

// TaskManager — singleton con mutex, igual que en Python.
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

var (
	taskManOnce sync.Once
	taskMan     *TaskManager
)

// GetTaskManager devuelve la instancia única.
func GetTaskManager() *TaskManager {
	taskManOnce.Do(func() { taskMan = &TaskManager{tasks: map[string]*Task{}} })
	return taskMan
}

// newUUID4 replica str(uuid.uuid4()).
func newUUID4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	// version 4 / variant bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return strings.Join([]string{s[:8], s[8:12], s[12:16], s[16:20], s[20:]}, "-")
}

// CreateTask devuelve el task_id de una task nueva en status pending.
func (m *TaskManager) CreateTask(taskType string, metadata map[string]any) string {
	id := newUUID4()
	now := time.Now()
	t := &Task{
		TaskID:    id,
		TaskType:  taskType,
		Status:    TaskPending,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  orEmptyMap(metadata),
	}
	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()
	return id
}

// GetTask copia la task bajo lock para que la llamada lea sin carreras.
func (m *TaskManager) GetTask(id string) (*Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// TaskUpdate replica los argumentos "Optional" de update_task: nil = sin cambio.
type TaskUpdate struct {
	Status         *TaskStatus
	Progress       *int
	Message        *string
	Result         map[string]any
	Error          *string
	ProgressDetail map[string]any
}

// UpdateTask aplica los cambios con la misma semántica None = no tocar.
func (m *TaskManager) UpdateTask(id string, up TaskUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return
	}
	t.UpdatedAt = time.Now()
	if up.Status != nil {
		t.Status = *up.Status
	}
	if up.Progress != nil {
		t.Progress = *up.Progress
	}
	if up.Message != nil {
		t.Message = *up.Message
	}
	if up.Result != nil {
		t.Result = up.Result
	}
	if up.Error != nil {
		t.ErrorMsg = up.Error
	}
	if up.ProgressDetail != nil {
		t.ProgressDetail = up.ProgressDetail
	}
}

// CompleteTask marca un task terminado con progress=100 y result.
func (m *TaskManager) CompleteTask(id string, result map[string]any) {
	p := 100
	msg := locale.T("", "progress.taskComplete", nil)
	status := TaskCompleted
	m.UpdateTask(id, TaskUpdate{Status: &status, Progress: &p, Message: &msg, Result: result})
}

// FailTask marca un task fallido.
func (m *TaskManager) FailTask(id string, errMsg string) {
	msg := locale.T("", "progress.taskFailed", nil)
	status := TaskFailed
	errStr := errMsg
	m.UpdateTask(id, TaskUpdate{Status: &status, Message: &msg, Error: &errStr})
}

// ListTasks devuelve TaskDict ordenados por created_at desc.
func (m *TaskManager) ListTasks(taskType string) []TaskDict {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TaskDict, 0, len(m.tasks))
	for _, t := range m.tasks {
		if taskType != "" && t.TaskType != taskType {
			continue
		}
		out = append(out, t.ToDict())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

// CleanupOldTasks borra tasks completed/failed más viejos que maxAgeHours.
func (m *TaskManager) CleanupOldTasks(maxAgeHours int) {
	cutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.tasks {
		if t.CreatedAt.Before(cutoff) && (t.Status == TaskCompleted || t.Status == TaskFailed) {
			delete(m.tasks, id)
		}
	}
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
