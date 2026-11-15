#!/usr/bin/env bash
# 卸载 rkmon：停止服务、移除单元与二进制；配置文件默认保留，避免误删凭据以外的自定义项
set -euo pipefail

PREFIX=/opt/rkmon
CONF=/etc/rkmon/config.json
PURGE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift 2;;
    --config) CONF="$2"; shift 2;;
    --purge) PURGE=1; shift;;
    -h|--help) echo "用法: sudo ./uninstall.sh [--prefix DIR] [--config FILE] [--purge]"; exit 0;;
    *) echo "未知参数: $1" >&2; exit 1;;
  esac
done

[ "$(id -u)" -eq 0 ] || { echo "需要 root 权限" >&2; exit 1; }

if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/rkmon.service ]; then
  systemctl disable --now rkmon.service 2>/dev/null || true
  rm -f /etc/systemd/system/rkmon.service
  systemctl daemon-reload || true
  echo "已停止并移除 rkmon.service"
else
  # 无 systemd：只结束本程序的进程，不误杀其它服务
  pkill -f "$PREFIX/rkmon --config" 2>/dev/null || true
  echo "已尝试结束 rkmon 进程"
fi

rm -f "$PREFIX/rkmon" "$PREFIX/start.sh"
rmdir "$PREFIX" 2>/dev/null || true

if [ "$PURGE" -eq 1 ]; then
  rm -f "$CONF"
  CONFDIR=$(dirname "$CONF")
  rmdir "$CONFDIR" 2>/dev/null || true
  echo "已删除配置: $CONF"
else
  echo "配置已保留: $CONF（加 --purge 可删除）"
fi
echo "卸载完成。运行用户未删除，如需清理请手动执行 userdel rkmon"
