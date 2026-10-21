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
# Nothing installs without matching tools/assistant_model_hashes.txt (§3 review rule). A byte count
# is not integrity: the compiled .wasm pulled here runs as browser code under this application's own
# origin, with the assistant's API access, so trusting transport security alone means trusting
# whatever huggingface.co and raw.githubusercontent.com serve on the day the script happens to run.
# Pin once from a network you trust (`--pin`), review what it recorded, commit it; every later run —
# including this one on an air-gapped host fed by hand — verifies against that review. `--verify`
# re-checks an existing install without downloading anything.
#
# The URLs mirror the records baked into the vendored web-llm bundle (v0.2.86): weights from
# huggingface.co/mlc-ai, the compiled library from mlc-ai/binary-mlc-llm-libs under the same
# modelVersion path. Re-run after bumping the bundle to refresh both together (§3 review rule).
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: tools/fetch_assistant_model.sh [--force] [--pin] [--verify]

Fetches the model artifacts named by MODEL_ID (default Hermes-3-Llama-3.1-8B-q4f32_1-MLC) into
web/assets/models/<MODEL_ID>/. Anything already present with the size it promises is kept,
so an interrupted run can simply be repeated. Every artifact is then checked against the
SHA-256 pinned in tools/assistant_model_hashes.txt; without that file nothing installs.

  -h, --help    show this help
  --force       download every artifact again, overwriting what is on disk (FORCE=1 does the same)
  --pin         fetch and record the hashes instead of checking them, writing
                tools/assistant_model_hashes.txt for review — run it once, from a trusted network,
                then commit what it wrote
  --verify      check what is on disk against the pinned hashes and download nothing

Environment:
  MODEL_ID              which model to install (see the case list in this script)
  CONTEXT_WINDOW_SIZE   tokens of context to configure; defaults to the window the model was
                        trained at (8192 for a Llama-3.1-based Hermes-3), not the largest it
                        declares — the KV cache is allocated for the whole window up front, and
                        vram_required_MB in app-config.json grows with it.
USAGE
}

FORCE="${FORCE:-0}"
PINNING="${PIN_MODEL_HASHES:-0}"
VERIFY_ONLY=0
for arg in "$@"; do
  case "${arg}" in
    -h | --help) usage; exit 0 ;;
    --force) FORCE=1 ;;
    --pin) PINNING=1 ;;
    --verify) VERIFY_ONLY=1 ;;
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
#
# A matching size only proves a file is the right length, and length is the one property an
# attacker who can answer for this URL controls for free (§3 review rule: a `.wasm` here is
# browser-executed code, so a substituted artifact runs with the assistant's own origin and its
# API access). Every artifact therefore also has to match the SHA-256 recorded in
# tools/assistant_model_hashes.txt — checked on download and again on every file this script keeps
# rather than re-fetching, so a tampered install directory is caught by a re-run and by --verify.
# The manifest is written by --pin (see usage) and reviewed like any other pinned dependency:
# transport security alone would leave this script trusting whatever huggingface.co serves on the
# day it happens to run.
fetch() {
  local outfile="$1" url="$2" expected="${3:-}" size=""
  # --verify: an existing install checked against the pinned hashes, no network at all. This is
  # what an operator runs on an air-gapped host after copying the tree in, and what a change
  # review runs before trusting a directory that may have been touched by anything.
  if [[ "${VERIFY_ONLY}" == "1" ]]; then
    verify_sha256 "${outfile}" || return 1
    echo "-- ${outfile} verified"
    return 0
  fi
  if [[ "${FORCE}" != "1" && -f "${outfile}" ]]; then
    size=$(( $(wc -c < "${outfile}") ))
    if { [[ -z "${expected}" ]] && (( size > 0 )); } || [[ "${size}" == "${expected}" ]]; then
      verify_sha256 "${outfile}" || return 1
      # Recorded here as well as after a download, so the manifest covers every artifact this
      # script touches — including the three JSONs, which no manifest lists and which an
      # enumerate-after-the-fact loop reliably forgot.
      if [[ "${PINNING}" == "1" ]]; then record_sha256 "${outfile}"; fi
      echo "-- ${outfile} present (${size} bytes, hash verified)"
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
  verify_sha256 "${outfile}.part" "$(basename "${outfile}")" || {
    rm -f "${outfile}.part"
    return 1
  }
  mv -f "${outfile}.part" "${outfile}"
  if [[ "${PINNING}" == "1" ]]; then record_sha256 "${outfile}"; fi
}

