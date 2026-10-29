<div align="center">
  <img src="assets/icon-256.png" width="96" alt="quota-keeper logo"/>
  <h1>quota-keeper</h1>
  <p><b>自动检测并补锚 Codex 订阅的 5 小时限额窗口。</b></p>
  <p><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a></p>
  <p>
    <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-green.svg"></a>
    <img alt="Go 1.24+" src="https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&amp;logoColor=white">
    <a href="../../actions/workflows/release.yml"><img alt="release" src="https://github.com/TNT-Likely/quota-keeper/actions/workflows/release.yml/badge.svg"></a>
  </p>
</div>

quota-keeper 是一个 Go 编写的本地 CLI + 托盘小工具，为 ChatGPT/Codex 订阅的 **5 小时限额窗口**做自动检测与自动补锚。单二进制，守护核心零第三方依赖。

## 为什么需要它

Codex 订阅的 5 小时窗口何时重置由**服务端**决定，只有发起请求才能从响应头拿到权威的重置时刻。用固定 cron 猜重置点，猜错就白白浪费窗口起锚时间。quota-keeper 直接读官方响应头里的 `x-codex-primary-*`（5h）与 `x-codex-secondary-*`（周）限额字段，在窗口重置后精准补一发最小请求，让每一轮窗口都从可控时刻开始。

- **自动检测**：每次请求解析 5h / 周窗口的用量百分比与重置时刻（服务端权威值，非估算）
- **自动补锚**：窗口重置后自动触发；可选每日固定锚点兜底
- **凭据免配置**：复用本机 [Codex CLI](https://github.com/openai/codex) 登录态（`~/.codex/auth.json`）；token 过期自动用 refresh_token 刷新并安全写回
- **多模型**：任意数量模型独立跟踪
- **跨平台托盘控制台**：macOS 菜单栏 / Windows 托盘 / Linux AppIndicator：状态常显、立即触发、暂停/恢复、日志查看
- **隐私**：凭据只在本机读写，不上传、不入库

> ⚠️ **免责声明**：本工具调用的是 ChatGPT 后端的非公开接口，接口形态可能随官方版本变化；请自行评估风险并遵守 OpenAI 服务条款。

## 安装

```bash
go install github.com/TNT-Likely/quota-keeper@latest
```

或从 [Releases](../../releases) 下载对应平台二进制（守护 + 托盘，macOS / Windows / Linux），或自行构建：

```bash
go build -o quota-keeper .            # 守护 / CLI（纯标准库）
go build -o quota-keeper-tray ./tray  # 托盘控制台（依赖 fyne.io/systray）
```

前提：本机已用 Codex CLI 登录过（存在 `~/.codex/auth.json`）。

## 快速开始

```bash
mkdir -p ~/.config/quota-keeper
cp config.example.json ~/.config/quota-keeper/config.json
$EDITOR ~/.config/quota-keeper/config.json   # 改模型名与锚点
quota-keeper once     # 先跑一轮，拿到当前窗口状态
quota-keeper status   # 查看状态与下次动作
```

## 命令

| 命令 | 说明 |
| --- | --- |
| `run` | 常驻守护：按锚点/窗口重置时刻自动触发（配 launchd/systemd 用） |
| `once` | 执行一轮检测后退出 |
| `trigger [模型]` | 立即触发一次（默认取配置第一个模型） |
| `status` | 查看各模型窗口状态、倒计时与下次动作 |
| `refresh-token` | 手动刷新登录凭据并写回 auth.json |
| `pause` / `resume` | 暂停/恢复自动触发（写入/删除 paused 标记文件） |

## 调度策略

对每个被跟踪的模型，守护在以下两个时刻的**较早者**发起触发：

1. **窗口重置点 + 缓冲秒**（默认 60s）：来自服务端 `x-codex-primary-reset-at` 响应头。上一窗口若已耗尽（429），重置后立即补锚；若未耗尽，一发最小请求让窗口继续成链。
2. **每日锚点**（可选）：固定 `HH:MM` 时刻兜底，避免状态丢失后长时间无人触发。

401 时自动刷新凭据并重试一次；网络错误按 `error_retry_seconds` 退避；pid 文件防止多实例。

> 服务端语义：重置时刻在窗口**首次请求**时即固定，窗口内重复触发不会顺延它；因此续链会在每次重置后尽早补锚，锚点配置只作兜底。

## 状态栏/托盘控制台（可选）

`quota-keeper-tray` 是同仓库的跨平台托盘程序（macOS 菜单栏 / Windows 托盘 / Linux AppIndicator）：

- 托盘常显状态：✓/⏳ + 5h 用量 + 重置倒计时 + **下次触发时间**（30 秒刷新）
- 立即触发 / 暂停守护 / 恢复守护 / 打开日志（触发记录）/ 退出托盘

图标以内嵌生成代码（`tray/icon_data.go`）打进二进制；替换 `tray/*.png` 后重新生成：

```bash
go run ./tools/gen_icon_data
```

> macOS 提示：托盘需要 cgo。若 CommandLineTools 与 SDK 版本错位（报 `tapi error: unknown architecture`），用 `DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer go build ./tray` 指定完整 Xcode 工具链。

托盘的"暂停/恢复"与 CLI 的 `pause`/`resume` 操作的是同一个 paused 标记文件，可混用。

## 开机自启（macOS launchd）

`~/Library/LaunchAgents/dev.tnt.quota-keeper.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.tnt.quota-keeper</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-c</string>
    <string>exec $HOME/.local/bin/quota-keeper run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/dev/null</string>
  <key>StandardErrorPath</key>
  <string>/dev/null</string>
</dict>
</plist>
```

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.tnt.quota-keeper.plist
```

（托盘同理，`RunAtLoad` 保持 true、`KeepAlive` 设为 false。）

## 配置项

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `models` | `["gpt-5.6-luna"]` | 要跟踪/触发的模型列表 |
| `anchors` | 空 | 每日固定锚点 `["HH:MM", ...]`，不需要就删掉 |
| `auth_path` | `~/.codex/auth.json` | Codex CLI 凭据路径 |
| `state_path` | `~/.local/state/quota-keeper/state.json` | 状态文件（含 `next_event`） |
| `log_path` | `~/.local/state/quota-keeper/quota-keeper.log` | 日志文件 |
| `endpoint` | ChatGPT Codex 后端 | 请求端点 |
| `window_minutes` | `300` | 服务端未返回窗口长度时的兜底值 |
| `refresh_buffer_seconds` | `60` | 重置点之后延迟多少秒再触发 |
| `error_retry_seconds` | `300` | 出错后的重试间隔 |
| `tick_seconds` | `20` | 守护循环轮询间隔 |

## 隐私与安全

凭据只在本机读写（`auth.json` 与 `config.json` 建议保持 `0600`）。`config.json`、状态、日志与构建产物均已 git-ignore，不会入库、不会离开你的机器。

## Roadmap

- [x] 跨平台状态栏控制台
- [ ] Claude provider（读钥匙串凭据，Anthropic OAuth）
- [ ] CLIProxyAPI transport（凭据在代理后方的场景）
- [ ] Windows / Linux 自启动辅助

## License

[MIT](LICENSE)
