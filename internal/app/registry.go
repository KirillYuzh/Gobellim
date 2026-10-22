package app

import (
	"github.com/KirillYuzh/Gobellim/internal/tool"
)

// Registry collects tool declarations and compiles them into a ToolSet. It is
// the only place where declaration errors surface.
type Registry = tool.Registry

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return tool.NewRegistry() }

// ToolSet is an immutable, compiled set of tools passed to New as Config.Tools.
type ToolSet = tool.ToolSet
