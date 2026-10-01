<?php
// CSRF protection for every state-changing browser request (REQ-UI-005,
// REQ-AUTH-037): a per-session 32-byte hex token presented as a hidden
// `csrf_token` form field or an `X-CSRF-Token` header on fetch calls.
//
// The router validates before the controller runs, so a rejected request never
// reaches the API and never performs a write (User_Interface_Design.md §3.3).
// Public routes carry it too — the login and password forms are state-changing
// even with no session behind them (Authentication_Authorization_Design.md §2.8).

declare(strict_types=1);

namespace Clara;

final class Csrf
{
    private function __construct() {}

    /** The active token, issued when the session started. */
    public static function token(): string
    {
        $token = $_SESSION['csrf_token'] ?? null;
        if (!is_string($token) || $token === '') {
            $token = bin2hex(random_bytes(32));
            $_SESSION['csrf_token'] = $token;
        }

        return $token;
    }

    /** Hidden input for a POST form. The token is hex, so escaping is trivial. */
    public static function field(): string
    {
        return '<input type="hidden" name="csrf_token" value="'
            . htmlspecialchars(self::token(), ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8') . '">';
    }

    /**
     * Validates the token presented with this request. Constant-time compare;
     * absence is a failure, not a pass.
     */
    public static function validate(Request $request): bool
    {
        $presented = $request->field('csrf_token');
        if ($presented === '') {
            $header = $request->header('x-csrf-token');
            $presented = $header ?? '';
        }
        if ($presented === '') {
            return false;
        }

        return hash_equals(self::token(), $presented);
    }
}
