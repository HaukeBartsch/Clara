#!/usr/bin/env bash
# Installs the Clara assistant's model artifacts (REQ-AI-003, ASM-AI-2): one tool-capable
# WebGPU model, compiled for @mlc-ai/web-llm, placed under web/assets/models/ so production
# hosts serve them from their own origin and never fetch anything at runtime. Run this once at
# installation time on a machine with internet access; the application itself stays offline.
#
#   tools/fetch_assistant_model.sh                  # default: Hermes-3-Llama-3.1-8B-q4f32_1-MLC (~6 GB)
#   tools/fetch_assistant_model.sh --force          # re-download everything, overwriting
#   MODEL_ID=Hermes-3-Llama-3.2-3B-q4f16_1-MLC tools/fetch_assistant_model.sh
#
# A file that is already there with the size the manifest promises is kept rather than fetched
# again, so re-running after an interrupted run finishes the install instead of starting it over;
# --force (or FORCE=1) ignores what is on disk and pulls every artifact again.
#
# The URLs mirror the records baked into the vendored web-llm bundle (v0.2.86): weights from
# huggingface.co/mlc-ai, the compiled library from mlc-ai/binary-mlc-llm-libs under the same
# modelVersion path. Re-run after bumping the bundle to refresh both together (§3 review rule).
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: tools/fetch_assistant_model.sh [--force]

Fetches the model artifacts named by MODEL_ID (default Hermes-3-Llama-3.1-8B-q4f32_1-MLC)
into web/assets/models/<MODEL_ID>/. Anything already present with the size it promises is kept,
so an interrupted run can simply be repeated.

  -h, --help   show this help
  --force      download every artifact again, overwriting what is on disk (FORCE=1 does the same)

Environment:
  MODEL_ID              which model to install (see the case list in this script)
  CONTEXT_WINDOW_SIZE   tokens of context to configure; defaults to the window the model was
                        trained at (8192 for a Llama-3.1-based Hermes-3), not the largest it
                        declares — the KV cache is allocated for the whole window up front, and
                        vram_required_MB in app-config.json grows with it.
USAGE
}

FORCE="${FORCE:-0}"
for arg in "$@"; do
  case "${arg}" in
    -h | --help) usage; exit 0 ;;
    --force) FORCE=1 ;;
    *) echo "unknown argument ${arg} (see --help)" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL_ID="${MODEL_ID:-Hermes-3-Llama-3.1-8B-q4f32_1-MLC}"
WEBLLM_VERSION="v0_2_86/base"   # matches web/assets/vendor/webllm — change together
DEST="${REPO_ROOT}/web/assets/models/${MODEL_ID}/resolve/main"
LIB_BASE="https://raw.githubusercontent.com/mlc-ai/binary-mlc-llm-libs/main/web-llm-models"

# WASM and LOW_RESOURCE come from the model's record in the vendored bundle (`prebuiltAppConfig`),
# the only source of truth for what this bundle can load. The list holds every model the bundle
# admits to function calling — `functionCallingModelIds` — and nothing else, because that is a hard
# gate in the engine: `ChatCompletionRequest.tools` on any other model id throws
# `UnsupportedModelIdError` before inference, and the assistant sends tools on every turn. A Qwen3
# install therefore loads its weights, shows "Model ready", and then fails each question; that is
# what this list exists to prevent rather than to accommodate.
case "${MODEL_ID}" in
  Hermes-3-Llama-3.1-8B-q4f32_1-MLC) WASM="Llama-3_1-8B-Instruct-q4f32_1_cs1k-webgpu.wasm"; LOW_RESOURCE=false ;;
  Hermes-3-Llama-3.1-8B-q4f16_1-MLC) WASM="Llama-3_1-8B-Instruct-q4f16_1_cs1k-webgpu.wasm"; LOW_RESOURCE=false ;;
  Hermes-3-Llama-3.2-3B-q4f32_1-MLC) WASM="Llama-3.2-3B-Instruct-q4f32_1_cs1k-webgpu.wasm"; LOW_RESOURCE=true ;;
  Hermes-3-Llama-3.2-3B-q4f16_1-MLC) WASM="Llama-3.2-3B-Instruct-q4f16_1_cs1k-webgpu.wasm"; LOW_RESOURCE=true ;;
  *)
    echo "unknown MODEL_ID ${MODEL_ID}: add its compiled-library file name to this case (it comes from the web-llm bundle's model record)" >&2
    echo "only these ids can be used at all: Hermes-3-Llama-3.1-8B-q4f3[2_1|16_1]-MLC, Hermes-3-Llama-3.2-3B-q4f[32_1|16_1]-MLC — the bundle refuses ChatCompletionRequest.tools for every other model (AI_Assistant_Design.md §7)" >&2
    exit 2 ;;
