# rkmon

**单文件、非 Docker、零外部依赖的设备实时监控**。Go 静态二进制 + 内嵌 Vue3 前端，
默认只监听 `127.0.0.1`，首装强制随机强密码，7×24 常驻只占极小资源。

本仓库是 RK3568 媒体中心监控页的**通用化版本**：同一份 `web/index.html` 既跑在
DG-TN3568（Python 采集后端），也内嵌在 Go 二进制里供任意 Linux 设备使用。两边共用
`/api/data` 数据契约，因此组件、拖拽布局、主题、自定义组件能力完全一致。

---

## 1. 快速开始

### 方式 A：本地二进制（推荐，离线可用）

```bash
# 下载对应架构的 rkmon-<tag> 与 .sha256 到同一目录
sha256sum -c rkmon-linux-amd64.sha256
sudo bash install.sh --binary ./rkmon-linux-amd64
```

安装脚本会打印一次随机生成的密码，**只显示这一次**，请立即保存。安装完成即可访问：

```
http://127.0.0.1:8094/
用户名：admin
密码：<安装时打印>
```

### 方式 B：在线一键

```bash
sudo bash install.sh --download --release v0.1.0
```

脚本自动识别架构、下载对应产物并校验 SHA256 后再安装。

### 方式 C：无 systemd / 容器 / OpenWrt

```bash
sudo bash install.sh --no-systemd --prefix /opt/rkmon
# 之后用 supervisor / openrc / runit 托管 /opt/rkmon/start.sh
```

卸载：`sudo bash uninstall.sh`（默认保留配置，加 `--purge` 连配置一起删）。

> **只绑回环是刻意的**。要局域网/公网访问，请在前面加自己的反向代理并启用 TLS，
> 不要把 `listen` 直接改成 `0.0.0.0`。理由见第 6 节。

---

## 2. 架构支持

| 产物 | GOARCH | 典型设备 | 状态 |
|---|---|---|---|
| `rkmon-linux-amd64` | amd64 | x86_64 主机 / 软路由 / J4125 / N100 | CI 交叉编译 |
| `rkmon-linux-arm64` | arm64 | RK3568 / RK3588 / 树莓派 4/5 / 手机 | CI 交叉编译 |
| `rkmon-linux-armv7` | arm + GOARM=7 | 树莓派 2/3、32 位 ARM 开发板 | CI 交叉编译 |
| `rkmon-linux-armv6` | arm + GOARM=6 | 树莓派 1 / Zero / 老 ARM 小设备 | CI 交叉编译 |

全部由 GitHub Actions 的 Linux runner 用 `CGO_ENABLED=0` **纯交叉编译**产出静态二进制：

- ❌ 不使用 QEMU、不跑 `tonistiigi/binfmt`、不注册 `binfmt_misc`
- ❌ 不需要 `--privileged`、不挂 `/proc/sys`、不需要 Docker
- ❌ 不在你的 Windows 机器上编译，也不需要另找一台电脑
- ✅ glibc/musl 差异被静态链接绕开；armv6 是 Go 的一等目标（`GOARM=6`）

选择 Go 而非 Rust 的关键原因就是 **armv6 支持**：Rust 对 armv6 基本是 tier 3，
交叉编译链会显著变复杂；Go 只需一个 `GOARM` 环境变量。

**已实测编译**（2026-11-15，在 RK3568 上一个 `--network=none` 的临时容器里，
`GOPROXY=off`、`CGO_ENABLED=0`，编译完镜像与临时目录全部删除）：

```
go version go1.22.12 linux/arm64
go vet ./...        -> VET_OK
go test ./...       -> 通过（无测试文件）
四架构 go build     -> amd64 / arm64 / armv6 / armv7 全部 OK
产物大小            -> 5.3 – 5.6 MB（含 214 KB 内嵌前端）
```

四份二进制同时生成在 `dist/`（该目录被 `.gitignore` 忽略，不进仓库；
正式分发由 GitHub Actions 的 release job 完成）。注意这**不是**在 Windows 上编译的，
也没有在设备上注册任何二进制格式。

---

## 3. 前端能力

