package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/foxcolab/pact/internal/policy"
	"github.com/foxcolab/pact/internal/workspace"
)

// Tool names. The catalogue maps each one to exactly one resource and operation.
const (
	ToolListFiles   = "list_files"
	ToolReadFile    = "read_file"
	ToolWriteFile   = "write_file"
	ToolRunTests    = "run_tests"
	ToolDeploy      = "deploy"
	ToolReadSecret  = "read_secret"
	ToolHTTPRequest = "http_request"
	ToolCreateUser  = "create_user"
	ToolLLMGenerate = "llm_generate"
)

var (
	secretNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)
	hostRe       = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	roleRe       = regexp.MustCompile(`^[a-z_]{1,32}$`)
	httpMethods  = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true}
)

func strArg(args map[string]any, name string) string {
	s, _ := args[name].(string)
	return s
}

// Derive computes the resource and operation of a tool call from the tool name
// and its arguments. The broker never trusts a resource string supplied by the
// agent, so a secret read cannot be labelled as a file read.
func Derive(ws, tool string, args map[string]any) (policy.Request, error) {
	req := policy.Request{Tool: tool}
	switch tool {
	case ToolListFiles, ToolReadFile, ToolWriteFile:
		p, err := workspace.CleanPath(strArg(args, "path"))
		if err != nil {
			return req, err
		}
		if p == "" && tool != ToolListFiles {
			return req, errors.New("path is required")
		}
		if tool == ToolWriteFile {
			if _, ok := args["content"].(string); !ok {
				return req, errors.New("content must be a string")
			}
		}
		req.Resource = "repo:" + ws + "/" + p
		req.Op = "read"
		if tool == ToolWriteFile {
			req.Op = "write"
		}
	case ToolRunTests:
		req.Resource, req.Op = "tests:"+ws, "run"
	case ToolDeploy:
		req.Resource, req.Op = "deploy:"+ws, "deploy"
	case ToolLLMGenerate:
		req.Resource, req.Op = "llm:gemini", "generate"
		raw, _ := args["request"].(json.RawMessage)
		if err := ValidateLLMRequest(raw); err != nil {
			return req, err
		}
	case ToolReadSecret:
		name := strArg(args, "name")
		if !secretNameRe.MatchString(name) || strings.Contains(name, "..") {
			return req, errors.New("name must be a secret path such as prod/db-password")
		}
		req.Resource, req.Op = "secret:"+name, "read"
	case ToolHTTPRequest:
		u, err := url.Parse(strArg(args, "url"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return req, errors.New("url must be an absolute http or https URL")
		}
		host := strings.ToLower(u.Hostname())
		if !hostRe.MatchString(host) {
			return req, errors.New("url has no valid host")
		}
		method := strings.ToUpper(strArg(args, "method"))
		if method == "" {
			method = "GET"
		}
		if !httpMethods[method] {
			return req, fmt.Errorf("unsupported method %q", method)
		}
		req.Resource, req.Op = "net:"+host, method
	case ToolCreateUser:
		role := strings.ToLower(strArg(args, "role"))
		if role == "" {
			role = "user"
		}
		if !roleRe.MatchString(role) {
			return req, errors.New("role must be lowercase letters")
		}
		if strArg(args, "username") == "" {
			return req, errors.New("username is required")
		}
		req.Resource, req.Op = "iam:user", "create_"+role
	default:
		return req, fmt.Errorf("unknown tool %q", tool)
	}
	return req, nil
}

// Fields a proxied generateContent request may carry. Anything else is refused.
var (
	llmTopLevel = map[string]bool{"contents": true, "systemInstruction": true, "system_instruction": true, "tools": true,
		"toolConfig": true, "tool_config": true, "generationConfig": true, "generation_config": true, "safetySettings": true, "safety_settings": true}
	llmToolKeys = map[string]bool{"functionDeclarations": true, "function_declarations": true}
	llmPartDeny = map[string]bool{"fileData": true, "file_data": true}
)

// ValidateLLMRequest keeps the model proxy from becoming a second way out. The
// agent may send text and declare functions, and nothing else: provider-side
// tools such as web search, URL fetching or code execution, and file URIs the
// provider would fetch, would let a compromised agent reach the network through
// Gemini instead of through the broker.
func ValidateLLMRequest(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("llm request body is required")
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(raw, &req); err != nil {
		return errors.New("llm request must be a JSON object")
	}
	for k := range req {
		if !llmTopLevel[k] {
			return fmt.Errorf("llm request field %q is not allowed through the proxy", k)
		}
	}
	var tools []map[string]json.RawMessage
	if t, ok := req["tools"]; ok && string(t) != "null" {
		if err := json.Unmarshal(t, &tools); err != nil {
			return errors.New("llm request tools must be a list of objects")
		}
	}
	for _, tool := range tools {
		for k := range tool {
			if !llmToolKeys[k] {
				return fmt.Errorf("llm tool %q is not allowed through the proxy; only function declarations are", k)
			}
		}
	}
	type content struct {
		Parts []map[string]json.RawMessage `json:"parts"`
	}
	checkParts := func(c content) error {
		for _, part := range c.Parts {
			for k := range part {
				if llmPartDeny[k] {
					return fmt.Errorf("llm request part %q is not allowed through the proxy", k)
				}
			}
		}
		return nil
	}
	var contents []content
	if c, ok := req["contents"]; ok {
		if err := json.Unmarshal(c, &contents); err != nil {
			return errors.New("llm request contents must be a list")
		}
	}
	for _, key := range []string{"systemInstruction", "system_instruction"} {
		if si, ok := req[key]; ok && string(si) != "null" {
			var c content
			if err := json.Unmarshal(si, &c); err == nil {
				contents = append(contents, c)
			}
		}
	}
	for _, c := range contents {
		if err := checkParts(c); err != nil {
			return err
		}
	}
	return nil
}
