package api

import (
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

type runtimeToolExecutor struct {
	executor  *tool.Executor
	hooks     *runtimeHookAdapter
	history   *message.History
	allow     map[string]struct{}
	root      string
	host      string
	sessionID string
	yolo      bool // skip all whitelist and permission checks

	permissionResolver tool.PermissionResolver
}

type registeredToolRefs struct {
	taskTool      *toolbuiltin.TaskTool
	streamMonitor *toolbuiltin.StreamMonitorTool
}
