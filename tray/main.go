// quota-keeper-tray —— quota-keeper 的跨平台状态栏(托盘)控制台。
// macOS 菜单栏 / Windows 托盘 / Linux 托盘(AppIndicator) 同一套代码。
// 只做展示与控制，核心调度仍由 quota-keeper run 守护完成。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/systray"
)

var version = "dev"

const (
	binPath    = "~/.local/bin/quota-keeper"
	statePath  = "~/.local/state/quota-keeper/state.json"
	logPath    = "~/.local/state/quota-keeper/quota-keeper.log"
	pausedPath = "~/.local/state/quota-keeper/paused"
)

func home(p string) string {
	h, _ := os.UserHomeDir()
	if p == "~" {
		return h
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(h, p[2:])
	}
	return p
}

type WindowInfo struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
}

type ModelState struct {
	Kind    string      `json:"kind"`
	Primary *WindowInfo `json:"primary"`
}

type QKState struct {
	Models map[string]*ModelState `json:"models"`
}

func readState() *QKState {
	b, err := os.ReadFile(home(statePath))
	if err != nil {
		return nil
	}
	var s QKState
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func isPaused() bool {
	_, err := os.Stat(home(pausedPath))
	return err == nil
}

func setPaused(p bool) {
	if p {
		_ = os.WriteFile(home(pausedPath), []byte(time.Now().Format(time.RFC3339)), 0o600)
		return
	}
	_ = os.Remove(home(pausedPath))
}

func runQK(args ...string) {
	c := exec.Command(home(binPath), args...)
	_ = c.Start()
}

func openFile(p string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", p)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", p)
	default:
		c = exec.Command("xdg-open", p)
	}
	_ = c.Start()
}

// iconPNG 用标准库现画一个琥珀底、深色时钟指针的 32x32 图标。
func iconPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	bg := color.RGBA{R: 245, G: 166, B: 35, A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	fg := color.RGBA{R: 40, G: 40, B: 40, A: 255}
	for x := 15; x <= 17; x++ {
		for y := 7; y <= 17; y++ {
			img.Set(x, y, fg)
		}
	}
	for x := 17; x <= 24; x++ {
		for y := 13; y <= 15; y++ {
			img.Set(x, y, fg)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

var (
	mStatus *systray.MenuItem
	mTrig   *systray.MenuItem
	mToggle *systray.MenuItem
	mLog    *systray.MenuItem
	mQuit   *systray.MenuItem
)

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(iconPNG())
	systray.SetTitle("QK")
	systray.SetTooltip("quota-keeper")
	mStatus = systray.AddMenuItem("", "")
	mStatus.Disable()
	systray.AddSeparator()
	mTrig = systray.AddMenuItem("立即触发", "立即发送一次最小请求")
	mToggle = systray.AddMenuItem("暂停守护", "暂停/恢复自动触发")
	mLog = systray.AddMenuItem("打开日志", "查看触发记录")
	systray.AddSeparator()
	mQuit = systray.AddMenuItem("退出托盘", "退出托盘(守护进程 quota-keeper run 不受影响)")
	updateMenu()
	go refreshLoop()
	for {
		select {
		case <-mTrig.ClickedCh:
			runQK("trigger")
		case <-mToggle.ClickedCh:
			setPaused(!isPaused())
			updateMenu()
		case <-mLog.ClickedCh:
			openFile(home(logPath))
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func onExit() {}

func refreshLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		updateMenu()
	}
}

func updateMenu() {
	now := time.Now()
	if isPaused() {
		mToggle.SetTitle("恢复守护")
		mStatus.SetTitle("⏸ 已暂停")
		systray.SetTooltip("quota-keeper 已暂停")
		return
	}
	mToggle.SetTitle("暂停守护")
	s := readState()
	if s == nil || len(s.Models) == 0 {
		mStatus.SetTitle("… 无状态(先跑 once)")
		return
	}
	for _, ms := range s.Models {
		line := ""
		if ms.Primary != nil {
			line = fmt.Sprintf("5h %3.0f%%", ms.Primary.UsedPercent)
			if ms.Primary.ResetAt > 0 {
				d := time.Unix(ms.Primary.ResetAt, 0).Sub(now)
				if d < 0 {
					d = 0
				}
				line += "，重置 " + humanShort(d)
			}
		}
		switch ms.Kind {
		case "ok":
			mStatus.SetTitle("✓ " + line)
		case "cooling":
			mStatus.SetTitle("⏳ " + line)
		default:
			mStatus.SetTitle("… " + line)
		}
		break // 只展示第一个模型
	}
	systray.SetTooltip(fmt.Sprintf("quota-keeper %s", version))
}

func humanShort(d time.Duration) string {
	m := int(d.Minutes())
	if m < 0 {
		m = 0
	}
	if m >= 60 {
		return fmt.Sprintf("%dh%02dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}
