<?php
// The data-entry rules of User_Interface_Design.md §8 that are pure functions of what the API
// returned and what the browser posted — kept apart from the controllers so each one is
// testable without a page around it (Plan/Web_Implementation.md §8):
//
//   * current values from the record history, walked most-recent-first (§8.3, REQ-API-137);
//   * the form's render items — single fields, matrix groups, section headings (§8.2);
//   * the submission policy (GD-14, REQ-UI-031): exactly the fields that carry a value plus
//     the fields whose stored value the user removed;
//   * the per-field detail of a rejected import (§3.7.2, §8.6 step 4).

declare(strict_types=1);

namespace Clara;

final class DataEntry
{
    /** Field types that take a value in the form (§8.2); description, header, calculated never do. */
    public const ENTERABLE_TYPES = ['text', 'notes', 'dropdown', 'radio', 'checkbox', 'yesno', 'matrix'];

    /** Separates a checkbox's name from a choice code in its value columns (REQ-DB-042). */
    public const CHOICE_SEPARATOR = '___';

    /** The three completion states of record-status and the dropdown (§6.3, §8.5). */
    public const STATES = ['no_data', 'some_data', 'finished'];

    /** One history page (the API caps `limit` at 200, §1) and the walk's ceiling. */
    public const HISTORY_PAGE = 200;
    public const HISTORY_MAX_PAGES = 25;

    /** Import row keys that are not fields (§3.7.1) — never taken from a posted value map. */
    private const RESERVED_KEYS = ['record_id', 'form_name', 'event_name', 'redcap_event_name',
        'redcap_repeat_instrument', 'redcap_repeat_instance'];

    private function __construct() {}

    /**
     * A record name a user may type for a new participant (§6.3): non-empty, at most 100
     * characters, no control characters and no slash — the name travels as one path segment
     * of `/projects/{id}/records/{record}` and of the API's record routes.
     */
    public static function validRecordName(string $name): bool
    {
        return $name !== ''
            && mb_strlen($name) <= 100
            && trim($name) === $name
            && preg_match('/[\x00-\x1F\x7F\/\\\\]/', $name) === 0;
    }

    /** True for a field the form renders as an input (§8.2). */
    public static function isEnterable(array $field): bool
    {
        return in_array((string) ($field['field_type'] ?? ''), self::ENTERABLE_TYPES, true);
    }

    /**
     * The names a field's values are stored and imported under (REQ-DB-042): a checkbox has one
     * per choice, `<field_name>___<code>` — checked stores `1`, unchecked nothing — and every
     * other field its own name.
     *
     * @return list<string>
     */
    public static function valueNames(array $field): array
    {
        $name = (string) ($field['field_name'] ?? '');
        if (($field['field_type'] ?? '') !== 'checkbox') {
            return [$name];
        }

        return array_map(static fn (array $choice): string => $name . self::CHOICE_SEPARATOR . $choice['code'],
            self::choices((string) ($field['choices'] ?? '')));
    }

    /**
     * True when `$name` is one of the value columns of a checkbox called `$field` — the shape a
     * history entry of that checkbox carries (REQ-DB-042). A checkbox code is lower-case
     * letters, digits and `_` (REQ-VAL-022).
     */
    public static function isChoiceColumnOf(string $name, string $field): bool
    {
        return preg_match('/^' . preg_quote($field . self::CHOICE_SEPARATOR, '/') . '[a-z0-9_]+$/', $name) === 1;
    }

    /**
     * True for a field whose annotation carries `@READONLY` (REQ-DB-046): the form and the
     * survey page show its value and take no input for it. The tag counts as a whole word in
     * any letter case; REDCap's `@READONLY-FORM` and `@READONLY-SURVEY` are other tags.
     */
    public static function isReadonly(array $field): bool
    {
        return preg_match('/(?<![\w@-])@READONLY(?![\w-])/i', (string) ($field['field_annotation'] ?? '')) === 1;
    }

    /**
     * Posted values without the read-only fields' value names (REQ-DB-046): the browser is not
     * trusted to leave them out, so a value posted for one never reaches the import.
     *
     * @param array<string, string>      $values
     * @param list<array<string, mixed>> $fields the instrument's fields
     * @return array<string, string>
     */
    public static function withoutReadonly(array $values, array $fields): array
    {
        foreach ($fields as $field) {
            if (self::isReadonly($field)) {
                foreach (self::valueNames($field) as $name) {
                    unset($values[$name]);
                }
            }
        }

        return $values;
    }

    /**
     * A free-text field: `text` without a validation type, or `notes` — multi-line free text
     * (Data_Validation_Design.md §5, REQ-DB-044).
     */
    public static function isFreeText(array $field): bool
    {
        $type = $field['field_type'] ?? '';

        return $type === 'notes' || ($type === 'text' && (string) ($field['validation_type'] ?? '') === '');
    }

