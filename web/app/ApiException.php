<?php
// The Go API's failure envelope — {error, message, status} of
// Design/API_Endpoints_Design.md §4.2 — carried into PHP as an exception so a
// controller can branch on the stable `error` code rather than parse a body
// (Plan/Web_Implementation.md §5: "Maps {error,message,status} to an exception
// carrying the stable code for Messages.php").
//
// The status alone is never enough: login answers the two-factor states with
// 401s that are not failures (§2.2 of User_Interface_Design.md), so every
// branch in Auth goes through code().

declare(strict_types=1);

namespace Clara;

final class ApiException extends \RuntimeException
{
    /**
     * @param string $errorCode the stable §4.2 code (`bad_password`, `forbidden`)
     * @param int    $status    the HTTP status the API answered with
     */
    public function __construct(
        private readonly string $errorCode,
        string $message,
        private readonly int $status
    ) {
        // \Exception::$code is an int and unusable for a string code, so the
        // code lives beside it and is read through code().
        parent::__construct($message !== '' ? $message : $errorCode);
    }

    /** The stable machine-readable code of §4.2 (e.g. `bad_password`). */
    public function code(): string
    {
        return $this->errorCode;
    }

    /** The HTTP status the API answered with. */
    public function status(): int
    {
        return $this->status;
    }

    /** Builds from a decoded error body, tolerating a missing field. */
    public static function fromBody(array $body, int $status): self
    {
        $code = isset($body['error']) && is_string($body['error']) ? $body['error'] : 'internal';
        $message = isset($body['message']) && is_string($body['message']) ? $body['message'] : '';

        return new self($code, $message, $status);
    }
}
