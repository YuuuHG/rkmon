package main

import (
	"bufio"
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type cpuSample struct {
	idle  uint64
	total uint64
}

type netSample struct {
	ts time.Time
	rx uint64
	tx uint64
}

// State 只保存内存中的环形历史，不写数据库、不落盘，重启后历史归零。
type State struct {
	cfg     *Config
	mu      sync.RWMutex
	now     map[string]interface{}
	hist    []map[string]interface{}
	prevCPU []cpuSample
	prevNet netSample
	netInit bool
	probes  []*ServiceProbe
}

func NewState(c *Config) *State {
	s := &State{
		cfg:  c,
		now:  make(map[string]interface{}),
		hist: make([]map[string]interface{}, 0, c.HistoryLen),
	}
	for i := range c.Probes {
		if c.Probes[i].Disabled {
			continue
		}
		s.probes = append(s.probes, NewServiceProbe(c.Probes[i]))
	}
	for _, p := range s.probes {
		go p.Loop(context.Background())
	}
	return s
}

func (s *State) Loop(ctx context.Context) {
	s.sample()
	t := time.NewTicker(time.Duration(s.cfg.IntervalMS) * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sample()
		}
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (s *State) sample() {
	ts := nowUnix()
	cur, err := readCPUTimes()
	cpu := 0.0
	cores := []interface{}{}
	if err == nil {
		if len(s.prevCPU) > 0 && len(cur) > 0 {
			cpu = round1(cpuPct(cur[0], s.prevCPU[0]))
			for i := 1; i < len(cur) && i < len(s.prevCPU); i++ {
				cores = append(cores, round1(cpuPct(cur[i], s.prevCPU[i])))
			}
		}
		s.prevCPU = cur
	}

	ns, ifaces := netSampleNow(s.cfg)
	rxBps, txBps := 0.0, 0.0
	if s.netInit {
		dt := ns.ts.Sub(s.prevNet.ts).Seconds()
		if dt > 0 && ns.rx >= s.prevNet.rx && ns.tx >= s.prevNet.tx {
			rxBps = float64(ns.rx-s.prevNet.rx) / dt
			txBps = float64(ns.tx-s.prevNet.tx) / dt
		}
	}
	s.netInit = true
	s.prevNet = ns

	hddPath, hdd := findDataDisk(s.cfg)
	now := map[string]interface{}{
		"ts":    ts,
		"cpu":   cpu,
		"cores": cores,
		"load1": loadAvg1(),
		"mem":   memInfo(),
		"temp":  tempC(),
		"net": map[string]interface{}{
			"rx_bps":   round1(rxBps),
			"tx_bps":   round1(txBps),
			"rx_total": ns.rx,
			"tx_total": ns.tx,
			"ifaces":   ifaces,
			"ts":       ts,
		},
		"sys": map[string]interface{}{
			"emmc":     diskInfo("/"),
			"hdd":      hdd,
			"hdd_path": hddPath,
			"pwm":      "",
			"uptime_h": uptimeHours(),
		},
		"ffmpeg":       []interface{}{},
		"ff_vcopy":     false,
		"sessions_raw": "",
	}
	// 服务探测结果无条件合并：失败时也会带上 err/ok=false，
	// 前端据此显示「接口异常 / 断」的明确状态，而不是空白卡片。
	for _, p := range s.probes {
		d, _ := p.Snapshot()
		now[p.Name()] = d
	}
	if _, ok := now["jf"]; !ok {
		now["jf"] = map[string]interface{}{"active": false}
	}
	if _, ok := now["immich"]; !ok {
		now["immich"] = map[string]interface{}{"ok": false}
	}

	point := map[string]interface{}{
		"t": ts, "cpu": cpu, "dec": 0, "enc": 0, "rga": 0, "npu": 0,
		"gpu": 0, "rx": round1(rxBps), "tx": round1(txBps), "jf_rate": 0,
	}
	s.mu.Lock()
	s.now = now
	s.hist = append(s.hist, point)
	if len(s.hist) > s.cfg.HistoryLen {
		s.hist = s.hist[len(s.hist)-s.cfg.HistoryLen:]
	}
	s.mu.Unlock()
}

func (s *State) Snapshot() map[string]interface{} {
	s.mu.RLock()
	hist := make([]map[string]interface{}, len(s.hist))
	copy(hist, s.hist)
	now := make(map[string]interface{}, len(s.now))
	for k, v := range s.now {
		now[k] = v
	}
	s.mu.RUnlock()
	return map[string]interface{}{
		"now":           now,
		"hist":          hist,
		"long":          []interface{}{},
		"long_interval": 0,
		"srv_ts":        nowUnix(),
		"ui_hidden":     append([]string{}, s.cfg.HiddenWidgets...),
	}
}

func readCPUTimes() ([]cpuSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []cpuSample{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) < 9 {
			continue
		}
		var total uint64
		for i := 1; i < 9; i++ {
			v, _ := strconv.ParseUint(fs[i], 10, 64)
			total += v
		}
		idle, _ := strconv.ParseUint(fs[4], 10, 64)
		idle2, _ := strconv.ParseUint(fs[5], 10, 64)
		out = append(out, cpuSample{idle: idle + idle2, total: total})
	}
	return out, sc.Err()
}

func cpuPct(cur, prev cpuSample) float64 {
	dt := cur.total - prev.total
	if dt == 0 {
		return 0
	}
	di := cur.idle - prev.idle
	return float64(dt-di) / float64(dt) * 100
}

func loadAvg1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fs := strings.Fields(string(b))
	if len(fs) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(fs[0], 64)
	return v
}

