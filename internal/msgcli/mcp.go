package msgcli

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// lolad msg mcp: a Model Context Protocol server over stdio that gives an
// agent the user-channel tools: message_user, ask_user and get_user_messages
// (spec: github.com/… user-channel, section 4). lolad lists it in every
// agent's MCP config at launch (see harness) under the name "user", so the
// tools and their schemas sit in the model's context: it reaches for real UI
// (copyable commands and values, links, checklists, forms, done buttons)
// instead of formatting text, and collects what the user sent without
// anyone reading its transcript. Each message_user / ask_user call is one
// signed mail, exactly like tell-user --json.

// usertools.json is the spec's tools/tools.json, served as it is: lolad
// offers every optional capability (secret, file, attach, push).
//
//go:embed usertools.json
var userToolsJSON []byte

// instructions.md is the spec's skill/AGENTS.md: the rule, for models that
// read MCP instructions.
//
//go:embed instructions.md
var mcpInstructions string

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// structuredSince is the first protocol version with tool titles, output
// schemas and structuredContent.
const structuredSince = "2025-06-18"

func structuredVersion(v string) bool { return v >= structuredSince }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// serveMCP answers newline-delimited JSON-RPC on in/out until in closes.
func (c *client) serveMCP(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	enc := json.NewEncoder(out)
	version := mcpVersions[1]
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			if err := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue // a notification (initialized, cancelled): nothing to answer
		}
		res := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			version = mcpVersions[1]
			if slices.Contains(mcpVersions, p.ProtocolVersion) {
				version = p.ProtocolVersion
			}
			res.Result = map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "user", "title": "lolad user channel", "version": "1"},
				"instructions":    strings.TrimSpace(mcpInstructions),
			}
		case "ping":
			res.Result = map[string]any{}
		case "tools/list":
			res.Result = map[string]any{"tools": mcpTools(version)}
		case "tools/call":
			res.Result, res.Error = c.callTool(req.Params, structuredVersion(version))
		default:
			res.Error = &rpcError{-32601, "method not found: " + req.Method}
		}
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
	return sc.Err()
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}
}

// toolOK is a successful result: the readable text, and the structured
// content for clients that take it.
func toolOK(text string, data any, structured bool) map[string]any {
	r := toolResult(text, false)
	if structured {
		r["structuredContent"] = data
	}
	return r
}

func (c *client) callTool(params json.RawMessage, structured bool) (map[string]any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{-32602, "bad call: " + err.Error()}
	}
	switch p.Name {
	case "message_user", "ask_user":
	case "get_user_messages":
		if !noArguments(p.Arguments) {
			return toolResult("get_user_messages takes no arguments.", true), nil
		}
		c.refresh()
		res, err := c.getUserMessages()
		if err != nil {
			return toolResult("couldn't read what the user sent: "+err.Error(), true), nil
		}
		return toolOK(res.Text, res.Structured, structured), nil
	default:
		return nil, &rpcError{-32602, "unknown tool: " + p.Name}
	}
	in, err := decodeToolInput(p.Name, p.Arguments)
	if err != nil {
		return toolResult(err.Error()+". Fix the arguments and call again.", true), nil
	}
	ask := p.Name == "ask_user"
	m, err := buildUserMessage(ask, in)
	if err != nil {
		return toolResult(err.Error()+". Fix the arguments and call again.", true), nil
	}
	if len(m.Attach) > 0 {
		if m.Attach, err = resolveAttach(attachBase(), m.Attach); err != nil {
			return toolResult(err.Error()+".", true), nil
		}
	}
	c.refresh() // this server outlives renames
	id, err := c.sendToUser(m)
	if err != nil {
		return toolResult("not sent: "+err.Error(), true), nil
	}
	if !ask {
		return toolOK(fmt.Sprintf("Sent (message %s).", id), map[string]any{"id": id}, structured), nil
	}
	return toolOK(fmt.Sprintf("Asked (request %s). It waits for the user; keep working. The answer will arrive in this session; get_user_messages also returns it.", id),
		map[string]any{"id": id, "status": "open"}, structured), nil
}

// noArguments reports whether a get_user_messages call gave no arguments:
// absent, or an empty object.
func noArguments(raw json.RawMessage) bool {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && m != nil && len(m) == 0
}

// mcpTools is usertools.json as served: without the x- keys (for the host
// only), and without title and outputSchema for clients older than
// structuredSince.
func mcpTools(version string) []any {
	var doc struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(userToolsJSON, &doc); err != nil {
		panic("usertools.json: " + err.Error())
	}
	out := make([]any, 0, len(doc.Tools))
	for _, t := range doc.Tools {
		t = stripHostKeys(t).(map[string]any)
		if !structuredVersion(version) {
			delete(t, "title")
			delete(t, "outputSchema")
		}
		out = append(out, t)
	}
	return out
}

func stripHostKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			if strings.HasPrefix(k, "x-") {
				continue
			}
			m[k] = stripHostKeys(e)
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, e := range x {
			s[i] = stripHostKeys(e)
		}
		return s
	}
	return v
}

// mcpUsage is shown by lolad msg mcp --help.
const mcpUsage = "usage: lolad msg mcp  (an MCP server on stdin/stdout with the message_user, ask_user and get_user_messages tools; lolad starts it for each agent)"
