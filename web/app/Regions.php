<?php
// The preferred region on the web side (master spec "Organization by hospital regions",
// User_Interface_Design.md §2.8/§4/§5.2/§5.13): the region codes in REQ-DB-006 order with their
// display text, and the grouping the Control Panel project list shares with the overview
// (REQ-UI-076/077). A region is a project organization code — the codes and texts are those of
// ControlPanelController::ORGANIZATIONS, the API keeps the same codes as its allowlist — and it
// only orders what a page lists: nothing here decides what anyone may see (REQ-AUTH-089).

declare(strict_types=1);

namespace Clara;

use Clara\Controllers\ControlPanelController;

final class Regions
{
    /** @return list<string> the codes in REQ-DB-006 order — the order groups follow */
    public static function codes(): array
    {
        return array_keys(ControlPanelController::ORGANIZATIONS);
    }

    public static function isCode(string $code): bool
    {
        return array_key_exists($code, ControlPanelController::ORGANIZATIONS);
    }

    /**
     * A code's display text — the project form's (§5.2), OTHER translated. A code the
     * enumeration no longer offers is shown as itself.
     */
    public static function label(I18n $i18n, string $code): string
    {
        $text = ControlPanelController::ORGANIZATIONS[$code] ?? $code;

        return $text !== '' ? $text : $i18n->t('admin.projects.organization.' . strtolower($code));
    }

    /**
     * Every code with its display text, in REQ-DB-006 order — a region select's options.
     *
     * @return array<string, string>
     */
    public static function options(I18n $i18n): array
    {
        $out = [];
        foreach (self::codes() as $code) {
            $out[$code] = self::label($i18n, $code);
        }

        return $out;
    }

    /**
     * Projects grouped by organization (REQ-UI-077): the group the API flags as the acting user's
     * region (`in_preferred_region`) first, the others in REQ-DB-006 order, a code outside it after
     * them in code order — the overview's rule (§4), which dashboard.js applies in the browser. Inside
     * a group the projects keep the order they came in; the caller sorts them first.
     *
     * @param list<array<string, mixed>> $projects
     * @return list<array{code: string, preferred: bool, projects: list<array<string, mixed>>}>
     */
    public static function group(array $projects): array
    {
        $groups = [];
        foreach ($projects as $project) {
            $code = (string) ($project['organization'] ?? '');
            $groups[$code] ??= ['code' => $code, 'preferred' => false, 'projects' => []];
            $groups[$code]['projects'][] = $project;
            if (($project['in_preferred_region'] ?? false) === true) {
                $groups[$code]['preferred'] = true;
            }
        }

        $order = array_flip(self::codes());
        $groups = array_values($groups);
        usort($groups, static fn (array $a, array $b): int =>
            [!$a['preferred'], $order[$a['code']] ?? count($order), $a['code']]
            <=> [!$b['preferred'], $order[$b['code']] ?? count($order), $b['code']]);

        return $groups;
    }
}
