<?php

/**
 * The Control Panel's Monitoring section (§5.11, REQ-UI-064): live API performance and
 * rate-limiter state. The server renders the first paint from one read of
 * `GET /api/v1/system/metrics` (Design §4.25) and `GET /api/v1/system/blocked-ips` (§4.26);
 * `monitoring.js` then polls this section's data region once a second while the page is
 * visible, filling the stat cards, the eight d3 line charts of the configured period —
 * MONITORING_PERIOD_MINUTES, ten minutes by default (REQ-CFG-034) — as observed since the page
 * was opened, and the blocked-IP table below. The charts row's `data-clara-mon-period-minutes`
 * is how the script learns that period, and it drops every timepoint older than it.
 * A plain table-sm re-render is
 * enough here — the list is bounded by the number of addresses in a blockout (the Tabulator
 * rule in AGENTS.md is for tables that need it).
 */

use Clara\View;

/** Compact seconds → "2d 04:12" / "4:12:05" style uptime, mirroring monitoring.js. */
$fmtUptime = static function (float $seconds): string {
    $seconds = (int) floor($seconds);
    if ($seconds < 60) {
        return $seconds . 's';
    }
    $d = intdiv($seconds, 86400);
    $h = intdiv($seconds % 86400, 3600);
    $m = intdiv($seconds % 3600, 60);
    return $d > 0 ? $d . 'd ' . $h . ':' . str_pad((string) $m, 2, '0', STR_PAD_LEFT)
        : $h . ':' . str_pad((string) $m, 2, '0', STR_PAD_LEFT);
};

$stat = static function (string $key, string $labelKey, string $value): string {
    $palette = [
        'rps' => 'clara-mon-stat-card-a',
        'latency' => 'clara-mon-stat-card-b',
        'goroutines' => 'clara-mon-stat-card-c',
        'heap' => 'clara-mon-stat-card-d',
        'pool' => 'clara-mon-stat-card-a',
        'waits' => 'clara-mon-stat-card-b',
        'throughput' => 'clara-mon-stat-card-c',
        'blocked' => 'clara-mon-stat-card-d',
        'uptime' => 'clara-mon-stat-card-a',
    ];
    $cardClass = $palette[$key] ?? 'clara-mon-stat-card-a';

    return '<div class="card ' . View::e($cardClass) . '"><div class="card-body py-2">'
        . '<div class="small text-body-secondary">' . View::e($labelKey) . '</div>'
        . '<div class="fs-5" data-clara-mon-stat="' . View::e($key) . '">' . $value . '</div>'
        . '</div></div>';
};

$metricsOk = is_array($metrics);
$pool = $metricsOk ? ($metrics['db']['pool'] ?? null) : null;
$blocked = is_array($rateLimit) ? ($rateLimit['blocked'] ?? []) : [];

// First-paint values: everything one snapshot answers without a second sample.
// Requests/s and interval mean latency need two samples, hence the em dash.
$emDash = '<span class="text-body-secondary">—</span>';
$values = [
    'rps' => $emDash,
    'latency' => $emDash,
    'goroutines' => $metricsOk ? (string) (int) ($metrics['process']['goroutines'] ?? 0) : $emDash,
    'heap' => $metricsOk ? number_format(((int) ($metrics['process']['heap_alloc_bytes'] ?? 0)) / 1048576, 1) . ' MiB' : $emDash,
    'pool' => $pool !== null
        ? (string) (int) $pool['in_use'] . ' / ' . (string) (int) $pool['max_open']
        : $emDash,
    'waits' => $emDash,
    'throughput' => $emDash,
    'blocked' => is_array($rateLimit) ? (string) count($blocked) : $emDash,
    'uptime' => $metricsOk ? $fmtUptime((float) ($metrics['uptime_seconds'] ?? 0)) : $emDash,
];

$charts = ['rps', 'latency', 'goroutines', 'heap', 'pool', 'waits', 'gc', 'throughput'];
$failed = !$metricsOk || !is_array($rateLimit);
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.monitoring.title')) ?></h1>

<p class="text-body-secondary small"><?= View::e($view->t('admin.monitoring.intro', [
    'minutes' => (string) $periodMinutes,
])) ?></p>

<p class="text-body-secondary small" data-clara-mon-failed <?= $failed ? '' : 'hidden' ?>>
    <?= View::e($view->t('admin.monitoring.failed', [
        'error' => (string) ($metricsError ?? $rateLimitError ?? ''),
    ])) ?>
</p>

<div class="row row-cols-2 row-cols-md-3 row-cols-xl-5 g-2 mb-4">
    <?php foreach ($values as $key => $rendered): ?>
        <div class="col"><?= $stat($key, $view->t('admin.monitoring.stat.' . $key), $rendered) ?></div>
    <?php endforeach; ?>
</div>

<p class="text-body-secondary small" data-clara-mon-updated>
    <?= View::e($view->t('admin.monitoring.window')) ?>
</p>

<div class="row g-3 mb-4" data-clara-mon-period-minutes="<?= (int) $periodMinutes ?>">
    <?php foreach ($charts as $chart): ?>
        <div class="col-md-6 col-xl-3">
            <figure class="mb-0 clara-mon-chart" data-clara-mon-chart="<?= View::e($chart) ?>">
                <figcaption class="small text-body-secondary mt-1"><?= View::e($view->t('admin.monitoring.chart.' . $chart)) ?></figcaption>
            </figure>
        </div>
    <?php endforeach; ?>
</div>

<section aria-labelledby="clara-mon-blocked-heading">
    <h2 class="h6" id="clara-mon-blocked-heading"><?= View::e($view->t('admin.monitoring.blocked.title')) ?></h2>
    <p class="text-body-secondary small mb-2" data-clara-mon-rate-settings>
        <?php if (is_array($rateLimit)): ?>
            <?= View::e($view->t(
                ($rateLimit['enabled'] ?? false) ? 'admin.monitoring.blocked.on' : 'admin.monitoring.blocked.off',
                [
                    'rpm' => (string) (int) ($rateLimit['rpm'] ?? 0),
                    'minutes' => (string) (int) ($rateLimit['block_minutes'] ?? 0),
                ]
            )) ?>
        <?php endif; ?>
    </p>
    <div class="table-responsive">
        <table class="table table-sm align-middle clara-mon-blocked">
            <thead><tr>
                <th scope="col"><?= View::e($view->t('admin.monitoring.blocked.column.ip')) ?></th>
                <th scope="col" class="text-nowrap"><?= View::e($view->t('admin.monitoring.blocked.column.until')) ?></th>
                <th scope="col" class="text-nowrap"><?= View::e($view->t('admin.monitoring.blocked.column.remaining')) ?></th>
            </tr></thead>
            <tbody data-clara-mon-blocked-body>
            <?php if ($blocked === []): ?>
                <tr data-clara-mon-blocked-empty>
                    <td colspan="3" class="text-body-secondary"><?= View::e($view->t('admin.monitoring.blocked.none')) ?></td>
                </tr>
            <?php else: ?>
                <?php foreach ($blocked as $entry): ?>
                    <tr>
                        <td><?= View::e((string) ($entry['ip'] ?? '')) ?></td>
                        <td class="text-nowrap small"><?= View::e((string) ($entry['blocked_until'] ?? '')) ?></td>
                        <td class="text-nowrap small"><?= (int) ($entry['seconds_remaining'] ?? 0) ?></td>
                    </tr>
                <?php endforeach; ?>
            <?php endif; ?>
            </tbody>
        </table>
    </div>
</section>
