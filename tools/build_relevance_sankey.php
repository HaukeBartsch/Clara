<?php

/**
 * Regenerates `web/app/data/relevance_sankey.php` — the code-to-relevance structure the API
 * documentation section draws as a Sankey diagram (REQ-UI-060, User_Interface_Design.md §5.9).
 *
 * Source: docs/Requirement_Relevance_Classification.md, the master of the mirrored table in
 * docs/index.md. Production deployment carries only bin/, web/ and .env (INSTALL.md §3), so the
 * documents themselves never reach the server — this generated file is what ships. CI runs
 * `--check` to keep the two in step: edit the classification document, re-run this tool, commit both.
 *
 *   php tools/build_relevance_sankey.php           # write the file
 *   php tools/build_relevance_sankey.php --check   # fail if it is stale (CI)
 *
 * The output is deterministic — no timestamps, source order preserved — so a re-run over an
 * unchanged document changes nothing.
 *
 * Values: one unit is one code-category pair, the row of the classification table a code appears
 * in. A code carrying two categories contributes one pair to each; every node's inflow equals its
 * outflow by construction, and the per-class totals are cross-checked against the document's own
 * "Totals across all documents" line before anything is written.
 */

declare(strict_types=1);

if (PHP_SAPI !== 'cli') {
    fwrite(STDERR, "run from the command line\n");
    exit(2);
}

$root = dirname(__DIR__);
$sourcePath = $root . '/docs/Requirement_Relevance_Classification.md';
$outputPath = $root . '/web/app/data/relevance_sankey.php';
$check = ($argv[1] ?? '') === '--check';

/** Functional area of a Requirements document: `API_Endpoints_Requirement.md` → `api-endpoints`. */
function areaOf(string $filename): ?string
{
    if (!preg_match('#^Requirements/([A-Za-z_]+?)(?:_Requirement|_Requirements)\.md$#', $filename, $m)) {
        return null;
    }

    return strtolower(str_replace('_', '-', rtrim($m[1], '_')));
}

/** Code family from the ID prefix — the second grouping level (REQ-UI-060). */
function kindOf(string $code): ?string
{
    return [
        'REQ' => 'req', 'ASM' => 'asm', 'DEV' => 'dev',
        'BR' => 'br', 'GD' => 'gd',
    ][explode('-', $code)[0]] ?? null;
}

/** The code family as it is written: `REQ-API-005` → `REQ-API`, charter `GD-1` → `GD`. */
function prefixOf(string $code): string
{
    $parts = explode('-', $code);

    return count($parts) >= 3 ? $parts[0] . '-' . $parts[1] : $parts[0];
}

$classes = ['security', 'performance', 'workflow', 'scalability', 'resilience'];
$kinds = ['req', 'asm', 'dev', 'br', 'gd'];

$lines = file($sourcePath);
if ($lines === false) {
    fwrite(STDERR, "cannot read {$sourcePath}\n");
    exit(2);
}

// pairs[area][kind][class] = pair count; codes[area][kind] = distinct code set; the document
// order of areas is first-seen order, which is the table's own grouping order.
$pairs = [];
$codes = [];
$prefixes = [];
$documentOrder = [];
$pairsByClass = array_fill_keys($classes, 0);

foreach ($lines as $number => $line) {
    if (!preg_match('#^\|\s*`(?P<file>[^`]+)`\s*\|\s*(?P<class>[a-z]+)\s*\|\s*(?P<codes>.*?)\s*\|\s*$#', $line, $row)) {
        continue;
    }

    $area = areaOf($row['file']);
    if ($area === null) {
        fwrite(STDERR, "line " . ($number + 1) . ": not a Requirements document row: {$row['file']}\n");
        exit(1);
    }
    if (!in_array($row['class'], $classes, true)) {
        fwrite(STDERR, "line " . ($number + 1) . ": unknown relevance class '{$row['class']}'\n");
        exit(1);
    }

    $documentOrder[$area] ??= count($documentOrder);
    foreach (preg_split('/,\s*/', $row['codes'], -1, PREG_SPLIT_NO_EMPTY) as $code) {
        $code = trim(str_replace('`', '', $code));
        $kind = kindOf($code);
        if ($kind === null) {
            fwrite(STDERR, "line " . ($number + 1) . ": unknown code family: {$code}\n");
            exit(1);
        }

        $pairs[$area][$kind][$row['class']] = ($pairs[$area][$kind][$row['class']] ?? 0) + 1;
        $codes[$area][$kind][$code] = true;
        $prefixes[$area][$kind][prefixOf($code)] = true;
        $pairsByClass[$row['class']]++;
    }
}