esac

# One download, skipped when the file is already there. `expected` is the byte count promised by
# the manifest where one is known, which is how a truncated shard from an interrupted run gets
# recognised and fetched again; a file with no promised size (the two JSONs, each tokenizer file,
# the compiled library) is kept as long as it is not empty. --force ignores all of that. Content
# always lands through a .part file, so an interrupted curl cannot leave a short file sitting
# under its real name — where the check above would have taken it for a complete one.
fetch() {
  local outfile="$1" url="$2" expected="${3:-}" size=""
  if [[ "${FORCE}" != "1" && -f "${outfile}" ]]; then
    size=$(( $(wc -c < "${outfile}") ))
    if { [[ -z "${expected}" ]] && (( size > 0 )); } || [[ "${size}" == "${expected}" ]]; then
      echo "-- ${outfile} present (${size} bytes)"
      return 0
    fi
    if [[ -n "${expected}" ]]; then
      echo "-- ${outfile}: ${size} of ${expected} bytes, fetching again"
    else
      echo "-- ${outfile}: empty, fetching again"
    fi
  fi
  curl -fL --proto '=https' -o "${outfile}.part" "${url}" || {
    rm -f "${outfile}.part"
    return 1
  }
  mv -f "${outfile}.part" "${outfile}"
}

mkdir -p "${DEST}"
cd "${DEST}"

if [[ "${FORCE}" == "1" ]]; then echo "== --force: fetching every artifact again"; fi
echo "== fetching ${MODEL_ID} into ${DEST}"
fetch mlc-chat-config.json "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/mlc-chat-config.json"
fetch ndarray-cache.json "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/ndarray-cache.json"

# The manifest the engine loads weights from. Nothing in a file this script parses names it — the
# bundle asks for it by its own constant: web-llm v0.2.86 requests
# `<model>/resolve/main/tensor-cache.json` on every load (`fetchTensorCache()`, before any shard),
# and a 404 there fails the whole load, which is how an install holding every shard left the
# Assistant unable to start. huggingface.co/mlc-ai publishes one for every MODEL_ID above, today
# byte-identical to that model's ndarray-cache.json; this bundle no longer requests
# ndarray-cache.json at all, but it stays fetched as the list this script enumerates below.
fetch tensor-cache.json "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/tensor-cache.json"

# Everything the engine asks for under this model's URL, one line per file: its name and the byte
# count promised for it (empty where nothing promises one), separated by a tab. The lines are every
# weight shard the manifest names, plus every tokenizer file the chat config names. The raw-shard
# layout is what the vendored web-llm bundle reads (`new URL(record.dataPath, modelUrl)`), and the
# one huggingface.co/mlc-ai serves today. An earlier revision of this script read the older array
# shape (`[{…, "Data": [{"path": …}]}]`); against the current manifest that matched nothing, so
# the loop below ran zero times and a model with no weights at all installed "successfully". An
# unexpected shape is therefore an error now, never an empty list. The tokenizer files come from
# the config's own "tokenizer_files" list rather than a hardcoded set, because web-llm requests
# exactly those — a missing one surfaces in the browser as a 404 on /resolve/main/<file> and an
# engine error the install had no way of reporting.
LIST="$(mktemp)"
trap 'rm -f "${LIST}"' EXIT
php -r '
$manifest = json_decode(file_get_contents("ndarray-cache.json"), true);
$records = is_array($manifest) ? ($manifest["records"] ?? null) : null;
if (!is_array($records) || $records === []) {
    fwrite(STDERR, "ndarray-cache.json holds no \"records\" list — unexpected manifest shape\n");
    exit(1);
}
$chat = json_decode(file_get_contents("mlc-chat-config.json"), true);
$tokenizer = is_array($chat) ? ($chat["tokenizer_files"] ?? null) : null;
if (!is_array($tokenizer) || $tokenizer === []) {
    fwrite(STDERR, "mlc-chat-config.json holds no \"tokenizer_files\" list — unexpected config shape\n");
    exit(1);
}
$paths = [];
foreach ($records as $record) {
    if (empty($record["dataPath"])) { continue; }
    $path = (string) $record["dataPath"];
    if (!array_key_exists($path, $paths)) {
        $paths[$path] = isset($record["nbytes"]) ? (string) (int) $record["nbytes"] : "";
    }
}
if ($paths === []) { fwrite(STDERR, "no manifest record carries a \"dataPath\"\n"); exit(1); }
foreach ($tokenizer as $file) {
    $file = (string) $file;
    if (!array_key_exists($file, $paths)) { $paths[$file] = ""; }
}
foreach ($paths as $path => $nbytes) { printf("%s\t%s\n", $path, $nbytes); }
' > "${LIST}"

