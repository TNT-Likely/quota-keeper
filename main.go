// quota-keeper —— 订阅账号 Codex 5 小时限额窗口的自动检测与自动补锚 CLI。
// 读取本机 Codex CLI 的登录凭据(~/.codex/auth.json)直连官方后端，
// 从响应头读取 5h/周窗口用量与重置时刻，在窗口重置后自动补发最小请求完成续锚。
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

// ---------- 配置 ----------

type Config struct {
	Models           []string `json:"models"`
	Anchors          []string `json:"anchors"`
	AuthPath         string   `json:"auth_path"`
	StatePath        string   `json:"state_path"`
	LogPath          string   `json:"log_path"`
	Endpoint         string   `json:"endpoint"`
	ClientID         string   `json:"client_id"`
	TokenURL         string   `json:"token_url"`
	WindowMinutes    int      `json:"window_minutes"`
	RefreshBufferSec int      `json:"refresh_buffer_seconds"`
	ErrorRetrySec    int      `json:"error_retry_seconds"`
	TickSeconds      int      `json:"tick_seconds"`
}

func homeExpand(p string) string {
	h, _ := os.UserHomeDir()
	if p == "~" {
		return h
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(h, p[2:])
	}
	return p
}

func configPath() string {
	if p := os.Getenv("QUOTA_KEEPER_CONFIG"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "quota-keeper", "config.json")
}

func defaultConfig() *Config {
	h, _ := os.UserHomeDir()
	return &Config{
		Models:           []string{"gpt-5.6-luna"},
		AuthPath:         filepath.Join(h, ".codex", "auth.json"),
		StatePath:        filepath.Join(h, ".local", "state", "quota-keeper", "state.json"),
		LogPath:          filepath.Join(h, ".local", "state", "quota-keeper", "quota-keeper.log"),
		Endpoint:         "https://chatgpt.com/backend-api/codex/responses",
		ClientID:         "app_EMoamEEZ73f0CkXaXp7hrann",
		TokenURL:         "https://auth.openai.com/oauth/token",
		WindowMinutes:    300,
		RefreshBufferSec: 60,
		ErrorRetrySec:    300,
		TickSeconds:      20,
	}
}

func loadConfig() *Config {
	c := defaultConfig()
	if b, err := os.ReadFile(configPath()); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			fmt.Fprintf(os.Stderr, "警告: 配置文件解析失败，使用默认配置: %v\n", err)
		}
	}
	c.AuthPath = homeExpand(c.AuthPath)
	c.StatePath = homeExpand(c.StatePath)
	c.LogPath = homeExpand(c.LogPath)
	if len(c.Models) == 0 {
		c.Models = []string{"gpt-5.6-luna"}
	}
	if c.WindowMinutes <= 0 {
		c.WindowMinutes = 300
	}
	if c.RefreshBufferSec <= 0 {
		c.RefreshBufferSec = 60
	}
	if c.ErrorRetrySec <= 0 {
		c.ErrorRetrySec = 300
	}
	if c.TickSeconds <= 0 {
		c.TickSeconds = 20
	}
	return c
}

// ---------- 登录凭据 ----------

type Auth struct {
	Tokens struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

func readAuth(path string) (*Auth, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取凭据失败: %w", err)
	}
	var a Auth
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("凭据解析失败: %w", err)
	}
	if a.Tokens.AccessToken == "" {
		return nil, errors.New("凭据中没有 access_token（是否未用 Codex CLI 登录？）")
	}
	return &a, nil
}

func saveAuth(path string, a *Auth) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".qk-tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// tokenExpired 解析 id_token(JWT) 的 exp，提前 5 分钟视为过期。
func tokenExpired(a *Auth) bool {
	parts := strings.Split(a.Tokens.IDToken, ".")
	if len(parts) < 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return false
	}
	return time.Now().Unix() >= claims.Exp-300
}