    /**
     * The choices a form shows for a field: a yesno field's fixed coding (`1` = Yes, `0` = No)
     * carries the labels of the interface language (REQ-DB-043) — `$t` translates a key — and
     * every other field its stored labels.
     *
     * @param callable(string): string $t
     * @return list<array{code: string, label: string}>
     */
    public static function shownChoices(array $field, callable $t): array
    {
        $choices = self::choices((string) ($field['choices'] ?? ''));
        if (($field['field_type'] ?? '') !== 'yesno') {
            return $choices;
        }

        return array_map(static fn (array $choice): array => [
            'code' => $choice['code'],
            'label' => match ($choice['code']) { '1' => $t('record.yes'), '0' => $t('record.no'), default => $choice['label'] },
        ], $choices);
    }

    /**
     * Current values from the record history (§8.3): each (event, field)'s value is the one
     * its most recent entry left — the `new` side of a create/update, nothing after a delete.
     * The history is read newest-first (`order=newest`, REQ-API-137) and the walk stops as
     * soon as every wanted key has been seen, so opening a form costs pages over the recent
     * changes rather than the whole history (Plan §7 rule 14). `$fetch(?string $cursor)`
     * returns one decoded page: `{entries: […], next_cursor: string|null}`.
     *
     * Keys are `<unique_event_name>|<field_name>`. A key that no entry mentions is absent —
     * it has no stored value.
     *
     * @param list<string> $wanted
     * @return array{values: array<string, string>, truncated: bool}
     */
    public static function currentValues(callable $fetch, array $wanted, int $maxPages = self::HISTORY_MAX_PAGES): array
    {
        $open = array_fill_keys($wanted, true);
        $seen = [];
        $cursor = null;
        $pages = 0;
        $truncated = false;

        while ($open !== []) {
            if ($pages >= $maxPages) {
                $truncated = true;
                break;
            }
            $page = $fetch($cursor);
            $pages++;

            foreach ((is_array($page['entries'] ?? null) ? $page['entries'] : []) as $entry) {
                if (!is_array($entry)) {
                    continue;
                }
                $event = (string) ($entry['event'] ?? '');
                $deleted = ($entry['action'] ?? '') === 'delete';
                foreach ((is_array($entry['fields'] ?? null) ? $entry['fields'] : []) as $change) {
                    $key = $event . '|' . (string) ($change['field'] ?? '');
                    if (!isset($open[$key])) {
                        continue; // not wanted, or already settled by a newer entry
                    }
                    $new = $change['new'] ?? null;
                    $seen[$key] = $deleted || !is_scalar($new) ? '' : (string) $new;
                    unset($open[$key]);
                }
            }

            $cursor = $page['next_cursor'] ?? null;
            if (!is_string($cursor) || $cursor === '') {
                break;
            }
        }

        // A value the history last cleared is no value at all.
        return ['values' => array_filter($seen, static fn (string $v): bool => $v !== ''), 'truncated' => $truncated];
    }

    /**
     * The field references of a branching expression — `[event][field]` as written,
     * `[field]` resolved to the project's first event in canonical order (Data_Validation_Design
     * §7.1, GD-15). Only used to decide which values outside the form the evaluator needs;
     * string literals are skipped so a bracket inside quotes is not read as a reference, and
     * collecting one candidate too many costs nothing.
     *
     * @return list<string> `<event>|<field>` keys
     */
    public static function references(string $expression, string $firstEvent): array
    {
        // String literals are text — double-quoted, or single-quoted in REDCap's spelling
        // (REQ-VAL-044).
        $code = preg_replace('/"(?:[^"\\\\]|\\\\.)*"?|\'(?:[^\'\\\\]|\\\\.)*\'?/', '""', $expression) ?? '';
        preg_match_all('/\[([^\]]*)\](?:\[([^\]]*)\])?/', $code, $matches, PREG_SET_ORDER);

        $keys = [];
        foreach ($matches as $m) {
            $key = isset($m[2]) && $m[2] !== ''
                ? $m[1] . '|' . self::boxField($m[2])
                : $firstEvent . '|' . self::boxField($m[1]);
            $keys[$key] = true;
        }

        return array_keys($keys);
    }

    /**
     * REDCap's checkbox spelling `field(code)` as the value column it names, `field___code`
     * (REQ-VAL-044, REQ-DB-042); any other reference segment passes through.
     */
    public static function boxField(string $segment): string
    {
        return preg_match('/^([A-Za-z0-9_]+)\(([A-Za-z0-9_-]+)\)$/', $segment, $m) === 1
            ? $m[1] . self::CHOICE_SEPARATOR . $m[2] : $segment;
    }

