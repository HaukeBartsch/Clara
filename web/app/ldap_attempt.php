<?php
// One LDAP attempt, run as a child process by SubprocessLdapLauncher
// (Authentication_Authorization_Design.md §2.2): search for the entry, then bind as
// that entry with the password the user supplied (ASM-AUTH-2). The launcher keeps one
// of these per server under the selected name so they really do run in parallel —
// ext-ldap is synchronous and the SAPI has no threads.
//
// Two rules shape how it is invoked, both about secrets (REQ-AUTH-036):
//
//   * The job arrives on **stdin**, never in argv. A process's command line is
//     world-readable through /proc, so a directory bind password or the user's
//     password passed as an argument would be visible to every other process on the
//     host for as long as this one runs.
//   * The answer is exactly one JSON line on stdout and nothing else — no warning,
//     no stack trace. A PHP notice printed into that stream would be read as the
//     attempt's outcome, and it could name a DN or a credential while doing it. So
//     errors are reported as outcomes, never as output.
//
// The decisions (what may be attempted, filter escaping, error mapping, which
// attributes carry the identity) live in Clara\LdapAttempt so they are testable; what
// is here is the ext-ldap calls that apply them.

declare(strict_types=1);

ini_set('display_errors', '0');
error_reporting(0);

require __DIR__ . '/LdapAttempt.php';

use Clara\LdapAttempt;

/** Prints the answer and stops — the only way this process ends. */
function answer(array $result): never
{
    fwrite(STDOUT, json_encode($result, JSON_UNESCAPED_SLASHES) . "\n");
    exit(0);
}

$job = json_decode((string) stream_get_contents(STDIN), true);
if (!is_array($job)) {
    answer(['outcome' => 'unreachable', 'reason' => 'no_job']);
}

// Empty credentials, or a server the installation never finished configuring.
$refused = LdapAttempt::preflight($job);
if ($refused !== null) {
    answer($refused);
}

if (!function_exists('ldap_connect')) {
    // Config refuses startup in this state; reaching it means the child was run by
    // hand or the extension vanished after boot.
    answer(['outcome' => 'unreachable', 'reason' => 'extension_missing']);
}

$timeout = max(1, (int) ($job['timeout_seconds'] ?? 5));

/** One directory connection, configured the same way every time. */
$connect = static function () use ($job, $timeout) {
    $link = @ldap_connect((string) $job['url']);
    if ($link === false) {
        return null;
    }

    // Protocol version 3 (the only one worth speaking), no referral chasing — a
    // referral would send this attempt to a server the installation did not
    // authorize for this source name (REQ-AUTH-063) — and a network timeout so an
    // unreachable directory settles instead of hanging the race (§2.9).
    ldap_set_option($link, LDAP_OPT_PROTOCOL_VERSION, 3);
    ldap_set_option($link, LDAP_OPT_REFERRALS, 0);
    @ldap_set_option($link, LDAP_OPT_NETWORK_TIMEOUT, $timeout);
    @ldap_set_option($link, LDAP_OPT_TIMEOUT, $timeout);

    return $link;
};

// --- step 1: find the entry -----------------------------------------------------
$searchLink = $connect();
if ($searchLink === null) {
    answer(['outcome' => 'unreachable', 'reason' => 'connect']);
}

// The read account (REQ-CFG-012). With no bind DN configured the search is anonymous,
// which is what §2.2 step 1 describes for a directory that permits it; a refused
// search bind is our misconfiguration, not the user's failure.
$searchBound = ($job['bind_dn'] ?? '') === ''
    ? @ldap_bind($searchLink)
    : @ldap_bind($searchLink, (string) $job['bind_dn'], (string) $job['bind_password']);

if (!$searchBound) {
    answer(['outcome' => 'unreachable', 'reason' => 'search_bind', 'errno' => ldap_errno($searchLink)]);
}

$result = @ldap_search(
    $searchLink,
    (string) $job['search_base'],
    LdapAttempt::filter((string) $job['uid_attr'], (string) $job['login']),
    [(string) $job['email_attr'], (string) $job['name_attr']]
);

if ($result === false) {
    answer(['outcome' => LdapAttempt::mapError(ldap_errno($searchLink)), 'stage' => 'search']);
}

$entries = @ldap_get_entries($searchLink, $result);
if (!is_array($entries) || (int) ($entries['count'] ?? 0) < 1) {
    // No entry for that login name: a credential failure in the general sense, and
    // recorded distinctly so an outage never hides behind it (§2.2 step 4).
    answer(['outcome' => 'no_entry']);
}

// `uid_attr` is expected unique; where a directory holds several entries with it,
// the first is the one this attempt binds — the same choice every directory client
// makes, and one that still requires the submitted password to match.
$entryDn = (string) @ldap_get_dn($searchLink, ldap_first_entry($searchLink, $result));
if ($entryDn === '') {
    answer(['outcome' => 'unreachable', 'reason' => 'no_dn']);
}

$attributes = [];
foreach ((array) $entries[0] as $key => $value) {
    if (is_string($key)) {
        $attributes[strtolower($key)] = $value;
    }
}

// The address is read from the entry we found, not from what the user typed: that is
// what makes the directory the authority on identity (§2.2 step 3, REQ-AUTH-004).
$identity = LdapAttempt::identity($attributes, (string) $job['email_attr'], (string) $job['name_attr']);

// --- step 2: bind as that entry -------------------------------------------------
$userLink = $connect();
if ($userLink === null) {
    answer(['outcome' => 'unreachable', 'reason' => 'reconnect']);
}

if (!@ldap_bind($userLink, $entryDn, (string) $job['password'])) {
    answer(['outcome' => LdapAttempt::mapError(ldap_errno($userLink)), 'stage' => 'user_bind']);
}

@ldap_unbind($userLink);
@ldap_unbind($searchLink);

if ($identity['email'] === '') {
    // The password matched but the entry names no address under the configured
    // claim: there is no account to log in as, and inventing one from the submitted
    // login would be PHP deciding identity rather than the directory.
    answer(['outcome' => 'unreachable', 'reason' => 'no_email_attribute']);
}

answer(['outcome' => 'ok'] + $identity);
