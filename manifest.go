package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServiceProbe 是可插拔采集的最小实现。所有端点都来自本地配置文件，
// 前端不能提交任意 URL；采集失败会写入明确状态，不阻塞 1 秒主循环。
type ServiceProbe struct {
	cfg    ProbeConfig
	client *http.Client
	mu     sync.RWMutex
	data   map[string]interface{}
	ok     bool
}

func NewServiceProbe(c ProbeConfig) *ServiceProbe {
	return &ServiceProbe{
		cfg:    c,
		client: &http.Client{Timeout: time.Duration(c.TimeoutMs) * time.Millisecond},
		data:   map[string]interface{}{},
	}
}

func (p *ServiceProbe) Name() string { return p.cfg.Name }

func (p *ServiceProbe) Loop(ctx context.Context) {
	p.run()
	t := time.NewTicker(time.Duration(p.cfg.IntervalSec) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.run()
		}
	}
}

func (p *ServiceProbe) Snapshot() (map[string]interface{}, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]interface{}, len(p.data)+2)
	for k, v := range p.data {
		out[k] = v
	}
	out["ts"] = nowUnix()
	out["ok"] = p.ok
	return out, p.ok
}

func (p *ServiceProbe) setFail(reason string) {
	p.mu.Lock()
	p.ok = false
	p.data = map[string]interface{}{"err": reason}
	p.mu.Unlock()
}

func (p *ServiceProbe) run() {
	if p.cfg.URL == "" {
		p.setFail("未配置 URL")
		return
	}
	req, err := http.NewRequest("GET", p.cfg.URL, nil)
	if err != nil {
		p.setFail("请求构造失败")
		return
	}
	if p.cfg.TokenFile != "" {
		if b, err := os.ReadFile(p.cfg.TokenFile); err == nil {
			tok := strings.TrimSpace(string(b))
			if strings.EqualFold(p.cfg.TokenHeader, "Authorization") {
				tok = "Bearer " + tok
			}
			req.Header.Set(p.cfg.TokenHeader, tok)
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		p.setFail("连接失败或超时")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.setFail("HTTP " + strconv.Itoa(resp.StatusCode))
		return
	}
	var root interface{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&root); err != nil {
		p.setFail("响应不是合法 JSON")
		return
	}
	out := map[string]interface{}{}
	if p.cfg.Type == "jellyfin" {
		out = jellyfinMap(root)
	} else {
		for src, dst := range p.cfg.FieldMap {
			if v, ok := lookup(root, src); ok {
				out[dst] = v
			}
		}
		for _, f := range p.cfg.Fields {
			if v, ok := lookup(root, f); ok {
				out[sanitizeKey(f)] = v
			}
		}
	}
	if len(out) == 0 {
		p.setFail("未匹配到任何字段")
		return
	}
	p.mu.Lock()
	p.data = out
	p.ok = true
	p.mu.Unlock()
}

// jellyfinMap 把 Jellyfin /Sessions 映射成前端预设组件认识的结构。
func jellyfinMap(root interface{}) map[string]interface{} {
	out := map[string]interface{}{"active": false}
	arr, ok := root.([]interface{})
	if !ok {
		return out
	}
	playing := 0
	var best map[string]interface{}
	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if _, has := m["NowPlayingItem"]; !has || m["NowPlayingItem"] == nil {
			continue
		}
		playing++
		if best == nil {
			best = m
		} else {
			a, _ := m["LastActivityDate"].(string)
			b, _ := best["LastActivityDate"].(string)
			if a > b {
				best = m
			}
		}
	}
	if best == nil {
		return out
	}
	np, _ := best["NowPlayingItem"].(map[string]interface{})
	ti, _ := best["TranscodingInfo"].(map[string]interface{})
	ps, _ := best["PlayState"].(map[string]interface{})
	pick := func(src map[string]interface{}, key, dst string) {
		if src == nil {
			return
		}
		if v, ok := src[key]; ok && v != nil {
			out[dst] = v
		}
	}
	out["active"] = true
	out["n_playing"] = playing
	pick(best, "UserName", "user")
	pick(best, "DeviceName", "device")
	pick(best, "Client", "client")
	pick(np, "Name", "title")
	pick(ps, "IsPaused", "paused")
	pick(ps, "PlayMethod", "method")
	pick(ti, "HardwareAccelerationType", "hw")
	pick(ti, "VideoCodec", "video_codec")
	pick(ti, "AudioCodec", "audio_codec")
	pick(ti, "Width", "w")
	pick(ti, "Height", "h")
	pick(ti, "Bitrate", "bitrate")
	pick(ti, "Framerate", "fps")
	pick(ti, "CompletionPercentage", "pct")
	return out
}

func lookup(root interface{}, path string) (interface{}, bool) {
	cur := root
	for _, k := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]interface{}:
			nv, exists := v[k]
			if !exists {
				return nil, false
			}
			cur = nv
		case []interface{}:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
		if cur == nil {
			return nil, false
		}
	}
	return cur, true
}

func sanitizeKey(s string) string {
	return strings.NewReplacer(".", "_", "[", "_", "]", "_").Replace(s)
}
