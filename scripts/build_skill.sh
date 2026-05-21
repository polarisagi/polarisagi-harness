#!/bin/bash
set -e

# build_skill.sh
# 自动编译 skills/builtin/ 下的 Go 源码为 Wasm 二进制

PROJECT_ROOT=$(pwd)
SKILL_DIR="${PROJECT_ROOT}/skills/builtin"

# 检查环境变量
export GOOS=wasip1
export GOARCH=wasm

echo "Compiling WebAssembly skills (GOOS=wasip1 GOARCH=wasm)..."

count=0
for dir in "${SKILL_DIR}"/*; do
  if [ -d "$dir" ]; then
    if [ -f "$dir/impl.go" ]; then
      skill_name=$(basename "$dir")
      echo "  -> Building ${skill_name}..."
      
      cd "$dir"
      # 使用 Go 原生 wasip1 的 c-shared 模式编译以支持 //go:wasmexport
      go build -buildmode=c-shared -o impl.wasm impl.go
      
      if [ $? -eq 0 ]; then
        echo "     [OK] impl.wasm created."
        count=$((count+1))
      else
        echo "     [FAILED] Could not build ${skill_name}"
      fi
      cd "${PROJECT_ROOT}"
    fi
  fi
done

echo "Successfully built ${count} Wasm skill(s)."