func uptimeHours() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fs := strings.Fields(string(b))
	if len(fs) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(fs[0], 64)
	return round1(v / 3600)
}

func memInfo() map[string]interface{} {
	total, avail := 0.0, 0.0
	f, err := os.Open("/proc/meminfo")
	if err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) < 2 {
				continue
			}
			v, _ := strconv.ParseFloat(fs[1], 64)
			switch fs[0] {
			case "MemTotal:":
				total = v
			case "MemAvailable:":
				avail = v
			}
		}
		f.Close()
	}
	if avail == 0 {
		avail = total
	}
	return map[string]interface{}{
		"total_mb": round1(total / 1024),
		"used_mb":  round1((total - avail) / 1024),
		"free_mb":  round1(avail / 1024),
	}
}

func tempC() float64 {
	max := 0.0
	paths, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			continue
		}
		if c := v / 1000; c > max {
			max = c
		}
	}
	return round1(max)
}

func diskInfo(path string) map[string]interface{} {
	zero := map[string]interface{}{
		"total_gb": float64(0), "free_gb": float64(0), "used_gb": float64(0),
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return zero
	}
	total := float64(st.Blocks) * float64(st.Bsize) / 1073741824.0
	free := float64(st.Bavail) * float64(st.Bsize) / 1073741824.0
	return map[string]interface{}{
		"total_gb": round2(total),
		"free_gb":  round2(free),
		"used_gb":  round2(total - free),
	}
}

func findDataDisk(c *Config) (string, map[string]interface{}) {
	mounts := map[string]bool{}
	if f, err := os.Open("/proc/mounts"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 {
				mounts[fs[1]] = true
			}
		}
		f.Close()
	}
	for _, p := range c.DiskPaths {
		if p == "/" {
			continue
		}
		if mounts[p] {
			return p, diskInfo(p)
		}
	}
	return "/", diskInfo("/")
}

func excluded(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// netSampleNow 只汇总带 /sys/class/net/<iface>/device 的物理网卡，
// 排除 lo / veth / docker / br-* 等虚接口，避免容器内部流量被重复计入。
func netSampleNow(c *Config) (netSample, []string) {
	ns := netSample{ts: time.Now()}
	ifaces := []string{}
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return ns, ifaces
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo <= 2 {
			continue
		}
		line := sc.Text()
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		if name == "" || name == "lo" || excluded(name, c.NetExclude) {
			continue
		}
		if _, err := os.Stat("/sys/class/net/" + name + "/device"); err != nil {
			continue
		}
		fs := strings.Fields(line[idx+1:])
		if len(fs) < 9 {
			continue
		}
		rx, e1 := strconv.ParseUint(fs[0], 10, 64)
		tx, e2 := strconv.ParseUint(fs[8], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		ns.rx += rx
		ns.tx += tx
		ifaces = append(ifaces, name)
	}
	return ns, ifaces
}
