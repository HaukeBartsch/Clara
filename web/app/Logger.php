<?php
// Structured logging for the web layer: one JSON object per line on STDERR,
// filtered by LOG_LEVEL_WEB (REQ-CFG-019). nginx/PHP-FPM capture it; there is
// no log file of our own to rotate.
//
// Log hygiene (Plan/Web_Implementation.md §7 rule 11, REQ-API-005): tokens, the
// service secret, passwords and record values never appear at any level. The
// redaction below is a backstop for a caller that passes too much — the rule is
// still to pass an explicit, small context: identifiers and codes, not payloads.

declare(strict_types=1);

namespace Clara;

final class Logger
{
    private const LEVELS = ['debug' => 10, 'info' => 20, 'warn' => 30, 'error' => 40];

    /** Context keys whose value is dropped regardless of level. */
    private const REDACT_KEYS = [
        'token', 'tokens', 'password', 'current_password', 'new_password', 'secret',
        'client_secret', 'authorization', 'cookie', 'csrf_token', 'service_token',
        'code_verifier', 'code', 'mfa_code', 'recovery_code', 'otp', 'apikey', 'api_key',
    ];

    public function __construct(
        private readonly string $level,
        private readonly bool $debug
    ) {}

    public static function fromConfig(Config $config): self
    {
        return new self($config->logLevelWeb, $config->isDevelopment);
    }

    public function debug(string $message, array $context = []): void
    {
        $this->write('debug', $message, $context);
    }

    public function info(string $message, array $context = []): void
    {
        $this->write('info', $message, $context);
    }

    /** Anything a user could notice but that is not the application's fault. */
    public function warn(string $message, array $context = []): void
    {
        $this->write('warn', $message, $context);
    }

    /** A failure that needs an operator: unexpected exception, API 5xx, bad state. */
    public function error(string $message, array $context = []): void
    {
        $this->write('error', $message, $context);
    }

    private function write(string $level, string $message, array $context): void
    {
        $configured = self::LEVELS[$this->level] ?? 20;
        if ((self::LEVELS[$level] ?? 20) < $configured) {
            return;
        }

        $entry = [
            'ts' => gmdate('Y-m-d\TH:i:s\Z'),
            'level' => $level,
            'component' => 'web',
            'msg' => $message,
        ];
        if ($context !== []) {
            $entry['ctx'] = $this->redact($context);
        }

        $line = json_encode($entry, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        if ($line === false) {
            // A context value that cannot be encoded must not swallow the line.
            $line = '{"ts":"' . gmdate('Y-m-d\TH:i:s\Z') . '","level":"' . $level
                . '","component":"web","msg":"log encode failure"}';
        }

        file_put_contents('php://stderr', $line . "\n");
    }

    /**
     * Drops the values a caller should never have passed (several layers deep)
     * and truncates long strings so one bad context cannot write a megabyte.
     */
    private function redact(array $context): array
    {
        $out = [];
        foreach ($context as $key => $value) {
            if (is_string($key) && in_array(strtolower($key), self::REDACT_KEYS, true)) {
                $out[$key] = '[redacted]';
                continue;
            }
            if (is_array($value)) {
                $out[$key] = $this->redact($value);
                continue;
            }
            if (is_string($value) && strlen($value) > 400) {
                $out[$key] = substr($value, 0, 400) . '…';
                continue;
            }
            $out[$key] = $value;
        }

        return $out;
    }
}
