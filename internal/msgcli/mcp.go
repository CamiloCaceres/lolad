package msgcli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// lolad msg mcp: a Model Context Protocol server over stdio that gives an
// agent the user tools, message_user and ask_user. lolad lists it in every
// agent's MCP config at launch (see harness), so the tools and their schemas
// sit in the model's context: it reaches for real UI (copyable commands and
// values, links, checklists, forms, done buttons) instead of formatting text.
// Each call is one signed mail, exactly like tell-user --json.

const mcpInstructions = "Your human user reads you in lolad's inbox, not in this terminal. Reach them only through these tools: message_user to tell them something, ask_user when you need something from them or need them to do something. Lay out anything they will copy, run, open or work through as blocks."

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

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
			v := mcpVersions[1]
			if slices.Contains(mcpVersions, p.ProtocolVersion) {
				v = p.ProtocolVersion
			}
			res.Result = map[string]any{
				"protocolVersion": v,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "lolad", "version": "1"},
				"instructions":    mcpInstructions,
			}
		case "ping":
			res.Result = map[string]any{}
		case "tools/list":
			res.Result = map[string]any{"tools": mcpTools()}
		case "tools/call":
			res.Result = c.callTool(req.Params)
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

func (c *client) callTool(params json.RawMessage) map[string]any {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolResult("bad call: "+err.Error(), true)
	}
	if p.Name != "message_user" && p.Name != "ask_user" {
		return toolResult("unknown tool "+p.Name, true)
	}
	in, err := decodeToolInput(p.Arguments)
	if err != nil {
		return toolResult(err.Error(), true)
	}
	// Attachments are paths in the agent's folder.
	home := os.Getenv("LOLABOT_HOME")
	for i, a := range in.Attach {
		if home != "" && !filepath.IsAbs(a) {
			in.Attach[i] = filepath.Join(home, a)
		}
	}
	ask := p.Name == "ask_user"
	m, err := buildUserMessage(ask, in)
	if err != nil {
		return toolResult(err.Error()+". Fix the arguments and call again.", true)
	}
	id, err := c.sendToUser(m)
	if err != nil {
		return toolResult("not sent: "+err.Error(), true)
	}
	if !ask {
		return toolResult("Sent to the user (message "+id+").", false)
	}
	return toolResult("Request "+id+" is waiting for the user. Carry on with other work: they answer when they are ready, and the answer arrives in your inbox as a message (\"Answers to your requests\", maybe with answers to other requests). A skipped request means carry on without it. Don't ask again meanwhile.", false)
}

