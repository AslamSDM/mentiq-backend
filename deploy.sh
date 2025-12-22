#!/bin/bash

# MentiQ Backend Deployment Script
# Usage: ./deploy.sh [server] [user]

set -e  # Exit on error

# Configuration (modify these or pass as arguments)
SERVER="${1:-65.109.6.92}"
SSH_USER="${2:-root}"
REMOTE_DIR="/mentiq"
LOCAL_DIR="$(pwd)"
SERVICE_NAME="mentiq"

echo "🚀 Starting deployment to $SSH_USER@$SERVER..."

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Step 1: Rsync files to server
echo -e "${BLUE}📦 Syncing files to server...${NC}"
rsync -avz --progress \
  --exclude='.git' \
  --exclude='*.log' \
  --exclude='.env' \
  --exclude='mentiq-backend' \
  --exclude='tmp' \
  "$LOCAL_DIR/" "$SSH_USER@$SERVER:$REMOTE_DIR/"

echo -e "${GREEN}✓ Files synced${NC}"

# Step 2: Build and restart on server
echo -e "${BLUE}🔨 Building and restarting service...${NC}"
ssh "$SSH_USER@$SERVER" << 'ENDSSH'
  cd /mentiq
  echo "Building Go application..."
  go build -o mentiq-backend
  echo "Restarting mentiq service..."
  systemctl restart mentiq
  systemctl status mentiq --no-pager
ENDSSH

echo -e "${GREEN}✓ Build complete${NC}"
echo -e "${GREEN}✓ Service restarted${NC}"

# Step 3: Check service status
echo -e "${BLUE}📊 Checking service health...${NC}"
sleep 2
ssh "$SSH_USER@$SERVER" "systemctl is-active mentiq && echo 'Service is running' || echo 'Service failed to start'"

echo -e "${GREEN}🎉 Deployment complete!${NC}"
