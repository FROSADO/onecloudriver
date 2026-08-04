#!/usr/bin/env bash
# setup-fuse.sh — Verifies the environment is ready for FUSE integration tests
#
# Uso:
#   source scripts/setup-fuse.sh  (para exportar SKIP_INTEGRATION_TESTS)
#   bash scripts/setup-fuse.sh    (verification only)
#
# Salida:
#   - Success (exit 0): environment ready for integration tests
#   - Fallo (exit 1): faltan dependencias; exporta SKIP_INTEGRATION_TESTS=1

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

ERRORS=0

echo "🔍 Verifying environment for FUSE integration tests..."
echo ""

# 1. fusermount3 o fusermount
FUSERMOUNT=""
if command -v fusermount3 &>/dev/null; then
    FUSERMOUNT="fusermount3"
    echo -e "  ✅ fusermount3: $(which fusermount3)"
elif command -v fusermount &>/dev/null; then
    FUSERMOUNT="fusermount"
    echo -e "  ✅ fusermount: $(which fusermount)"
else
    echo -e "  ${RED}❌ fusermount3/fusermount no encontrado.${NC}"
    echo "     Instalar: sudo apt-get install fuse3  (o fuse)"
    ERRORS=$((ERRORS + 1))
fi

# 2. /dev/fuse
if [ -c /dev/fuse ]; then
    echo -e "  ✅ /dev/fuse: disponible"
elif [ -e /dev/fuse ]; then
    echo -e "  ${YELLOW}⚠️  /dev/fuse existe pero no es un dispositivo de caracteres${NC}"
    ERRORS=$((ERRORS + 1))
else
    echo -e "  ${RED}❌ /dev/fuse no encontrado.${NC}"
    echo "     Load module: sudo modprobe fuse"
    ERRORS=$((ERRORS + 1))
fi

# 3. Permisos de /dev/fuse
if [ -r /dev/fuse ] && [ -w /dev/fuse ]; then
    echo -e "  ✅ /dev/fuse: lectura/escritura OK"
else
    echo -e "  ${YELLOW}⚠️  /dev/fuse sin permisos de lectura/escritura.${NC}"
    echo "     Arreglar: sudo chmod 666 /dev/fuse  o  sudo usermod -a -G fuse \$USER"
    # Not critical if the user is already in the fuse group
    if groups "$USER" | grep -q '\bfuse\b'; then
        echo -e "  ${GREEN}   → Usuario en grupo 'fuse', OK.${NC}"
    else
        ERRORS=$((ERRORS + 1))
    fi
fi

# 4. user_allow_other en /etc/fuse.conf (solo necesario si se usa allow_other)
if [ -f /etc/fuse.conf ]; then
    if grep -q '^user_allow_other' /etc/fuse.conf 2>/dev/null; then
        echo -e "  ✅ /etc/fuse.conf: user_allow_other habilitado"
    else
        echo -e "  ${YELLOW}⚠️  user_allow_other no habilitado (no necesario para tests actuales)${NC}"
    fi
else
    echo -e "  ${YELLOW}⚠️  /etc/fuse.conf no encontrado${NC}"
fi

# 5. Espacio en /tmp
if df /tmp 2>/dev/null | awk 'NR==2 {exit ($4 < 102400)}'; then
    echo -e "  ✅ /tmp: espacio suficiente"
else
    available=$(df /tmp 2>/dev/null | awk 'NR==2 {print $4}')
    echo -e "  ${YELLOW}⚠️  /tmp: espacio bajo (${available:-?} KB disponibles)${NC}"
fi

echo ""

if [ $ERRORS -eq 0 ]; then
    echo -e "${GREEN}✅ Environment ready for FUSE integration tests.${NC}"
    export SKIP_INTEGRATION_TESTS=0
    exit 0
else
    echo -e "${RED}❌ Missing $ERRORS prerequisite(s). Integration tests will be skipped.${NC}"
    echo "   Resuelve los problemas listados arriba y vuelve a ejecutar este script."
    export SKIP_INTEGRATION_TESTS=1
    exit 1
fi