    /**
     * The form's render items in field order (§8.2): consecutive matrix fields sharing a group
     * form one item — a row per sub-field under one header that states the coding once —
     * and every other field is an item of its own. A field's section header opens the item.
     *
     * @param list<array<string, mixed>> $fields in position order
     * @return list<array{kind: string, group: string, section: string, fields: list<array<string, mixed>>, choices: list<array{code: string, label: string}>}>
     */
    public static function layout(array $fields): array
    {
        $items = [];
        foreach ($fields as $field) {
            $type = (string) ($field['field_type'] ?? '');
            $group = (string) ($field['matrix_group'] ?? '');
            $last = array_key_last($items);
            if ($type === 'matrix' && $group !== '' && $last !== null
                && $items[$last]['kind'] === 'matrix' && $items[$last]['group'] === $group
                && (string) ($field['section_header'] ?? '') === '') {
                $items[$last]['fields'][] = $field;
                continue;
            }
            $items[] = [
                'kind' => $type === 'matrix' && $group !== '' ? 'matrix' : 'field',
                'group' => $group,
                'section' => (string) ($field['section_header'] ?? ''),
                'fields' => [$field],
                'choices' => self::choices((string) ($field['choices'] ?? '')),
            ];
        }

        return $items;
    }

    /**
     * The stored `code$label##code$label` encoding as rows (REQ-VAL-022).
     *
     * @return list<array{code: string, label: string}>
     */
    public static function choices(string $encoded): array
    {
        if ($encoded === '') {
            return [];
        }
        $rows = [];
        foreach (explode('##', $encoded) as $pair) {
            if ($pair === '') {
                continue;
            }
            [$code, $label] = array_pad(explode('$', $pair, 2), 2, '');
            $rows[] = ['code' => $code, 'label' => $label];
        }

        return $rows;
    }

    /**
     * The submission policy of GD-14 / REQ-UI-031 (§8.6 step 2): a field is sent when it
     * carries a value, or — as an explicit empty string — when it had a stored value that the
     * user removed. A field that had no value and was left empty is not sent at all, because
     * an empty value reaching the API clears the stored one (REQ-VAL-024).
     *
     * `$was` is each field's prefill (§8.3) as the form carried it; the comparison is done
     * here rather than only in the browser so the rule holds with JavaScript off as well.
     * Only names in field syntax are taken, and never an import row key (§3.7.1). A text area
     * posts its line breaks as CRLF (the HTML form encoding); they go on as LF, the form
     * `Data_Validation_Design.md` §5.1 step 2 stores, so an untouched `notes` value
     * (REQ-DB-044) arrives exactly as it is stored.
     *
     * @param array<string, string> $values posted values
     * @param array<string, string> $was    posted prefill
     * @return array<string, string>
     */
    public static function submission(array $values, array $was): array
    {
        $out = [];
        foreach (array_keys($values + $was) as $name) {
            $name = (string) $name;
            if (preg_match('/^[a-z0-9_]{1,100}$/', $name) !== 1 || in_array($name, self::RESERVED_KEYS, true)) {
                continue;
            }
            $value = str_replace("\r\n", "\n", $values[$name] ?? '');
            if ($value !== '') {
                $out[$name] = $value;
            } elseif (($was[$name] ?? '') !== '') {
                $out[$name] = '';
            }
        }

        return $out;
    }

    /**
     * The per-field detail of a rejected import row (§3.7.2): `Validation error: <field>:
     * <CODE> — <message>; …` split into one line per field. Text that names no field (a tuple
     * problem such as an unknown event) is kept under ''.
     *
     * @return array<string, string>
     */
    public static function importErrors(string $detail): array
    {
        $detail = preg_replace('/^Validation error:\s*/', '', $detail) ?? $detail;
        $out = [];
        foreach (explode('; ', $detail) as $part) {
            $part = trim($part);
            if ($part === '') {
                continue;
            }
            if (preg_match('/^([a-z0-9_]+): (.+)$/s', $part, $m) === 1) {
                $out[$m[1]] = isset($out[$m[1]]) ? $out[$m[1]] . '; ' . $m[2] : $m[2];
                continue;
            }
            $out[''] = isset($out['']) ? $out[''] . '; ' . $part : $part;
        }

        return $out;
    }

    /**
     * The browser's timezone as the import's `tz` (GD-16, REQ-VAL-041): an IANA name or a
     * `±HH:MM` offset; anything else is dropped and the API falls back to APP_TIMEZONE.
     */
    public static function validTimezone(string $tz): bool
    {
        return preg_match('#^(?:[A-Za-z][A-Za-z0-9_+\-]*(?:/[A-Za-z0-9_+\-]+){0,2}|[+-]\d{2}:\d{2})$#', $tz) === 1
            && strlen($tz) <= 64;
    }
}
