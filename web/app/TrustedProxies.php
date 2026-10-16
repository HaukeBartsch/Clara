<?php
// The addresses whose forwarded `X-Real-IP` this installation may believe: the parsed form of
// TRUSTED_PROXY_CIDRS (System_Configuration_Design.md §3.9). Both components consult that one
// variable before treating a forwarded address as the caller's own — the API in
// `dataapi.SourceIP`, the web layer in `Request::clientIp` (REQ-API-125) — because believing the
// header without asking who sent it lets a browser pick the address the rate limiter keys on and
// forge the IP of an audit entry (finding F9).
//
// An empty list trusts nobody, so every caller is measured by its connection address: wrong for
// a deployment behind a proxy, never spoofable. Matching mirrors Go's `net.IPNet.Contains`,
// including the one surprise worth having — `::ffff:10.1.2.3` is 10.1.2.3 and joins its ranges.

declare(strict_types=1);

namespace Clara;

final class TrustedProxies
{
    /** The documented default of TRUSTED_PROXY_CIDRS, spelled as the API reads it (§3.9). */
    public const DEFAULT_RAW = '127.0.0.0/8,::1';

    /**
     * @param list<array{string, string}> $nets   each range's packed network address and its mask
     * @param string                      $spelled the same ranges, canonically spelled (for display)
     */
    private function __construct(private readonly array $nets, private readonly string $spelled) {}

    /** Trusts no peer: only the connection's own address can be the caller's. */
    public static function none(): self
    {
        return new self([], '');
    }

    /**
     * Reads the comma-separated variable as `config.Load` does in Go: entries are trimmed, empty
     * ones skipped, a bare address is a single-host range, and anything else names the variable
     * in $errors so startup refuses it (REQ-CFG-005).
     *
     * @param array<int, string> $errors the collection Config::fromValues reports at the operator
     */
    public static function parse(string $raw, array &$errors): self
    {
        $nets = [];
        $spelled = [];
        foreach (explode(',', $raw) as $part) {
            $part = trim($part);
            if ($part === '') {
                continue;
            }

            $range = self::parseRange($part);
            if ($range === null) {
                $errors[] = "TRUSTED_PROXY_CIDRS: \"{$part}\" is not a CIDR range or IP address";
                continue;
            }

            [$packed, $mask, $bits] = $range;
            $nets[] = [$packed, $mask];
            $spelled[] = inet_ntop($packed) . '/' . $bits;
        }

        return new self($nets, implode(', ', $spelled));
    }

    /** Whether ip — the direct TCP peer, never a header — may name another caller. */
    public function contains(string $ip): bool
    {
        $packed = self::pack($ip);
        if ($packed === '') {
            return false;
        }

        foreach ($this->nets as [$net, $mask]) {
            if (strlen($net) === strlen($packed) && ($packed & $mask) === $net) {
                return true;
            }
        }

        return false;
    }

    /** The configured ranges as the operator wrote them, for the Configuration Check page. */
    public function describe(): string
    {
        return $this->spelled === '' ? 'none' : $this->spelled;
    }

    /**
     * One entry as network address, mask and prefix length — host bits dropped, the way
     * `net.ParseCIDR` returns it. Null when the entry names no range: not an address, a prefix
     * that is not a number, or one longer than its address family has (10.0.0.0/33).
     *
     * @return array{string, string, int}|null
     */
    private static function parseRange(string $entry): ?array
    {
        $bits = null;
        if (str_contains($entry, '/')) {
            [$entry, $prefix] = explode('/', $entry, 2);
            if (preg_match('/^\d{1,3}$/', $prefix) !== 1) {
                return null;
            }
            $bits = (int) $prefix;
        }

        $packed = self::pack($entry);
        if ($packed === '') {
            return null;
        }

        $maximum = strlen($packed) * 8;
        if ($bits === null) {
            $bits = $maximum; // a bare address is one host (/32 or /128), as in Go
        } elseif ($bits > $maximum) {
            return null;
        }

        $mask = str_repeat("\xff", intdiv($bits, 8));
        if ($bits % 8 !== 0) {
            $mask .= chr(0xff << (8 - $bits % 8) & 0xff);
        }
        $mask = str_pad($mask, strlen($packed), "\0");

        return [$packed & $mask, $mask, $bits];
    }

    /**
     * An address in packed form, or '' when the text names none. filter_var first: it rejects what
     * inet_pton would otherwise accept oddly (a zone identifier such as `fe80::1%eth0`, an empty
     * string, a value with spaces) and is the same "is this an IP address" question in both
     * families.
     */
    private static function pack(string $ip): string
    {
        if (filter_var($ip, FILTER_VALIDATE_IP) === false) {
            return '';
        }

        $packed = @inet_pton($ip);
        if ($packed === false) {
            return '';
        }

        // IPv4 mapped to IPv6 is the same host as the IPv4 address (Go's net.IP says so too).
        if (strlen($packed) === 16 && substr($packed, 0, 12) === str_repeat("\x00", 10) . "\xff\xff") {
            return substr($packed, 12);
        }

        return $packed;
    }
}
