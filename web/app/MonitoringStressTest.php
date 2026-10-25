<?php

declare(strict_types=1);

namespace Clara;

use RuntimeException;

final class MonitoringStressTest
{
    private readonly string $directory;
    private readonly string $script;

    public function __construct(private readonly Config $config)
    {
        $this->directory = sys_get_temp_dir() . '/clara-monitoring-stress-'
            . substr(hash('sha256', $config->apiBaseUrl . "\0" . $config->internalServiceToken), 0, 24);
        $this->script = dirname(__DIR__, 2) . '/stress/stress_api.py';
    }

    public function start(int $userId): void
    {
        if ($userId <= 0) {
            throw new RuntimeException('The signed-in administrator could not be identified.');
        }
        if (!function_exists('proc_open')) {
            throw new RuntimeException('PHP proc_open is unavailable; the background stress test cannot start.');
        }
        if (!is_file($this->script)) {
            throw new RuntimeException('The stress test script is missing.');
        }

        $this->ensureDirectory();
        $lock = fopen($this->directory . '/lock', 'c');
        if (!is_resource($lock)) {
            throw new RuntimeException('The stress test lock could not be opened.');
        }

        try {
            if (!flock($lock, LOCK_EX | LOCK_NB)) {
                throw new RuntimeException('A stress test is already being started.');
            }
            $current = $this->status();
            if (($current['status'] ?? '') === 'running' && $this->isRecent($current['updated_at'] ?? '')) {
                throw new RuntimeException('A stress test is already running.');
            }

            $python = $this->python();
            $statusPath = $this->directory . '/status.json';
            $this->writeStatus([
                'status' => 'running',
                'phase' => 'startup',
                'progress' => 0,
                'detail' => 'Starting the background stress test',
                'updated_at' => gmdate(DATE_ATOM),
            ]);

            $args = [
                $python,
                'python3',
                $this->script,
                '--base-url', $this->config->apiBaseUrl,
                '--user-id', (string) $userId,
                '--projects', '5',
                '--project-prefix', 'StressTest',
                '--min-events', '5',
                '--max-events', '10',
                '--min-instruments', '1',
                '--max-instruments', '20',
                '--min-fields', '10',
                '--max-fields', '100',
                '--min-records', '250',
                '--max-records', '350',
                '--batch', '25',
                '--seed', '0',
                '--metrics-interval', '5',
                '--status-file', $statusPath,
                '--out', $this->directory . '/output',
            ];
            $command = 'nohup ' . implode(' ', array_map('escapeshellarg', $args))
                . ' >> ' . escapeshellarg($this->directory . '/worker.log')
                . ' 2>&1 < /dev/null & echo $!';
            $process = proc_open(
                ['/bin/sh', '-c', $command],
                [0 => ['file', '/dev/null', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']],
                $pipes,
                null,
                [
                    'PATH' => getenv('PATH') ?: '/usr/bin:/bin',
                    'SERVICE_TOKEN' => $this->config->internalServiceToken,
                ]
            );
            if (!is_resource($process)) {
                throw new RuntimeException('The background stress test process could not be launched.');
            }

            $pid = trim((string) stream_get_contents($pipes[1]));
            fclose($pipes[1]);
            fclose($pipes[2]);
            $exitCode = proc_close($process);
            if ($exitCode !== 0 || !ctype_digit($pid) || (int) $pid <= 0) {
                $this->writeStatus([
                    'status' => 'failed',
                    'phase' => 'failed',
                    'progress' => 0,
                    'detail' => 'The background stress test process could not be launched',
                    'updated_at' => gmdate(DATE_ATOM),
                ]);
                throw new RuntimeException('The background stress test process could not be launched.');
            }
        } finally {
            flock($lock, LOCK_UN);
            fclose($lock);
        }
    }

    /** @return array<string, mixed>|null */
    public function status(): ?array
    {
        $path = $this->directory . '/status.json';
        if (!is_file($path)) {
            return null;
        }
        $contents = file_get_contents($path);
        if (!is_string($contents)) {
            throw new RuntimeException('The stress test status could not be read.');
        }
        $status = json_decode($contents, true);
        if (!is_array($status)) {
            throw new RuntimeException('The stress test status is invalid.');
        }

        return $status;
    }

    private function python(): string
    {
        $process = proc_open(
            ['/usr/bin/env', 'python3', '--version'],
            [0 => ['file', '/dev/null', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']],
            $pipes
        );
        if (!is_resource($process)) {
            throw new RuntimeException('Python 3 is unavailable; the background stress test cannot start.');
        }
        $version = trim((string) stream_get_contents($pipes[1]));
        $error = trim((string) stream_get_contents($pipes[2]));
        fclose($pipes[1]);
        fclose($pipes[2]);
        if (proc_close($process) !== 0 || !str_starts_with($version . $error, 'Python 3')) {
            throw new RuntimeException('Python 3 is unavailable; the background stress test cannot start.');
        }

        return '/usr/bin/env';
    }

    private function ensureDirectory(): void
    {
        if (!is_dir($this->directory) && !mkdir($this->directory, 0700, true) && !is_dir($this->directory)) {
            throw new RuntimeException('The stress test work directory could not be created.');
        }
        if (is_link($this->directory) || !chmod($this->directory, 0700)) {
            throw new RuntimeException('The stress test work directory is not safe to use.');
        }
    }

    /** @param array<string, mixed> $status */
    private function writeStatus(array $status): void
    {
        $path = $this->directory . '/status.json';
        $temporary = $path . '.' . getmypid() . '.tmp';
        $encoded = json_encode($status, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
        if (file_put_contents($temporary, $encoded, LOCK_EX) === false || !rename($temporary, $path)) {
            throw new RuntimeException('The stress test status could not be saved.');
        }
    }

    private function isRecent(mixed $timestamp): bool
    {
        if (!is_string($timestamp)) {
            return false;
        }
        $updated = strtotime($timestamp);

        return $updated !== false && time() - $updated < 3600;
    }
}