func refreshTokens(cfg *Config) error {
	a, err := readAuth(cfg.AuthPath)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", cfg.ClientID)
	form.Set("refresh_token", a.Tokens.RefreshToken)
	resp, err := http.Post(cfg.TokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return err
	}
	if tr.AccessToken == "" {
		return errors.New("刷新响应缺少 access_token")
	}
	a.Tokens.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.Tokens.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		a.Tokens.IDToken = tr.IDToken
	}
	a.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	return saveAuth(cfg.AuthPath, a)
}

// ---------- 触发与限额解析 ----------

type WindowInfo struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetAt       int64   `json:"reset_at"` // epoch 秒，来自服务端响应头，权威
}

type triggerResult struct {
	status    int
	body      string
	primary   *WindowInfo // 5h 窗口
	secondary *WindowInfo // 周窗口
	plan      string
}

func parseWindow(kind string, h http.Header) *WindowInfo {
	up := h.Get("x-codex-" + kind + "-used-percent")
	if up == "" {
		return nil
	}
	w := &WindowInfo{}
	w.UsedPercent, _ = strconv.ParseFloat(up, 64)
	if v := h.Get("x-codex-" + kind + "-window-minutes"); v != "" {
		w.WindowMinutes, _ = strconv.Atoi(v)
	}
	if v := h.Get("x-codex-" + kind + "-reset-at"); v != "" {
		w.ResetAt, _ = strconv.ParseInt(v, 10, 64)
	}
	return w
}

func doTrigger(cfg *Config, model string) (*triggerResult, error) {
	a, err := readAuth(cfg.AuthPath)
	if err != nil {
		return nil, err
	}
	if tokenExpired(a) {
		if err := refreshTokens(cfg); err != nil {
			return nil, fmt.Errorf("凭据已过期且刷新失败: %w", err)
		}
		if a, err = readAuth(cfg.AuthPath); err != nil {
			return nil, err
		}
	}
	payload := marshalPayload(model)
	res, err := postTrigger(cfg, a, payload)
	if err != nil {
		return nil, err
	}
	if res.status == http.StatusUnauthorized {
		// access_token 失效: 刷新后重试一次
		if rerr := refreshTokens(cfg); rerr != nil {
			res.body = "401 且凭据刷新失败: " + rerr.Error()
			return res, nil
		}
		if a, err = readAuth(cfg.AuthPath); err != nil {
			return res, nil
		}
		if res2, err2 := postTrigger(cfg, a, payload); err2 == nil {
			return res2, nil
		}
	}
	return res, nil
}

func marshalPayload(model string) []byte {
	payload := map[string]any{
		"model":        model,
		"instructions": "",
		"input": []map[string]any{{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "hi"},
			},
		}},
		"store":  false,
		"stream": true,
	}
	b, _ := json.Marshal(payload)
	return b
}

func postTrigger(cfg *Config, a *Auth, payload []byte) (*triggerResult, error) {
	req, err := http.NewRequest(http.MethodPost, cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Tokens.AccessToken)
	if a.Tokens.AccountID != "" {
		req.Header.Set("chatgpt-account-id", a.Tokens.AccountID)
	}
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "quota-keeper/"+version)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// stream:true 的 SSE 响应，最小请求下体量很小，读尽即可
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &triggerResult{
		status:    resp.StatusCode,
		body:      string(body),
		primary:   parseWindow("primary", resp.Header),
		secondary: parseWindow("secondary", resp.Header),
		plan:      resp.Header.Get("x-codex-plan-type"),
	}, nil
}

// ---------- 状态与调度 ----------

type ModelState struct {
	Kind        string      `json:"kind"` // ok | cooling | error
	Primary     *WindowInfo `json:"primary,omitempty"`
	Secondary   *WindowInfo `json:"secondary,omitempty"`
	PlanType    string      `json:"plan_type,omitempty"`
	LastStatus  int         `json:"last_status"`
	LastCheck   time.Time   `json:"last_check"`
	LastTrigger *time.Time  `json:"last_trigger,omitempty"`
	LastDetail  string      `json:"last_detail,omitempty"`
}

