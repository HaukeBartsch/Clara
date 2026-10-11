<?php
// The export action (`User_Interface_Design.md` §6.4, REQ-UI-020): one page offering the
// project export — CSV or JSON, codes or labels, the header style, the CSV delimiter and the
// arms to include — and, on `?download=1`, the file itself, proxied from
// `GET /api/v1/projects/{id}/export` piece by piece so a large export never sits in PHP's
// memory (REQ-TECH-011).
//
// The sensitivity badge states what the API will apply, computed the way the API
// computes it: from the export level of each (instrument, event) pair in the
// permissions block (REQ-API-126, REQ-AUTH-069), most protective first. The level a
// download states is the strongest protection any of its columns carries less of
// (§4.3) — not one level imposed on every column, which is what the old per-arm
// model did. The API decides; the badge only says beforehand what it will decide,
// and the audit entry records the level actually applied together with the pairs
// dropped for want of a level (REQ-API-076).

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
        'export.dropped_pairs', 'export.no_arm',
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
            // Pairs the member may not export: stated on the card rather than hidden
            // (§6.4, REQ-UI-020), and counted so a selection can say what it excludes.
            'dropped' => array_sum(array_column($arms, 'dropped')),
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
     * The arms the export may include, and what each contributes (§6.4). An arm is
     * offered when at least one of its (instrument, event) pairs carries an export
     * level — a right held only as a pair grant is an export right like any other
     * (REQ-AUTH-069) — with the most protective level its permitted pairs deliver
     * and the count of its pairs that contribute nothing (§4.3). A project without
     * events has no pairs, so its arm default is what applies.
     *
     * @param array<string, mixed> $detail
     * @return list<array{arm_num: int, name: string, level: string, dropped: int}>
     */
    public static function exportableArms(array $detail, Permissions $permissions): array
    {
        // Which arm each event belongs to: the permissions block names its pairs by
        // (event, instrument) and leaves the grouping to the reader, while §4.5
        // embeds each arm's events under the arm itself.
        $armRows = is_array($detail['arms'] ?? null) ? $detail['arms'] : [];
        $armOfEvent = [];
        foreach ($armRows as $arm) {
            if (!is_array($arm) || !isset($arm['arm_num'])) {
                continue;
            }
            $armNum = (int) $arm['arm_num'];
            foreach ((is_array($arm['events'] ?? null) ? $arm['events'] : []) as $event) {
                if (is_array($event) && isset($event['unique_event_name'])) {
                    $armOfEvent[(string) $event['unique_event_name']] = $armNum;
                }
            }
        }

        $levelsByArm = [];
        $droppedByArm = [];
        foreach ($permissions->pairsByExport() as $pair) {
            $armNum = $armOfEvent[$pair['event']] ?? 0;
            if (Permissions::exportRank($pair['export']) > 0) {
                $levelsByArm[$armNum][] = $pair['export'];
                continue;
            }
            $droppedByArm[$armNum] = ($droppedByArm[$armNum] ?? 0) + 1;
        }

        $arms = [];
        foreach ($armRows as $arm) {
            if (!is_array($arm) || !isset($arm['arm_num'])) {
                continue;
            }
            $armNum = (int) $arm['arm_num'];
            // An arm that has pairs is read through them: its default reaches only
            // the pairs nobody mapped, and those deliver no columns at all.
            $levels = $levelsByArm[$armNum] ?? [$permissions->exportLevel($armNum)];
            $permitted = array_values(array_filter(
                $levels,
                static fn (string $level): bool => Permissions::exportRank($level) > 0
            ));
            if ($permitted === []) {
                continue; // nothing here may be exported, so no control for it (REQ-UI-003)
            }
            $arms[] = [
                'arm_num' => $armNum,
                'name' => (string) ($arm['name'] ?? ''),
                'level' => self::lowestLevel($permitted),
                'dropped' => $droppedByArm[$armNum] ?? 0,
            ];
        }

        return $arms;
    }

    /**
     * The level a download of these arms is delivered at: the most protective among
     * them (§6.4) — which under per-pair sensitivity is what the file states it
     * holds, not a level applied to every column (Data_Export_Design.md §4.3).
     *
     * @param list<array{level: string}> $arms
     */
    public static function appliedLevel(array $arms): string
    {
        return self::lowestLevel(array_column($arms, 'level'));
    }

    /** The most protective of the given level names; '' when none of them exports. */
    private static function lowestLevel(array $levels): string
    {
        $level = '';
        foreach ($levels as $candidate) {
            if ($level === '' || Permissions::exportRank($candidate) < Permissions::exportRank($level)) {
                $level = $candidate;
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
