<?php
// The two left panels of the application in one place (§2.4, REQ-UI-046): what each
// panel lists, in which order, and which entry a page opens on. Both panels are
// computed from permissions plus a `built` flag rather than hard-coded in a template,
// because an entry whose page this build does not serve yet is absent from the panel —
// a link to a 404 is a disabled control by another name (§3.1, REQ-UI-003). Landing
// milestones flip `built` to true and the panel, the default section, and the header's
// Control Panel button all follow from here.

declare(strict_types=1);

namespace Clara;

final class Navigation
{
    /**
     * Control Panel sections (§5, REQ-UI-047) in panel order. Every one is `is_admin` —
     * the route guard decides that; this list decides what the panel shows.
     *
     * @return list<array{key: string, labelKey: string, built: bool}>
     */
    public static function adminSections(): array
    {
        return [
            ['key' => 'users', 'labelKey' => 'admin.section.users', 'built' => true],
            ['key' => 'projects', 'labelKey' => 'admin.section.projects', 'built' => true],
            ['key' => 'audits', 'labelKey' => 'admin.section.audits', 'built' => true],
            ['key' => 'translations', 'labelKey' => 'admin.section.translations', 'built' => true],
            ['key' => 'settings', 'labelKey' => 'admin.section.settings', 'built' => true],
        ];
    }

    /** The sections this build serves — what the panel and the header button offer. */
    public static function availableAdminSections(): array
    {
        return array_values(array_filter(
            self::adminSections(),
            static fn (array $section): bool => $section['built']
        ));
    }

    /** True when the header's Control Panel button has somewhere to go (§2.4). */
    public static function controlPanelExists(): bool
    {
        return self::availableAdminSections() !== [];
    }

    /**
     * The section to render: the requested one when this build serves it, otherwise the
     * first available — an unknown `?section=` is not an error (§2.1).
     */
    public static function resolveAdminSection(string $requested): array
    {
        foreach (self::availableAdminSections() as $section) {
            if ($section['key'] === $requested) {
                return $section;
            }
        }

        return self::availableAdminSections()[0] ?? [];
    }

    /**
     * The project page's candidate entries (§6.1, REQ-UI-017) in canonical order, each
     * carrying the two answers the panels need: may this member use it (`allowed`), and does
     * this build serve it (`built`). Splitting them out keeps the permission rule — Setup and
     * Design for `project_admin`, Record Status Dashboard at data access ≥ `read_only`,
     * Export at a non-`export_none` level on some arm, Members and Roles for `is_admin`,
     * Groups readable at `read_only` (REQ-UI-003) — checkable whether or not the section's
     * page exists yet.
     *
     * @return list<array{key: string, path: string, labelKey: string, allowed: bool, built: bool}>
     */
    public static function projectSectionDefinitions(int $projectId, Permissions $permissions): array
    {
        $base = '/projects/' . $projectId;
        $seesData = $permissions->anyArmReachesData(Permissions::dataRank('read_only'));

        return [
            ['key' => 'overview', 'path' => $base . '/overview', 'labelKey' => 'nav.overview',
                'allowed' => true, 'built' => true],
            ['key' => 'setup', 'path' => $base . '/setup', 'labelKey' => 'nav.setup',
                'allowed' => $permissions->projectAdmin, 'built' => false],
            ['key' => 'design', 'path' => $base . '/design', 'labelKey' => 'nav.design',
                'allowed' => $permissions->projectAdmin, 'built' => false],
            ['key' => 'record_status', 'path' => $base . '/record-status', 'labelKey' => 'nav.record_status',
                'allowed' => $seesData, 'built' => false],
            ['key' => 'export', 'path' => $base . '/export', 'labelKey' => 'nav.export',
                'allowed' => $permissions->canExportAny(), 'built' => false],
            ['key' => 'members', 'path' => $base . '/members', 'labelKey' => 'nav.members',
                'allowed' => Session::isAdmin(), 'built' => true],
            ['key' => 'roles', 'path' => $base . '/roles', 'labelKey' => 'nav.roles',
                'allowed' => Session::isAdmin(), 'built' => true],
            ['key' => 'groups', 'path' => $base . '/groups', 'labelKey' => 'nav.groups',
                'allowed' => $seesData, 'built' => true],
        ];
    }

    /**
     * The project page's left panel: the entries this member may use **and** this build
     * serves. An entry whose page does not exist yet is absent rather than disabled (§3.1,
     * REQ-UI-003), so landing milestones change one `built` flag and nothing else.
     *
     * @return list<array{key: string, path: string, labelKey: string}>
     */
    public static function projectSections(int $projectId, Permissions $permissions): array
    {
        $sections = [];
        foreach (self::projectSectionDefinitions($projectId, $permissions) as $section) {
            if (!$section['allowed'] || !$section['built']) {
                continue;
            }
            unset($section['allowed'], $section['built']);
            $sections[] = $section;
        }

        return $sections;
    }

    /**
     * Where entering a project lands (REQ-UI-017): the first entry after Overview —
     * Setup for a member who may change the setup, Record Status Dashboard for one with
     * data access — falling back to Overview when neither is available. Only those two
     * are landing candidates: Members, Roles or Groups also come after Overview in the
     * panel, but the rule names Setup and Record Status Dashboard alone.
     */
    public static function defaultProjectSection(int $projectId, Permissions $permissions): string
    {
        foreach (self::projectSections($projectId, $permissions) as $section) {
            if (in_array($section['key'], ['setup', 'record_status'], true)) {
                return $section['path'];
            }
        }

        return '/projects/' . $projectId . '/overview';
    }
}
