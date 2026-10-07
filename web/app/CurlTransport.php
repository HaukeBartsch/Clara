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

    /** At most this much of a non-streamed (error) body is kept. */
    private const MAX_BUFFERED_BODY = 65536;

    public function stream(string $method, string $url, array $headers, callable $onStart, callable $onChunk): array
    {
        $handle = curl_init($url);
        if ($handle === false) {
            return ['status' => 0, 'body' => '', 'streamed' => false, 'complete' => false];
        }

        $responseHeaders = [];
        $status = 0;
        $decided = false;
        $streaming = false;
        $buffer = '';

        curl_setopt_array($handle, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_CONNECTTIMEOUT => $this->connectTimeoutSeconds,
            // No overall timeout: a large export legitimately runs for minutes. A stalled
            // transfer is cut instead — below one byte a second for the usual timeout.
            CURLOPT_LOW_SPEED_LIMIT => 1,
            CURLOPT_LOW_SPEED_TIME => $this->timeoutSeconds,
            CURLOPT_HTTPHEADER => $headers,
            CURLOPT_HEADERFUNCTION => static function ($curl, string $line) use (&$responseHeaders): int {
                $parts = explode(':', $line, 2);
                if (count($parts) === 2) {
                    $responseHeaders[strtolower(trim($parts[0]))] = trim($parts[1]);
                }

                return strlen($line);
            },
            CURLOPT_WRITEFUNCTION => static function ($curl, string $data) use (
                &$decided, &$streaming, &$buffer, &$status, &$responseHeaders, $onStart, $onChunk
            ): int {
                if (!$decided) {
                    $decided = true;
                    $status = (int) curl_getinfo($curl, CURLINFO_RESPONSE_CODE);
                    $streaming = (bool) $onStart($status, $responseHeaders);
                }
                if ($streaming) {
                    $onChunk($data);
                } elseif (strlen($buffer) < self::MAX_BUFFERED_BODY) {
                    $buffer .= $data;
                }

                return strlen($data);
            },
        ]);

        $ok = curl_exec($handle) !== false;
        if (!$decided) {
            $status = $ok ? (int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE) : 0;
            // An empty body never reaches the write callback; the caller still decides.
            if ($status > 0) {
                $streaming = (bool) $onStart($status, $responseHeaders);
            }
        }

        return ['status' => $status, 'body' => $buffer, 'streamed' => $streaming, 'complete' => $ok];
    }
}
