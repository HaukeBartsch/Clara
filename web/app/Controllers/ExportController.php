<?php
// The export action (`User_Interface_Design.md` §6.4, REQ-UI-020): one page offering the
// project export — CSV or JSON, codes or labels, the header style, the CSV delimiter and the
// arms to include — and, on `?download=1`, the file itself, proxied from
// `GET /api/v1/projects/{id}/export` piece by piece so a large export never sits in PHP's
// memory (REQ-TECH-011).
//
// The sensitivity badge states what the API will apply, computed from the same values the
// API computes it from: the export level of each selected arm's **default** in the
// permissions block (REQ-API-126), the lowest — most protective — of them for a multi-arm
// download (§6.4, REQ-EXP-003). The API decides; the badge only says beforehand what it will
// decide, and the audit entry records the level actually applied (REQ-API-076).
//
// Known gap (Plan M6, report): the reworked requirements (REQ-API-075, REQ-UI-020) apply the
// export level per (instrument, event) pair. The export endpoint does not yet; it reads the
// arm defaults alone. A member whose export rights exist only as pair grants therefore sees
// a notice here instead of a form, rather than a download the API would refuse.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Permissions;
use Clara\Response;
use Clara\Session;
use Clara\StreamSink;

final class ExportController extends ProjectPageController
{
    public const FORMATS = ['csv', 'json'];
    public const VALUES = ['raw', 'label'];
    public const HEADERS = ['raw', 'label', 'both'];

    /** The CSV delimiters the form offers, by name — the character never travels in the URL. */
    public const DELIMITERS = ['comma' => ',', 'semicolon' => ';', 'tab' => "\t", 'pipe' => '|'];

    /** What a downloaded file may be labelled as; anything else goes out as a plain download. */
    private const CONTENT_TYPES = ['text/csv', 'application/json'];

    private const JS_KEYS = [
        'export.level.export_full', 'export.level.export_no_identifiers', 'export.level.export_de_identified',
        'export.level_help.export_full', 'export.level_help.export_no_identifiers', 'export.level_help.export_de_identified',
        'export.no_arm',
    ];

    /** GET /projects/{id}/export — the export page, or with `?download=1` the file (§2.1). */
    public function index(): Response
    {
        $detail = $this->projectDetail();
        $permissions = Permissions::fromProjectDetail($detail);
        // The card is present only with an exportable level somewhere (§6.4, REQ-UI-003).
        if (!$permissions->canExportAny()) {
            throw new ApiException('forbidden', '', 403);
        }
        $projectId = $this->projectIdOf($detail);
        $arms = self::exportableArms($detail, $permissions);

        if ($this->request->query('download') === '1') {
            return $this->download($projectId, $this->projectName($detail), $arms);
        }

        return $this->renderSection($detail, 'project/export', 'export.title', [
            'exportUrl' => '/projects/' . $projectId . '/export',
            'arms' => $arms,
            'level' => self::appliedLevel($arms),
            'formats' => self::FORMATS,
            'values' => self::VALUES,
            'headers' => self::HEADERS,
            'delimiters' => array_keys(self::DELIMITERS),
        ], [
            'scripts' => ['/assets/app.js', '/assets/js/export.js'],
            'jsKeys' => self::JS_KEYS,
        ], $this->projectMode($projectId));
    }

    /**
     * The arms the export may include: those whose default carries an export level above
     * `export_none` — exactly the candidates the API selects (API §4.14) — with that level.
     *
     * @param array<string, mixed> $detail
     * @return list<array{arm_num: int, name: string, level: string}>
     */
    public static function exportableArms(array $detail, Permissions $permissions): array
    {
        $arms = [];
        foreach ((is_array($detail['arms'] ?? null) ? $detail['arms'] : []) as $arm) {
            if (!is_array($arm)) {
                continue;
            }
            $armNum = (int) ($arm['arm_num'] ?? 0);
            $level = $permissions->exportLevel($armNum);
            if (Permissions::exportRank($level) > 0) {
                $arms[] = ['arm_num' => $armNum, 'name' => (string) ($arm['name'] ?? ''), 'level' => $level];
            }
        }

        return $arms;
    }