echo "== ${MODEL_ID}: $(wc -l < "${LIST}") artifacts named by the manifest and the chat config"
while read -r file nbytes; do
  fetch "${file}" "https://huggingface.co/mlc-ai/${MODEL_ID}/resolve/main/${file}" "${nbytes}"
done < "${LIST}"

echo "== compiled WebGPU library"
fetch "${WASM}" "${LIB_BASE}/${WEBLLM_VERSION}/${WASM}"

# Check the install before declaring it one: every shard the manifest names must exist with the
# size it promises, and every tokenizer file the chat config names plus the engine's own
# tensor-cache.json plus the compiled library must exist at all. Written out here — rather than
# trusting the loop above to have run — because a fetch that pulled nothing used to reach the same
# "== done" line as a complete one, and the application cannot tell the two apart (it only sees
# files under this directory). The tokenizer files were not checked for at all until an install
# missing all four reported success; the first anyone heard of it was the browser, on a 404 for
# /resolve/main/tokenizer.json. tensor-cache.json joins the list for the same reason: no manifest
# here names it, so only this check notices when it is not there.
WASM="${WASM}" php -r '
$manifest = json_decode(file_get_contents("ndarray-cache.json"), true);
$chat = json_decode(file_get_contents("mlc-chat-config.json"), true);
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
$tokenizer = array_map("strval", $chat["tokenizer_files"] ?? []);
// tensor-cache.json is named by the engine, not by a manifest read above, so it is checked by
// name: it is the one artifact whose absence nothing else here could report.
foreach (array_merge($tokenizer, ["tensor-cache.json", (string) getenv("WASM")]) as $path) {
    clearstatcache(true, $path);
    if (!is_file($path) || filesize($path) === 0) { $missing[] = $path; }
}
if ($missing !== [] || $short !== []) {
    fwrite(STDERR, sprintf("incomplete install: %d file(s) missing or empty, %d with the wrong size\n", count($missing), count($short)));
    foreach (array_slice(array_merge($missing, $short), 0, 10) as $line) { fwrite(STDERR, "  " . $line . "\n"); }
    fwrite(STDERR, "run this script again to fetch what is missing (--force fetches everything again)\n");
    exit(1);
}
printf("== %d shards, %d tokenizer files, tensor-cache.json and the compiled library present, %.2f GB of weights\n", count($manifest["records"]), count($tokenizer), $total / 1024 / 1024 / 1024);
'

# The engine's config override (design §7): one record whose URLs all live under this origin —
# `model` keeps the HF-style base and the bundle appends resolve/main/ itself. Both paths stay
# relative to this application's root so one host name serves any install; assistant.js turns them
# into absolute URLs before handing the record to CreateMLCEngine, which needs absolute ones (its
# cleanModelUrl() calls `new URL()` without a base).
#
# CONTEXT WINDOW, and the memory it costs. Every tool round carries the whole conversation plus the
# full tool list: twelve tools with their schemas are ~4.6 kB of JSON, which the engine folds into
# the prompt on top of Clara's instructions, and a `tool` result adds whatever that call returned.
# When a round no longer fits, the engine does not truncate — it throws
# ContextWindowSizeExceededError, mid-conversation, after the work has already been done. So the
# window wants to be generous; but it cannot simply be maximal, because the cache is paid for up
# front: 2 × layers × kv_heads × head_dim bytes per token (K and V, float16) for the *whole* window,
# allocated before the first token. Hermes-3-Llama-3.1-8B declares 131072 tokens, which is 16 GiB of
# cache on top of its weights — a load that dies with `DeviceLostError` on any ordinary card.
#
# The default is therefore the window the model was trained at: rope_scaling's
# original_max_position_embeddings (8192 for every Llama-3.1-based id in the case list), falling
# back to the bundle's own 4096 where a model declares no rope scaling. Above that number the model
# runs on extrapolated positions, which is a quality gamble nobody asked for; below it, rounds stop
# fitting. CONTEXT_WINDOW_SIZE=<tokens> picks another value either way, clamped to the model's own
# maximum, and the line this script prints says what the choice costs.
SIZING="$(CTX_REQUESTED="${CONTEXT_WINDOW_SIZE:-}" php -r '
$chat = json_decode(file_get_contents("mlc-chat-config.json"), true);
$max = (int) ($chat["context_window_size"] ?? 0);
if ($max <= 0) { fwrite(STDERR, "mlc-chat-config.json declares no usable context_window_size\n"); exit(1); }

