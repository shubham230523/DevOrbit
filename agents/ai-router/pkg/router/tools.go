package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrToolNotFound       = errors.New("tool not found in schema registry")
	ErrInvalidArguments   = errors.New("invalid tool arguments")
	ErrMissingRequiredArg = errors.New("missing required tool argument")
	ErrMalformedResponse  = errors.New("malformed response structure")
	ErrExecutionBlocked   = errors.New("stage B execution safety: project tools must not be executed in Stage B")
)

// Allowed Stage C Tool Names
const (
	ToolListProjects     = "list_projects"
	ToolListFiles        = "list_files"
	ToolReadFile         = "read_file"
	ToolSearchFiles      = "search_files"
	ToolCreateFile       = "create_file"
	ToolEditFile         = "edit_file"
	ToolDeleteFile       = "delete_file"
	ToolExecuteCommand   = "execute_command"
	ToolGetCommandOutput = "get_command_output"
	ToolCancelTask       = "cancel_task"
)

// ToolParamSchema defines parameter properties in JSON Schema format.
type ToolParamSchema struct {
	Type        string                     `json:"type"`
	Description string                     `json:"description,omitempty"`
	Enum        []string                   `json:"enum,omitempty"`
	Items       *ToolParamSchema           `json:"items,omitempty"`
	Properties  map[string]ToolParamSchema `json:"properties,omitempty"`
	Required    []string                   `json:"required,omitempty"`
}

// ToolDeclaration defines a function/tool exposed to Gemini.
type ToolDeclaration struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Parameters  map[string]ToolParamSchema `json:"parameters"`
	Required    []string                   `json:"required"`
}

// FunctionCall represents a model's request to call a tool.
type FunctionCall struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// FunctionResponse represents the result of executing a tool, returned to Gemini.
type FunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// GetStageCToolDeclarations returns schemas for the 10 defined Stage C coding tools.
func GetStageCToolDeclarations() []ToolDeclaration {
	return []ToolDeclaration{
		{
			Name:        ToolListProjects,
			Description: "Lists all workspace projects configured and discoverable on the host machine.",
			Parameters:  map[string]ToolParamSchema{},
			Required:    []string{},
		},
		{
			Name:        ToolListFiles,
			Description: "Lists files and directories under a specific path within a workspace project.",
			Parameters: map[string]ToolParamSchema{
				"path":      {Type: "string", Description: "Relative path within the workspace."},
				"recursive": {Type: "boolean", Description: "Whether to list recursively."},
			},
			Required: []string{"path"},
		},
		{
			Name:        ToolReadFile,
			Description: "Reads file content from the workspace.",
			Parameters: map[string]ToolParamSchema{
				"path":        {Type: "string", Description: "Relative file path within workspace."},
				"start_line":  {Type: "integer", Description: "Optional 1-based start line."},
				"line_count":  {Type: "integer", Description: "Optional line count to read."},
			},
			Required: []string{"path"},
		},
		{
			Name:        ToolSearchFiles,
			Description: "Searches for a text pattern or regular expression across workspace files.",
			Parameters: map[string]ToolParamSchema{
				"pattern":       {Type: "string", Description: "Pattern or text query to search."},
				"path":          {Type: "string", Description: "Optional subfolder to search within."},
				"case_sensitive": {Type: "boolean", Description: "Whether matching is case sensitive."},
			},
			Required: []string{"pattern"},
		},
		{
			Name:        ToolCreateFile,
			Description: "Creates a new file at the specified relative workspace path.",
			Parameters: map[string]ToolParamSchema{
				"path":      {Type: "string", Description: "Relative path where the file will be created."},
				"content":   {Type: "string", Description: "Initial content for the file."},
				"overwrite": {Type: "boolean", Description: "Whether to overwrite existing file."},
			},
			Required: []string{"path", "content"},
		},
		{
			Name:        ToolEditFile,
			Description: "Edits an existing file by replacing a target string with replacement text.",
			Parameters: map[string]ToolParamSchema{
				"path":        {Type: "string", Description: "Relative file path."},
				"target":      {Type: "string", Description: "Exact text snippet to be replaced."},
				"replacement": {Type: "string", Description: "Replacement content."},
			},
			Required: []string{"path", "target", "replacement"},
		},
		{
			Name:        ToolDeleteFile,
			Description: "Deletes a file at the specified workspace path.",
			Parameters: map[string]ToolParamSchema{
				"path": {Type: "string", Description: "Relative path of file to delete."},
			},
			Required: []string{"path"},
		},
		{
			Name:        ToolExecuteCommand,
			Description: "Executes a permitted shell command within the workspace root.",
			Parameters: map[string]ToolParamSchema{
				"command": {Type: "string", Description: "Executable name or command line."},
				"args":    {Type: "array", Description: "Arguments for the command.", Items: &ToolParamSchema{Type: "string"}},
			},
			Required: []string{"command"},
		},
		{
			Name:        ToolGetCommandOutput,
			Description: "Retrieves output for an asynchronous or long-running process.",
			Parameters: map[string]ToolParamSchema{
				"task_id": {Type: "string", Description: "Task or process ID."},
			},
			Required: []string{"task_id"},
		},
		{
			Name:        ToolCancelTask,
			Description: "Cancels or terminates an active command or process.",
			Parameters: map[string]ToolParamSchema{
				"task_id": {Type: "string", Description: "Task ID to cancel."},
			},
			Required: []string{"task_id"},
		},
	}
}