| 能力 | 说明 |
|---|---|
| 自由网格 | 12 列 CSS Grid，组件自带 `x/y/w/h`，可**跨行跨列**拖拽；右下角拉伸改尺寸；松手自动向上吸附 |
| 不按服务分区 | 页面没有强制分组，组件完全自由；组件库面板仍按语义分组方便查找 |
| 自定义组件 | 「组件 → + 新建组件」向导：白名单指标 → 图表类型 → 宽高；**不允许填 URL / HTML**（SSRF / XSS 防护） |
| 三态渲染 | 正常 / 无数据 / 数据源超时都有一等状态，服务挂了也只显示明确提示，绝不出现空白卡片 |
| 网络双轴 | 物理网卡总出入口，下行左轴、上行右轴独立量程；与实时性能曲线共用同一套平滑算法 |
| 曲线平滑 | A 原始折线 / B 移动平均 / **C 单调三次插值（默认）**；控制点单调约束抑制过冲假峰，只改显示不改原始采样 |
| 主题 | 蓝白浅色默认，一键切深色；`localStorage` 记住主题、组件、布局、自定义组件 |
| 响应式 | ≥1100px 12 列自由网格 / 760–1100px 6 列 / <760px 2 列自动顺序堆叠；触屏与鼠标共用 Pointer Events |
| 离线 | Vue 3.5.13 运行时**内联**进 `index.html`，零 CDN、零远程字体、零第三方图表库 |

所有图表为内联 SVG 手绘，页面不写入任何配置。

---

## 4. 数据契约

`GET /api/data` 返回（认证后）：

```json
{
  "now": {
    "ts": 1730000000,
    "cpu": 42.1, "cores": [40, 45, 39, 44], "load1": 1.2,
    "temp": 38.9, "mem": {"total_mb": 3896, "used_mb": 1620},
    "net": {"rx_bps": 2359.2, "tx_bps": 207377.6,
            "rx_total": 201879130, "tx_total": 17005104687,
            "ifaces": ["eth0"]},
    "sys": {"emmc": {"total_gb": 56.75, "used_gb": 24.16, "free_gb": 32.59},
            "hdd":  {"total_gb": 469.26, "used_gb": 336.91, "free_gb": 132.35},
            "hdd_path": "/media", "uptime_h": 21.1},
    "jf": {"active": false}, "immich": {"ok": false}
  },
  "hist": [{"t": 1730000000, "cpu": 42.1, "rx": 2359, "tx": 207377, "npu": 0}],
  "long": [], "long_interval": 0, "srv_ts": 1730000000,
  "ui_hidden": ["venc", "vdec", "npu", "rga", "gpu"]
}
```

- `hist` 是 1 秒采样的内存环形缓冲（默认 180 点），**不落盘、不写数据库**。
- `ui_hidden` 是可选字段：首次访问时按实际安装的服务自动隐藏不适用组件；
  旧后端没有该字段时行为不变。用户拖过布局后不再被覆盖。
- 字段缺失时前端统一走默认值 / 「无数据」状态，不会因为某服务没装而报错。

### 网络速率口径

汇总 `/proc/net/dev` 中**带 `/sys/class/net/<iface>/device` 的物理网卡**，
排除 `lo / veth* / docker0 / br-* / virbr* / wg* / tailscale* / cni* / flannel*` 等虚接口。
不排除会把容器内部流量重复计入，读数虚高数倍。只读差分，计数器回绕时输出 0 而不是假尖峰。

---

## 5. 可插拔服务探针（需求 6）

不同设备装的服务完全不同，所以采集层是 **声明式 manifest + 统一 `Probe` 接口**：

```go
type ServiceProbe struct { /* Name / Loop / Snapshot */ }
```

- 每个 probe 独立 goroutine、独立 `interval_sec` 与 `timeout_ms`，**失败不阻塞 1 秒主循环**
- 结果合并进 `now.<name>`；失败时写 `err` + `ok=false`，前端显示「接口异常」而不是空白
- 端点、超时、字段白名单**只能来自本地配置文件**，前端不能提交任意 URL
- 凭据放在 `token_file`（建议 0600），不写进配置、不进日志、不进仓库

内置两种类型：

