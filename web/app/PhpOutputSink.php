<?php
// The production StreamSink: headers through header(), each piece echoed and flushed at once.
// Output buffering is switched off when the body starts, or PHP would collect the whole
// download before the browser saw a byte (REQ-TECH-011); `X-Accel-Buffering: no` — set by
// the producer — asks nginx for the same in production (Technology_Stack_Design.md §5).

declare(strict_types=1);

namespace Clara;

final class PhpOutputSink implements StreamSink
{
    public function begin(int $status, array $headers): void
    {
        if (!headers_sent()) {
            http_response_code($status);
            foreach ($headers as $name => $value) {
                header($name . ': ' . $value);
            }
        }
        while (ob_get_level() > 0) {
            ob_end_flush();
        }
    }

    public function write(string $chunk): void
    {
        echo $chunk;
        flush();
    }
}
