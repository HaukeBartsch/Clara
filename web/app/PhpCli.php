<?php
// Which PHP binary to spawn for a child task — a detail that bites if guessed.
//
// PHP_BINARY names *the running binary*, and under PHP-FPM that is the FPM daemon, not
// a CLI interpreter: handing it `ldap_attempt.php` would not run the script. So the CLI
// is located by probing candidates and keeping the one that answers as `cli`, cached
// for the process. Nothing configures it — an override variable would have to be
// registered in System_Configuration_* first, and the probe covers the ordinary
// installations (same binary when PHP already runs as CLI, otherwise the packaged
// `php` next to it).

declare(strict_types=1);

namespace Clara;

final class PhpCli
{
    private static ?string $resolved = null;

    private function __construct() {}

    /**
     * An executable that runs PHP in the CLI SAPI.
     *
     * @throws ConfigError when none is found — an operator-facing message naming what
     *                     was tried, because it means the web server cannot start a
     *                     directory attempt at all (REQ-CFG-005).
     */
    public static function binary(): string
    {
        if (self::$resolved !== null) {
            return self::$resolved;
        }

        if (!function_exists('proc_open')) {
            throw new ConfigError(
                'Starting a directory attempt needs proc_open, which this PHP has disabled'
                . ' (disable_functions in php.ini).'
            );
        }

        $candidates = [];
        if (PHP_SAPI === 'cli') {
            $candidates[] = PHP_BINARY;
        }
        foreach (['/php', '/php8.4', '/php8.3', '/bin/php'] as $suffix) {
            $candidates[] = rtrim(PHP_BINDIR, '/') . $suffix;
        }
        $candidates[] = '/usr/bin/php';
        $candidates[] = '/usr/local/bin/php';

        $tried = [];
        foreach ($candidates as $candidate) {
            if (isset($tried[$candidate]) || !is_executable($candidate)) {
                $tried[$candidate] = true;
                continue;
            }

            if (self::probe($candidate) === 'cli') {
                $tried[$candidate] = true;

                return self::$resolved = $candidate;
            }

            $tried[$candidate] = true;
        }

        throw new ConfigError(
            'No PHP CLI interpreter could be found to run directory bind attempts — looked at: '
            . implode(', ', array_keys($tried)) . '.'
        );
    }

    /** What this binary says it is, or '' when it cannot be asked. */
    private static function probe(string $binary): string
    {
        $process = @proc_open(
            [$binary, '-r', 'echo PHP_SAPI;'],
            [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']],
            $pipes,
            null,
            null,
            ['suppress_errors' => true]
        );

        if (!is_resource($process)) {
            return '';
        }

        @fclose($pipes[0]);
        $sapi = trim((string) stream_get_contents($pipes[1]));
        @fclose($pipes[1]);
        @fclose($pipes[2]);
        @proc_close($process);

        return $sapi;
    }
}
