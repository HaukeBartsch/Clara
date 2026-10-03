<?php
// The project page (`User_Interface_Design.md` §6.1, REQ-UI-017): a left panel of the
// project's own functions with the selected one in the right-hand panel. This controller
// serves two routes of that shell — the entry point `GET /projects/{id}`, which resolves
// the first available section and answers 303, and Overview, which shows the summary, the
// read-only metadata block and the mode badge.
//
// One read serves a render — `GET /api/v1/projects/{id}` carries the metadata, the
// structure the counts come from, and the acting user's effective permissions
// (REQ-API-126). The permissions block is disclosure only: it decides which panel entries
// exist and which section opens, and every call behind them is re-checked by the API
// (REQ-AUTH-033, Plan/Web_Implementation.md §7 rule 15).
//
// Gating is the API's own — a member with no data access on any arm is refused by the
// read itself (§4.5) and lands on the §3.4 refusal page; PHP adds no visibility rule of
// its own (REQ-API-007).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Navigation;
use Clara\Permissions;
use Clara\Response;

final class ProjectController extends Controller
{
    /**
     * GET /projects/{id} — entering a project (§6.1, REQ-UI-017). Not a page of its own:
     * the first available section after Overview opens — Setup for a member who may change
     * the setup, Record Status Dashboard for one with data access, Overview for nobody else
     * having a way in. The permission read this needs is the same detail read the entry
     * makes anyway, which is why the project-overview page does not resolve it per row
     * (REQ-API-049 carries no permissions block).
     */
    public function index(): Response
    {
        $detail = $this->projectDetail();

        return Response::redirect(
            Navigation::defaultProjectSection($this->projectIdOf($detail), Permissions::fromProjectDetail($detail)),
            303
        );
    }

    /** GET /projects/{id}/overview — the project's own summary page (§6.1). */
    public function overview(): Response
    {
        $detail = $this->projectDetail();
        $projectId = $this->projectIdOf($detail);

        return $this->page('overview', [
            // The browser tab carries the project's own name — data, not a UI string, so it
            // is not translated (the shell falls back to the application name when empty).
            'pageTitle' => $this->projectName($detail),
            'metadata' => $this->metadata($detail),
            'summary' => $this->summary($detail),
            // The header names the project while the user is inside it, and the name goes
            // back to this page (§2.4).
            'brandProject' => $this->projectName($detail),
            'brandProjectUrl' => '/projects/' . $projectId . '/overview',
            // The project's left panel (§6.1): every entry this build serves and this
            // member may use, in canonical order (REQ-UI-003).
            'nav' => [
                'headingKey' => 'nav.project_sections',
                'items' => Navigation::projectSections($projectId, Permissions::fromProjectDetail($detail)),
            ],
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js'],
        ]);
    }

    /**
     * The `project` data region of this route (REQ-UI-044): the same summary and
     * metadata the rendered Overview shows, and nothing beyond what that page discloses.
     * A data request is answered with the object rather than with the entry's redirect.
     */
    public function data(): Response
    {
        $detail = $this->projectDetail();

        return Response::json([
            'summary' => $this->summary($detail),
            'metadata' => $this->metadata($detail),
        ]);
    }

    // --- the read, once -------------------------------------------------------

    /**
     * The detail object of §4.5 for this route's project id. A non-numeric id names no
     * project at all, and answering 404 here is what keeps the page from sending it to
     * the API as a path segment (the API would read it as an unknown id anyway).
     *
     * @return array<string, mixed>
     */
    private function projectDetail(): array
    {
        $raw = $this->request->pathParam('id');
        if (preg_match('/^\d{1,18}$/', $raw) !== 1) {
            throw new ApiException('not_found', '', 404);
        }

        $detail = $this->api->get('/api/v1/projects/' . (int) $raw);

        return is_array($detail) ? $detail : [];
    }

    /**
     * The id to build the project's own routes from: the detail object's, falling back to
     * the path parameter when a stubbed or partial read carries none.
     *
     * @param array<string, mixed> $detail
     */
    private function projectIdOf(array $detail): int
    {
        return (int) ($detail['id'] ?? $this->request->pathParam('id'));
    }

    /**
     * The three counts of §6.1: the record total the detail carries, and the structure
     * counts derived from that same read — instruments, and their fields summed
     * (API_Endpoints_Design.md §4.5).
     *
     * @param array<string, mixed> $detail
     * @return array{records: int, instruments: int, fields: int}
     */
    private function summary(array $detail): array
    {
        $instruments = is_array($detail['instruments'] ?? null) ? $detail['instruments'] : [];
        $fields = 0;
        foreach ($instruments as $instrument) {
            if (is_array($instrument)) {
                $fields += (int) ($instrument['field_count'] ?? 0);
            }
        }

        return [
            'records' => (int) ($detail['record_count'] ?? 0),
            'instruments' => count($instruments),
            'fields' => $fields,
        ];
    }

    /**
     * The read-only metadata block of §6.1 — editing it belongs to §5.2. Empty values
     * are dropped so the block holds only what the study actually recorded; dates and
     * timestamps are passed through exactly as stored (Plan/Web_Implementation.md §7
     * rule 9), which is also why nothing here formats them.
     *
     * @return array<string, string>
     */
    private function metadata(array $detail): array
    {
        $fields = [
            'project_name', 'organization', 'pi_name', 'pi_email', 'dm_name', 'dm_email',
            'rek_number', 'rek_start_date', 'rek_end_date', 'start_date', 'end_date',
            'participant_names',
        ];

        $out = [];
        foreach ($fields as $field) {
            $value = $detail[$field] ?? null;
            if ($value === null || $value === '') {
                continue;
            }
            $out[$field] = is_scalar($value) ? (string) $value : '';
        }

        return $out;
    }

    /** @param array<string, mixed> $detail */
    private function projectName(array $detail): string
    {
        return (string) ($detail['project_name'] ?? '');
    }
}
