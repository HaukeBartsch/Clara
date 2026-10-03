<?php
// Shared footing for the pages of the project shell (`User_Interface_Design.md` §6.1): the
// one detail read a render spends on its project, the mode read beside it, and the shell
// around a section's template — left panel, breadcrumb, mode badge.
//
// The detail read carries the acting user's effective permissions (REQ-API-126). They are
// disclosure only: they decide which panel entries and controls exist, and every call
// behind them is re-checked by the API (REQ-AUTH-033, Plan/Web_Implementation.md §7
// rule 15). Nothing here caches them beyond the request.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Navigation;
use Clara\Permissions;
use Clara\Response;

abstract class ProjectPageController extends Controller
{
    /** The three modes of GD-20. */
    public const MODES = ['development', 'production', 'analysis'];

    /**
     * The detail object of §4.5 for this route's project id. A non-numeric id names no
     * project at all, and answering 404 here is what keeps the page from sending it to
     * the API as a path segment (the API would read it as an unknown id anyway).
     *
     * @return array<string, mixed>
     */
    protected function projectDetail(): array
    {
        $detail = $this->api->get('/api/v1/projects/' . $this->projectIdFromPath());

        return is_array($detail) ? $detail : [];
    }

    /** The `{id}` of the route as a project id, or a 404 before any API call. */
    protected function projectIdFromPath(): int
    {
        $raw = $this->request->pathParam('id');
        if (preg_match('/^\d{1,18}$/', $raw) !== 1) {
            throw new ApiException('not_found', '', 404);
        }

        return (int) $raw;
    }

    /**
     * The id to build the project's own routes from: the detail object's, falling back to
     * the path parameter when a stubbed or partial read carries none.
     *
     * @param array<string, mixed> $detail
     */
    protected function projectIdOf(array $detail): int
    {
        return (int) ($detail['id'] ?? $this->request->pathParam('id'));
    }

    /** @param array<string, mixed> $detail */
    protected function projectName(array $detail): string
    {
        return (string) ($detail['project_name'] ?? '');
    }

    /**
     * `GET …/mode` (§4.21): the project's mode and whether a staging set is open. The badge
     * is information beside the name, so a refused or failed read degrades to "no mode"
     * ('' — the badge is omitted) instead of costing the user the page; a page that needs
     * the mode to decide what it offers treats '' as "offer nothing mode-dependent".
     *
     * @return array{mode: string, staging_open: bool}
     */
    protected function modeState(int $projectId): array
    {
        try {
            $read = $this->api->get('/api/v1/projects/' . $projectId . '/mode');
        } catch (ApiException $e) {
            $this->logger->info('project mode read failed', ['code' => $e->code(), 'status' => $e->status()]);

            return ['mode' => '', 'staging_open' => false];
        }
        $mode = is_array($read) ? (string) ($read['mode'] ?? '') : '';

        return [
            'mode' => in_array($mode, self::MODES, true) ? $mode : '',
            'staging_open' => is_array($read) && !empty($read['staging_open']),
        ];
    }

    /** The mode alone, for the badge (§6.6, REQ-UI-033). */
    protected function projectMode(int $projectId): string
    {
        return $this->modeState($projectId)['mode'];
    }

    /**
     * Renders one section of the project page: the shell around this section's template —
     * left panel, breadcrumb with the mode badge (§6.1, §6.6).
     *
     * @param array<string, mixed> $detail
     * @param array<string, mixed> $data
     * @param array<string, mixed> $options view options (scripts, jsKeys)
     */
    protected function renderSection(array $detail, string $template, string $titleKey, array $data, array $options, string $mode): Response
    {
        $projectId = $this->projectIdOf($detail);

        return $this->page($template, $data + [
            'pageTitle' => $this->i18n->t($titleKey) . ' · ' . $this->projectName($detail),
            'projectId' => $projectId,
            'projectName' => $this->projectName($detail),
            'brandProject' => $this->projectName($detail),
            'brandProjectUrl' => '/projects/' . $projectId . '/overview',
            'brandProjectMode' => $mode,
            'nav' => [
                'headingKey' => 'nav.project_sections',
                'items' => Navigation::projectSections($projectId, Permissions::fromProjectDetail($detail)),
            ],
        ], $options + ['titleKey' => '']);
    }

    /** A positive numeric body field as an id, or 0. */
    protected function fieldId(string $name): int
    {
        $raw = $this->request->field($name);

        return preg_match('/^\d{1,18}$/', $raw) === 1 ? (int) $raw : 0;
    }

    /**
     * A structure object id from the body — negative while a staging set is open, because
     * objects created in it carry provisional ids (Plan/Web_Implementation.md §7 rule 3) —
     * or 0 when the value names nothing.
     */
    protected function objectId(string $name): int
    {
        return self::parseObjectId($this->request->field($name));
    }

    /** Parses a structure object id: any non-zero integer (provisional ids are negative). */
    protected static function parseObjectId(string $raw): int
    {
        return preg_match('/^-?[1-9]\d{0,17}$/', $raw) === 1 ? (int) $raw : 0;
    }
}
