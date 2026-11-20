package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		// The resource names the host only, so a grant for a host must not
		// cover every port on it.
		if p := u.Port(); p != "" && !(u.Scheme == "http" && p == "80") && !(u.Scheme == "https" && p == "443") {
			return req, errors.New("url must use the standard port for its scheme")
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
	_, err := NormalizeLLMRequest(raw)
	return err
}

// NormalizeLLMRequest checks a generateContent request and returns the bytes to
// forward: re-encoded from the value that was checked, never the agent's own
// bytes, so the provider cannot read something the check did not (a repeated
// key, for example, which parsers resolve differently).
func NormalizeLLMRequest(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("llm request body is required")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeStrict(dec, 0)
	if err != nil {
		return nil, fmt.Errorf("llm request is not acceptable JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("llm request has trailing data")
	}
	req, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("llm request must be a JSON object")
	}
	for k := range req {
		if !llmTopLevel[k] {
			return nil, fmt.Errorf("llm request field %q is not allowed through the proxy", k)
		}
	}
	if t, ok := req["tools"]; ok && t != nil {
		tools, ok := t.([]any)
		if !ok {
			return nil, errors.New("llm request tools must be a list of objects")
		}
		for _, tool := range tools {
			m, ok := tool.(map[string]any)
			if !ok {
				return nil, errors.New("llm request tools must be a list of objects")
			}
			for k := range m {
				if !llmToolKeys[k] {
					return nil, fmt.Errorf("llm tool %q is not allowed through the proxy; only function declarations are", k)
				}
			}
		}
	}
	if c, ok := req["contents"]; ok && c != nil {
		if _, ok := c.([]any); !ok {
			return nil, errors.New("llm request contents must be a list")
		}
	}
	// File references can be nested (a function response may carry parts of its
	// own), so look at every object, at any depth.
	if k := findKey(req, llmPartDeny); k != "" {
		return nil, fmt.Errorf("llm request part %q is not allowed through the proxy", k)
	}
	out, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// maxLLMDepth bounds nesting, so a deeply nested body cannot exhaust the stack.
const maxLLMDepth = 64

// decodeStrict decodes one JSON value, refusing repeated object keys.
func decodeStrict(dec *json.Decoder, depth int) (any, error) {
	if depth > maxLLMDepth {
		return nil, errors.New("nested too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // string, json.Number, bool or nil
	}
	switch d {
	case '{':
		m := map[string]any{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			if _, dup := m[k]; dup {
				return nil, fmt.Errorf("key %q appears twice", k)
			}
			if m[k], err = decodeStrict(dec, depth+1); err != nil {
				return nil, err
			}
		}
		_, err = dec.Token() // the closing brace
		return m, err
	case '[':
		a := []any{}
		for dec.More() {
			v, err := decodeStrict(dec, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = dec.Token() // the closing bracket
		return a, err
	}
	return nil, fmt.Errorf("unexpected %q", d)
}

// findKey returns the first key from deny found in any object within v.
func findKey(v any, deny map[string]bool) string {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if deny[k] {
				return k
			}
			if f := findKey(e, deny); f != "" {
				return f
			}
		}
	case []any:
		for _, e := range t {
			if f := findKey(e, deny); f != "" {
				return f
			}
		}
	}
	return ""
}
