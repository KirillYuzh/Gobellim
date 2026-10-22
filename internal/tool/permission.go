package tool

// ToolPermission returns the RBAC permission name for a tool: "tool:"+name.
//
// Every tool declaration uses it as its default permission, so a policy grants
// access to one tool by naming it. The name lives here, in the package both
// the tool declaration and the RBAC engine share, so the two cannot drift.
func ToolPermission(name string) string { return "tool:" + name }
