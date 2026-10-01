<?php
// Permission gating for rendering (REQ-UI-003, REQ-AUTH-027). One helper per
// request resolves the acting user's effective levels for the target project and
// hands them to templates; a control whose permission is missing is not emitted —
// not disabled, not linked to a 403 page.
//
// The source is the `permissions` block of GET /api/v1/projects/{id}
// (REQ-API-126): `project_admin` plus one entry per arm with its data access and
// export level. It is disclosure, not a credential — every endpoint re-checks at
// call time (REQ-AUTH-033), so whatever this shows still has to survive the API's
// own decision. Never carry levels into the session (they go stale), and never
// fetch the read more than once per page (Plan/Web_Implementation.md §7 rule 15).

declare(strict_types=1);

namespace Clara;

final class Permissions
{
    /** Data-access ladder, mirroring authz.DataRank in the Go API (REQ-DB-009). */
    private const DATA_RANK = [
        'no_access' => 0,
        'read_only' => 1,
        'view_edit' => 2,
        'delete' => 3,
        'edit_survey_responses' => 4,
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

    private function __construct(
        public readonly bool $projectAdmin,
        array $dataByArm,
        array $exportByArm
    ) {
        $this->dataByArm = $dataByArm;
        $this->exportByArm = $exportByArm;
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

        return new self(!empty($block['project_admin']), $data, $export);
    }

    /** Nothing allowed at all — the shape a failed or absent read degrades to. */
    public static function none(): self
    {
        return new self(false, [], []);
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

    public function dataLevel(int $armNum): string
    {
        return $this->dataByArm[$armNum] ?? 'no_access';
    }

    public function exportLevel(int $armNum): string
    {
        return $this->exportByArm[$armNum] ?? 'export_none';
    }

    /** Reaches a data level on one arm (e.g. Permissions::dataRank('read_only')). */
    public function reachesData(int $armNum, int $minimumRank): bool
    {
        return self::dataRank($this->dataLevel($armNum)) >= $minimumRank;
    }

    /** Reaches an export level on one arm. */
    public function reachesExport(int $armNum, int $minimumRank): bool
    {
        return self::exportRank($this->exportLevel($armNum)) >= $minimumRank;
    }

    public function canView(int $armNum): bool
    {
        return $this->reachesData($armNum, self::dataRank('read_only'));
    }

    public function canEdit(int $armNum): bool
    {
        return $this->reachesData($armNum, self::dataRank('view_edit'));
    }

    public function canDelete(int $armNum): bool
    {
        return $this->reachesData($armNum, self::dataRank('delete'));
    }

    /** Any arm at all reaches this level — used by project-level gates. */
    public function anyArmReachesData(int $minimumRank): bool
    {
        foreach ($this->dataByArm as $level) {
            if (self::dataRank($level) >= $minimumRank) {
                return true;
            }
        }

        return false;
    }

    /** The export card's gate: a non-`export_none` level on some arm (§6.4). */
    public function canExportAny(): bool
    {
        foreach ($this->exportByArm as $level) {
            if (self::exportRank($level) > 0) {
                return true;
            }
        }

        return false;
    }

    /** Every arm the project has, with the acting user's levels — for arm tabs. */
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
