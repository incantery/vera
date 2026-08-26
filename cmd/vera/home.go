package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/incantery/vera/home"
)

// `vera home`: where her memory is.
//
// One line, so it composes — `cd $(vera home)`, `ls $(vera home)/memory`
// — because the whole point of memory being files is that the ordinary
// tools work on it.
func showHome() error {
	fmt.Println(home.Path(veraHomeSetting()))
	return nil
}

// veraHomeSetting looks where verad looks. verad is usually started by
// launchd, which passes no shell environment, so VERA_HOME is set in
// ~/.config/vera/*.env like everything else — and `vera home` printing
// a different directory than the one being written to would be worse
// than not having the command.
func veraHomeSetting() string {
	if v := strings.TrimSpace(os.Getenv("VERA_HOME")); v != "" {
		return v
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".config", "vera", "*.env"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
			k, v, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(k) != "VERA_HOME" {
				continue
			}
			v = strings.TrimSpace(v)
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
			return v
		}
	}
	return ""
}
