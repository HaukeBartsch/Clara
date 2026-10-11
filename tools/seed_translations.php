<?php
// Bulk loader for the draft nb/nn translation catalogs (Plan/Web_Implementation.md M6,
// §5.7, REQ-API-100): reads tools/i18n/<language>.json and PUTs it to /api/v1/i18n/strings
// in batches — the same sanctioned mechanism the Control Panel editor uses, so the drafts
// land as i18n_strings rows (never as parallel PHP catalogs, per the en.php header rule).
// The files are machine-quality DRAFTS awaiting human review; that status lives in each
// file's "@draft" entry, which this loader skips like every other "@"-prefixed key.
//
//   php tools/seed_translations.php                 # dry run: validate and report only
//   php tools/seed_translations.php --apply         # write both languages
//   php tools/seed_translations.php --language=nb --apply
//
// Connection comes from the environment with dev-stack defaults: CLARA_API_URL,
// CLARA_SERVICE_TOKEN (X-Internal-Service-Token) and CLARA_ADMIN_USER_ID
// (X-Internal-User-Id — the endpoint is admin-only, REQ-API-099/100). Validation is
// total before anything is sent: every key must exist in web/app/i18n/en.php and every
// {placeholder} multiset must match its English value exactly; one offender refuses the run.
//
// Require-able for tests/php/i18n_seed_test.php: only a direct CLI invocation executes.

declare(strict_types=1);

if (PHP_SAPI !== 'cli') {
    exit;
}

/** The English source of truth every draft is validated against (§5.7). */
function seed_english_catalog(): array
{
    /** @var array<string,string> $catalog */
    $catalog = require __DIR__ . '/../web/app/i18n/en.php';

    return $catalog;
}

/**
 * Reads one draft JSON file into a key => text map. Keys starting with "@" are file
 * metadata (the "@draft" note) and skipped; every value must be a string.
 *
 * @return array<string,string>
 */
function seed_load_draft(string $path): array
{
    $raw = file_get_contents($path);
    if ($raw === false) {
        throw new RuntimeException('cannot read ' . $path);
    }
    $decoded = json_decode($raw, true, 64, JSON_THROW_ON_ERROR);
    if (!is_array($decoded)) {
        throw new RuntimeException($path . ': expected a JSON object');
    }

    $entries = [];
    foreach ($decoded as $key => $value) {
        if (!is_string($key) || !is_string($value)) {
            throw new RuntimeException($path . ': only string key/value pairs are allowed');
        }
        if (str_starts_with($key, '@')) {
            continue;
        }
        $entries[$key] = $value;
    }

    return $entries;
}

/**
 * The {placeholder} multiset of a string — occurrences counted, not just names, so a
 * doubled {count}{count} cannot pass against a single one.
 *
 * @return array<string,int>
 */
function seed_placeholders(string $text): array
{
    preg_match_all('/\{([a-zA-Z_][a-zA-Z0-9_]*)\}/', $text, $matches);
    $counts = [];
    foreach ($matches[1] as $name) {
        $counts[$name] = ($counts[$name] ?? 0) + 1;
    }

    return $counts;
}

/**
 * Validates a draft against the English catalog and returns one line per offender — an
 * empty array when the draft is loadable. Offenders: unknown keys, missing or extra
 * placeholders (names or multiplicities), and empty texts (empty means "remove" on the
 * API, REQ-API-100, which a translation draft never intends).
 *
 * @param array<string,string> $draft
 * @param array<string,string> $english
 * @return list<string>
 */
function seed_validate(array $draft, array $english): array
{
    $problems = [];
    foreach ($draft as $key => $text) {
        if (!array_key_exists($key, $english)) {
            $problems[] = $key . ': not a key of the English catalog';
            continue;
        }
        if ($text === '') {
            $problems[] = $key . ': empty text would remove the override on the API';
        }
        $want = seed_placeholders($english[$key]);
        $have = seed_placeholders($text);
        foreach (array_diff_assoc($want, $have) as $name => $count) {
            $problems[] = $key . ': placeholder {' . $name . '} needed ' . $count . '×, found ' . ($have[$name] ?? 0) . '×';
        }
        foreach (array_diff_assoc($have, $want) as $name => $count) {
            $problems[] = $key . ': unknown placeholder {' . $name . '} ×' . $count;
        }
    }

    return $problems;
}

