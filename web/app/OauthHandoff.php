<?php
// The form_post handoff of the OAuth2 callback (REQ-CFG-041, Authentication_Authorization_Design.md
// §2.1 step 3a).
//
// With OAUTH2_N_RESPONSE_MODE=form_post the provider returns the authorization response as a
// cross-site POST. The browser does not send the SameSite=Lax session cookie on it, so that
// request cannot find the sign-in it belongs to. POST /auth/callback therefore keeps `code`,
// `state` (or the provider's `error`) here under a random handle and answers 303 to
// GET /auth/callback?handoff=<handle>: a top-level GET, which carries the session cookie, and
// where Sequence A continues unchanged — including the comparison of `state` with the session's
// own transaction. The code never appears in a URL.
//
// A handle is single use and lives HANDLE_SECONDS. It is worth nothing on its own: whoever
// redeems it still needs the browser session whose transaction issued that `state`. Records
// are files beside the sessions (same directory, mode 0600), named by the handle's SHA-256 so
// the directory never holds a redeemable value, and with a prefix PHP's session collector
// never touches (it removes `sess_*` only).

declare(strict_types=1);

namespace Clara;

final class OauthHandoff
{
    /** How long a handle can be redeemed: the browser follows the 303 at once. */
    public const HANDLE_SECONDS = 60;

    /** The largest authorization response accepted: a code and a state are a few hundred bytes. */
    public const MAX_BODY_BYTES = 16384;

    /** A field longer than this is not an authorization response. */
    private const MAX_FIELD_BYTES = 4096;

    private const PREFIX = 'clara_handoff_';

    public function __construct(private readonly string $dir) {}

    /**
     * The authorization response of a form-encoded body, or null when it is not one: exactly
     * one `state`, and exactly one `code` or exactly one `error` (RFC 6749 §4.1.2/§4.1.2.1),
     * each within bounds. The raw body decides, because $_POST keeps the last of two equal
     * names; a body that cannot be read (a test request, a SAPI that consumed it) falls back to
     * the parsed fields.
     *
     * @param array<string, mixed> $post
     * @return array{code: string, state: string, error: string}|null
     */
    public static function responseFields(string $rawBody, array $post): ?array
    {
        if (strlen($rawBody) > self::MAX_BODY_BYTES) {
            return null;
        }
        $one = static function (string $name) use ($rawBody, $post): ?string {
            if ($rawBody !== '') {
                $values = Request::formValues($rawBody, $name);

                return count($values) === 1 ? $values[0] : ($values === [] ? '' : null);
            }
            $value = $post[$name] ?? '';

            return is_string($value) ? $value : null;
        };
        $state = $one('state');
        $code = $one('code');
        $error = $one('error');
        if ($state === null || $code === null || $error === null) {
            return null;
        }
        foreach ([$state, $code, $error] as $value) {
            if (strlen($value) > self::MAX_FIELD_BYTES) {
                return null;
            }
        }
        if ($state === '' || ($code === '') === ($error === '')) {
            return null;
        }

        return ['code' => $code, 'state' => $state, 'error' => $error];
    }

    /**
     * Keeps an authorization response for HANDLE_SECONDS and returns the handle that redeems
     * it, or null when it could not be stored. Expired records are swept on the way.
     *
     * @param array{code: string, state: string, error: string} $fields
     */
    public function put(array $fields, int $now): ?string
    {
        $this->sweep($now);
        $handle = bin2hex(random_bytes(32));
        $path = $this->path($handle);
        $json = json_encode(['at' => $now] + $fields);
        $handleFile = @fopen($path, 'x');
        if ($handleFile === false || !is_string($json)) {
            return null;
        }
        @chmod($path, 0600);
        $written = fwrite($handleFile, $json);
        fclose($handleFile);
        if ($written !== strlen($json)) {
            @unlink($path);

            return null;
        }

        return $handle;
    }

    /**
     * Redeems a handle once: the record is claimed by an atomic rename before it is read, so
     * two requests presenting the same handle cannot both get it. Null for an unknown,
     * malformed, already used or expired handle.
     *
     * @return array{code: string, state: string, error: string}|null
     */
    public function take(string $handle, int $now): ?array
    {
        if (preg_match('/^[0-9a-f]{64}$/', $handle) !== 1) {
            return null;
        }
        $path = $this->path($handle);
        $claimed = $path . '.' . bin2hex(random_bytes(8));
        if (!@rename($path, $claimed)) {
            return null;
        }
        $raw = @file_get_contents($claimed);
        @unlink($claimed);
        $record = is_string($raw) ? json_decode($raw, true) : null;
        if (!is_array($record) || !is_int($record['at'] ?? null)
            || $now - $record['at'] > self::HANDLE_SECONDS || $record['at'] > $now + 5) {
            return null;
        }
        foreach (['code', 'state', 'error'] as $key) {
            if (!is_string($record[$key] ?? null)) {
                return null;
            }
        }

        return ['code' => $record['code'], 'state' => $record['state'], 'error' => $record['error']];
    }

    /** Removes records older than a handle can live: a browser that never followed the 303. */
    private function sweep(int $now): void
    {
        foreach (glob(rtrim($this->dir, '/') . '/' . self::PREFIX . '*') ?: [] as $file) {
            $modified = @filemtime($file);
            if ($modified !== false && $now - $modified > self::HANDLE_SECONDS) {
                @unlink($file);
            }
        }
    }

    private function path(string $handle): string
    {
        return rtrim($this->dir, '/') . '/' . self::PREFIX . hash('sha256', $handle);
    }
}
