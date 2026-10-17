<?php
// The statistics the assistant's tools compute (AI_Assistant_Design.md §4, REQ-AI-010):
// marginal frequencies with a numeric summary and histogram bins, and Welch's t-test.
// Pure PHP over exported rows — no external stats runtime exists in this application
// (DEV-AI-3) — and every number is computed from the values the API handed out, missing
// values skipped exactly as the export left them.

declare(strict_types=1);

namespace Clara\Mcp;

final class Stats
{
    /** Histogram bins a marginal result may carry at most (§4: bounded for the model context). */
    public const MAX_BINS = 20;

    /**
     * Marginal distribution of one column (REQ-AI-010): value → count + share for every
     * distinct non-missing value (ties ordered by count then value, capped with an "other"
     * aggregation), plus n/mean/sd/min/max and equal-width histogram bins when the column is
     * numeric. The bins are what the d3 chart draws (§6 of the design); `is_numeric_column`
     * tells the renderer which one to prefer.
     *
     * @param list<string|null> $values raw exported values
     * @return array<string, mixed>
     */
    public static function marginal(array $values): array
    {
        $counts = [];
        $numeric = [];
        $n = 0;
        foreach ($values as $value) {
            if ($value === null || $value === '') {
                continue;
            }
            $key = (string) $value;
            $counts[$key] = ($counts[$key] ?? 0) + 1;
            $n++;
            // A column counts as numeric when every non-missing value reads as a number.
            if (is_numeric($value) && count($numeric) <= 2 * self::MAX_BINS * 1000) {
                $numeric[] = (float) $value;
            }
        }

        arsort($counts);
        $categories = [];
        $otherCount = 0;
        foreach ($counts as $value => $count) {
            if (count($categories) < self::MAX_BINS) {
                $categories[] = ['value' => $value, 'count' => $count,
                    'share' => $n > 0 ? round($count / $n, 4) : 0.0];
                continue;
            }
            $otherCount += $count;
        }

        $result = [
            'non_missing' => $n,
            'distinct' => count($counts),
            'categories' => $categories,
            'is_numeric_column' => false,
        ];
        if ($otherCount > 0) {
            $result['other_count'] = $otherCount;
        }

        // The numeric block needs *every* value to be numeric — one code label and the column
        // is categorical, however many numbers it holds.
        $isNumeric = $numeric !== [] && count($numeric) === $n;
        if ($isNumeric) {
            $mean = array_sum($numeric) / count($numeric);
            $variance = count($numeric) > 1
                ? array_sum(array_map(static fn (float $v): float => ($v - $mean) ** 2, $numeric)) / (count($numeric) - 1)
                : 0.0;
            $min = min($numeric);
            $max = max($numeric);
            $result['is_numeric_column'] = true;
            $result['mean'] = round($mean, 6);
            $result['sd'] = round(sqrt($variance), 6);
            $result['min'] = $min;
            $result['max'] = $max;
            $result['histogram'] = self::bins($numeric, $min, $max);
        }

        return $result;
    }

    /**
     * Welch's t-test (REQ-AI-010): two independent groups, unequal variances assumed.
     * Returns the group summaries, t, the Welch–Satterthwaite degrees of freedom and the
     * two-sided p-value from the incomplete beta function.
     *
     * @param list<string|null> $values  measurement column (missing skipped)
     * @param list<string|null> $groups  grouping column aligned with $values
     * @return array<string, mixed>|null null when the groups are not exactly two non-empty ones
     */
    public static function welch(array $values, array $groups): ?array
    {
        $byGroup = [];
        foreach ($values as $index => $value) {
            $group = (string) ($groups[$index] ?? '');
            if ($value === null || $value === '' || $group === '' || !is_numeric($value)) {
                continue;
            }
            $byGroup[$group][] = (float) $value;
        }
        if (count($byGroup) !== 2) {
            return null;
        }

        [$a, $b] = array_values($byGroup);
        // Deterministic order: the alphabetically first group is "group_a".
        if ((string) array_key_first($byGroup) > (string) array_key_last($byGroup)) {
            [$a, $b] = [$b, $a];
            $names = array_reverse(array_map('strval', array_keys($byGroup)));
        } else {
            $names = array_map('strval', array_keys($byGroup));
        }

        $stat = static function (array $xs): array {
            $n = count($xs);
            $mean = array_sum($xs) / $n;
            $variance = $n > 1
                ? array_sum(array_map(static fn (float $v): float => ($v - $mean) ** 2, $xs)) / ($n - 1)
                : 0.0;
            return ['n' => $n, 'mean' => $mean, 'variance' => $variance];
        };
        [$sa, $sb] = [$stat($a), $stat($b)];

        // Each group needs n ≥ 2 for a variance; with a zero-variance pair the t statistic is
        // undefined when the means coincide — report that difference plainly instead.
        if ($sa['n'] < 2 || $sb['n'] < 2) {
            return null;
        }

        $se2 = $sa['variance'] / $sa['n'] + $sb['variance'] / $sb['n'];
        $diff = $sa['mean'] - $sb['mean'];
        if ($se2 <= 0.0) {
            return null;
        }

        $t = $diff / sqrt($se2);
        $df = ($sa['variance'] / $sa['n'] + $sb['variance'] / $sb['n']) ** 2 / (
            ($sa['variance'] / $sa['n']) ** 2 / ($sa['n'] - 1)
            + ($sb['variance'] / $sb['n']) ** 2 / ($sb['n'] - 1)
        );
        $p = self::studentTRoundTwoSided(abs($t), $df);

        return [
            'group_a' => ['label' => $names[0], 'n' => $sa['n'], 'mean' => round($sa['mean'], 6), 'sd' => round(sqrt($sa['variance']), 6)],
            'group_b' => ['label' => $names[1], 'n' => $sb['n'], 'mean' => round($sb['mean'], 6), 'sd' => round(sqrt($sb['variance']), 6)],
            'mean_difference' => round($diff, 6),
            't' => round($t, 6),
            'df' => round($df, 3),
            'p_two_sided' => $p,
        ];
    }

