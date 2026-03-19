package clochepb

// V2 task-oriented message types.
// These complement the generated cloche.pb.go types.
// They are defined here as plain Go structs since protoc is not available
// for regeneration; full proto wire encoding requires regenerating from the
// updated api/proto/cloche/v1/cloche.proto.

// ListTasksRequest is the request message for the ListTasks RPC.
type ListTasksRequest struct {
	All        bool   `json:"all,omitempty"`
	ProjectDir string `json:"project_dir,omitempty"`
	State      string `json:"state,omitempty"`
	Limit      int32  `json:"limit,omitempty"`
}

// TaskSummary is a concise representation of a task returned by ListTasks.
type TaskSummary struct {
	TaskId          string `json:"task_id,omitempty"`
	Title           string `json:"title,omitempty"`
	Status          string `json:"status,omitempty"`
	ProjectDir      string `json:"project_dir,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	LatestAttemptId string `json:"latest_attempt_id,omitempty"`
	AttemptCount    int32  `json:"attempt_count,omitempty"`
}

// ListTasksResponse is the response message for the ListTasks RPC.
type ListTasksResponse struct {
	Tasks []*TaskSummary `json:"tasks,omitempty"`
}

// GetTaskRequest is the request message for the GetTask RPC.
type GetTaskRequest struct {
	TaskId string `json:"task_id,omitempty"`
}

// AttemptSummary is a concise representation of an attempt.
type AttemptSummary struct {
	AttemptId string `json:"attempt_id,omitempty"`
	TaskId    string `json:"task_id,omitempty"`
	Result    string `json:"result,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
}

// GetTaskResponse is the response message for the GetTask RPC.
type GetTaskResponse struct {
	TaskId     string            `json:"task_id,omitempty"`
	Title      string            `json:"title,omitempty"`
	Status     string            `json:"status,omitempty"`
	ProjectDir string            `json:"project_dir,omitempty"`
	Attempts   []*AttemptSummary `json:"attempts,omitempty"`
}

// GetAttemptRequest is the request message for the GetAttempt RPC.
type GetAttemptRequest struct {
	AttemptId string `json:"attempt_id,omitempty"`
}

// GetAttemptResponse is the response message for the GetAttempt RPC.
type GetAttemptResponse struct {
	AttemptId string `json:"attempt_id,omitempty"`
	TaskId    string `json:"task_id,omitempty"`
	Result    string `json:"result,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	RunId     string `json:"run_id,omitempty"`
}