| type | 用途 |
|---|---|
| `http_json` | 通用 GET + JSON，用 `field_map` 把源路径映射到 `now.<name>` 下的键 |
| `jellyfin` | 专用：解析 `/Sessions`，输出片名 / 码率 / 帧率 / 播放方式 / 转码进度 |

影音中心示例（`/etc/rkmon/config.json` 的 `probes` 片段）：

```json
"probes": [
  {
    "name": "jf", "type": "jellyfin",
    "url": "http://127.0.0.1:8096/Sessions",
    "token_file": "/etc/rkmon/jellyfin.token", "token_header": "X-Emby-Token",
    "interval_sec": 3, "timeout_ms": 2000,
    "widgets": ["jfplay"]
  },
  {
    "name": "immich", "type": "http_json",
    "url": "http://127.0.0.1:2283/api/server-info",
    "token_file": "/etc/rkmon/immich.token", "token_header": "x-api-key",
    "interval_sec": 15, "field_map": { "version": "version" }
  }
]
```

启用这些服务时，把对应组件从 `hidden_widgets` 里删掉即可。

**诚实的边界**：需要 Postgres 直连、进程 argv 解析、Docker socket 的 probe 目前
**未实现（仅设计）**。`Probe` 接口已预留，加一种 probe 不需要改前端或主循环。
Immich 的完整人脸/转码进度在 RK3568 专用 Python 后端里才有（那里用 SQL 直读 Postgres）。

---

## 6. 安全设计（需求 7）

先讲清楚一个事实：**同网段端口一定会被扫到**，做不到「扫不到」，只能做到「扫到也进不来」。
端口 knocking / SPA 能藏端口，但明显违背轻量化，本项目不采用。

| 措施 | 实现 |
|---|---|
| 默认只绑 `127.0.0.1` | 局域网需自己加反代 + TLS；不直接暴露管理面 |
| 首装强制随机强密码 | `--init` 生成 24 位随机密码，配置文件里只存 `salt` + 2 万轮 SHA-256 派生值，**禁止弱口令、不存明文** |
| 配置权限 | `config.json` 0600、目录 0750、进程以非 root `rkmon` 用户运行 |
| 认证失败限速 | 同一 IP 连续 5 次失败锁定 60s，之后指数退避；常量时间比较 |
| 成功凭据缓存 | 60s 缓存（按 Authorization 的 SHA-256 索引），避免每次请求重算慢哈希 |
| 只读 | 只实现 GET/HEAD，其它方法 405；页面不写任何配置 |
| 减少指纹 | 不输出版本号 / 路径指纹；401 响应与其它路径一致；`X-Content-Type-Options` / `X-Frame-Options` / CSP |
| SSRF / XSS | 自定义组件只能选白名单指标，不接受 URL 与 HTML；`default-src 'self'` |
| systemd 加固 | `NoNewPrivileges` / `ProtectSystem=strict` / `ProtectHome` / 清空 capabilities / `MemoryMax=64M` / `CPUQuota=20%` |
| 日志脱敏 | 不记录 Authorization、不打印密码（除 `--init` 那一次） |

**CSP 取舍**：页面内联的是 Vue 完整构建（带运行时模板编译器），需要
`script-src 'unsafe-inline' 'unsafe-eval'`。原策略已有 `unsafe-inline`，增加
`unsafe-eval` 的边际风险很小，且这是回环 + Basic Auth + 只读页面。若要保持严格 CSP，
需改用预编译 render 的 runtime-only Vue（**仅设计，未实现**）。

---

## 7. 轻量化

| 档位 | 目标设备 | RSS 目标 | 空闲 CPU 目标 | 采样 / 历史 | 二进制目标 |
|---|---|---|---|---|---|
| 标准 | amd64 / arm64 / armv7，≥512 MB | ≤20 MB | ≤1% | 1s / 180 点 | ≤15 MB |
| 精简 | armv6，64–128 MB | ≤12 MB | ≤0.5% | 2–5s / 历史减半 | ≤10 MB |

参照系（RK3568 上 **已实测** 的旧 Python 版）：`rkmon.py` RSS 22.4 MB / CPU 6.5%，
`monitor_auth_proxy.py` RSS 19.0 MB / CPU 0.7%，合计 41.4 MB；
单次 `/proc/net/dev` 读取 359 µs，1 秒采样约 0.036% CPU —— 采集本身几乎免费。

