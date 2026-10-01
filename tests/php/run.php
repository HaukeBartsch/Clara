<?php
// Runs every tests/php/*_test.php and reports. Exit code 0 only when nothing
// failed — CI treats a red harness as a failed build (Design/Technology_Stack_Design.md
// §6, Plan/Web_Implementation.md §8).
//
//   php tests/php/run.php

declare(strict_types=1);

require __DIR__ . '/support.php';

$started = microtime(true);
$files = glob(__DIR__ . '/*_test.php') ?: [];
sort($files);

foreach ($files as $file) {
    require $file;
}

$suites = [];
foreach ($GLOBALS['cases'] as $case) {
    $suites[$case['suite']][] = $case;
}

$failed = 0;
foreach ($suites as $suite => $cases) {
    echo "\n{$suite}\n";
    foreach ($cases as $case) {
        if ($case['error'] === null) {
            echo "  ✓ {$case['name']}\n";
            continue;
        }
        $failed++;
        echo "  ✗ {$case['name']}\n      {$case['error']}\n";
    }
}

$total = count($GLOBALS['cases']);
printf(
    "\n%d tests, %d failed (%.1f ms)\n",
    $total,
    $failed,
    (microtime(true) - $started) * 1000
);

exit($failed === 0 ? 0 : 1);
