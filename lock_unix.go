//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// acquireLock 用 pid 文件防止多实例；返回 false 表示已有实例在运行。
func acquireLock() bool {
	h, _ := os.UserHomeDir()
	p := filepath.Join(h, ".local", "state", "quota-keeper", "quota-keeper.pid")
	if b, err := os.ReadFile(p); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid != os.Getpid() {
			if syscall.Kill(pid, 0) == nil {
				return false
			}
		}
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o600)
	return true
}
