# quota-keeper

为 ChatGPT Plus 等订阅的 Codex **5 小时限额窗口**做自动检测与自动补锚的本地命令行小工具。Go 实现，单二进制，零第三方依赖（纯标准库）。

## 为什么需要它

Codex 订阅的 5 小时限额窗口何时重置由服务端决定，只有发起请求才能从响应头拿到权威的重置时刻。用固定 cron 猜重置点，猜错就白白浪费窗口起锚时间。quota-keeper 直接读官方响应头里的 `x-codex-primary-*` / `x-codex-secondary-*` 限额字段，在窗口重置后精准补一发最小请求，让每一轮窗口都从可控时刻开始。

- **自动检测**：每次请求解析 5h / 周窗口的用量百分比与重置时刻（服务端权威值，非估算）
- **自动补锚**：窗口重置后自动触发，重置与续锚无缝衔接；可选每日固定锚点兜底
- **凭据免配置**：复用本机 [Codex CLI](https://github.com/openai/codex) 登录态（`~/.codex/auth.json`）；token 过期自动用 refresh_token 刷新并安全写回
- **多模型**：配置多个模型分别跟踪窗口状态
- **隐私**：凭据只在本机读写，不上传、不入库

> ⚠️ 免责声明：本工具调用的是 ChatGPT 后端的非公开接口，接口形态可能随官方版本变化；请自行评估风险并遵守 OpenAI 服务条款。

## 安装

```bash
go install github.com/TNT-Likely/quota-keeper@latest
```

或从 [Releases](../../releases) 下载对应平台二进制，或自行构建：

```bash
go build -o quota-keeper .
```

前提：本机已用 Codex CLI 登录过（存在 `~/.codex/auth.json`）。

## 快速开始

```bash
mkdir -p ~/.config/quota-keeper
cp config.example.json ~/.config/quota-keeper/config.json
$EDITOR ~/.config/quota-keeper/config.json
quota-keeper once
quota-keeper status
```

## 命令

| 命令 | 说明 |
| --- | --- |
| `run` | 常驻守护：到点（锚点/窗口重置时刻）自动触发，建议配 launchd/systemd |
| `once` | 执行一轮检测后退出 |
| `trigger [模型]` | 立即触发一次（默认取配置第一个模型） |
| `status` | 查看各模型窗口状态、倒计时与下次动作 |
| `refresh-token` | 手动刷新登录凭据并写回 auth.json |

## 调度策略

对每个被跟踪的模型，`run` 守护在以下两个时刻的**较早者**发起触发：

1. **窗口重置点 + 缓冲秒**（默认 60s）：来自服务端 `x-codex-primary-reset-at` 响应头。上一窗口若已耗尽（429），重置后立即补锚；若未耗尽，到点重锚一次让窗口继续成链。
2. **固定锚点**（可选）：每天固定时刻兜底，防止状态丢失后长时间无人触发。

401 时自动刷新凭据并重试一次；网络错误按 `error_retry_seconds` 退避；pid 文件防止多实例。

## macOS launchd 常驻

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

## 配置项

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `models` | `["gpt-5.6-luna"]` | 要跟踪/触发的模型列表 |
| `anchors` | 空 | 每日固定锚点 `["HH:MM", ...]`，不需要就删掉 |
| `auth_path` | `~/.codex/auth.json` | Codex CLI 凭据路径 |
| `state_path` | `~/.local/state/quota-keeper/state.json` | 状态文件 |
| `log_path` | `~/.local/state/quota-keeper/quota-keeper.log` | 日志文件 |
| `endpoint` | ChatGPT Codex 后端 | 请求端点 |
| `window_minutes` | `300` | 服务端未返回窗口长度时的兜底值 |
| `refresh_buffer_seconds` | `60` | 重置点之后延迟多少秒再触发 |
| `error_retry_seconds` | `300` | 出错后的重试间隔 |
| `tick_seconds` | `20` | 守护循环轮询间隔 |

## Roadmap

- [ ] Claude provider（读钥匙串凭据，anthropic oauth）
- [ ] CLIProxyAPI transport（凭据不在本机、经代理转发的场景）
- [ ] macOS 菜单栏 GUI

## English

quota-keeper is a tiny Go CLI that keeps your ChatGPT/Codex subscription's 5-hour rate-limit window anchored. It talks directly to the Codex backend using your local Codex CLI login (`~/.codex/auth.json`), reads the authoritative `x-codex-*` rate-limit headers, and fires a minimal request right after each window resets, so no time is lost to an unanchored window. Token refresh is automatic; fixed daily anchors are optional. See the sections above for install, config and commands.

## License

MIT
