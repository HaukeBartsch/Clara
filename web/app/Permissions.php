<?php
// Permission gating for rendering (REQ-UI-003, REQ-AUTH-027). One helper per
// request resolves the acting user's effective levels for the target project and
// hands them to templates; a control whose permission is missing is not emitted —
// not disabled, not linked to a 403 page.
//
// The source is the `permissions` block of GET /api/v1/projects/{id}
// (REQ-API-126): `project_admin`, one `arms[]` entry per arm with that arm's
// **default**, and one `grants[]` entry per mapped (instrument, event) pair with
// the levels and the two rights **as they resolve for that pair** (REQ-AUTH-069).
// It is disclosure, not a credential — every endpoint re-checks at call time
// (REQ-AUTH-033), so whatever this shows still has to survive the API's own
// decision. Never carry levels into the session (they go stale), and never fetch
// the read more than once per page (Plan/Web_Implementation.md §7 rule 15).

declare(strict_types=1);

namespace Clara;

final class Permissions
{
    /**
     * Data-access ladder, mirroring authz.DataRank in the Go API (REQ-DB-009).
     * It ends at `view_edit`: deleting values and editing collected surveys are
     * rights of a pair, never rungs above it (REQ-AUTH-070).
     */
    private const DATA_RANK = [
        'no_access' => 0,
        'read_only' => 1,
        'view_edit' => 2,
    ];

    /** Export ladder, mirroring authz.ExportRank (REQ-API-026). */
    private const EXPORT_RANK = [
        'export_none' => 0,
        'export_de_identified' => 1,
        'export_no_identifiers' => 2,
        'export_full' => 3,
    ];

    /** @var array<int, string> arm_num => data access level */
    private array $dataByArm;

    /** @var array<int, string> arm_num => export level */
    private array $exportByArm;

    /**
     * Resolved pair grants, keyed "unique_event_name\0instrument". The API sends
     * every mapped pair with its own levels and rights, so a lookup here never
     * has to combine a grant with an arm default — only answer for a pair the
     * response does not carry (a project without events, an object created after
     * the read), which falls back to the arm.
     *
     * @var array<string, array{data: string, export: string, delete_values: bool, edit_surveys: bool}>
     */
    private array $pairs;

    private function __construct(
        public readonly bool $projectAdmin,
        array $dataByArm,
        array $exportByArm,
        array $pairs
    ) {
        $this->dataByArm = $dataByArm;
        $this->exportByArm = $exportByArm;
        $this->pairs = $pairs;
    }

    /**
     * Reads the project detail once and takes its permissions block. Throws the
     * ApiException through: a denied read means the page itself is denied, and
     * the caller renders 403/404 exactly as §3.4 fixes them.
     */
    public static function forProject(ApiClient $api, int $projectId): self
    {
        $detail = $api->get('/api/v1/projects/' . $projectId);

        return self::fromProjectDetail(is_array($detail) ? $detail : []);
    }

    /** Builds from an already-fetched project detail object (the PUT echo carries it too). */
    public static function fromProjectDetail(array $detail): self
    {
        $block = $detail['permissions'] ?? [];
        if (!is_array($block)) {
            $block = [];
        }

        $data = [];
        $export = [];
        foreach (($block['arms'] ?? []) as $arm) {
            if (!is_array($arm) || !isset($arm['arm_num'])) {
                continue;
            }
            $armNum = (int) $arm['arm_num'];
            // An ungranted arm reports no_access / export_none — a level is never
            // inferred from a missing row (REQ-AUTH-019).
            $data[$armNum] = (string) ($arm['data_access_level'] ?? 'no_access');
            $export[$armNum] = (string) ($arm['export_level'] ?? 'export_none');
        }

        $pairs = [];
        foreach (($block['grants'] ?? []) as $grant) {
            if (!is_array($grant)) {
                continue;
            }
            $uen = (string) ($grant['unique_event_name'] ?? '');
            $instrument = (string) ($grant['instrument'] ?? '');
            if ($uen === '' || $instrument === '') {
                continue;
            }
            $pairs[self::pairKey($uen, $instrument)] = [
                'data' => (string) ($grant['data_access_level'] ?? 'no_access'),
                'export' => (string) ($grant['export_level'] ?? 'export_none'),
                'delete_values' => !empty($grant['delete_values']),
                'edit_surveys' => !empty($grant['edit_surveys']),
            ];
        }

        return new self(!empty($block['project_admin']), $data, $export, $pairs);
    }

    /** Nothing allowed at all — the shape a failed or absent read degrades to. */
    public static function none(): self
    {
        return new self(false, [], [], []);
    }

    public static function dataRank(string $level): int
    {
        // An unknown level ranks 0: never silently more permissive than expected.
        return self::DATA_RANK[$level] ?? 0;
    }

    public static function exportRank(string $level): int
    {
        return self::EXPORT_RANK[$level] ?? 0;
    }

    private static function pairKey(string $uniqueEventName, string $instrument): string
    {
        return $uniqueEventName . "\0" . $instrument;
    }

