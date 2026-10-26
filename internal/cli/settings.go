package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func settingsCommand(ctx context.Context, base string, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: lolad %s <agent> (run lolad help)", args[0])
	}
	name := args[1]
	path := "/api/agents/" + url.PathEscape(name)
	rest := args[2:]
	getRuntime := func() (core.Runtime, error) {
		var b bytes.Buffer
		err := request(ctx, base, "GET", path+"/runtime", nil, &b)
		var rt core.Runtime
		if err == nil {
			err = json.Unmarshal(b.Bytes(), &rt)
		}
		return rt, err
	}
	update := func(rt core.Runtime) error {
		var b bytes.Buffer
		if err := request(ctx, base, "PUT", path+"/runtime", rt, &b); err != nil {
			return err
		}
		_, err := out.Write(b.Bytes())
		if err != nil {
			return err
		}
		pendingHint(b.Bytes(), out)
		return nil
	}
	changed := func(method, suffix string, body any) error {
		if err := request(ctx, base, method, path+suffix, body, out); err != nil {
			return err
		}
		var b bytes.Buffer
		if err := request(ctx, base, "GET", path, nil, &b); err == nil {
			pendingHint(b.Bytes(), out)
		}
		return nil
	}
	switch args[0] {
	case "model":
		if len(rest) == 0 {
			rt, e := getRuntime()
			if e != nil {
				return e
			}
			return json.NewEncoder(out).Encode(map[string]any{"model": rt.Model})
		}
		if len(rest) != 1 {
			return fmt.Errorf("usage: lolad model <agent> [<model>]")
		}
		rt, e := getRuntime()
		if e != nil {
			return e
		}
		rt.Model = rest[0]
		return update(rt)
	case "skills":
		if len(rest) == 0 {
			return request(ctx, base, "GET", path+"/skills", nil, out)
		}
		if len(rest) != 2 {
			return fmt.Errorf("usage: lolad skills <agent> [mode inherit|barebones | add <id> | rm <id>]")
		}
		switch rest[0] {
		case "mode":
			rt, e := getRuntime()
			if e != nil {
				return e
			}
			rt.Skills = core.SkillsMode(rest[1])
			return update(rt)
		case "add":
			return changed("POST", "/skills", map[string]string{"from": rest[1]})
		case "rm":
			return changed("DELETE", "/skills/"+url.PathEscape(rest[1]), nil)
		}
	case "env":
		if len(rest) == 0 || (len(rest) == 1 && rest[0] == "ls") {
			return request(ctx, base, "GET", path+"/env", nil, out)
		}
		if len(rest) != 2 {
			return fmt.Errorf("usage: lolad env <agent> [ls | set KEY (value on stdin) | rm KEY]; values must never be passed as arguments")
		}
		switch rest[0] {
		case "set":
			b, e := io.ReadAll(io.LimitReader(stdin, (1<<20)+1))
			if e != nil {
				return e
			}
			if len(b) > 1<<20 {
				return fmt.Errorf("env value exceeds 1 MB")
			}
			value := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
			return changed("PUT", "/env/"+url.PathEscape(rest[1]), map[string]string{"value": value})
		case "rm":
			return changed("DELETE", "/env/"+url.PathEscape(rest[1]), nil)
		}
	case "email":
		if len(rest) == 0 || (len(rest) == 1 && rest[0] == "show") {
			return request(ctx, base, "GET", path+"/email", nil, out)
		}
		if len(rest) == 2 && rest[0] == "test" {
			return request(ctx, base, "POST", path+"/email/test", map[string]string{"address": rest[1]}, out)
		}
	}
	return fmt.Errorf("invalid %s arguments; run lolad help", args[0])
}

func pendingHint(data []byte, out io.Writer) {
	var v core.AgentStatus
	if json.Unmarshal(data, &v) == nil && v.PendingRestart {
		fmt.Fprintf(out, "Restart to apply (%s): lolad restart %s\n", v.PendingReason, v.Name)
	}
}