// ---------------------------------------------------------------- schemas

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func obj(desc string, props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if desc != "" {
		o["description"] = desc
	}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func blockKind(kind, desc string, props map[string]any, required ...string) map[string]any {
	props["type"] = map[string]any{"type": "string", "const": kind}
	return obj(desc, props, append([]string{"type"}, required...)...)
}

func blocksSchema() map[string]any {
	title := str("Short heading, optional.")
	return map[string]any{
		"type":        "array",
		"maxItems":    core.MaxBlocks,
		"description": "The message laid out as UI, in order. Use a block for anything the user will copy, run, open or work through; keep explanations in text blocks. Never put a command, a value to paste or a URL to open inside text when a block fits.",
		"items": map[string]any{"anyOf": []any{
			blockKind(core.BlockText, "Markdown text: explanation, context.", map[string]any{"text": str("Markdown.")}, "text"),
			blockKind(core.BlockCommand, "A command the user runs themselves. Shown with a Copy button.", map[string]any{
				"code":  str("The exact command(s), ready to paste. No prompt characters ($)."),
				"where": str("Where to run it, as a phrase: \"in Terminal on your Mac\", \"on the server, over SSH\", \"in PowerShell as administrator\"."),
				"admin": map[string]any{"type": "boolean", "description": "True when it asks for an administrator password (sudo); the user sees a warning."},
				"title": title,
			}, "code"),
			blockKind(core.BlockCode, "Text the user pastes somewhere: a config snippet, a file's contents, an HTML tag. Shown with a Copy button.", map[string]any{
				"code":  str("The exact text."),
				"title": str("What it is and where it goes, e.g. \"Caddyfile\" or \"Paste in Settings → Custom code\"."),
			}, "code"),
			blockKind(core.BlockValues, "Values the user copies one at a time into a form or dashboard: a DNS record, settings, IDs, credentials they must type elsewhere. Each value gets its own copy button.", map[string]any{
				"title": str("What these are, e.g. \"DNS record\"."),
				"rows": map[string]any{"type": "array", "maxItems": core.MaxBlockRows, "items": obj("", map[string]any{
					"label": str("The field's name as the user sees it in that form, e.g. \"Type\", \"Points to\"."),
					"value": str("Exactly what to enter."),
				}, "label", "value")},
			}, "rows"),
			blockKind(core.BlockLink, "A page the user should open, shown as a button.", map[string]any{
				"label": str("What opens, e.g. \"Open Hostinger DNS\"."),
				"url":   str("http(s) URL."),
			}, "url"),
			blockKind(core.BlockSteps, "Things to do in order; the user ticks them off.", map[string]any{
				"title": title,
				"items": map[string]any{"type": "array", "maxItems": core.MaxBlockRows, "items": str("One step, Markdown.")},
			}, "items"),
		}},
	}
}

func mcpTools() []any {
	common := map[string]any{
		"title":  str("Short headline, shown first. Optional; defaults to the start of the message."),
		"text":   str("Markdown. Plain conversation can be just this. Shown before the blocks."),
		"blocks": blocksSchema(),
		"attach": map[string]any{"type": "array", "maxItems": 10, "description": "Paths of files to attach (relative to your folder). Zip folders first.", "items": map[string]any{"type": "string"}},
	}
	msg := map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"say", "update", "deliver"}, "description": "say (default): conversation. update: progress and what remains. deliver: a finished result."}}
	ask := map[string]any{
		"options": map[string]any{"type": "array", "minItems": 2, "maxItems": maxOptions, "items": map[string]any{"type": "string"}, "description": "2-6 short buttons for a decision, e.g. [\"Deploy now\", \"Wait until Monday\"]. The user can also answer in words."},
		"task": obj("Use when the user must do something themselves (run a command, add a DNS record, change a setting in a dashboard): lay out exactly what to do with blocks, and they get a done button, a couldn't button and room to say what happened.", map[string]any{
			"done":   str("Done button label, e.g. \"Added\" or \"Done\"."),
			"failed": str("Couldn't button label, default \"I couldn't do it\"."),
		}),
		"fields": map[string]any{"type": "array", "maxItems": core.MaxAskFields, "description": "A small form, for details only the user has.", "items": obj("", map[string]any{
			"label":    str("What to fill in, as the user sees it."),
			"type":     map[string]any{"type": "string", "enum": core.FieldTypes, "description": "text (one line, default), longtext (a paragraph), number, date (YYYY-MM-DD), choice (one of options), logs (pasted output or error logs, kept verbatim), file (a screenshot, log file or document; arrives attached to the answer), secret (API key, token or password: saved straight into your .env under name; the value never appears in chat or in the answer)."},
			"name":     str("Key for the answer. Required for a secret: the env var to save it as, e.g. OPENAI_API_KEY."),
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For choice."},
			"optional": map[string]any{"type": "boolean"},
		})},
	}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	return []any{
		map[string]any{
			"name":  "message_user",
			"title": "Message the user",
			"description": "Send your human user a message in their lolad inbox (they don't see your terminal). For conversation, progress and finished results. " +
				"Lay it out with blocks whenever it holds something they will copy, run, open or work through: commands as command blocks, values for a form or dashboard as a values block, snippets and file contents as code blocks, pages as link blocks, ordered to-dos as steps. " +
				"If you need an answer, or need them to do something and tell you, use ask_user instead.",
			"inputSchema": obj("", with(common, msg)),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
		map[string]any{
			"name":  "ask_user",
			"title": "Ask the user",
			"description": "Ask your human user for something, or ask them to do something, without stopping your work. The request waits quietly in their conversation until they are ready (they may skip it); the answer arrives later in your inbox. " +
				"Choose how they answer: options (buttons for a decision), fields (a small form: text, number, date, choice, pasted logs, files such as screenshots, and secrets such as API keys, which go straight to your .env and never into chat), task (they do something themselves and press Done), or none of these (they answer in words). " +
				"For a task, lay out every step, command and value as blocks so they can copy each one. One request per thing you need; keep it short and self-contained. Never ask for a key, token or password any other way than a secret field.",
			"inputSchema": obj("", with(common, ask)),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
	}
}

// mcpUsage is shown by lolad msg mcp --help.
const mcpUsage = "usage: lolad msg mcp  (an MCP server on stdin/stdout with the message_user and ask_user tools; lolad starts it for each agent)"
