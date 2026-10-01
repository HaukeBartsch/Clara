<?php
// Configuration failure reported at boot (REQ-CFG-005). The message is
// operator-facing and lists every problem found in one pass; it never carries
// secret values, only variable names.

declare(strict_types=1);

namespace Clara;

final class ConfigError extends \RuntimeException {}
