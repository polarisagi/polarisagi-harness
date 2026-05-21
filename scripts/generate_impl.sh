#!/bin/bash
set -e
for dir in skills/builtin/*; do
  if [ -d "$dir" ] && [ ! -f "$dir/impl.go" ]; then
    skill_name=$(basename "$dir")
    echo "package main

import (
	\"encoding/json\"

	\"github.com/mrlaoliai/polaris-harness/pkg/cognition/skill/sdk\"
)

func init() {
	sdk.Register(func(input []byte) ([]byte, error) {
		// MVP implementation for ${skill_name}
		var in map[string]interface{}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}

		out := map[string]interface{}{
			\"status\": \"${skill_name} executed successfully\",
			\"received\": in,
		}

		return json.Marshal(out)
	})
}

func main() {}
" > "$dir/impl.go"
    echo "Generated $dir/impl.go"
  fi
done
