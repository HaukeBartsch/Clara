<?php
// What the two data pages of M5 share — the Record Status Dashboard (§6.3) and the record
// view (§8): the data-access gate, the project's structure as data entry sees it (arms the
// member may read, events in canonical order, the instruments mapped to each), and the way a
// data-API failure becomes a line for the user.
//
// The structure comes from the project detail read the page spends anyway (arms with their
// events in canonical order, instruments with positions — API §4.5) plus the instrument ×
// event mapping (§4.12): no read per arm, per event or per record (Plan §7 rule 13).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Permissions;

abstract class DataEntryController extends ProjectPageController
{
    /**
     * Data access ≥ `read_only` on some arm (§2.1 gating of both routes). The API refuses
     * the reads behind the page as well; refusing here keeps a member without data access
     * from a page of empty regions (REQ-UI-003).
     *
     * @param array<string, mixed> $detail
     */
    protected function requireDataAccess(array $detail): Permissions
    {
        $permissions = Permissions::fromProjectDetail($detail);
        if (!$permissions->anyArmReachesData(Permissions::dataRank('read_only'))) {
            throw new ApiException('forbidden', '', 403);
        }

        return $permissions;
    }

    /**
     * `GET …/instrument-event-mapping` as arm_num → instrument name → mapped event names.
     *
     * @return array<int, array<string, list<string>>>
     */
    protected function mapping(int $projectId): array
    {
        $out = [];
        foreach ($this->api->get('/api/v1/projects/' . $projectId . '/instrument-event-mapping') as $arm) {
            if (!is_array($arm)) {
                continue;
            }
            $byInstrument = [];
            foreach ((is_array($arm['mapping'] ?? null) ? $arm['mapping'] : []) as $name => $events) {
                $byInstrument[(string) $name] = is_array($events) ? array_values(array_map('strval', $events)) : [];
            }
            $out[(int) ($arm['arm_num'] ?? 0)] = $byInstrument;
        }

        return $out;
    }

    /**
     * The arms the member may read, each with its events in canonical order (GD-15) and, per
     * event, the instruments mapped to it in instrument order (REQ-API-074's column order).
     * An arm without read access is not listed at all (GD-2); an event with no instrument is
     * kept — it simply has nothing to enter.
     *
     * @param array<string, mixed>              $detail
     * @param array<int, array<string, list<string>>> $mapping
     * @param list<array<string, mixed>>        $instruments in position order
     * @return list<array{arm_num: int, name: string, events: list<array{unique_event_name: string, event_name: string, instruments: list<array<string, mixed>>}>}>
     */
    protected static function structure(array $detail, Permissions $permissions, array $mapping, array $instruments): array
    {
        $arms = [];
        foreach ((is_array($detail['arms'] ?? null) ? $detail['arms'] : []) as $arm) {
            if (!is_array($arm)) {
                continue;
            }
            $armNum = (int) ($arm['arm_num'] ?? 0);
            if (!$permissions->canView($armNum)) {
                continue;
            }
            $events = [];
            foreach ((is_array($arm['events'] ?? null) ? $arm['events'] : []) as $event) {
                if (!is_array($event)) {
                    continue;
                }
                $uen = (string) ($event['unique_event_name'] ?? '');
                $mapped = [];
                foreach ($instruments as $instrument) {
                    if (in_array($uen, $mapping[$armNum][(string) ($instrument['name'] ?? '')] ?? [], true)) {
                        $mapped[] = $instrument;
                    }
                }
                $events[] = [
                    'unique_event_name' => $uen,
                    'event_name' => (string) ($event['event_name'] ?? $uen),
                    'instruments' => $mapped,
                ];
            }
            $arms[] = ['arm_num' => $armNum, 'name' => (string) ($arm['name'] ?? ''), 'events' => $events];
        }

        return $arms;
    }

    /**
     * The project's instruments in position order: the detail's list (id, name, position) —
     * or, where the page needs the survey flag and branching logic, `GET …/instruments`.
     *
     * @param list<array<string, mixed>> $instruments
     * @return list<array<string, mixed>>
     */
    protected static function byPosition(array $instruments): array
    {
        $list = array_values(array_filter($instruments, 'is_array'));
        usort($list, static fn (array $a, array $b): int => (int) ($a['position'] ?? 0) <=> (int) ($b['position'] ?? 0));

        return $list;
    }

    /**
     * The project's first event in canonical order — what a `[field]` reference resolves to
     * (Data_Validation_Design.md §7.1, GD-15): arm 1's first event, from the detail read.
     *
     * @param array<string, mixed> $detail
     */
    protected static function firstEvent(array $detail): string
    {
        foreach ((is_array($detail['arms'] ?? null) ? $detail['arms'] : []) as $arm) {
            foreach ((is_array($arm['events'] ?? null) ? $arm['events'] : []) as $event) {
                if (is_array($event) && ($event['unique_event_name'] ?? '') !== '') {
                    return (string) $event['unique_event_name'];
                }
            }
        }

        return '';
    }

    /**
     * The line for a failed data-API call (§3.4, §8.6): the two states the data API reports
     * that are not permission refusals get their own text — the project is in analysis mode
     * (GD-20), or the acting user holds no token because they are not a member (an
     * administrator may see a project without belonging to it, REQ-API-102) — and everything
     * else maps exactly as an administration-API failure does.
     */
    protected function dataApiFailure(ApiException $e): string
    {
        $this->logger->info('data entry call rejected', ['code' => $e->code(), 'status' => $e->status()]);

        return match ($e->code()) {
            'analysis_mode' => $this->i18n->t('record.analysis_closed'),
            'not_member' => $this->i18n->t('record.not_member'),
            'invalid_token' => $this->i18n->t('record.token_refused'),
            default => Messages::forApiException($this->i18n, $e),
        };
    }

    /**
     * A data-API call with the member's token (§8.6).
     *
     * @param array<string, mixed> $params
     * @return array<mixed>
     */
    protected function dataCall(int $projectId, array $params): array
    {
        return $this->api->dataApi()->forMember($projectId, $params);
    }

    /** @return list<array<string, mixed>> */
    protected static function listOf(mixed $value): array
    {
        return is_array($value) ? array_values(array_filter($value, 'is_array')) : [];
    }

    /** The record name of the route, decoded from its path segment (§2.1 `{record}`). */
    protected function recordFromPath(): string
    {
        $record = rawurldecode($this->request->pathParam('record'));
        if ($record === '' || mb_strlen($record) > 255 || preg_match('/[\x00-\x1F\x7F]/', $record) === 1) {
            throw new ApiException('not_found', '', 404);
        }

        return $record;
    }

    /** The browser route of one record's view, optionally on one (event, instrument). */
    protected static function recordUrl(int $projectId, string $record, string $event = '', string $instrument = ''): string
    {
        $url = '/projects/' . $projectId . '/records/' . rawurlencode($record);
        $query = array_filter(['event' => $event, 'instrument' => $instrument], static fn (string $v): bool => $v !== '');

        return $query === [] ? $url : $url . '?' . http_build_query($query, '', '&', PHP_QUERY_RFC3986);
    }
}
