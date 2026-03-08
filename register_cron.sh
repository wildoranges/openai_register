#!/bin/bash

# OpenAI 账号定时注册脚本
# 每天北京时间 8:05 执行

# 获取脚本所在目录（不硬编码路径）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 设置环境变量
export PATH=$PATH:/usr/local/go/bin
export CI=true
export DEBIAN_FRONTEND=noninteractive

# 日志文件
LOG_DIR="$SCRIPT_DIR/logs"
mkdir -p $LOG_DIR
LOG_FILE="$LOG_DIR/register_$(date +%Y%m%d_%H%M%S).log"

echo "========================================" | tee -a $LOG_FILE
echo "开始定时注册任务: $(date '+%Y-%m-%d %H:%M:%S')" | tee -a $LOG_FILE
echo "========================================" | tee -a $LOG_FILE

# 使用专用配置文件运行注册
# config_cron.json 已配置 count: 50
# xvfb-run 为 Linux 服务器提供虚拟 X server
xvfb-run -a --server-args="-screen 0 1920x1080x24" timeout 3600 ./openai-register --config ./config_cron.json >> $LOG_FILE 2>&1

echo "========================================" | tee -a $LOG_FILE
echo "定时注册任务完成: $(date '+%Y-%m-%d %H:%M:%S')" | tee -a $LOG_FILE
echo "========================================" | tee -a $LOG_FILE

# 清理7天前的日志
find $LOG_DIR -name "*.log" -mtime +7 -delete
