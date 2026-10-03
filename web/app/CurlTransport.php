<?php
// The production transport: ext-curl (Plan/Web_Implementation.md §4). It reports
// status and body only — every decision about what a response means lives in
// ApiClient, so the same rules apply to any other implementation of Transport.

declare(strict_types=1);

namespace Clara;

final class CurlTransport implements Transport
{
    public function __construct(
        private readonly int $connectTimeoutSeconds = 3,
        private readonly int $timeoutSeconds = 15
    ) {
        // Naming a missing extension here — rather than failing three layers down
        // inside a request — is an operator-facing message, so it travels as a
        // ConfigError and renders on the page that names configuration problems
        // (REQ-CFG-005).
        if (!function_exists('curl_init')) {
            throw new ConfigError(
                'The PHP cURL extension is required but not loaded — install it (Debian/Ubuntu: php'
                . PHP_MAJOR_VERSION . '.' . PHP_MINOR_VERSION . '-curl).'
            );
        }
    }

    public function request(string $method, string $url, array $headers, ?string $body): array
    {
        $handle = curl_init($url);
        if ($handle === false) {
            return ['status' => 0, 'body' => ''];
        }

        curl_setopt_array($handle, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_CONNECTTIMEOUT => $this->connectTimeoutSeconds,
            CURLOPT_TIMEOUT => $this->timeoutSeconds,
            CURLOPT_HTTPHEADER => $headers,
        ]);
        if ($body !== null) {
            curl_setopt($handle, CURLOPT_POSTFIELDS, $body);
        }

        $raw = curl_exec($handle);
        $status = (int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE);
        // No curl_close(): the handle is an object released at scope exit
        // (the function has been a no-op since PHP 8.0 and deprecated in 8.5).

        // A failed exchange — refused, timed out, unresolvable — carries no status.
        return $raw === false ? ['status' => 0, 'body' => ''] : ['status' => $status, 'body' => (string) $raw];
    }
}
