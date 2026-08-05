#!/usr/bin/env bash
# setup-fuse.sh — Verifies the environment is ready for FUSE integration tests
#
# Usage:
#   source scripts/setup-fuse.sh  (to export SKIP_INTEGRATION_TESTS)
#   bash scripts/setup-fuse.sh    (verification only)
#
# Output:
#   - Success (exit 0): environment ready for integration tests
#   - Failure (exit 1): missing dependencies; exports SKIP_INTEGRATION_TESTS=1

set -euo pipefail

# Fallback for containers/CI where $USER is not set
USER="${USER:-$(id -un)}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

ERRORS=0

echo "🔍 Verifying environment for FUSE integration tests..."
echo ""

# 1. fusermount3 or fusermount
FUSERMOUNT=""
if command -v fusermount3 &>/dev/null; then
    FUSERMOUNT="fusermount3"
    echo -e "  ✅ fusermount3: $(which fusermount3)"
elif command -v fusermount &>/dev/null; then
    FUSERMOUNT="fusermount"
    echo -e "  ✅ fusermount: $(which fusermount)"
else
    echo -e "  ${RED}❌ fusermount3/fusermount not found.${NC}"
    echo "     Install: sudo apt-get install fuse3  (or fuse)"
    ERRORS=$((ERRORS + 1))
fi

# 2. /dev/fuse
if [ -c /dev/fuse ]; then
    echo -e "  ✅ /dev/fuse: available"
elif [ -e /dev/fuse ]; then
    echo -e "  ${YELLOW}⚠️  /dev/fuse exists but is not a character device${NC}"
    ERRORS=$((ERRORS + 1))
else
    echo -e "  ${RED}❌ /dev/fuse not found.${NC}"
    echo "     Load module: sudo modprobe fuse"
    ERRORS=$((ERRORS + 1))
fi

# 3. /dev/fuse permissions
if [ -r /dev/fuse ] && [ -w /dev/fuse ]; then
    echo -e "  ✅ /dev/fuse: read/write OK"
else
    echo -e "  ${YELLOW}⚠️  /dev/fuse without read/write permissions.${NC}"
    echo "     Fix: sudo chmod 666 /dev/fuse  or  sudo usermod -a -G fuse \$USER"
    # Not critical if the user is already in the fuse group
    if groups "$USER" | grep -q '\bfuse\b'; then
        echo -e "  ${GREEN}   → User in group 'fuse', OK.${NC}"
    else
        ERRORS=$((ERRORS + 1))
    fi
fi

# 4. user_allow_other in /etc/fuse.conf (only needed if allow_other is used)
if [ -f /etc/fuse.conf ]; then
    if grep -q '^user_allow_other' /etc/fuse.conf 2>/dev/null; then
        echo -e "  ✅ /etc/fuse.conf: user_allow_other enabled"
    else
        echo -e "  ${YELLOW}⚠️  user_allow_other not enabled (not needed for current tests)${NC}"
    fi
else
    echo -e "  ${YELLOW}⚠️  /etc/fuse.conf not found${NC}"
fi

# 5. Space in /tmp
if df /tmp 2>/dev/null | awk 'NR==2 {exit ($4 < 102400)}'; then
    echo -e "  ✅ /tmp: enough space"
else
    available=$(df /tmp 2>/dev/null | awk 'NR==2 {print $4}')
    echo -e "  ${YELLOW}⚠️  /tmp: low space (${available:-?} KB available)${NC}"
fi

echo ""

if [ $ERRORS -eq 0 ]; then
    echo -e "${GREEN}✅ Environment ready for FUSE integration tests.${NC}"
    export SKIP_INTEGRATION_TESTS=0
    exit 0
else
    echo -e "${RED}❌ Missing $ERRORS prerequisite(s). Integration tests will be skipped.${NC}"
    echo "   Fix the issues listed above and re-run this script."
    export SKIP_INTEGRATION_TESTS=1
    exit 1
fi