    /**
     * The acting user's levels and rights on one (instrument, event) pair — the
     * scope every data decision is taken at (REQ-AUTH-069). A pair the response
     * does not carry resolves to its arm's default, which carries neither right:
     * a right exists on a pair or not at all (REQ-AUTH-070).
     *
     * @return array{data: string, export: string, delete_values: bool, edit_surveys: bool}
     */
    public function pair(string $uniqueEventName, string $instrument, int $armNum): array
    {
        $grant = $this->pairs[self::pairKey($uniqueEventName, $instrument)] ?? null;
        if ($grant !== null) {
            return $grant;
        }

        return [
            'data' => $this->dataLevel($armNum),
            'export' => $this->exportLevel($armNum),
            'delete_values' => false,
            'edit_surveys' => false,
        ];
    }

    public function dataLevel(int $armNum): string
    {
        return $this->dataByArm[$armNum] ?? 'no_access';
    }

    public function exportLevel(int $armNum): string
    {
        return $this->exportByArm[$armNum] ?? 'export_none';
    }

    /** Reaches a data level through one arm's **default** (project-level gates). */
    public function reachesData(int $armNum, int $minimumRank): bool
    {
        return self::dataRank($this->dataLevel($armNum)) >= $minimumRank;
    }

    /** Reaches an export level through one arm's default. */
    public function reachesExport(int $armNum, int $minimumRank): bool
    {
        return self::exportRank($this->exportLevel($armNum)) >= $minimumRank;
    }

    /** Reads anything on the arm by its default — pair grants are checked at the pair. */
    public function canView(int $armNum): bool
    {
        return $this->reachesData($armNum, self::dataRank('read_only'));
    }

    public function canEdit(int $armNum): bool
    {
        return $this->reachesData($armNum, self::dataRank('view_edit'));
    }

    /** Reaches a data level on one pair (e.g. Permissions::dataRank('view_edit')). */
    public function reachesPairData(string $uniqueEventName, string $instrument, int $armNum, int $minimumRank): bool
    {
        return self::dataRank($this->pair($uniqueEventName, $instrument, $armNum)['data']) >= $minimumRank;
    }

    /** The pair's own export level — what decides the columns of its data (REQ-AUTH-069). */
    public function pairExport(string $uniqueEventName, string $instrument, int $armNum): string
    {
        return $this->pair($uniqueEventName, $instrument, $armNum)['export'];
    }

    public function canViewPair(string $uniqueEventName, string $instrument, int $armNum): bool
    {
        return $this->reachesPairData($uniqueEventName, $instrument, $armNum, self::dataRank('read_only'));
    }

    /**
     * Data entry and survey-link issuance both need data access ≥ `view_edit` on
     * the pair (REQ-API-082); modifying an already-collected survey additionally
     * needs the right below it (REQ-AUTH-071).
     */
    public function canEditPair(string $uniqueEventName, string $instrument, int $armNum): bool
    {
        return $this->reachesPairData($uniqueEventName, $instrument, $armNum, self::dataRank('view_edit'));
    }

    /** The **delete instrument values** right of the pair (REQ-AUTH-018/070). */
    public function canDeleteValues(string $uniqueEventName, string $instrument, int $armNum): bool
    {
        return $this->pair($uniqueEventName, $instrument, $armNum)['delete_values'];
    }

    /** The **edit collected surveys** right of the pair (REQ-AUTH-071). */
    public function canEditSurveys(string $uniqueEventName, string $instrument, int $armNum): bool
    {
        return $this->pair($uniqueEventName, $instrument, $armNum)['edit_surveys'];
    }

    /**
     * Reaches a data level anywhere — on an arm default or on any pair grant. A
     * member whose only `view_edit` is one pair's override still reaches it here,
     * which is what the project-level gates read (REQ-AUTH-069).
     */
    public function anyArmReachesData(int $minimumRank): bool
    {
        foreach ($this->dataByArm as $level) {
            if (self::dataRank($level) >= $minimumRank) {
                return true;
            }
        }
        foreach ($this->pairs as $grant) {
            if (self::dataRank($grant['data']) >= $minimumRank) {
                return true;
            }
        }

        return false;
    }

    /** May delete values on at least one pair — the record page's delete control. */
    public function canDeleteValuesAnywhere(): bool
    {
        foreach ($this->pairs as $grant) {
            if ($grant['delete_values']) {
                return true;
            }
        }

        return false;
    }

    /** The export card's gate: a non-`export_none` level on some arm or pair (§6.4). */
    public function canExportAny(): bool
    {
        foreach ($this->exportByArm as $level) {
            if (self::exportRank($level) > 0) {
                return true;
            }
        }
        foreach ($this->pairs as $grant) {
            if (self::exportRank($grant['export']) > 0) {
                return true;
            }
        }

        return false;
    }

    /** Every arm the project has, with the acting user's defaults — for arm tabs. */
    public function arms(): array
    {
        $arms = [];
        foreach ($this->dataByArm as $armNum => $dataLevel) {
            $arms[$armNum] = [
                'arm_num' => $armNum,
                'data_access_level' => $dataLevel,
                'export_level' => $this->exportByArm[$armNum] ?? 'export_none',
            ];
        }
        ksort($arms);

        return array_values($arms);
    }
}
