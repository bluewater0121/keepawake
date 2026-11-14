//go:build !windows

package main

import (
	"fmt"
	"os"
)

// 非 Windows 平台占位实现：
// SetThreadExecutionState / SC_MONITORPOWER 均为 Windows 专有 API。
// Linux 可改用 systemd-inhibit / D-Bus，macOS 可改用 caffeinate。
func main() {
	fmt.Fprintln(os.Stderr, "keepawake 目前仅支持 Windows。")
	fmt.Fprintln(os.Stderr, "Linux 可用: systemd-inhibit --what=sleep:idle <命令>")
	fmt.Fprintln(os.Stderr, "macOS 可用: caffeinate -s <命令>")
	os.Exit(1)
}
