//go:build windows

package main

// Windows 下暂不做单实例检测。
func acquireLock() bool { return true }
