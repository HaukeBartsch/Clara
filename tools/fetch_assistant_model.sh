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

# Every weight shard the manifest names. The manifest is an object with a "records" list whose
# entries carry a relative "dataPath" and their size in "nbytes" — the raw-shard layout the
# vendored web-llm bundle reads (`new URL(record.dataPath, modelUrl)`), and the one
# huggingface.co/mlc-ai serves today. An earlier revision of this script read the older array
# shape (`[{…, "Data": [{"path": …}]}]`); against the current manifest that matched nothing, so
# the loop below ran zero times and a model with no weights at all installed "successfully". An
# unexpected shape is therefore an error now, never an empty shard list.
SHARDS="$(mktemp)"
trap 'rm -f "${SHARDS}"' EXIT
php -r '
$manifest = json_decode(file_get_contents("ndarray-cache.json"), true);
$records = is_array($manifest) ? ($manifest["records"] ?? null) : null;
if (!is_array($records) || $records === []) {
    fwrite(STDERR, "ndarray-cache.json holds no \"records\" list — unexpected manifest shape\n");
    exit(1);
}
$paths = [];
foreach ($records as $record) {
    if (!empty($record["dataPath"])) { $paths[] = (string) $record["dataPath"]; }
}
if ($paths === []) { fwrite(STDERR, "no manifest record carries a \"dataPath\"\n"); exit(1); }
echo implode("\n", array_unique($paths)), "\n";
' > "${SHARDS}"

echo "== ${MODEL_ID}: $(wc -l < "${SHARDS}") weight shards"
while read -r shard; do
  echo "-- ${shard}"
  curl -fL --proto '=https' -o "${shard}" "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/${shard}"
done < "${SHARDS}"

echo "== compiled WebGPU library"
curl -fL --proto '=https' -o "${WASM}" "${LIB_BASE}/${WEBLLM_VERSION}/${WASM}"

# Check the install before declaring it one: every shard the manifest names must exist with the
# size it promises. Written out here — rather than trusting the loop above to have run — because
# a fetch that pulled nothing used to reach the same "== done" line as a complete one, and the
# application cannot tell the two apart (it only sees files under this directory).
php -r '
$manifest = json_decode(file_get_contents("ndarray-cache.json"), true);
$missing = [];
$short = [];
$total = 0;
foreach ($manifest["records"] as $record) {
    $path = $record["dataPath"] ?? null;
    if ($path === null) { continue; }
    clearstatcache(true, $path);
    if (!is_file($path)) { $missing[] = $path; continue; }
    $size = filesize($path);
    $total += $size;
    if (isset($record["nbytes"]) && $size !== (int) $record["nbytes"]) {
        $short[] = sprintf("%s: %d of %d bytes", $path, $size, (int) $record["nbytes"]);
    }
}
if ($missing !== [] || $short !== []) {
    fwrite(STDERR, sprintf("incomplete install: %d shard(s) missing, %d with the wrong size\n", count($missing), count($short)));
    foreach (array_slice(array_merge($missing, $short), 0, 10) as $line) { fwrite(STDERR, "  " . $line . "\n"); }
    exit(1);
}
printf("== %d shards present, %.2f GB\n", count($manifest["records"]), $total / 1024 / 1024 / 1024);
'

# The engine's config override (design §7): one record whose URLs all live under this origin —
# `model` keeps the HF-style base and the bundle appends resolve/main/ itself. Both paths stay
# relative to this application's root so one install serves any host name; assistant.js turns
# them into absolute URLs before handing the record to CreateMLCEngine, which needs absolute
# ones (its cleanModelUrl() calls `new URL()` without a base).
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