/**
 * Chunks a draft into PUT bodies of at most $size entries, API wire order (§4.19).
 *
 * @param array<string,string> $entries
 * @return list<array{language:string,entries:list<array{key:string,text:string}>}>
 */
function seed_batches(string $language, array $entries, int $size = 200): array
{
    $batches = [];
    $current = [];
    foreach ($entries as $key => $text) {
        $current[] = ['key' => $key, 'text' => $text];
        if (count($current) === $size) {
            $batches[] = ['language' => $language, 'entries' => $current];
            $current = [];
        }
    }
    if ($current !== []) {
        $batches[] = ['language' => $language, 'entries' => $current];
    }

    return $batches;
}

/** Sends one batch; returns the decoded response. Non-2xx and transport failures throw. */
function seed_put_batch(string $apiUrl, string $token, string $userId, array $batch): array
{
    $handle = curl_init(rtrim($apiUrl, '/') . '/api/v1/i18n/strings');
    if ($handle === false) {
        throw new RuntimeException('cannot initialise the request to ' . $apiUrl);
    }
    curl_setopt_array($handle, [
        CURLOPT_CUSTOMREQUEST => 'PUT',
        CURLOPT_POSTFIELDS => json_encode($batch, JSON_THROW_ON_ERROR | JSON_UNESCAPED_UNICODE),
        CURLOPT_HTTPHEADER => [
            'Accept: application/json',
            'Content-Type: application/json',
            'X-Internal-Service-Token: ' . $token,
            'X-Internal-User-Id: ' . $userId,
        ],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 30,
    ]);
    $body = curl_exec($handle);
    $status = (int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE);
    $error = curl_error($handle);
    curl_close($handle);

    if ($body === false) {
        throw new RuntimeException('the API could not be reached: ' . $error);
    }
    if ($status < 200 || $status >= 300) {
        throw new RuntimeException('the API answered HTTP ' . $status . ': ' . trim((string) $body));
    }
    $decoded = json_decode((string) $body, true);

    return is_array($decoded) ? $decoded : [];
}

/** @param list<string> $argv */
function seed_main(array $argv): int
{
    $apply = in_array('--apply', $argv, true);
    $languages = null;
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--language=')) {
            $languages = explode(',', substr($argument, 11));
        }
    }
    $languages ??= ['nb', 'nn'];

    $apiUrl = getenv('CLARA_API_URL') !== false ? (string) getenv('CLARA_API_URL') : 'http://127.0.0.1:8085';
    $token = getenv('CLARA_SERVICE_TOKEN') !== false ? (string) getenv('CLARA_SERVICE_TOKEN') : 'clara-dev-481516-svc';
    $userId = getenv('CLARA_ADMIN_USER_ID') !== false ? (string) getenv('CLARA_ADMIN_USER_ID') : '1';

    $english = seed_english_catalog();
    $failed = false;
    foreach ($languages as $language) {
        $path = __DIR__ . '/i18n/' . $language . '.json';
        try {
            $draft = seed_load_draft($path);
        } catch (Throwable $error) {
            fwrite(STDERR, $language . ': ' . $error->getMessage() . "\n");
            $failed = true;
            continue;
        }

        $problems = seed_validate($draft, $english);
        if ($problems !== []) {
            foreach ($problems as $problem) {
                fwrite(STDERR, $language . ': ' . $problem . "\n");
            }
            fwrite(STDERR, $language . ': ' . count($problems) . " validation problem(s), nothing was sent\n");
            $failed = true;
            continue;
        }

        $batches = seed_batches($language, $draft);
        if (!$apply) {
            echo $language . ': ' . count($draft) . ' entries validate against the English catalog ('
                . count($batches) . " batch(es)) — dry run, use --apply to write\n";
            continue;
        }

        $changed = 0;
        try {
            foreach ($batches as $batch) {
                $response = seed_put_batch($apiUrl, $token, $userId, $batch);
                $changed += (int) ($response['changed'] ?? 0);
            }
        } catch (Throwable $error) {
            fwrite(STDERR, $language . ': ' . $error->getMessage() . "\n");
            $failed = true;
            continue;
        }
        echo $language . ': ' . count($draft) . ' entries sent, ' . $changed . " changed on the server\n";
    }

    return $failed ? 1 : 0;
}

if (realpath((string) ($argv[0] ?? '')) === __FILE__) {
    exit(seed_main($argv));
}