// The document's own totals line must agree with what the rows say — a stale total fails here
// rather than silently mis-drawing the diagram.
$totalsLine = implode('', array_filter($lines, static fn (string $l): bool => str_starts_with($l, 'Totals across all documents:')));
foreach ($classes as $class) {
    if (!preg_match('/\b' . $class . ' (\d+)/', $totalsLine, $m)) {
        fwrite(STDERR, "no '{$class}' total found in the document's totals line\n");
        exit(1);
    }
    if ((int) $m[1] !== $pairsByClass[$class]) {
        fwrite(STDERR, "total mismatch for {$class}: document says {$m[1]}, rows say {$pairsByClass[$class]} — fix the document first\n");
        exit(1);
    }
}

// Deterministic node order: areas in document order; groups directly below their area in kind
// order; classes in the fixed classification order. JS passes this order to d3-sankey as the
// node sort, so the diagram reads the same on every render.
$nodes = [];
$links = [];
foreach (array_keys($documentOrder) as $areaIndex => $area) {
    // A code belongs to exactly one family (its prefix), so the per-family sums are distinct codes.
    $groupNodes = [];
    $areaCodes = 0;
    foreach ($kinds as $kindIndex => $kind) {
        if (!isset($pairs[$area][$kind])) {
            continue;
        }

        $familyPrefixes = array_keys($prefixes[$area][$kind]);
        if (count($familyPrefixes) !== 1) {
            fwrite(STDERR, "area {$area}/{$kind} carries more than one code family prefix: " . implode(', ', $familyPrefixes) . "\n");
            exit(1);
        }

        $groupId = "g-{$area}-{$kind}";
        $groupNodes[] = [
            'id' => $groupId,
            'column' => 1,
            'order' => $areaIndex * 10 + $kindIndex,
            'prefix' => $familyPrefixes[0],
            'kind' => $kind,
            'codes' => count($codes[$area][$kind]),
        ];
        $links[] = ['source' => "a-{$area}", 'target' => $groupId, 'value' => array_sum($pairs[$area][$kind])];

        foreach ($classes as $class) {
            $value = $pairs[$area][$kind][$class] ?? 0;
            if ($value > 0) {
                $links[] = ['source' => $groupId, 'target' => "c-{$class}", 'value' => $value];
            }
        }

        $areaCodes += count($codes[$area][$kind]);
    }

    $nodes[] = ['id' => "a-{$area}", 'column' => 0, 'order' => $areaIndex, 'area' => $area, 'codes' => $areaCodes];
    foreach ($groupNodes as $node) {
        $nodes[] = $node;
    }
}

foreach ($classes as $classIndex => $class) {
    $nodes[] = [
        'id' => "c-{$class}",
        'column' => 2,
        'order' => $classIndex,
        'class' => $class,
        'codes' => $pairsByClass[$class],
    ];
}

$generated = "<?php\n"
    . "// GENERATED by tools/build_relevance_sankey.php from docs/Requirement_Relevance_Classification.md\n"
    . "// — do not edit. The API documentation section's relevance Sankey (REQ-UI-060,\n"
    . "// User_Interface_Design.md §5.9) reads this; production deployment carries no docs/ folder\n"
    . "// (INSTALL.md §3), so the generated file is what ships. Re-run the tool after editing the\n"
    . "// classification document; CI checks freshness with --check.\n"
    . "//\n"
    . "// One link unit is one code-category pair. Node columns: 0 functional area, 1 code family\n"
    . "// within the area (the two grouping levels of REQ-UI-060), 2 relevance class. Display text\n"
    . "// is translated at render time from the `area`/`class` keys; group nodes carry their code\n"
    . "// family prefix verbatim.\n\n"
    . "declare(strict_types=1);\n\n"
    . "return " . var_export(['nodes' => $nodes, 'links' => $links], true) . ";\n";

if ($check) {
    if (!is_file($outputPath) || file_get_contents($outputPath) !== $generated) {
        fwrite(STDERR, "web/app/data/relevance_sankey.php is stale — run: php tools/build_relevance_sankey.php\n");
        exit(1);
    }
    exit(0);
}

if (file_put_contents($outputPath, $generated) === false) {
    fwrite(STDERR, "cannot write {$outputPath}\n");
    exit(2);
}

printf("wrote %s: %d nodes, %d links, %d code-category pairs\n",
    basename($outputPath), count($nodes), count($links), array_sum($pairsByClass));
