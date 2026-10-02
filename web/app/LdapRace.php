<?php
// The LDAP half of the named-source credential race (Authentication_Authorization_Design.md
// §2.2 and §2.9): every directory registered under the name the user selected is
// attempted concurrently, each with its own timeout so one unreachable server cannot
// stall the login, and the first attempt to report "login ok" supplies the identity.
//
// The two methods exist because of when they have to run. `start()` spawns the children
// and returns immediately; the caller then performs Sequence F's local verify in this
// process, so the local check overlaps with the directory binds instead of queueing
// behind them (DEV-AUTH-14). `finish()` collects what settled and picks the winner.
//
// Nothing here logs a password or a DN: the child receives both on stdin and reports
// only an outcome plus, for a winner, the entry's address (§2.2 step 3 — the directory
// is the authority on identity, so the email used is the one it stored, not the one
// that was typed).

declare(strict_types=1);

namespace Clara;

final class LdapRace
{
    private readonly LdapLauncher $launcher;

    /** Wall-clock ceiling for one directory, from the moment it is spawned (§2.9). */
    private const ATTEMPT_TIMEOUT_SECONDS = 5;

    public function __construct(
        private readonly Config $config,
        private readonly Logger $logger,
        ?LdapLauncher $launcher = null
    ) {
        $this->launcher = $launcher ?? new SubprocessLdapLauncher($logger, self::ATTEMPT_TIMEOUT_SECONDS);
    }

    /**
     * Starts one attempt per server under the selected name.
     *
     * @param list<array<string, string|int>> $servers the servers Config parsed
     * @return array{set: LdapAttemptSet, deadline_unix_ms: float}|null null when there
     *         is no directory to try, which lets the caller skip the whole branch
     */
    public function start(array $servers, string $login, string $password): ?array
    {
        if ($servers === []) {
            return null;
        }

        $jobs = [];
        foreach ($servers as $server) {
            $jobs[] = [
                // The source id the audit details and §2.9's attempts map name a server
                // by: "ldap-N" (API_Endpoints_Design.md §4.3).
                'id' => 'ldap-' . $server['index'],
                'url' => (string) $server['url'],
                'bind_dn' => (string) $server['bind_dn'],
                'bind_password' => (string) $server['bind_password'],
                'search_base' => (string) $server['search_base'],
                'uid_attr' => (string) $server['uid_attr'],
                'email_attr' => (string) $server['email_attr'],
                'name_attr' => (string) $server['name_attr'],
                'login' => $login,
                'password' => $password,
                'timeout_seconds' => self::ATTEMPT_TIMEOUT_SECONDS,
            ];
        }

        $set = $this->launcher->start($jobs, self::ATTEMPT_TIMEOUT_SECONDS);

        return [
            'set' => $set,
            'deadline_unix_ms' => microtime(true) * 1000 + self::ATTEMPT_TIMEOUT_SECONDS * 1000,
        ];
    }

    /**
     * Settles a running set and reads the race's result out of it. A null set — no
     * directory under the selected name, so nothing was ever started — is not an error:
     * it contributes no outcomes, and the local attempt decides the login alone.
     *
     * The winner is the first `ok` in arrival order (§2.9 "first login ok wins"); a
     * later one is discarded here, before any Sequence C call exists, which is what
     * keeps a second session or a duplicate audit success impossible (REQ-AUTH-065).
     *
     * @return array{outcomes: array<string, string>, winner: array{provider: string, email: string, display_name: string}|null}
     */
    public function finish(?array $running): array
    {
        if ($running === null) {
            return ['outcomes' => [], 'winner' => null];
        }

        /** @var LdapAttemptSet $set */
        $set = $running['set'];

        try {
            $settled = $set->settle((int) $running['deadline_unix_ms']);
        } catch (\Throwable $e) {
            // A launcher that throws is a broken deployment, not a credential failure:
            // report every source as unreachable and let the login answer generically.
            $this->logger->error('ldap race failed', ['type' => $e::class]);
            $settled = [];
        }

        $outcomes = [];
        $winner = null;

        foreach ($settled as $sourceId => $result) {
            $outcome = (string) $result['outcome'];
            // The attempts map the API records carries one word per source; a winner is
            // recorded as `ok` only if it is not used, and never appears at all when it
            // finalizes the login (that call reports success through Sequence C).
            $outcomes[(string) $sourceId] = $outcome;

            if ($outcome === 'ok' && $winner === null) {
                $email = (string) ($result['email'] ?? '');
                if ($email !== '') {
                    $winner = [
                        'provider' => (string) $sourceId,
                        'email' => $email,
                        'display_name' => (string) ($result['display_name'] ?? ''),
                    ];
                } else {
                    // A bind that produced no address cannot name an account; recorded as
                    // unreachable so it never reads as a wrong password.
                    $outcomes[(string) $sourceId] = 'unreachable';
                }
            } elseif ($outcome === 'ok') {
                // A second directory agreed about who this is. Discarded, per §2.9 — and
                // worth one log line, because two directories claiming the same identity
                // with different addresses is a configuration question.
                $this->logger->warn('discarding a later directory success', [
                    'source' => (string) $sourceId,
                    'differs' => ((string) ($result['email'] ?? '')) !== $winner['email'],
                ]);
                $outcomes[(string) $sourceId] = 'ok_ignored';
            }
        }

        return ['outcomes' => $outcomes, 'winner' => $winner];
    }

    /** Stops a set nobody is waiting for any more — a winner already finalized login. */
    public function abandon(?array $running): void
    {
        if ($running !== null) {
            $running['set']->cancel();
        }
    }
}
