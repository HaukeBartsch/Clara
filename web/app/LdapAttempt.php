<?php
// The parts of one LDAP attempt that are worth testing on their own
// (Authentication_Authorization_Design.md §2.2): what may be attempted at all, how a
// login name becomes a search filter, which directory error means what, and which
// attributes of an entry carry the identity. The ext-ldap calls themselves live in
// web/app/ldap_attempt.php, the child process that runs them — kept thin so this is
// where the decisions are.

declare(strict_types=1);

namespace Clara;

final class LdapAttempt
{
    /** Directory error numbers this code distinguishes (RFC 4511 appendix A). */
    private const INVALID_CREDENTIALS = 49;

    private const NO_SUCH_OBJECT = 32;

    private function __construct() {}

    /**
     * What may be attempted at all, or null to proceed.
     *
     * The empty password is the one that matters: an LDAP simple bind with no
     * password is not "no credentials supplied", it is an **anonymous bind** that
     * succeeds. Refusing it here means a child can never report a successful login
     * for a user who typed nothing, whatever the directory thinks.
     *
     * @param array<string, mixed> $job
     */
    public static function preflight(array $job): ?array
    {
        if ((string) ($job['login'] ?? '') === '' || (string) ($job['password'] ?? '') === '') {
            return ['outcome' => 'bad_password'];
        }

        foreach (['url', 'search_base', 'uid_attr'] as $required) {
            if (trim((string) ($job[$required] ?? '')) === '') {
                // A server the installation did not finish configuring: that is an
                // outage for this attempt, not a credential failure.
                return ['outcome' => 'unreachable', 'reason' => 'misconfigured'];
            }
        }

        return null;
    }

    /**
     * The search filter for a login name. Escaping is not optional: an unescaped
     * `*)(uid=*` turns the lookup into "return every entry" (and, on some
     * directories, into more).
     */
    public static function filter(string $uidAttr, string $login): string
    {
        $attribute = preg_replace('/[^A-Za-z0-9;-]/', '', $uidAttr) ?? 'uid';

        return '(' . $attribute . '=' . self::escapeFilterValue($login) . ')';
    }

    /**
     * RFC 4515 escaping. ext-ldap provides it; the explicit form is here so this
     * function — and the tests that pin it — work on a PHP without the extension,
     * which is the state a developer's machine is in before `php-ldap` is installed.
     */
    private static function escapeFilterValue(string $value): string
    {
        if (function_exists('ldap_escape')) {
            return ldap_escape($value, '', LDAP_ESCAPE_FILTER);
        }

        return str_replace(
            ['\\', '*', '(', ')', "\x00"],
            ['\\5c', '\\2a', '\\28', '\\29', '\\00'],
            $value
        );
    }

    /**
     * What a directory error means for the race (§2.2 step 2 and §2.9 "All failed").
     * A wrong password is the user's; an entry that is not there is a credential
     * failure too, phrased differently in the details; anything else — connection
     * refused, TLS failure, timeout, a bind of our own search account that the
     * directory refused — is the source being unavailable, which must never read as
     * "wrong password" on the login page.
     */
    public static function mapError(int $errno): string
    {
        return match ($errno) {
            self::INVALID_CREDENTIALS => 'bad_password',
            self::NO_SUCH_OBJECT => 'no_entry',
            default => 'unreachable',
        };
    }

    /**
     * The identity an entry carries: its address under the configured claim and its
     * display name (§2.2 step 1, REQ-AUTH-004). Directories return attribute names in
     * whatever case they like, so the lookup is case-insensitive; multi-valued
     * attributes contribute their first value, which is what every directory client
     * does and what a `mail`-typed attribute almost always means in practice.
     *
     * @param array<string, mixed> $attributes the entry's raw attributes, lower-cased keys
     */
    public static function identity(array $attributes, string $emailAttr, string $nameAttr): array
    {
        return [
            'email' => strtolower(trim(self::firstValue($attributes, $emailAttr))),
            'display_name' => self::firstValue($attributes, $nameAttr),
        ];
    }

    /** One attribute's first value, or '' when the entry does not carry it. */
    private static function firstValue(array $attributes, string $name): string
    {
        $entry = $attributes[strtolower($name)] ?? null;

        if (is_array($entry)) {
            $first = $entry[0] ?? '';

            return is_scalar($first) ? (string) $first : '';
        }

        return is_scalar($entry) ? (string) $entry : '';
    }
}
