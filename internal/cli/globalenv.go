package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/agentcfg"
	"github.com/CamiloCaceres/lolad/internal/harness"
)

// globalEnvCommand edits <state>/global.env, the env file every agent inherits
// (one DeepSeek key for all agents). It works on the file directly, so it runs
// without the daemon; agents pick changes up on their next (re)start.
func globalEnvCommand(stateDir string, args []string, stdin io.Reader, out io.Writer) error {
	if e := os.MkdirAll(stateDir, 0700); e != nil {
		return e
	}
	path := filepath.Join(stateDir, harness.GlobalEnvFile)
	dir := agentcfg.New(stateDir, "", agentcfg.Options{})
	if len(args) == 0 || (len(args) == 1 && args[0] == "ls") {
		vars, e := dir.Env(path)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(vars)
	}
	if len(args) != 2 {
		return fmt.Errorf("usage: lolad global-env [ls | set KEY (value on stdin) | rm KEY]; values must never be passed as arguments")
	}
	switch args[0] {
	case "set":
		b, e := io.ReadAll(io.LimitReader(stdin, (1<<20)+1))
		if e != nil {
			return e
		}
		if len(b) > 1<<20 {
			return fmt.Errorf("env value exceeds 1 MB")
		}
		value := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if e = dir.SetEnv(path, args[1], value); e != nil {
			return e
		}
		_ = os.Chmod(path, 0600)
		fmt.Fprintf(out, "%s saved for every agent; restart agents to apply\n", args[1])
		return nil
	case "rm":
		if e := dir.DeleteEnv(path, args[1]); e != nil {
			return e
		}
		fmt.Fprintf(out, "%s removed; restart agents to apply\n", args[1])
		return nil
	}
	return fmt.Errorf("usage: lolad global-env [ls | set KEY | rm KEY]")
}
