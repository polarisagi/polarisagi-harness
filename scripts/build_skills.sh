#!/usr/bin/env bash

set -e

# ANSI Color Codes
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m'

echo "=> Checking for TinyGo..."
if ! command -v tinygo &> /dev/null; then
    echo -e "${YELLOW}Warning: 'tinygo' could not be found.${NC}"
    echo "Wasm skills compilation will be skipped."
    echo "To compile skills, please install TinyGo (https://tinygo.org/getting-started/)."
    exit 0
fi

SKILLS_DIR="skills/builtin"

if [ ! -d "$SKILLS_DIR" ]; then
    echo -e "${RED}Error: Directory $SKILLS_DIR does not exist.${NC}"
    exit 1
fi

echo "=> Compiling built-in skills to Wasm..."

SUCCESS_COUNT=0
FAIL_COUNT=0

# Find all directories containing impl.go
for impl_file in $(find "$SKILLS_DIR" -name "impl.go"); do
    skill_dir=$(dirname "$impl_file")
    skill_name=$(basename "$skill_dir")

    echo "  -> Compiling skill: $skill_name"

    # Run tinygo build in the skill directory
    cd "$skill_dir"

    # We use -target=wasi for wazero compatibility
    if tinygo build -o impl.wasm -target=wasi impl.go; then
        echo -e "     ${GREEN}✓ Success${NC}"
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        echo -e "     ${RED}��� Failed${NC}"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi

    # Return to project root
    cd - > /dev/null
done

echo ""
echo "=> Compilation Summary:"
echo -e "   ${GREEN}Successful: $SUCCESS_COUNT${NC}"
if [ $FAIL_COUNT -gt 0 ]; then
    echo -e "   ${RED}Failed: $FAIL_COUNT${NC}"
    exit 1
fi

exit 0