    /**
     * Equal-width histogram over the observed range: bin edges rounded outwards so every
     * value falls in a bin; a degenerate range (min = max) yields one bin.
     *
     * @param list<float> $xs
     * @return list<array{from: float, to: float, count: int}>
     */
    private static function bins(array $xs, float $min, float $max): array
    {
        if ($min >= $max) {
            return [['from' => $min, 'to' => $max, 'count' => count($xs)]];
        }

        $binCount = min(self::MAX_BINS, max(1, (int) ceil(sqrt(count($xs)))));
        $width = ($max - $min) / $binCount;
        $counts = array_fill(0, $binCount, 0);
        foreach ($xs as $x) {
            $index = min($binCount - 1, (int) floor(($x - $min) / $width));
            $counts[$index]++;
        }

        $bins = [];
        for ($i = 0; $i < $binCount; $i++) {
            $bins[] = [
                'from' => round($min + $i * $width, 6),
                'to' => round($i === $binCount - 1 ? $max : $min + ($i + 1) * $width, 6),
                'count' => $counts[$i],
            ];
        }

        return $bins;
    }

    /**
     * Two-sided p of Student's t: I_x(df/2, 1/2) with x = df/(df + t²) — the regularized
     * incomplete beta of Numerical Recipes §6.4, evaluated by its continued fraction.
     */
    private static function studentTRoundTwoSided(float $t, float $df): float
    {
        return self::clip(self::betai($df / 2, 0.5, $df / ($df + $t * $t)));
    }

    /** Regularized incomplete beta I_x(a, b) (Numerical Recipes `betai`). */
    private static function betai(float $a, float $b, float $x): float
    {
        if ($x <= 0.0) {
            return 0.0;
        }
        if ($x >= 1.0) {
            return 1.0;
        }

        $logBt = self::logGamma($a + $b) - self::logGamma($a) - self::logGamma($b)
            + $a * log($x) + $b * log(1.0 - $x);
        $bt = exp($logBt);

        return $x < ($a + 1.0) / ($a + $b + 2.0)
            ? $bt * self::betacf($a, $b, $x) / $a
            : 1.0 - $bt * self::betacf($b, $a, 1.0 - $x) / $b;
    }

    /** The continued fraction of I_x(a, b) (Numerical Recipes `betacf`, modified Lentz). */
    private static function betacf(float $a, float $b, float $x): float
    {
        $eps = 3e-12;
        $fpmin = 1e-300;
        $qab = $a + $b;
        $qap = $a + 1.0;
        $qam = $a - 1.0;

        $c = 1.0;
        $d = 1.0 - $qab * $x / $qap;
        if (abs($d) < $fpmin) {
            $d = $fpmin;
        }
        $d = 1.0 / $d;
        $h = $d;
        $c = 1.0;
        $d = 1.0 - $qab * $x / $qap;
        if (abs($d) < $fpmin) {
            $d = $fpmin;
        }
        $d = 1.0 / $d;
        $h = $d;

        for ($m = 1; $m <= 300; $m++) {
            $m2 = 2 * $m;
            $aa = $m * ($b - $m) * $x / (($qam + $m2) * ($a + $m2));
            $d = 1.0 + $aa * $d;
            if (abs($d) < $fpmin) {
                $d = $fpmin;
            }
            $c = 1.0 + $aa / $c;
            if (abs($c) < $fpmin) {
                $c = $fpmin;
            }
            $d = 1.0 / $d;
            $h *= $d * $c;

            $aa = -($a + $m) * ($qab + $m) * $x / (($a + $m2) * ($qap + $m2));
            $d = 1.0 + $aa * $d;
            if (abs($d) < $fpmin) {
                $d = $fpmin;
            }
            $c = 1.0 + $aa / $c;
            if (abs($c) < $fpmin) {
                $c = $fpmin;
            }
            $d = 1.0 / $d;
            $delta = $d * $c;
            $h *= $delta;
            if (abs($delta - 1.0) < $eps) {
                break;
            }
        }

        return $h;
    }

    private static function clip(float $p): float
    {
        return max(0.0, min(1.0, $p));
    }

    /**
     * ln Γ(x) for x > 0 by the Lanczos approximation (g = 7, nine coefficients). The build's
     * libm binding has no `lgamma`, and this is the only place a log-gamma is needed — ~15
     * significant digits, far past the four the test statistic reports.
     */
    private static function logGamma(float $x): float
    {
        $coefficients = [
            0.99999995180260716302, 676.520368121885098569, -1259.13921672240287047,
            771.323428777653078848, -176.615029162140599065, 12.5073432786869048147,
            -0.138571095265720116082, 9.9843695780195708595e-6, 1.50563273514931155834e-7,
        ];

        if ($x < 0.5) {
            // Reflection: Γ(x)Γ(1−x) = π / sin(πx).
            return log(M_PI / sin(M_PI * $x)) - self::logGamma(1.0 - $x);
        }

        $x -= 1.0;
        $ag = $coefficients[0];
        for ($i = 1; $i < 9; $i++) {
            $ag += $coefficients[$i] / ($x + $i);
        }
        $t = $x + 7.5; // g + 1/2 with g = 7

        return 0.5 * log(2 * M_PI) + ($x + 0.5) * log($t) - $t + log($ag);
    }
}
