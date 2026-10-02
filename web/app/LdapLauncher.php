<?php
// The seam the LDAP race runs its directory attempts through
// (Authentication_Authorization_Design.md §2.2/§2.9).
//
// The production implementation spawns one child process per server
// (SubprocessLdapLauncher), because ext-ldap is synchronous and PHP has no threads
// in the SAPI this application runs in: separate processes are what make "all LDAP
// sources under the selected name, in parallel with the local check" (§2.9,
// DEV-AUTH-14) literally true rather than a queue of waits.
//
// Tests inject a fake launcher that answers from a fixture, so winner selection,
// deadline handling and outcome mapping are exercised with no directory and no
// ext-ldap in sight.

declare(strict_types=1);

namespace Clara;

interface LdapLauncher
{
    /**
     * Starts one attempt per job, all concurrently, and returns the set to settle.
     * Each job carries one server's configuration plus the submitted credentials;
     * nothing in a job may reach a log line or an error message (REQ-AUTH-036).
     *
     * @param list<array<string, scalar>> $jobs
     */
    public function start(array $jobs, int $perAttemptTimeoutSeconds): LdapAttemptSet;
}
