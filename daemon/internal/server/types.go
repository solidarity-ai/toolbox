package server

import "time"

type SessionState struct {
	Mode          string   `json:"mode"`
	WorkingDir    string   `json:"working_dir"`
	PreparedTools []string `json:"prepared_tools,omitempty"`
}

type ClientSnapshot struct {
	PID           int       `json:"pid"`
	Mode          string    `json:"mode"`
	WorkingDir    string    `json:"working_dir"`
	PreparedTools []string  `json:"prepared_tools,omitempty"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastSyncAt    time.Time `json:"last_sync_at"`
}
