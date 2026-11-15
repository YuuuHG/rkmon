#!/usr/bin/env bash
# rkmon 一键安装（非 Docker / 非 root 运行 / 不改分区 / 不重启设备）
set -euo pipefail

PREFIX=/opt/rkmon
CONF=/etc/rkmon/config.json
SVC_USER=rkmon
PORT=8094
NO_SYSTEMD=0
BIN=""
ARCH_OVERRIDE=""
DOWNLOAD=0
REL_TAG="latest"

usage() {
  cat <<'EOF'
用法: sudo ./install.sh [选项]
  --prefix DIR     安装目录（默认 /opt/rkmon）
  --config FILE    配置文件（默认 /etc/rkmon/config.json，已存在则保留）
  --user NAME      运行用户（默认 rkmon，非 root）
  --port N         监听端口（默认 8094，仅绑 127.0.0.1）
  --binary FILE    指定本地二进制（默认按架构自动找 ./rkmon-<tag>）
  --arch TAG       覆盖架构标签: linux-amd64|linux-arm64|linux-armv7|linux-armv6
  --download       从 GitHub Release 自动下载对应架构（需外网；默认离线优先）
  --release TAG    下载时指定版本标签（默认 latest）
  --no-systemd     不安装 systemd 单元，仅生成 start.sh
  -h, --help       显示帮助
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift 2;;
    --config) CONF="$2"; shift 2;;
    --user) SVC_USER="$2"; shift 2;;
    --port) PORT="$2"; shift 2;;
    --binary) BIN="$2"; shift 2;;
    --arch) ARCH_OVERRIDE="$2"; shift 2;;
    --download) DOWNLOAD=1; shift;;
    --release) REL_TAG="$2"; shift 2;;
    --no-systemd) NO_SYSTEMD=1; shift;;
    -h|--help) usage; exit 0;;
    *) echo "未知参数: $1" >&2; usage; exit 1;;
  esac
done

[ "$(id -u)" -eq 0 ] || { echo "需要 root 权限（仅用于创建用户/写 systemd 单元）" >&2; exit 1; }

M=$(uname -m)
case "$M" in
  x86_64|amd64)   ATAG=linux-amd64;;
  aarch64|arm64)  ATAG=linux-arm64;;
  armv7*|armhf)   ATAG=linux-armv7;;
  armv6*)         ATAG=linux-armv6;;
  *) echo "不支持的架构: $M（本项目只发布 linux amd64/arm64/armv7/armv6）" >&2; exit 1;;
esac
[ -n "$ARCH_OVERRIDE" ] && ATAG="$ARCH_OVERRIDE"
echo "架构: $M -> $ATAG"

# 1. 定位二进制
TMP=""
if [ "$DOWNLOAD" -eq 1 ]; then
  TMP=$(mktemp -d)
  BASE="https://github.com/YuuuHG/rkmon/releases/download/${REL_TAG}"
  curl -fL --retry 3 -o "$TMP/rkmon"         "$BASE/rkmon-${ATAG}"
  curl -fL --retry 3 -o "$TMP/rkmon.sha256"  "$BASE/rkmon-${ATAG}.sha256"
  (cd "$TMP" && sed -i "s|  .*|  rkmon|" rkmon.sha256 && sha256sum -c rkmon.sha256)
  SRC="$TMP/rkmon"
elif [ -n "$BIN" ]; then
  SRC="$BIN"
elif [ -x "./rkmon-${ATAG}" ]; then
  SRC="./rkmon-${ATAG}"
elif [ -x "./rkmon" ]; then
  SRC="./rkmon"
else
  echo "找不到二进制。请把 rkmon-${ATAG} 放到当前目录，或用 --binary / --download" >&2
  exit 1
fi
[ -x "$SRC" ] || chmod +x "$SRC" 2>/dev/null || true
echo "二进制: $SRC ($(wc -c < "$SRC") B)"

# 2. 端口占用检查（不抢占已有服务）
if command -v ss >/dev/null 2>&1; then
  if ss -lnt 2>/dev/null | grep -qE ":${PORT}[[:space:]]"; then
    echo "端口 ${PORT} 已被占用，请用 --port 换一个" >&2; exit 1
  fi