// ValidateFunctionCall validates that call.Name is a recognized tool and call.Arguments satisfy required fields.
func ValidateFunctionCall(call *FunctionCall) error {
	if call == nil {
		return errors.New("nil function call")
	}

	call.Name = strings.TrimSpace(call.Name)
	if call.Name == "" {
		return errors.New("empty function call name")
	}

	tools := GetStageCToolDeclarations()
	var decl *ToolDeclaration
	for _, d := range tools {
		if d.Name == call.Name {
			decl = &d
			break
		}
	}

	if decl == nil {
		return fmt.Errorf("%w: '%s'", ErrToolNotFound, call.Name)
	}

	if call.Arguments == nil {
		call.Arguments = make(map[string]any)
	}

	for _, reqKey := range decl.Required {
		val, exists := call.Arguments[reqKey]
		if !exists || val == nil {
			return fmt.Errorf("%w: '%s' for tool '%s'", ErrMissingRequiredArg, reqKey, call.Name)
		}
		if strVal, ok := val.(string); ok && strings.TrimSpace(strVal) == "" {
			return fmt.Errorf("%w: '%s' cannot be empty string for tool '%s'", ErrInvalidArguments, reqKey, call.Name)
		}
	}

	return nil
}

// ParseFunctionCallsFromJSON safely parses JSON function calls and validates them against known tool schemas.
func ParseFunctionCallsFromJSON(rawJSON []byte) ([]FunctionCall, error) {
	if len(rawJSON) == 0 {
		return nil, ErrMalformedResponse
	}

	// Try single FunctionCall
	var single FunctionCall
	if err := json.Unmarshal(rawJSON, &single); err == nil && single.Name != "" {
		if err := ValidateFunctionCall(&single); err != nil {
			return nil, err
		}
		return []FunctionCall{single}, nil
	}

	// Try slice of FunctionCalls
	var list []FunctionCall
	if err := json.Unmarshal(rawJSON, &list); err == nil && len(list) > 0 {
		for i := range list {
			if err := ValidateFunctionCall(&list[i]); err != nil {
				return nil, err
			}
		}
		return list, nil
	}

	return nil, ErrMalformedResponse
}

// RedactSecret replaces sensitive token or API key matches in text.
func RedactSecret(input, secret string) string {
	if secret == "" || len(secret) < 4 {
		return input
	}
	escaped := regexp.QuoteMeta(secret)
	re := regexp.MustCompile(escaped)
	return re.ReplaceAllString(input, "[REDACTED]")
}