**Go 版已实测**（RK3568 / linux-arm64，实机运行 `rkmon-linux-arm64`，1 秒采样）：

| 指标 | 实测 | 原目标 | 结论 |
|---|---|---|---|
| 常驻 RSS | **6.0 MB** | ≤20 MB（精简档 ≤12 MB） | 大幅优于，精简档也达标 |
| 空闲 CPU | **0.6%** | ≤1%（精简档 ≤0.5%） | 标准档达标 |
| 单文件体积 | **5.3 – 5.6 MB** | ≤15 MB（精简档 ≤10 MB） | 达标 |
| 数据接口 | 401 / 200 / 405 / 404 全部正确 | — | 已验证 |

测量方式：启动后第 3 秒读 `/proc/<pid>/status` 的 `VmRSS` 与 `ps` 的 `%CPU`；
同时验证 `--init` 生成 24 位随机密码、配置 0600 且不含明文、日志无 panic。
不同设备请自行用 `ps -o rss,pcpu -C rkmon` 复核，我不把这台板子的数值外推成通用结论。

精简档落地方式：配置 `interval_ms: 2000~5000`、`history_len: 90`、`probes: []`，
并删掉 `rkmon.service` 里旧 systemd 不支持的 `MemoryMax` / `CPUQuota` 行。

---

## 8. 前端兼容性（已实测）

用 CDP 真实模拟三种视口，全部 `overflow:false`、18 个组件全部挂载：

| 视口 | 列数 | board / tile 宽度 | 结果 |
|---|---|---|---|
| 390px 手机 | 2 | 366 / 366 px | 全宽顺序堆叠，键值完整、按钮换行整齐 |
| 768px 平板 | 6 | 713 / 231 px | 自动流式排列 |
| 1280px 桌面 | 12 | 1225 / 196 px | 自由网格，拖拽与缩放可用 |

浏览器差异、旧内核 Safari、键盘可达性、布局跨设备同步属于**仅设计**，未做系统验证。
窄屏（<1100px）会自动禁用拖拽并改为顺序排列，避免触屏误操作。

---

## 9. 状态说明（已验证 / 已规划 / 仅设计）

| 档 | 内容 |
|---|---|
| ✅ **已验证** | RK3568 上 Python 采集后端 + 新版前端：真实生产路径（含 CSP 响应头）渲染、深浅主题、3/18 组件布局、跨行拖拽与持久化、自定义组件三态、网络双轴真实数据、390/768/1280 三视口 CDP 实测、401/200 认证；Go 版 `go vet` / `go test` / 四架构交叉编译 / arm64 实机运行 / 接口与资源实测；四份产物 SHA256 全部校验通过 |
| ⚠️ **已规划，未执行** | `install.sh` 在真实设备上完整安装（只做了 `bash -n` 语法检查与静态审读，没有为了测试去改你的系统服务）；GitHub Actions 首次运行与 release 分发；amd64 / armv6 / armv7 三份二进制的**实机运行**（只验证了能编译与 `file` 结构，未在这三种 CPU 上跑） |
| 💡 **仅设计** | Postgres / 进程 argv / Docker socket probe、布局跨设备同步、runtime-only Vue 严格 CSP、port knocking、TLS 内置、Prometheus 导出 |

---

## 10. 本地构建

```bash
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=dev" -o rkmon .
./rkmon --init --config ./config.json
./rkmon --config ./config.json
```

`web/index.html` 由 `//go:embed` 编进二进制，部署时**只需要一个文件**。
改前端后重新 `go build` 即可，无需 npm、无需 Vite、无需网络。

## 11. 目录结构

```
.
├── main.go / config.go / auth.go / probe.go / manifest.go / http.go
├── web/index.html            # 与 RK3568 生产页完全同一份（Vue3 运行时已内联）
├── config.example.json       # 最小系统监控配置
├── install.sh / uninstall.sh / rkmon.service
├── docs/DESIGN.md            # 架构取舍与「别人装的服务不一样」的完整讨论
└── .github/workflows/release.yml
```

## 12. License

MIT © 2026 YuuuHG
