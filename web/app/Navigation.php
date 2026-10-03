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
            ['key' => 'users', 'labelKey' => 'admin.section.users', 'built' => false],
            ['key' => 'projects', 'labelKey' => 'admin.section.projects', 'built' => false],
            ['key' => 'audits', 'labelKey' => 'admin.section.audits', 'built' => false],
            ['key' => 'translations', 'labelKey' => 'admin.section.translations', 'built' => false],
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
     * The project page's left panel (§6.1, REQ-UI-017) in canonical order: Overview,
     * Setup, Design, Record Status Dashboard, Export, then the project-scoped
     * administration entries. Each appears only when the acting user may use it —
     * `project_admin` for Setup and Design, data access ≥ `read_only` for the record
     * status dashboard and for reading groups, a non-`export_none` level on some arm for
     * Export, `is_admin` for Members and Roles (REQ-UI-003).
     *
     * @return list<array{key: string, path: string, labelKey: string}>
     */
    public static function projectSections(int $projectId, Permissions $permissions): array
    {
        $base = '/projects/' . $projectId;

        // key => [path, labelKey, may the acting user use it, does this build serve it]
        $candidates = [
            ['overview', $base . '/overview', 'nav.overview', true, true],
            ['setup', $base . '/setup', 'nav.setup', $permissions->projectAdmin, false],
            ['design', $base . '/design', 'nav.design', $permissions->projectAdmin, false],
            ['record_status', $base . '/record-status', 'nav.record_status',
                $permissions->anyArmReachesData(Permissions::dataRank('read_only')), false],
            ['export', $base . '/export', 'nav.export', $permissions->canExportAny(), false],
            ['members', $base . '/members', 'nav.members', Session::isAdmin(), false],
            ['roles', $base . '/roles', 'nav.roles', Session::isAdmin(), false],
            ['groups', $base . '/groups', 'nav.groups',
                $permissions->anyArmReachesData(Permissions::dataRank('read_only')), false],
        ];

        $sections = [];
        foreach ($candidates as [$key, $path, $labelKey, $allowed, $built]) {
            if (!$allowed || !$built) {
                continue;
            }
            $sections[] = ['key' => $key, 'path' => $path, 'labelKey' => $labelKey];
        }

        return $sections;
    }

    /**
     * Where entering a project lands (REQ-UI-017): the first entry after Overview —
     * Setup for a member who may change the setup, Record Status Dashboard for one with
     * data access — falling back to Overview when neither is available. Overview never
     * wins by default; it is reached by its own entry.
     */
    public static function defaultProjectSection(int $projectId, Permissions $permissions): string
    {
        foreach (self::projectSections($projectId, $permissions) as $section) {
            if ($section['key'] !== 'overview') {
                return $section['path'];
            }
        }

        return '/projects/' . $projectId . '/overview';
    }
}
