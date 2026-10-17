#!/usr/bin/env bash
# Installs the Clara assistant's model artifacts (REQ-AI-003, ASM-AI-2): one tool-capable
# WebGPU model, compiled for @mlc-ai/web-llm, placed under web/assets/models/ so production
# hosts serve them from their own origin and never fetch anything at runtime. Run this once at
# installation time on a machine with internet access; the application itself stays offline.
#
#   tools/fetch_assistant_model.sh                  # default: Qwen3-8B-q4f32_1-MLC (~5 GB)
#   MODEL_ID=Qwen3-1.7B-q4f32_1-MLC tools/fetch_assistant_model.sh
#
# The URLs mirror the records baked into the vendored web-llm bundle (v0.2.86): weights from
# huggingface.co/mlc-ai, the compiled library from mlc-ai/binary-mlc-llm-libs under the same
# modelVersion path. Re-run after bumping the bundle to refresh both together (§3 review rule).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL_ID="${MODEL_ID:-Qwen3-8B-q4f32_1-MLC}"
WEBLLM_VERSION="v0_2_86/base"   # matches web/assets/vendor/webllm — change together
DEST="${REPO_ROOT}/web/assets/models/${MODEL_ID}/resolve/main"
LIB_BASE="https://raw.githubusercontent.com/mlc-ai/binary-mlc-llm-libs/main/web-llm-models"

# The compiled-library file name is per model (the bundle's record names it); known models:
case "${MODEL_ID}" in
  Qwen3-8B-q4f32_1-MLC)   WASM="Qwen3-8B-q4f32_1_cs1k-webgpu.wasm"; VRAM=6852 ;;
  Qwen3-8B-q4f16_1-MLC)   WASM="Qwen3-8B-q4f16_1_cs1k-webgpu.wasm"; VRAM=5095 ;;
  Qwen3-4B-q4f32_1-MLC)   WASM="Qwen3-4B-q4f32_1-webgpu.wasm";     VRAM=3680 ;;
  Qwen3-1.7B-q4f32_1-MLC) WASM="Qwen3-1.7B-q4f32_1-webgpu.wasm";   VRAM=1958 ;;
  *) echo "unknown MODEL_ID ${MODEL_ID}: add its compiled-library file name to this case (it comes from the web-llm bundle's model record)" >&2; exit 2 ;;
esac

mkdir -p "${DEST}"
cd "${DEST}"

echo "== fetching ${MODEL_ID} into ${DEST}"
curl -fL --proto '=https' -o mlc-chat-config.json \
  "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/mlc-chat-config.json"
curl -fL --proto '=https' -o ndarray-cache.json \
  "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/ndarray-cache.json"

# Every weight shard the cache manifest names (safetensors shards, one per entry).
php -r '
$manifest = json_decode(file_get_contents("ndarray-cache.json"), true);
if (!is_array($manifest)) { fwrite(STDERR, "unreadable ndarray-cache.json\n"); exit(1); }
foreach ($manifest as $entry) {
    foreach ($entry["Data"] ?? [] as $object) {
        if (!empty($object["path"])) { echo $object["path"], "\n"; }
    }
}' | sort -u | while read -r shard; do
  echo "-- ${shard}"
  curl -fL --proto '=https' -o "${shard}" "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/${shard}"
done

echo "== compiled WebGPU library"
curl -fL --proto '=https' -o "${WASM}" "${LIB_BASE}/${WEBLLM_VERSION}/${WASM}"

# The engine's config override (design §7): one record whose URLs all live under this origin —
# `model` keeps the HF-style base and the bundle appends resolve/main/ itself.
cat > "${REPO_ROOT}/web/assets/models/app-config.json" <<JSON
{
  "model_id": "${MODEL_ID}",
  "frontend_version_check": false,
  "model_list": [
    {
      "model": "/assets/models/${MODEL_ID}",
      "model_id": "${MODEL_ID}",
      "model_lib": "/assets/models/${MODEL_ID}/resolve/main/${WASM}",
      "vram_required_MB": ${VRAM},
      "low_resource_required": false
    }
  ]
}
JSON

echo "== done: $(du -sh "${DEST}" | cut -f1) installed; app-config.json written."
