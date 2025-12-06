#!/bin/bash
# deploy.sh - Manual deployment script for a single service
# Usage: ./deploy.sh /path/to/project

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <project_path>"
    echo "Example: $0 /home/deploy/myapp"
    exit 1
fi

PROJECT_PATH="$1"

# Validate path
if [ ! -d "$PROJECT_PATH" ]; then
    log_error "Directory does not exist: $PROJECT_PATH"
    exit 1
fi

if [ ! -f "$PROJECT_PATH/docker-compose.yml" ] && [ ! -f "$PROJECT_PATH/docker-compose.yaml" ] && [ ! -f "$PROJECT_PATH/compose.yml" ] && [ ! -f "$PROJECT_PATH/compose.yaml" ]; then
    log_error "No docker-compose file found in: $PROJECT_PATH"
    exit 1
fi

cd "$PROJECT_PATH"

log_info "Starting deployment in: $PROJECT_PATH"
echo "=============================================="

# Git operations
log_info "Resetting git state..."
git reset --hard

log_info "Cleaning untracked files..."
git clean -fd

log_info "Pulling latest changes..."
git pull

echo "=============================================="

# Docker operations
log_info "Building and recreating containers..."
docker compose up -d --build --force-recreate

echo "=============================================="
log_success "Deployment completed!"

# Show running containers
log_info "Running containers:"
docker compose ps