type State struct {
	Models map[string]*ModelState `json:"models"`
}

func loadState(path string) *State {
	st := &State{Models: map[string]*ModelState{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Models == nil {
		st.Models = map[string]*ModelState{}
	}
	return st
}

func saveState(path string, st *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseAnchor(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("非法锚点 %q", s)
	}
	m := 0
	if len(parts) == 2 {
		if m, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
			return 0, 0, fmt.Errorf("非法锚点 %q", s)
		}
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("非法锚点 %q", s)
	}
	return h, m, nil
}

// nextAnchorAfter 返回 t 之后(严格大于)最近的锚点时刻。
func nextAnchorAfter(anchors []string, t time.Time) (time.Time, bool) {
	var best time.Time
	for _, a := range anchors {
		h, m, err := parseAnchor(a)
		if err != nil {
			continue
		}
		cand := time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, t.Location())
		if !cand.After(t) {
			cand = cand.Add(24 * time.Hour)
		}
		if best.IsZero() || cand.Before(best) {
			best = cand
		}
	}
	return best, !best.IsZero()
}

// nextEventFor 计算该模型下一次应触发的时间；返回零值表示无状态、应立即触发，
// 返回已过去的时刻同样表示应立即触发。
func nextEventFor(cfg *Config, st *State, model string) time.Time {
	ms := st.Models[model]
	if ms == nil {
		return time.Time{}
	}
	now := time.Now()
	buffer := time.Duration(cfg.RefreshBufferSec) * time.Second
	switch ms.Kind {
	case "cooling":
		if ms.Primary != nil && ms.Primary.ResetAt > 0 {
			return time.Unix(ms.Primary.ResetAt, 0).Add(buffer)
		}
		return ms.LastCheck.Add(time.Duration(cfg.ErrorRetrySec) * time.Second)
	case "ok":
		var t time.Time
		ref := now
		if ms.LastTrigger != nil {
			ref = *ms.LastTrigger
		}
		if a, ok := nextAnchorAfter(cfg.Anchors, ref); ok {
			t = a
		}
		// 窗口结束点: 优先信服务端 reset-at，否则按上次触发+窗口长度推算
		var we time.Time
		if ms.Primary != nil && ms.Primary.ResetAt > 0 {
			we = time.Unix(ms.Primary.ResetAt, 0).Add(buffer)
		} else {
			we = ref.Add(time.Duration(cfg.WindowMinutes) * time.Minute)
		}
		if t.IsZero() || we.Before(t) {
			t = we
		}
		return t
	default: // error 等
		return ms.LastCheck.Add(time.Duration(cfg.ErrorRetrySec) * time.Second)
	}
}

func fireDue(cfg *Config, st *State, force string) {
	now := time.Now()
	for _, model := range cfg.Models {
		if force != "" && model != force {
			continue
		}
		if force == "" {
			if next := nextEventFor(cfg, st, model); !next.IsZero() && now.Before(next) {
				continue
			}
		}
		res, err := doTrigger(cfg, model)
		ms := st.Models[model]
		if ms == nil {
			ms = &ModelState{}
			st.Models[model] = ms
		}
		ms.LastCheck = time.Now()
		if err != nil {
			ms.Kind = "error"
			ms.LastStatus = 0
			ms.LastDetail = err.Error()
			logf(cfg, "%s ✗ %s", model, err.Error())
			continue
		}
		ms.LastStatus = res.status
		ms.Primary = res.primary
		ms.Secondary = res.secondary
		if res.plan != "" {
			ms.PlanType = res.plan
		}
		now2 := time.Now()
		ms.LastTrigger = &now2
		switch {
		case res.status == http.StatusOK:
			ms.Kind = "ok"
			ms.LastDetail = "触发成功，窗口已锚定"
		case res.status == http.StatusTooManyRequests:
			ms.Kind = "cooling"
			ms.LastDetail = firstJSONField(res.body, "message")
		default:
			ms.Kind = "error"
			ms.LastDetail = truncate(res.body, 200)
		}
		logf(cfg, "%s %s", model, describe(res))
	}
}