# The pinned hashes for this model, "<sha256>  <MODEL_ID>/<path>" — the same format as
# sha256sum's output, one line per artifact, sorted so a diff of two revisions shows what changed
# rather than reordering. Keyed under MODEL_ID because two models installed side by side hold
# different files under the same names.
HASH_FILE="${REPO_ROOT}/tools/assistant_model_hashes.txt"
HASH_TMP="$(mktemp)"

# verify_sha256 <file> — compare one file against its pinned hash. PIN_PINNING=1 (--pin) records
# instead of checking, and an absent manifest is fatal otherwise: a fetch that verifies nothing is
# exactly the hole this closes, so it must fail loudly rather than fall back to size checks.
verify_sha256() {
  local file="$1" key="${2:-}" want="" got
  [[ -f "${file}" ]] || die "no such file to hash: ${file}"
  # The manifest key is the artifact name; it is passed explicitly for a download still sitting in
  # its .part file, whose suffix is not part of any entry.
  [[ -n "${key}" ]] || key="$(basename "${file}")"
  if [[ "${PINNING}" == "1" ]]; then
    return 0
  fi
  if [[ ! -f "${HASH_FILE}" ]]; then
    die "no pinned hashes in ${HASH_FILE} — refusing an unverified install. Run once with --pin on a machine you trust, review the file (upstream release notes, a second network path), commit it, then re-run."
  fi
  want="$(php -r '
$h = [];
foreach (file($_SERVER["argv"][1], FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
    $m = preg_split("/\s+/", $line, 2);
    if (count($m) === 2) { $h[$m[1]] = $m[0]; }
}
// Keyed "<MODEL_ID>/<file>", so two models installed side by side cannot share an entry name.
echo $h[$_SERVER["argv"][2] . "/" . $_SERVER["argv"][3]] ?? "";
' "${HASH_FILE}" "${MODEL_ID}" "${key}")"
  if [[ -z "${want}" ]]; then
    die "${key} has no pinned hash in ${HASH_FILE} — refusing it. If this artifact is new (upstream added one, or MODEL_ID changed), run --pin and review what it recorded."
  fi
  got="$(php -r 'echo hash_file("sha256", $_SERVER["argv"][1]);' "${file}")"
  if [[ "${got}" != "${want}" ]]; then
    die "${key} does not match its pinned hash — want ${want}, got ${got}. Either the download is corrupt or it is not what was reviewed; nothing was installed from it."
  fi
}

# record_sha256 <file> — append this file's hash to the manifest (--pin only).
record_sha256() {
  local file="$1"
  printf '%s  %s/%s\n' \
    "$(php -r 'echo hash_file("sha256", $_SERVER["argv"][1]);' "${file}")" \
    "${MODEL_ID}" "$(basename "${file}")" >> "${HASH_TMP}"
}

die() { echo "ERROR: $*" >&2; exit 1; }

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
trap 'rm -f "${LIST}" "${HASH_TMP}"' EXIT
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
// Every name below becomes an output path in this directory, so each has to be a bare file name:
// a "../"-prefixed or absolute path from a hostile or merely broken manifest would write outside
// the model directory with the privileges of whoever ran this script. The charset is what real
// shard and tokenizer names use (model-00001.p-00000-slice_matmul.ts, tok.ggml.txt), so the guard
// rejects only what could escape. No apostrophe may appear anywhere in this program: it sits
// inside a single-quoted bash argument, where one silently ends that argument and PHP runs half a
// script — which fails quietly, so `bash -n` is part of the review for this file.
$assertName = static function (string $name): string {
    // Double-quoted on purpose: this whole program is a single-quoted bash argument, so a
    // single-quoted regex here would close it and hand PHP something else entirely.
    if (preg_match("/^[A-Za-z0-9][A-Za-z0-9._-]*$/", $name) !== 1) {
        fwrite(STDERR, sprintf("refusing unusable file name from the manifest: \"%s\"\n", $name));
        exit(1);
    }
    return $name;
};
$paths = [];
foreach ($records as $record) {
    if (empty($record["dataPath"])) { continue; }
    $path = $assertName((string) $record["dataPath"]);
    if (!array_key_exists($path, $paths)) {
        $paths[$path] = isset($record["nbytes"]) ? (string) (int) $record["nbytes"] : "";
    }
}
if ($paths === []) { fwrite(STDERR, "no manifest record carries a \"dataPath\"\n"); exit(1); }
foreach ($tokenizer as $file) {
    $file = $assertName((string) $file);
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

# --verify stops here: the install is on disk, every artifact matched its pinned hash, and a check
# run for reassurance should not also be a write.
if [[ "${VERIFY_ONLY}" == "1" ]]; then
  echo "== verified against ${HASH_FILE}"
  exit 0
fi

# --pin records what was just fetched as the hashes every later run will demand. This is
# trust-on-first-install, and deliberately so: nothing here can tell a genuine upstream artifact
# from one substituted before anyone looked, so the human review of this file is the control —
# read it against the model's release notes, or fetch once more over another path and compare.
if [[ "${PINNING}" == "1" ]]; then
  # fetch() recorded every artifact it accepted, so there is nothing to enumerate here — a list
  # built after the fact is how three JSON files ended up unpinned the first time.
  #
  # Merge with what is already committed instead of replacing it. Keys carry MODEL_ID so two models
  # installed side by side share one manifest, and rewriting the file from this run alone would drop
  # the other model without a word — after which --verify refuses an install that is byte for byte
  # what somebody reviewed. This run wins on a key it carries, because updating that artifact is why
  # anyone re-pins. Written to a temporary file and moved into place, so an interrupted pin leaves
  # the manifest someone committed intact rather than half-written.
  php -r '
  $read = static function (string $path): array {
      $out = [];
      if (!is_file($path)) { return $out; }
      foreach (file($path, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
          $m = preg_split("/\s+/", trim($line), 2);
          if (count($m) === 2) { $out[$m[1]] = $m[0]; }
      }
      return $out;
  };
  $fresh = $read($_SERVER["argv"][1]);
  $prior = $read($_SERVER["argv"][2]);
  // The union operator keeps the left value on a shared key, so this run wins where it has a row.
  $merged = $fresh + $prior;
  ksort($merged, SORT_STRING);
  $handle = fopen($_SERVER["argv"][3], "wb");
  if ($handle === false) { fwrite(STDERR, sprintf("cannot write %s\n", $_SERVER["argv"][3])); exit(1); }
  foreach ($merged as $key => $hash) { fwrite($handle, sprintf("%s  %s\n", $hash, $key)); }
  fclose($handle);
  printf("== pinned %d hashes into %s (%d from this run, %d carried over)\n",
      count($merged), $_SERVER["argv"][3], count($fresh), count($merged) - count($fresh));
  ' "${HASH_TMP}" "${HASH_FILE}" "${HASH_FILE}.tmp" || die "could not write ${HASH_FILE}"
  mv -f "${HASH_FILE}.tmp" "${HASH_FILE}"
  echo "==   review that file (git diff) and commit it — it is what every later install is checked against"
fi

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

# Subresource Integrity for the artifacts web-llm can check itself (its `integrity` record:
# `.config`, `.model_lib`, `.tokenizer` keyed by file name, each an SRI string). The checks above
# run when this script runs; these run in the browser on every load, so a model directory that is
# modified afterwards — by hand, by a bad rsync, or by anything with write access to the web root —
# fails with an IntegrityError instead of executing. Every key here is optional in the bundle, so
# this only ever adds a check.
INTEGRITY_JSON="$(WASM="${WASM}" php -r '
$sri = static fn (string $f): string => "sha256-" . base64_encode(hex2bin(hash_file("sha256", $f)));
$chat = json_decode(file_get_contents("mlc-chat-config.json"), true);
$out = [
    "config" => $sri("mlc-chat-config.json"),
    "model_lib" => $sri((string) getenv("WASM")),
];
$tokenizer = [];
foreach (is_array($chat) ? ($chat["tokenizer_files"] ?? []) : [] as $f) {
    $f = (string) $f;
    if (is_file($f)) { $tokenizer[$f] = $sri($f); }
}
if ($tokenizer !== []) { $out["tokenizer"] = $tokenizer; }
echo json_encode($out, JSON_UNESCAPED_SLASHES);
')"

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
      "overrides": { "context_window_size": ${CTX} },
      "integrity": ${INTEGRITY_JSON}
    }
  ]
}
JSON

echo "== done: $(du -sh "${DEST}" | cut -f1) installed; app-config.json written."
