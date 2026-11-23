package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigVersion is the newest config.yaml layout this build understands. The
// optional top-level "version:" key says which layout a file uses; a missing
// key means 1. Files are never rewritten just to add it.
const ConfigVersion = 1

// FileError is a YAML problem in one file, with the line when known.
type FileError struct {
	Path string
	Line int
	Msg  string
	Err  error
}

func (e *FileError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s, line %d: %s", e.Path, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Msg)
}

func (e *FileError) Unwrap() error { return e.Err }

var yamlLine = regexp.MustCompile(`^line (\d+): (.*)$`)

// YAMLError rewrites a yaml.v3 error ("yaml: line 3: mapping values are not
// allowed in this context") as "<path>, line 3: mapping values are not
// allowed in this context". Other errors get the path in front.
func YAMLError(path string, err error) error {
	if err == nil {
		return nil
	}
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		line, msgs := 0, make([]string, 0, len(te.Errors))
		for _, s := range te.Errors {
			if m := yamlLine.FindStringSubmatch(s); m != nil {
				if line == 0 {
					line, _ = strconv.Atoi(m[1])
					msgs = append(msgs, m[2])
				} else {
					msgs = append(msgs, s)
				}
			} else {
				msgs = append(msgs, s)
			}
		}
		return &FileError{Path: path, Line: line, Msg: strings.Join(msgs, "; "), Err: err}
	}
	s := strings.TrimPrefix(err.Error(), "yaml: ")
	if m := yamlLine.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return &FileError{Path: path, Line: n, Msg: m[2], Err: err}
	}
	return &FileError{Path: path, Msg: s, Err: err}
}

// ListenSet reports whether the config file at path sets a non-empty
// top-level listen: itself. A missing file sets nothing.
func ListenSet(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var m struct {
		Listen string `yaml:"listen"`
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return false, YAMLError(path, err)
	}
	return strings.TrimSpace(m.Listen) != "", nil
}

var listenLine = regexp.MustCompile(`^listen\s*:`)

// SetListen writes "listen: <addr>" into the config file at path, editing the
// text so every other line and comment stays as it was: an existing top-level
// listen: line is replaced, otherwise the key is appended. The file is
// replaced atomically (temp file and rename) and keeps its mode.
func SetListen(path, addr, note string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	src := string(b)
	if os.IsNotExist(err) {
		// A missing config means "search the built-in agents folder"; a file
		// without agents_dirs would silently stop that, so write it out.
		var sb strings.Builder
		sb.WriteString("agents_dirs:\n")
		for _, d := range DefaultConfig().AgentsDirs {
			sb.WriteString("  - " + strconv.Quote(d) + "\n")
		}
		src = sb.String()
	}
	nl := "\n"
	if strings.Contains(src, "\r\n") {
		nl = "\r\n"
	}
	line := "listen: " + strconv.Quote(addr)
	lines := strings.Split(src, nl)
	done := false
	for i, l := range lines {
		if listenLine.MatchString(l) {
			// Only a single-line value can be replaced in place.
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], " ") {
				return fmt.Errorf("%s: listen: spans several lines; set it by hand", path)
			}
			lines[i] = line
			done = true
			break
		}
	}
	out := strings.Join(lines, nl)
	if !done {
		if out != "" && !strings.HasSuffix(out, nl) {
			out += nl
		}
		if note != "" {
			out += "# " + note + nl
		}
		out += line + nl
	}
	var probe struct {
		Listen string `yaml:"listen"`
	}
	if err := yaml.Unmarshal([]byte(out), &probe); err != nil || probe.Listen != addr {
		return fmt.Errorf("%s: could not set listen: safely; set it by hand to %s", path, addr)
	}
	return writeFileAtomic(path, []byte(out), 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
