<?php

declare(strict_types=1);

/**
 * Decides whether a semgrep run passes, given that this project's accepted findings live in
 * .semgrep/baseline.txt rather than in `nosemgrep` comments next to the code (the policy in
 * docs/Static_Analysis_Triage.md: a suppression comment silences whatever occupies that line
 * later, and the useful property of the triage report is that it cannot drift into covering a
 * real bug).
 *
 * A finding passes when its rule id and file appear in the baseline; anything else fails the
 * build. Line numbers are not part of the identity — they move with every edit to the file,
 * while a triage decision is about the site — so the accepted set is per file, as coarse as the
 * document row it stands for and no coarser.
 *
 * Usage: semgrep --json … | php ci/semgrep_baseline.php <baseline> <repo-root>
 *
 * Exit 0 nothing new · exit 1 a finding outside the baseline, or a scan that did not really
 * happen (unparseable report, files semgrep could not read) — an unrun check is not a passed one.
 */

$baselinePath = $argv[1] ?? '';
$repoRoot = rtrim((string) ($argv[2] ?? ''), '/');
if ($baselinePath === '' || $repoRoot === '' || !is_file($baselinePath)) {
    fwrite(STDERR, "usage: semgrep --json … | php ci/semgrep_baseline.php <baseline-file> <repo-root>\n");
    exit(2);
}

$accepted = [];
foreach (file($baselinePath, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
    if (str_starts_with(trim($line), '#')) {
        continue;
    }
    $pair = preg_split("/\t+/", trim($line), 2);
    if (!is_array($pair) || count($pair) !== 2) {
        fwrite(STDERR, "malformed baseline line (want <rule id><TAB><path>): {$line}\n");
        exit(2);
    }
    $accepted[$pair[0] . "\t" . $pair[1]] = true;
}

$report = stream_get_contents(STDIN);
$json = json_decode((string) $report, true);
if (!is_array($json) || !array_key_exists('results', $json)) {
    fwrite(STDERR, "could not read semgrep's JSON report — the gate did not run, so it cannot pass."
        . " (semgrep prints its own errors on stderr; --metrics=off keeps it from posting them.)\n");
    exit(1);
}

// A file semgrep failed to analyze is a hole in the scan, and a green run over a partial scan is
// exactly the failure this script exists to prevent. Timeouts and parse failures land here.
$scanErrors = $json['errors'] ?? [];
if ($scanErrors !== []) {
    fwrite(STDERR, 'semgrep reported ' . count($scanErrors) . " error(s) while scanning; those files were not analyzed:\n");
    foreach (array_slice($scanErrors, 0, 10) as $err) {
        fwrite(STDERR, sprintf("  %s: %s\n", (string) ($err['type'] ?? 'error'), substr((string) ($err['message'] ?? ''), 0, 200)));
    }
    exit(1);
}

$new = [];
$acceptedCount = 0;
$seen = [];
foreach ($json['results'] as $finding) {
    $rule = preg_replace('/^rules\./', '', (string) ($finding['check_id'] ?? ''));
    $path = str_replace([$repoRoot . '/', $repoRoot], ['', ''], (string) ($finding['path'] ?? ''));
    $key = $rule . "\t" . ltrim($path, '/');
    $seen[$key] = true;
    if (isset($accepted[$key])) {
        $acceptedCount++;
        continue;
    }
    $new[] = [$rule, $key, (int) ($finding['start']['line'] ?? 0), trim((string) ($finding['extra']['message'] ?? ''))];
}

if ($new !== []) {
    fwrite(STDERR, count($new) . " finding(s) not recorded in " . basename($baselinePath) . ":\n");
    foreach ($new as [$rule, $key, $line, $message]) {
        $file = substr($key, (int) strpos($key, "\t") + 1);
        fwrite(STDERR, sprintf("  %s:%d  %s\n      %s\n", $file, $line, $rule, mb_strimwidth($message, 0, 160, '…')));
    }
    fwrite(STDERR, "Triage each one into docs/Static_Analysis_Triage.md — fix it, or accept it with a reason and\n"
        . "a 'Recheck when' condition there and add its rule and file to .semgrep/baseline.txt.\n"
        . "Do not add a nosemgrep marker: the decision has to stay readable next to the others.\n");
    exit(1);
}

printf("semgrep: %d finding(s), all recorded in %s\n", $acceptedCount, basename($baselinePath));
$stale = array_values(array_filter(array_keys($accepted), static fn (string $k): bool => !isset($seen[$k])));
if ($stale !== []) {
    // Not a failure: the site may simply not match today's ruleset. It is worth printing, because
    // a baseline nobody prunes stops meaning what it claims to mean.
    printf("  %d baseline entr(y|ies) matched nothing this run (prune if the site is gone):\n", count($stale));
    foreach ($stale as $key) {
        printf("    %s\n", str_replace("\t", '  ', $key));
    }
}
