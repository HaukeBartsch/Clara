<?php
// A running set of LDAP directory attempts, started together by an LdapLauncher
// (Authentication_Authorization_Design.md §2.2/§2.9). It is a set rather than a
// list of handles because the race's caller must be able to start every attempt,
// go and run the local check of Sequence F, and only then collect what settled —
// DEV-AUTH-14 forbids attempting the servers one after another, so "who answers
// first" is decided by the set, not by the order the code asks in.

declare(strict_types=1);

namespace Clara;

interface LdapAttemptSet
{
    /**
     * Blocks until every attempt has settled or the deadline (unix time in ms)
     * passes, and returns one outcome per job id. `ok` carries the entry's email
     * and display name; anything else is a reason (§2.2). An attempt still running
     * when the deadline passes settles as `unreachable` — a directory that answers
     * too late to take part in this login is not reachable for it, and the race
     * does not wait for it (§2.9).
     *
     * @return array<string, array{outcome: string, email?: string, display_name?: string}>
     */
    public function settle(int $deadlineUnixMs): array;

    /** Stops anything still running — called once a winner has finalized the login. */
    public function cancel(): void;
}