    /**
     * The level a download of these arms is delivered at: the lowest among them (§6.4,
     * REQ-EXP-003), or '' when there is none to export.
     *
     * @param list<array{level: string}> $arms
     */
    public static function appliedLevel(array $arms): string
    {
        $level = '';
        foreach ($arms as $arm) {
            if ($level === '' || Permissions::exportRank($arm['level']) < Permissions::exportRank($level)) {
                $level = $arm['level'];
            }
        }

        return $level;
    }

    /**
     * The streamed download. The query is rebuilt from known values only — an unknown format
     * falls back to CSV, an arm the member may not export is dropped — so nothing from the
     * browser reaches the API unchecked; the API validates again and decides (REQ-AUTH-033).
     *
     * A refusal arrives before any byte of the file and becomes a line on the export page; a
     * transfer that breaks off later cannot be reported to the browser any more and is
     * logged instead.
     *
     * @param list<array{arm_num: int, name: string, level: string}> $arms
     */
    private function download(int $projectId, string $projectName, array $arms): Response
    {
        $back = '/projects/' . $projectId . '/export';
        $format = in_array($this->request->query('format'), self::FORMATS, true) ? $this->request->query('format') : 'csv';
        $allowed = array_column($arms, 'arm_num');
        $selected = array_values(array_unique(array_filter(
            array_map('intval', array_filter($this->request->queryList('arm'), static fn (string $a): bool => preg_match('/^\d{1,4}$/', $a) === 1)),
            static fn (int $a): bool => in_array($a, $allowed, true)
        )));
        if ($selected === []) {
            $this->flashDanger($this->i18n->t('export.no_arm'));

            return Response::redirect($back, 303);
        }

        $query = ['format' => $format, 'arm' => $selected];
        $values = $this->request->query('rawOrLabel');
        if (in_array($values, self::VALUES, true)) {
            $query['rawOrLabel'] = $values;
        }
        $headers = $this->request->query('rawOrLabelHeaders');
        if (in_array($headers, self::HEADERS, true)) {
            $query['rawOrLabelHeaders'] = $headers;
        }
        $delimiter = self::DELIMITERS[$this->request->query('delimiter')] ?? null;
        if ($format === 'csv' && $delimiter !== null) {
            $query['csvDelimiter'] = $delimiter;
        }

        $filename = self::filename($projectName, $format);
        $path = '/api/v1/projects/' . $projectId . '/export';

        return Response::stream(function (StreamSink $sink) use ($path, $query, $filename, $back): void {
            try {
                $this->api->stream($path, $query,
                    static function (int $status, array $upstream) use ($sink, $filename): void {
                        // The session is not needed any more once the file starts, and holding
                        // its lock for the length of a download would block the user's other
                        // pages (Session::close).
                        Session::close();
                        $sink->begin(200, [
                            'Content-Type' => self::contentType($upstream['content-type'] ?? ''),
                            'Content-Disposition' => 'attachment; filename="' . $filename . '"',
                            'Cache-Control' => 'no-store',
                            // Asks nginx not to buffer the proxied body (Technology_Stack §5).
                            'X-Accel-Buffering' => 'no',
                        ]);
                    },
                    static fn (string $chunk) => $sink->write($chunk)
                );
            } catch (ApiException $e) {
                $this->logger->info('export refused', ['code' => $e->code(), 'status' => $e->status()]);
                Session::flash('danger', Messages::forApiException($this->i18n, $e));
                $sink->begin(303, ['Location' => $back, 'Cache-Control' => 'no-store']);
            }
        });
    }

    /** `<project>_<YYYY-MM-DD>.<ext>` with nothing a header could misread. */
    public static function filename(string $projectName, string $format): string
    {
        $base = trim((string) preg_replace('/[^A-Za-z0-9._-]+/', '_', $projectName), '._-');

        return ($base === '' ? 'export' : substr($base, 0, 80)) . '_' . gmdate('Y-m-d') . '.' . $format;
    }

    /** The upstream media type when it is one an export produces; a plain download otherwise. */
    private static function contentType(string $upstream): string
    {
        $type = strtolower(trim(explode(';', $upstream)[0]));

        return in_array($type, self::CONTENT_TYPES, true) ? $type . '; charset=UTF-8' : 'application/octet-stream';
    }
}
