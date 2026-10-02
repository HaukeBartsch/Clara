<?php
// The project workspace landing page (`User_Interface_Design.md` §6.1, REQ-UI-017):
// the summary and the read-only metadata block for one project, plus the action
// cards that lead into the rest of the workspace.
//
// One read serves the whole page — `GET /api/v1/projects/{id}` carries the metadata,
// the structure the counts come from, and the acting user's effective permissions
// (REQ-API-126). The permissions block is disclosure only: it decides which cards and
// sidebar entries exist, and every call behind them is re-checked by the API
// (REQ-AUTH-033, Plan/Web_Implementation.md §7 rule 15).
//
// Gating is the API's own — a member with no data access on any arm is refused by the
// read itself (§4.5) and lands on the §3.4 refusal page; PHP adds no visibility rule of
// its own (REQ-API-007).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Permissions;
use Clara\Response;

final class ProjectController extends Controller
{
    /** GET /projects/{id} — the rendered page. */
    public function index(): Response
    {
        $detail = $this->projectDetail();
        $permissions = Permissions::fromProjectDetail($detail);

        /*
         * Action cards (§6.1): one per workspace surface, present only when the acting
         * user may use it — and only when this build has the page it leads to, because a
         * card that opens a 404 is a disabled control by another name (§3.1, REQ-UI-003).
         * Members and Roles arrive with M3, Setup and Design with M4, Record status and
         * Export with M5; each adds one row here and one in the sidebar's project-context
         * section, gated by the same $permissions object this page already holds.
         */
        $cards = [];

        return $this->page('project', [
            // The browser tab carries the project's own name — data, not a UI string, so it
            // is not translated (the shell falls back to the application name when empty).
            'pageTitle' => $this->projectName($detail),
            'project' => $this->metadata($detail),
            'summary' => $this->summary($detail),
            'cards' => $cards,
            // The brand bar names the project while the user is inside it (§2.4).
            'brandProject' => $this->projectName($detail),
            'brandProjectUrl' => '/projects/' . (int) ($detail['id'] ?? 0),
            // The sidebar's Projects section (§2.4 item 1) — one list read per render,
            // never one per entry (Plan/Web_Implementation.md §7 rule 13).
            'sidebarProjects' => $this->visibleProjects(),
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js'],
        ]);
    }

    /**
     * The `project` data region of this route (REQ-UI-044): the same summary and
     * metadata the rendered page shows, and nothing beyond what that page discloses.
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
