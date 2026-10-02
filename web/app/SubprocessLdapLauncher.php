<?php
// The production LdapLauncher: one child process per directory, all running at once
// (Authentication_Authorization_Design.md §2.2/§2.9, DEV-AUTH-14).
//
// Why processes: ext-ldap is synchronous and PHP-FPM has no threads, so a loop over
// `ldap_search` would attempt the servers one after another — which is precisely what
// DEV-AUTH-14 replaced. Separate children make the attempts genuinely concurrent, let
// each carry its own timeout, and keep the parent free to run Sequence F's local
// verify in between (§2.9: the first "login ok" wins, so a slow directory must not
// hold up a fast source that already answered).
//
// The children are spawned by SubprocessLdapAttempts, which owns the handles and the
// rules about secrets that go with them.

declare(strict_types=1);

namespace Clara;

final class SubprocessLdapLauncher implements LdapLauncher
{
    public function __construct(
        private readonly Logger $logger,
        /** Wall-clock ceiling for one directory attempt (§2.9's per-source timeout). */
        public readonly int $attemptTimeoutSeconds = 5
    ) {}

    public function start(array $jobs, int $perAttemptTimeoutSeconds): LdapAttemptSet
    {
        return new SubprocessLdapAttempts(
            $jobs,
            $perAttemptTimeoutSeconds > 0 ? $perAttemptTimeoutSeconds : $this->attemptTimeoutSeconds,
            $this->logger
        );
    }
}