$model = is_array($chat["model_config"] ?? null) ? $chat["model_config"] : [];

// The window the model was trained at, before rope extrapolation to context_window_size. Llama-3.1
// keeps it inside model_config.rope_scaling; a config without rope scaling has no such number.
$rope = is_array($model["rope_scaling"] ?? null) ? $model["rope_scaling"] : [];
$trained = (int) ($rope["original_max_position_embeddings"] ?? 0);

$requested = (string) getenv("CTX_REQUESTED");
$ctx = $requested === "" ? ($trained > 0 ? min($max, $trained) : min($max, 4096)) : (int) $requested;
if ($ctx <= 0) { fwrite(STDERR, "CONTEXT_WINDOW_SIZE must be a positive number of tokens\n"); exit(1); }
if ($ctx > $max) {
    fwrite(STDERR, sprintf("-- CONTEXT_WINDOW_SIZE %d is above the maximum this model declares; using %d\n", $ctx, $max));
    $ctx = $max;
}

$layers = (int) ($model["num_hidden_layers"] ?? 0);
$heads = (int) ($model["num_key_value_heads"] ?? $model["num_attention_heads"] ?? 0);
$attentionHeads = (int) ($model["num_attention_heads"] ?? 0);
$headDim = (int) ($model["head_dim"] ?? 0);
if ($headDim <= 0 && $attentionHeads > 0) { $headDim = intdiv((int) ($model["hidden_size"] ?? 0), $attentionHeads); }
if ($layers <= 0 || $heads <= 0 || $headDim <= 0) {
    fwrite(STDERR, "cannot size the KV cache: the chat config names no layers, kv heads or head dim\n"); exit(1);
}

// K and V, float16 — what the WebGPU runtime keeps per token in every layer, for the whole window.
// A global cache, which is what every id in the case list has: sliding_window_size is -1, so
// nothing here would shrink with a sliding window.
$kvBytesPerToken = 2 * $layers * $heads * $headDim * 2;
$weights = 0;
foreach ((json_decode(file_get_contents("ndarray-cache.json"), true)["records"] ?? []) as $record) {
    $weights += (int) ($record["nbytes"] ?? 0);
}
$weightsMb = (int) ceil($weights / 1024 / 1024);
$kvMb = (int) ceil($ctx * $kvBytesPerToken / 1024 / 1024);
// "vram_required_MB" is written into app-config.json as the operator-facing figure for this install
// — weights plus cache plus 950 MB of logits and runtime buffers, which reproduces what the bundle
// records for Hermes-3-Llama-3.1-8B-q4f32_1 at its 4096 window (5770 against 5779). It is *not* a
// gate: web-llm v0.2.86 never reads the field, so asking for more than the device holds is not
// refused here — it dies mid-load as DeviceLostError, whose own message already advises "a smaller
// context length". This number is what tells an administrator which side of that they are on.
if ($trained > 0 && $ctx > $trained) {
    fwrite(STDERR, sprintf("-- context window %d is above the %d this model was trained at; positions beyond that are rope-extrapolated\n", $ctx, $trained));
}
printf("CTX=%d\nKV_MB=%d\nWEIGHTS_MB=%d\nVRAM=%d\n", $ctx, $kvMb, $weightsMb, $weightsMb + $kvMb + 950);
')" || { echo "could not size the context window from mlc-chat-config.json" >&2; exit 1; }
eval "${SIZING}"
echo "== context window ${CTX} tokens: KV cache ${KV_MB} MB on top of ${WEIGHTS_MB} MB of weights, asking the engine for ${VRAM} MB"

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
      "low_resource_required": ${LOW_RESOURCE},
      "overrides": { "context_window_size": ${CTX} }
    }
  ]
}
JSON

echo "== done: $(du -sh "${DEST}" | cut -f1) installed; app-config.json written."
