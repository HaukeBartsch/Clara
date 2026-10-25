<?php
// The export action (`User_Interface_Design.md` §6.4, REQ-UI-020/069): one page offering the
// project export — CSV or JSON, codes or labels, the header style, the CSV delimiter, decimal
// separator and byte-order mark, and the arms to include — and, on `?download=1`, the file
// itself, proxied from `GET /api/v1/projects/{id}/export` piece by piece so a large export
// never sits in PHP's memory (REQ-TECH-011). Beside it, `?download=dictionary` streams the
// project's data dictionary as the API's REDCap instrument ZIP (REQ-API-153): instrument.csv
// in REDCap's headings, every instrument named in Form Name, design only and no record values
// — the file the Design page's import reads (§7.6).
//
// The CSV options start from the user's UI language: a language whose spreadsheets read the
// comma as decimal separator gets semicolon, decimal comma and the byte-order mark
// pre-selected, every other one the API's own defaults (REQ-UI-020, DEV-EXP-6). It is only a
// pre-selection — the form shows each option, and the API never guesses a language.
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

    /** The decimal separators the form offers, by name, as the API's `decimalCharacter` (REQ-EXP-015). */
    public const DECIMALS = ['period' => '.', 'comma' => ','];

    /** UI languages whose spreadsheet convention is the decimal comma with semicolon lists. */
    public const COMMA_DECIMAL_LANGUAGES = ['nb', 'nn'];

    /** Marks a download as UTF-8 for spreadsheet software opening it directly (REQ-EXP-016). */
    private const UTF8_BOM = "\xEF\xBB\xBF";

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

        if ($this->request->query('download') === 'odm') {
            if (!Session::isAdmin()) {
                throw new ApiException('forbidden', '', 403);
            }

            return $this->downloadODM($projectId, $this->projectName($detail));
        }
        if ($this->request->query('download') === '1') {
            return $this->download($projectId, $this->projectName($detail), $arms);
        }
        if ($this->request->query('download') === 'dictionary') {
            return $this->downloadDictionary($projectId);
        }

        return $this->renderSection($detail, 'project/export', 'export.title', [
            'exportUrl' => '/projects/' . $projectId . '/export',
            'isAdmin' => Session::isAdmin(),
            'arms' => $arms,
            'level' => self::appliedLevel($arms),
            // Pairs the member may not export: stated on the card rather than hidden
            // (§6.4, REQ-UI-020), and counted so a selection can say what it excludes.
            'dropped' => array_sum(array_column($arms, 'dropped')),
            'formats' => self::FORMATS,
            'values' => self::VALUES,
            'headers' => self::HEADERS,
            'delimiters' => array_keys(self::DELIMITERS),
            'decimals' => array_keys(self::DECIMALS),
            'csvDefaults' => self::csvDefaults(Session::uiLanguage()),
        ], [
            'scripts' => ['/assets/app.js', '/assets/js/export.js'],
            'jsKeys' => self::JS_KEYS,
        ], $this->projectMode($projectId));
    }

    /**
     * The CSV options the form starts from for a UI language (REQ-UI-020): semicolon, decimal
     * comma and the byte-order mark where spreadsheets expect them, the API's defaults — comma,
     * the stored period, no mark — everywhere else.
     *
     * @return array{delimiter: string, decimal: string, bom: bool}
     */
    public static function csvDefaults(string $language): array
    {
        if (in_array($language, self::COMMA_DECIMAL_LANGUAGES, true)) {
            return ['delimiter' => 'semicolon', 'decimal' => 'comma', 'bom' => true];
        }

        return ['delimiter' => 'comma', 'decimal' => 'period', 'bom' => false];
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
        $decimal = self::DECIMALS[$this->request->query('decimal')] ?? null;
        if ($format === 'csv' && $decimal !== null) {
            $query['decimalCharacter'] = $decimal;
        }
        // The mark is the download's, not the API's (REQ-EXP-016): written here ahead of the
        // first proxied byte, so programmatic callers of the API never receive one.
        $bom = $format === 'csv' && $this->request->query('bom') === '1';

        $filename = self::filename($projectName, $format);
        $path = '/api/v1/projects/' . $projectId . '/export';

        return Response::stream(function (StreamSink $sink) use ($path, $query, $filename, $back, $bom): void {
            try {
                $this->api->stream($path, $query,
                    static function (int $status, array $upstream) use ($sink, $filename, $bom): void {
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
                        if ($bom) {
                            $sink->write(self::UTF8_BOM);
                        }
                    },
                    static fn (string $chunk) => $sink->write($chunk)
                );
            } catch (ApiException $e) {
                $this->logger->info('export refused', ['code' => $e->code(), 'status' => $e->status()]);
                Session::flash('danger', $this->apiFailureMessage($e));
                $sink->begin(303, ['Location' => $back, 'Cache-Control' => 'no-store']);
            }
        });
    }

    /**
     * The data dictionary download (`?download=dictionary`, REQ-UI-020): the API's REDCap
     * instrument ZIP of the project (`GET …/instruments/export`, REQ-API-153), streamed under
     * the name the API gives it — `<project>_<YYYY-MM-DD>_<HHMM>.zip`. Design only, no record
     * values, so it carries none of the data form's options; a refusal arrives before any byte
     * and becomes a flash line on the page like any other failure.
     */
    private function downloadDictionary(int $projectId): Response
    {
        $back = '/projects/' . $projectId . '/export';
        $path = '/api/v1/projects/' . $projectId . '/instruments/export';

        return Response::stream(function (StreamSink $sink) use ($path, $back): void {
            try {
                $this->api->stream($path, [],
                    static function (int $status, array $upstream) use ($sink): void {
                        Session::close();
                        $sink->begin(200, [
                            'Content-Type' => 'application/zip',
                            'Content-Disposition' => 'attachment; filename="'
                                . self::dictionaryFilename($upstream['content-disposition'] ?? '') . '"',
                            'Cache-Control' => 'no-store',
                        ]);
                    },
                    static fn (string $chunk) => $sink->write($chunk)
                );
            } catch (ApiException $e) {
                $this->logger->info('dictionary download refused', ['code' => $e->code(), 'status' => $e->status()]);
                Session::flash('danger', $this->apiFailureMessage($e));
                $sink->begin(303, ['Location' => $back, 'Cache-Control' => 'no-store']);
            }
        });
    }

    /** The installation-admin-only project interchange download (REQ-ODM-005). */
    private function downloadODM(int $projectId, string $projectName): Response
    {
        $back = '/projects/' . $projectId . '/export';
        $path = '/api/v1/projects/' . $projectId . '/export/odm';

        return Response::stream(function (StreamSink $sink) use ($path, $back, $projectName): void {
            try {
                $this->api->stream($path, [],
                    static function (int $status, array $upstream) use ($sink, $projectName): void {
                        Session::close();
                        $sink->begin(200, [
                            'Content-Type' => 'application/xml; charset=UTF-8',
                            'Content-Disposition' => 'attachment; filename="'
                                . self::odmFilename($upstream['content-disposition'] ?? '', $projectName) . '"',
                            'Cache-Control' => 'no-store',
                            'X-Accel-Buffering' => 'no',
                        ]);
                    },
                    static fn (string $chunk) => $sink->write($chunk)
                );
            } catch (ApiException $e) {
                $this->logger->info('ODM export refused', ['code' => $e->code(), 'status' => $e->status()]);
                Session::flash('danger', $this->apiFailureMessage($e));
                $sink->begin(303, ['Location' => $back, 'Cache-Control' => 'no-store']);
            }
        });
    }

    /** An API filename is forwarded only when it has the expected safe ODM shape. */
    public static function odmFilename(string $disposition, string $projectName): string
    {
        if (preg_match('/filename="([A-Za-z0-9._-]{1,120}\.xml)"/', $disposition, $m) === 1) {
            return $m[1];
        }
        $base = trim((string) preg_replace('/[^A-Za-z0-9._-]+/', '_', $projectName), '._-');

        return ($base === '' ? 'export' : substr($base, 0, 80)) . '_' . gmdate('Y-m-d') . '.xml';
    }

    /**
     * The archive's name as the API's Content-Disposition gives it (REQ-API-153); a name of
     * any other shape is not passed on to the browser.
     */
    public static function dictionaryFilename(string $disposition): string
    {
        return preg_match('/filename="([A-Za-z0-9._-]{1,120}\.zip)"/', $disposition, $m) === 1
            ? $m[1] : 'data_dictionary.zip';
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