func describe(res *triggerResult) string {
	base := fmt.Sprintf("HTTP %d", res.status)
	if res.primary != nil {
		base += fmt.Sprintf("，5h 已用 %.0f%%", res.primary.UsedPercent)
		if res.primary.ResetAt > 0 {
			rt := time.Unix(res.primary.ResetAt, 0)
			base += fmt.Sprintf("，%s 重置(还剩 %s)", rt.Format("15:04:05"), humanDur(time.Until(rt)))
		}
	}
	if res.secondary != nil && res.secondary.UsedPercent > 0 {
		base += fmt.Sprintf("，周已用 %.0f%%", res.secondary.UsedPercent)
	}
	switch res.status {
	case http.StatusOK:
		base += " → 窗口已锚定"
	case http.StatusTooManyRequests:
		base += " → 冷却中，重置后自动补锚"
	case http.StatusUnauthorized:
		base += " → 凭据无效"
	}
	return base
}

// ---------- 日志与小工具 ----------

func humanDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes())
	if m >= 60 {
		return fmt.Sprintf("%d小时%02d分", m/60, m%60)
	}
	return fmt.Sprintf("%d分%02d秒", m, int(d.Seconds())%60)
}

func logf(cfg *Config, format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, args...)
	fmt.Println(line)
	if cfg.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(cfg.LogPath), 0o700)
		f, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstJSONField(body, field string) string {
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) == nil {
		if e, ok := m["error"].(map[string]any); ok {
			if v, ok := e[field].(string); ok && v != "" {
				return v
			}
		}
	}
	return truncate(body, 120)
}

// ---------- 暂停/恢复 ----------

func pausedPath(cfg *Config) string {
	return filepath.Join(filepath.Dir(cfg.StatePath), "paused")
}

func isPaused(cfg *Config) bool {
	_, err := os.Stat(pausedPath(cfg))
	return err == nil
}

func cmdPause(cfg *Config) {
	_ = os.MkdirAll(filepath.Dir(pausedPath(cfg)), 0o700)
	if err := os.WriteFile(pausedPath(cfg), []byte(time.Now().Format(time.RFC3339)), 0o600); err != nil {
		fmt.Println("暂停失败:", err)
		os.Exit(1)
	}
	logf(cfg, "守护已暂停，自动触发停用(手动 trigger 仍可用)")
}

func cmdResume(cfg *Config) {
	if err := os.Remove(pausedPath(cfg)); err != nil && !os.IsNotExist(err) {
		fmt.Println("恢复失败:", err)
		os.Exit(1)
	}
	logf(cfg, "守护已恢复，自动触发重新生效")
}

// ---------- 命令 ----------

func cmdRun(cfg *Config) {
	if !acquireLock() {
		fmt.Println("quota-keeper 已在运行，本次退出")
		return
	}
	logf(cfg, "quota-keeper %s 启动 (pid %d)，模型 %v，锚点 %v", version, os.Getpid(), cfg.Models, cfg.Anchors)
	st := loadState(cfg.StatePath)
	paused := isPaused(cfg)
	for {
		if isPaused(cfg) {
			if !paused {
				logf(cfg, "守护进入暂停，自动触发停用")
				paused = true
			}
		} else {
			if paused {
				logf(cfg, "守护从暂停中恢复")
				paused = false
			}
			fireDue(cfg, st, "")
			saveState(cfg.StatePath, st)
		}
		time.Sleep(time.Duration(cfg.TickSeconds) * time.Second)
	}
}

func cmdOnce(cfg *Config) {
	st := loadState(cfg.StatePath)
	fireDue(cfg, st, "")
	saveState(cfg.StatePath, st)
	printStatus(cfg, st)
}

func cmdTrigger(cfg *Config, model string) {
	found := false
	for _, m := range cfg.Models {
		if m == model {
			found = true
		}
	}
	if !found {
		cfg.Models = append(cfg.Models, model)
	}
	st := loadState(cfg.StatePath)
	fireDue(cfg, st, model)
	saveState(cfg.StatePath, st)
	printStatus(cfg, st)
}

