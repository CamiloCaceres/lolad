// Package deps pins third-party modules the packages use so parallel work
// does not race on go.mod. Delete once every import is in real code.
package deps

import (
	_ "github.com/coder/websocket"
	_ "github.com/creack/pty"
	_ "github.com/robfig/cron/v3"
	_ "gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)
