<?php
/**
 * Control Panel — Audits (§5.6, REQ-UI-016). Read-only: a filter bar (a GET form, so a page
 * is reloadable — §3.6), the reverse-chronological table, and each entry's `details` as an
 * escaped key/value list in a disclosure (§3.2, REQ-UI-004). There is no write control on this
 * page (REQ-DB-024). Pagination follows the API's opaque forward cursor: "Next" carries it,
 * and "First page" returns to the newest entries (the cursor cannot be walked backwards).
 */

use Clara\View;

/** @var array<string, string> $filters */
/** @var list<array<string, mixed>> $entries */
$projectNames = [];
foreach ($projects as $project) {
    $projectNames[(int) $project['id']] = (string) $project['project_name'];
}
$pageQuery = static function (array $filters, string $cursor = ''): string {
    $query = ['section' => 'audits'];
    foreach ($filters as $key => $value) {
        if ($value !== '' && $value !== '0') {
            $query[$key] = $value;
        }
    }
    if ($cursor !== '') {
        $query['cursor'] = $cursor;
    }

    return '/admin?' . http_build_query($query);
};
$renderValue = static fn (mixed $value): string => is_scalar($value) || $value === null
    ? View::e($value === null ? 'null' : (is_bool($value) ? ($value ? 'true' : 'false') : (string) $value))
    : View::e((string) json_encode($value, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE));
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.audits.title')) ?></h1>

<form method="get" action="/admin" class="row g-2 align-items-end mb-3 clara-audit-filters">
    <input type="hidden" name="section" value="audits">
    <div class="col-6 col-md-2">
        <label class="form-label mb-0 small" for="audit-type"><?= View::e($view->t('admin.audits.type')) ?></label>
        <select class="form-select form-select-sm" id="audit-type" name="type">
            <?php foreach (['events', 'views'] as $type): ?>
                <option value="<?= $type ?>"<?= $filters['type'] === $type ? ' selected' : '' ?>><?= View::e($view->t('admin.audits.type.' . $type)) ?></option>
            <?php endforeach; ?>
        </select>
    </div>
    <div class="col-6 col-md-2">
        <label class="form-label mb-0 small" for="audit-project"><?= View::e($view->t('admin.audits.project')) ?></label>
        <select class="form-select form-select-sm" id="audit-project" name="project">
            <option value=""><?= View::e($view->t('admin.audits.all')) ?></option>
            <?php foreach ($projectNames as $id => $name): ?>
                <option value="<?= $id ?>"<?= $filters['project'] === (string) $id ? ' selected' : '' ?>><?= View::e($name) ?></option>
            <?php endforeach; ?>
        </select>
    </div>
    <div class="col-6 col-md-2">
        <label class="form-label mb-0 small" for="audit-user"><?= View::e($view->t('admin.audits.user')) ?></label>
        <select class="form-select form-select-sm" id="audit-user" name="user">
            <option value=""><?= View::e($view->t('admin.audits.all')) ?></option>
            <?php foreach ($users as $user): ?>
                <option value="<?= (int) $user['id'] ?>"<?= $filters['user'] === (string) $user['id'] ? ' selected' : '' ?>><?= View::e($user['email'] ?? '') ?></option>
            <?php endforeach; ?>
        </select>
    </div>
    <div class="col-6 col-md-2">
        <label class="form-label mb-0 small" for="audit-event"><?= View::e($view->t('admin.audits.event_type')) ?></label>
        <select class="form-select form-select-sm" id="audit-event" name="event_type">
            <option value=""><?= View::e($view->t('admin.audits.all')) ?></option>
            <?php foreach ($eventTypes as $code): ?>
                <option value="<?= View::e($code) ?>"<?= $filters['event_type'] === $code ? ' selected' : '' ?>><?= View::e($code) ?></option>
            <?php endforeach; ?>
        </select>
    </div>
    <div class="col-6 col-md-1">
        <label class="form-label mb-0 small" for="audit-from"><?= View::e($view->t('admin.audits.from')) ?></label>
        <input class="form-control form-control-sm" type="date" id="audit-from" name="from" value="<?= View::e($filters['from']) ?>">
    </div>
    <div class="col-6 col-md-1">
        <label class="form-label mb-0 small" for="audit-to"><?= View::e($view->t('admin.audits.to')) ?></label>
        <input class="form-control form-control-sm" type="date" id="audit-to" name="to" value="<?= View::e($filters['to']) ?>">
    </div>
    <div class="col-12 col-md-2">
        <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('admin.audits.filter')) ?></button>
        <a class="btn btn-sm btn-link" href="/admin?section=audits"><?= View::e($view->t('admin.audits.reset')) ?></a>
    </div>
</form>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-audits">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('admin.audits.when')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.audits.source')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.audits.project')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.audits.user')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.audits.event_type')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.audits.record')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.audits.details')) ?></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($entries === []): ?>
            <tr><td colspan="7" class="clara-region-empty"><?= View::e($view->t('admin.audits.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($entries as $entry): ?>
            <?php
            $projectId = $entry['project_id'] ?? null;
            $details = is_array($entry['details'] ?? null) ? $entry['details'] : [];
            // A record-view entry lists its records (REQ-AUD-008); an event names one.
            $record = $entry['target_record'] ?? ($entry['record_ids'] ?? '');
            $record = is_array($record) ? implode(', ', array_map('strval', $record)) : (string) $record;
            ?>
            <tr>
                <td class="text-nowrap small"><?= View::e($entry['created_at'] ?? '') ?></td>
                <td><span class="badge text-bg-light"><?= View::e($entry['source'] ?? '') ?></span></td>
                <td class="small"><?= View::e($projectId === null ? '' : ($projectNames[(int) $projectId] ?? '#' . (int) $projectId)) ?></td>
                <td class="small"><?= View::e($entry['email'] ?? '') ?></td>
                <td><code><?= View::e($entry['event_type'] ?? ($filters['type'] === 'views' ? 'record_view' : '')) ?></code></td>
                <td class="small"><?= View::e($record) ?></td>
                <td class="small">
                    <?php if ($details !== []): ?>
                        <details>
                            <summary class="text-truncate d-inline-block clara-audit-preview"><?= View::e(mb_strimwidth((string) json_encode($details, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), 0, 60, '…')) ?></summary>
                            <dl class="row mb-0 mt-1">
                                <?php foreach ($details as $key => $val): ?>
                                    <dt class="col-sm-4 fw-normal text-body-secondary"><?= View::e((string) $key) ?></dt>
                                    <dd class="col-sm-8 mb-1 text-break"><?= $renderValue($val) ?></dd>
                                <?php endforeach; ?>
                            </dl>
                        </details>
                    <?php endif; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>

<nav class="d-flex gap-2" aria-label="<?= View::e($view->t('admin.audits.pages')) ?>">
    <?php if (($cursor ?? '') !== ''): ?>
        <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($pageQuery($filters)) ?>"><?= View::e($view->t('admin.audits.first')) ?></a>
    <?php endif; ?>
    <?php if (($nextCursor ?? '') !== ''): ?>
        <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($pageQuery($filters, $nextCursor)) ?>"><?= View::e($view->t('admin.audits.next')) ?></a>
    <?php endif; ?>
</nav>