func cmdStatus(cfg *Config) {
	printStatus(cfg, loadState(cfg.StatePath))
}

func printStatus(cfg *Config, st *State) {
	for _, model := range cfg.Models {
		fmt.Printf("== %s ==\n", model)
		ms := st.Models[model]
		if ms == nil {
			fmt.Println("  状态未知，还没有动作记录（先跑一次 quota-keeper once）")
			continue
		}
		fmt.Printf("  状态: %s | 上次 HTTP %d @ %s\n", ms.Kind, ms.LastStatus, ms.LastCheck.Format("01-02 15:04:05"))
		if isPaused(cfg) {
			fmt.Println("  守护: 已暂停(自动触发停用，恢复用 quota-keeper resume)")
		}
		if ms.PlanType != "" {
			fmt.Printf("  套餐: %s\n", ms.PlanType)
		}
		if ms.Primary != nil {
			line := fmt.Sprintf("  5h 窗口: 已用 %.0f%%", ms.Primary.UsedPercent)
			if ms.Primary.ResetAt > 0 {
				rt := time.Unix(ms.Primary.ResetAt, 0)
				line += fmt.Sprintf("，%s 重置(还剩 %s)", rt.Format("01-02 15:04"), humanDur(time.Until(rt)))
			}
			fmt.Println(line)
		}
		if ms.Secondary != nil {
			line := fmt.Sprintf("  周窗口: 已用 %.0f%%", ms.Secondary.UsedPercent)
			if ms.Secondary.ResetAt > 0 {
				rt := time.Unix(ms.Secondary.ResetAt, 0)
				line += fmt.Sprintf("，%s 重置", rt.Format("01-02 15:04"))
			}
			fmt.Println(line)
		}
		next := nextEventFor(cfg, st, model)
		fmt.Printf("  下次动作: %s (%s后)\n", next.Format("01-02 15:04:05"), humanDur(time.Until(next)))
		if ms.LastDetail != "" {
			fmt.Printf("  上次详情: %s\n", ms.LastDetail)
		}
	}
}

func cmdRefreshToken(cfg *Config) {
	if err := refreshTokens(cfg); err != nil {
		fmt.Println("刷新失败:", err)
		os.Exit(1)
	}
	fmt.Println("✓ 凭据已刷新并写回", cfg.AuthPath)
}

func usage() {
	fmt.Print(`quota-keeper ` + version + ` —— Codex 5h 限额窗口自动检测与自动补锚

用法:
  quota-keeper run             常驻守护: 按锚点与窗口重置时刻自动触发(配 launchd 用)
  quota-keeper once            执行一轮检测后退出
  quota-keeper trigger [模型]   立即触发一次(默认取配置第一个模型)
  quota-keeper status          查看各模型窗口状态与下次动作
  quota-keeper refresh-token   强制刷新登录凭据并写回 auth.json
  quota-keeper pause           暂停自动触发(写入 paused 标记)
  quota-keeper resume          恢复自动触发
  quota-keeper help            本说明

配置: ~/.config/quota-keeper/config.json (见仓库 config.example.json)
状态: ~/.local/state/quota-keeper/state.json
`)
}

func main() {
	cfg := loadConfig()
	args := os.Args[1:]
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "run":
		cmdRun(cfg)
	case "once":
		cmdOnce(cfg)
	case "trigger":
		model := ""
		if len(args) > 1 {
			model = args[1]
		}
		if model == "" {
			model = cfg.Models[0]
		}
		cmdTrigger(cfg, model)
	case "status":
		cmdStatus(cfg)
	case "refresh-token":
		cmdRefreshToken(cfg)
	case "pause":
		cmdPause(cfg)
	case "resume":
		cmdResume(cfg)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Println("未知命令:", cmd)
		usage()
		os.Exit(2)
	}
}
