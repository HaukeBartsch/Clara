<?php
// The children one LDAP launch spawned: their handles, their answers, and the rules
// about secrets that come with holding them
// (Authentication_Authorization_Design.md §2.2/§2.9).
//
// Three things this deliberately does not do:
//
//   * **No shell.** proc_open gets an argument array, so no command string is built and
//     nothing can be steered into syntax by a login name or a directory URL.
//   * **Nothing on argv.** The job — which carries the directory's bind password and
//     the user's password — travels on the child's stdin. A process's command line is
//     world-readable through /proc, so passing it as an argument would expose both to
//     every other process on the host for as long as the attempt runs (REQ-AUTH-036).
//   * **No stderr content in logs.** A child that dies reports that through its exit
//     code; whatever it printed on the way out is discarded rather than logged, because
//     this is the one place both passwords are simultaneously in scope.

declare(strict_types=1);

namespace Clara;

final class SubprocessLdapAttempts implements LdapAttemptSet
{
    /** @var list<array{id: string, process: mixed, stdout: mixed, buffer: string}> */
    private array $running = [];

    /** @var array<string, array{outcome: string, email?: string, display_name?: string}> */
    private array $settled = [];

    /** True once every child has been stopped and reaped. */
    private bool $closed = false;

    /**
     * Spawns every child before returning, so the attempts overlap with each other and
     * with whatever the caller does next — that gap is where Sequence F's local verify
     * runs (§2.9).
     *
     * @param list<array<string, scalar>> $jobs
     */
    public function __construct(
        array $jobs,
        private readonly int $timeoutSeconds,
        private readonly Logger $logger
    ) {
        $script = CLARA_WEB_ROOT . '/app/ldap_attempt.php';

        foreach ($jobs as $job) {
            $id = (string) ($job['id'] ?? 'ldap');
            $child = $this->spawn($script, $job);

            if ($child === null) {
                // Nothing is running for this source: settle it now rather than let a
                // failure to spawn look like a slow directory.
                $this->settled[$id] = ['outcome' => 'unreachable', 'reason' => 'spawn_failed'];
                continue;
            }

            $this->running[] = $child + ['buffer' => ''];
        }
    }

    public function settle(int $deadlineUnixMs): array
    {
        while ($this->running !== []) {
            $remainingUs = (int) round(((float) $deadlineUnixMs - microtime(true) * 1000) * 1000);
            if ($remainingUs <= 0) {
                break;
            }

            $read = array_column($this->running, 'stdout');
            $write = $except = null;
            $slice = min($remainingUs, 250_000);
            // Waking at most a quarter-second lets one child's answer be picked up
            // while another is still talking, and keeps the deadline honest.
            if (@stream_select($read, $write, $except, (int) ($slice / 1_000_000), $slice % 1_000_000) === false) {
                continue; // interrupted by a signal: nothing was ready this round
            }

            foreach (array_keys($this->running) as $index) {
                if (!isset($this->running[$index])) {
                    continue;
                }

                $chunk = (string) @fread($this->running[$index]['stdout'], 8192);
                if ($chunk !== '') {
                    $this->running[$index]['buffer'] .= $chunk;
                }

                // The answer is one JSON line: complete on its newline, or when the
                // child closes without having written one.
                if (!str_contains($this->running[$index]['buffer'], "\n")
                    && !feof($this->running[$index]['stdout'])) {
                    continue;
                }

                $this->collect($index);
            }
        }

        // Still in flight at the deadline: a directory that could not take part in this
        // login (§2.9), settled as unreachable rather than left unknown.
        foreach ($this->running as $child) {
            $this->settled[(string) $child['id']] ??= ['outcome' => 'unreachable', 'reason' => 'timeout'];
        }

        $this->cancel();

        return $this->settled;
    }

    public function cancel(): void
    {
        if ($this->closed) {
            return;
        }

        $this->closed = true;

        foreach ($this->running as $child) {
            @proc_terminate($child['process']);
            @fclose($child['stdout']);
            // Reaped here so a stopped attempt does not linger as a zombie until PHP
            // exits — which, under FPM, is "never".
            @proc_close($child['process']);
        }

        $this->running = [];
    }

    /**
     * Reads one finished child's answer and settles it. Anything unreadable is an
     * unreachable source, never an exception: one directory's problem must not take
     * the login down while other attempts were succeeding (§2.9).
     */
    private function collect(int $index): void
    {
        $child = $this->running[$index];
        unset($this->running[$index]);

        $line = trim(explode("\n", $child['buffer'])[0] ?? '');
        $decoded = $line === '' ? null : json_decode($line, true);

        if (!is_array($decoded) || !isset($decoded['outcome'])) {
            $status = proc_get_status($child['process']);
            $this->logger->warn('ldap attempt produced no answer', [
                'exit_code' => is_array($status) ? (int) ($status['exitcode'] ?? -1) : -1,
            ]);
            $decoded = ['outcome' => 'unreachable', 'reason' => 'no_answer'];
        }

        $outcome = ['outcome' => (string) $decoded['outcome']];
        foreach (['email', 'display_name'] as $field) {
            if (isset($decoded[$field]) && is_scalar($decoded[$field])) {
                $outcome[$field] = (string) $decoded[$field];
            }
        }

        $this->settled[(string) $child['id']] = $outcome;

        @fclose($child['stdout']);
    }

    /**
     * Starts one child, writes its job to stdin and closes that pipe so the child sees
     * end-of-input. Null means it could not be started at all.
     *
     * @param array<string, scalar> $job
     * @return array{id: string, process: mixed, stdout: mixed}|null
     */
    private function spawn(string $script, array $job): ?array
    {
        $descriptors = [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']];

        $process = @proc_open(
            [PhpCli::binary(), $script],
            $descriptors,
            $pipes,
            null,
            null,
            ['suppress_errors' => true]
        );

        if (!is_resource($process)) {
            return null;
        }

        $payload = json_encode($job, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        if (!is_string($payload) || (int) @fwrite($pipes[0], $payload) < 1) {
            @fclose($pipes[0]);
            @proc_terminate($process);
            @proc_close($process);

            return null;
        }

        // Closing stdin is what lets the child's read of it return.
        @fclose($pipes[0]);
        // stderr is deliberately read nowhere — see the file comment.
        @fclose($pipes[2]);
        stream_set_blocking($pipes[1], false);

        return ['id' => (string) ($job['id'] ?? 'ldap'), 'process' => $process, 'stdout' => $pipes[1]];
    }
}
