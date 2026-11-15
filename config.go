package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ProbeConfig 声明一个外部服务探针。字段全部来自配置文件，不允许用户
// 在前端填写任意 URL（SSRF 防护）：页面只能绑定白名单指标。
type ProbeConfig struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"` // http_json | jellyfin
	URL         string            `json:"url"`
	TokenFile   string            `json:"token_file"`
	TokenHeader string            `json:"token_header"`
	IntervalSec int               `json:"interval_sec"`
	TimeoutMs   int               `json:"timeout_ms"`
	Fields      []string          `json:"fields"`
	FieldMap    map[string]string `json:"field_map"`
	Widgets     []string          `json:"widgets"`
	HideOnFail  bool              `json:"hide_on_fail"`
	Disabled    bool              `json:"disabled"`
}

type Config struct {
	Listen        string        `json:"listen"`
	User          string        `json:"user"`
	PassHash      string        `json:"pass_hash"`
	Salt          string        `json:"salt"`
	IntervalMS    int           `json:"interval_ms"`
	HistoryLen    int           `json:"history_len"`
	DiskPaths     []string      `json:"disk_paths"`
	NetExclude    []string      `json:"net_exclude"`
	HiddenWidgets []string      `json:"hidden_widgets"`
	Probes        []ProbeConfig `json:"probes"`
	ConfigPath    string        `json:"-"`
}

var defaultNetExclude = []string{
	"lo", "veth", "docker", "br-", "virbr", "tun", "tap", "wg", "tailscale",
	"zt", "flannel", "cni", "sit", "gre", "dummy", "ifb", "bond",
}

func defaultConfigPath() string {
	if v := os.Getenv("RKMON_CONFIG"); v != "" {
		return v
	}
	if _, err := os.Stat("/etc/rkmon/config.json"); err == nil {
		return "/etc/rkmon/config.json"
	}
	return "config.json"
}

func DefaultConfig() *Config {
	return &Config{
		Listen:        "127.0.0.1:8094",
		User:          "admin",
		IntervalMS:    1000,
		HistoryLen:    180,
		DiskPaths:     []string{"/media", "/mnt/data", "/mnt", "/"},
		NetExclude:    append([]string{}, defaultNetExclude...),
		HiddenWidgets: []string{},
		Probes:        []ProbeConfig{},
	}
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := DefaultConfig()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8094"
	}
	if c.User == "" {
		c.User = "admin"
	}
	if c.IntervalMS <= 0 {
		c.IntervalMS = 1000
	}
	if c.HistoryLen <= 0 || c.HistoryLen > 2000 {
		c.HistoryLen = 180
	}
	if len(c.DiskPaths) == 0 {
		c.DiskPaths = []string{"/media", "/mnt", "/"}
	}
	if len(c.NetExclude) == 0 {
		c.NetExclude = append([]string{}, defaultNetExclude...)
	}
	if c.PassHash == "" || c.Salt == "" {
		return nil, errors.New("配置缺少 pass_hash / salt：请先用 --init 生成随机强密码")
	}
	// 阻止直接复制 config.example.json 启动：占位字符串会让服务「能起来但登不上」。
	if !validHex64(c.PassHash) || len(c.Salt) < 16 {
		return nil, errors.New("pass_hash / salt 格式非法：请用 --init 生成，不要直接复制示例配置")
	}
	for i := range c.Probes {
		p := &c.Probes[i]
		if p.Type == "" {
			p.Type = "http_json"
		}
		if p.IntervalSec <= 0 {
			p.IntervalSec = 15
		}
		if p.TimeoutMs <= 0 {
			p.TimeoutMs = 2000
		}
		if p.TokenHeader == "" {
			p.TokenHeader = "Authorization"
		}
	}
	c.ConfigPath = path
	return c, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func randomPassword(n int) (string, error) {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%_-+="
	b, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range b {
		out[i] = chars[int(b[i])%len(chars)]
	}
	return string(out), nil
}

func randomHex(n int) (string, error) {
	b, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// derive 是零依赖的加盐慢哈希（2 万轮 SHA-256）。目的不是替代 argon2，
// 而是让离线爆破配置文件的成本显著上升；配合限速与随机强密码使用。
func derive(pass, salt string) string {
	h := sha256.Sum256([]byte(salt + ":" + pass))
	for i := 0; i < 19999; i++ {
		h = sha256.Sum256(h[:])
	}
	return hex.EncodeToString(h[:])
}

// InitConfig 生成随机强密码并写入 0600 配置；密码只通过 stdout 返回一次，
// 不写入配置文件、日志或命令行参数。
func InitConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("配置已存在，拒绝覆盖：%s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	pass, err := randomPassword(24)
	if err != nil {
		return err
	}
	salt, err := randomHex(16)
	if err != nil {
		return err
	}
	c := DefaultConfig()
	c.Salt = salt
	c.PassHash = derive(pass, salt)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		return err
	}
	fmt.Println("配置已写入：" + path)
	fmt.Println("用户名：admin")
	fmt.Println("密码（只显示一次，请立即保存）：" + pass)
	return nil
}

func validHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func nowUnix() int64 { return time.Now().Unix() }
