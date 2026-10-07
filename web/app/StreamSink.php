<?php
// Where a streamed response goes (Response::stream): the status and headers once, then the
// body in pieces as they arrive. Production writes to PHP's output (PhpOutputSink); the test
// harness records instead, so a streamed download is testable without a web server.

declare(strict_types=1);

namespace Clara;

interface StreamSink
{
    /**
     * Sends the status and headers. Called at most once and before any write(); the
     * response's own headers (CSP and the rest of Response's base set) are merged in by
     * Response, so a producer names only what is particular to its body.
     *
     * @param array<string, string> $headers
     */
    public function begin(int $status, array $headers): void;

    /** Hands one piece of the body on, immediately. */
    public function write(string $chunk): void;
}
