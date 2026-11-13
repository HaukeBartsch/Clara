<?php
// The session index of single sign-out (Authentication_Authorization_Design.md §3,
// REQ-AUTH-083/099): one small file per account under SESSION_DIR listing the ids of that
// account's live PHP sessions, so a provider's back-channel logout (Sequence L2) can delete
// their files. It exists only with OIDC_SINGLE_SIGN_OUT=1.
//
// It is keyed by user id — what the API answers for a verified logout token — and never by
// provider subject, which this layer does not see. An index that is wrong costs promptness
// only: the API has already raised the account's session epoch, so every session it misses
// still ends at its next API call. That is why every operation here swallows its failures
// instead of failing the request that triggered it.
//
// The files live beside the sessions, with the same directory and permissions (0700), and
// carry a name PHP's session garbage collector never touches (it removes `sess_*` only).

declare(strict_types=1);

namespace Clara;

final class SessionIndex
{
    /** What an index file is called: never `sess_…`, which PHP's collector would remove. */
    private const PREFIX = 'clara_index_';

    /** The id shape PHP's files handler stores (sess_<id>) — anything else is not looked up. */
    private const ID = '/^[A-Za-z0-9,-]{22,256}$/';

    public function __construct(private readonly string $dir) {}

    /**
     * Adds a session id to the account's index, and drops the listed ids whose session file
     * no longer exists — ended by timeout, sign-out or garbage collection.
     */
    public function add(int $userId, string $sessionId): void
    {
        if ($userId <= 0 || preg_match(self::ID, $sessionId) !== 1) {
            return;
        }
        $this->update($userId, function (array $ids) use ($sessionId): array {
            $live = array_values(array_filter($ids, fn (string $id): bool => is_file($this->sessionFile($id))));
            if (!in_array($sessionId, $live, true)) {
                $live[] = $sessionId;
            }

            return $live;
        });
    }

    /** Removes a session id from the account's index (sign-out, end of a session, rotation). */
    public function remove(int $userId, string $sessionId): void
    {
        if ($userId <= 0 || $sessionId === '') {
            return;
        }
        $this->update($userId, static fn (array $ids): array => array_values(array_filter(
            $ids,
            static fn (string $id): bool => $id !== $sessionId
        )));
    }

    /**
     * The ids the account's index lists.
     *
     * @return list<string>
     */
    public function sessionIds(int $userId): array
    {
        $raw = @file_get_contents($this->indexFile($userId));

        return is_string($raw) ? self::parse($raw) : [];
    }

    /**
     * Sequence L2 step 5: deletes the session file of every id the account's index lists, and
     * the index itself. Returns how many session files were removed.
     */
    public function endAll(int $userId): int
    {
        if ($userId <= 0) {
            return 0;
        }
        $removed = 0;
        $this->update($userId, function (array $ids) use (&$removed): array {
            foreach ($ids as $id) {
                if (@unlink($this->sessionFile($id))) {
                    $removed++;
                }
            }

            return [];
        });
        @unlink($this->indexFile($userId));

        return $removed;
    }

    /**
     * Reads, changes and writes one index under an exclusive lock, so two requests of the same
     * account — a sign-in in one tab, a rotation in another — cannot lose each other's change.
     *
     * @param callable(list<string>): list<string> $change
     */
    private function update(int $userId, callable $change): void
    {
        $path = $this->indexFile($userId);
        $handle = @fopen($path, 'c+');
        if ($handle === false) {
            return;
        }
        try {
            if (!flock($handle, LOCK_EX)) {
                return;
            }
            @chmod($path, 0600);
            $ids = self::parse((string) stream_get_contents($handle));
            $next = $change($ids);
            ftruncate($handle, 0);
            rewind($handle);
            fwrite($handle, $next === [] ? '' : implode("\n", $next) . "\n");
            fflush($handle);
            flock($handle, LOCK_UN);
        } finally {
            fclose($handle);
        }
    }

    /** @return list<string> */
    private static function parse(string $raw): array
    {
        $ids = [];
        foreach (explode("\n", $raw) as $line) {
            $line = trim($line);
            if (preg_match(self::ID, $line) === 1 && !in_array($line, $ids, true)) {
                $ids[] = $line;
            }
        }

        return $ids;
    }

    private function indexFile(int $userId): string
    {
        return rtrim($this->dir, '/') . '/' . self::PREFIX . $userId;
    }

    /** Where PHP's files handler keeps a session: session.save_path is SESSION_DIR (Session::start). */
    private function sessionFile(string $sessionId): string
    {
        return rtrim($this->dir, '/') . '/sess_' . $sessionId;
    }
}