fi

# 3. 创建非 root 运行用户
if ! id "$SVC_USER" >/dev/null 2>&1; then
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SVC_USER" 2>/dev/null || \
    useradd --system --no-create-home "$SVC_USER"
  else
    adduser -S -D -H "$SVC_USER"
  fi
fi

# 4. 安装二进制与配置目录（只对新建目录做 chown，不递归修改已有目录）
mkdir -p "$PREFIX"
install -m 755 "$SRC" "$PREFIX/rkmon"
CONFDIR=$(dirname "$CONF")
if [ ! -d "$CONFDIR" ]; then
  mkdir -p "$CONFDIR"
  chown "$SVC_USER:$SVC_USER" "$CONFDIR"
  chmod 750 "$CONFDIR"
fi

PASS=""
if [ -f "$CONF" ]; then
  echo "保留已有配置: $CONF"
  if grep -q 'rkmon --init 自动生成' "$CONF"; then
    echo "该配置含示例占位密码，拒绝启动。请删除后用 --init 重新生成" >&2
    exit 1
  fi
else
  OUT=$("$PREFIX/rkmon" --init --config "$CONF")
  chown "$SVC_USER:$SVC_USER" "$CONF"
  chmod 600 "$CONF"
  PASS=$(printf '%s\n' "$OUT" | grep '密码' | sed 's/.*：//' | tr -d '\r')
  printf '%s\n' "$OUT"
fi

# 5. 写监听地址（仅本机回环；需要局域网/公网时请自行加反向代理 + TLS）
if command -v python3 >/dev/null 2>&1 && grep -q '"listen"' "$CONF"; then
  python3 - "$CONF" "$PORT" <<'PY'
import json, sys
p, port = sys.argv[1], int(sys.argv[2])
d = json.load(open(p, encoding="utf-8"))
d["listen"] = "127.0.0.1:%d" % port
open(p, "w", encoding="utf-8").write(json.dumps(d, ensure_ascii=False, indent=2) + "\n")
PY
  chown "$SVC_USER:$SVC_USER" "$CONF"
  chmod 600 "$CONF"
fi

# 6. 服务单元
USE_SYSTEMD=0
if [ "$NO_SYSTEMD" -eq 0 ] && command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  USE_SYSTEMD=1
fi

if [ "$USE_SYSTEMD" -eq 1 ]; then
  UNIT=/etc/systemd/system/rkmon.service
  sed -e "s|ExecStart=.*|ExecStart=$PREFIX/rkmon --config $CONF|" \
      -e "s|^User=.*|User=$SVC_USER|" \
      -e "s|^Group=.*|Group=$SVC_USER|" \
      "$(dirname "$0")/rkmon.service" > "$UNIT"
  chmod 644 "$UNIT"
  systemctl daemon-reload
  systemctl enable --now rkmon.service
  echo "systemd: rkmon.service 已启用"
else
  cat > "$PREFIX/start.sh" <<EOF
#!/bin/sh
exec "$PREFIX/rkmon" --config "$CONF"
EOF
  chmod 755 "$PREFIX/start.sh"
  echo "未使用 systemd。请自行用 supervisor / openrc / nohup 托管: $PREFIX/start.sh"
  echo "（以 $SVC_USER 身份运行，不要 root）"
  su -s /bin/sh "$SVC_USER" -c "$PREFIX/start.sh" >/dev/null 2>&1 &
  echo "已尝试后台启动（无托管，重启后需手动拉起）"
fi

# 7. 验收
sleep 1
CODE=$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' --max-time 6 "http://127.0.0.1:${PORT}/" || true)
echo "未认证 GET / -> HTTP ${CODE}（应为 401）"
if [ -n "$PASS" ]; then
  CODE2=$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' --max-time 6 \
    --user "admin:${PASS}" "http://127.0.0.1:${PORT}/" || true)
  echo "带认证 GET / -> HTTP ${CODE2}（应为 200）"
fi
echo
echo "访问地址: http://127.0.0.1:${PORT}/  （仅本机；局域网请加反代 + TLS）"
echo "配置: $CONF (600)  卸载: sudo ./uninstall.sh"
[ -n "$TMP" ] && rm -rf "$TMP"
