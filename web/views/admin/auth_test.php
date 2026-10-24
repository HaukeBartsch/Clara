<?php

use Clara\View;

$result = is_array($report['result'] ?? null) ? $report['result'] : [];
$ldapDetail = is_array($result['diagnostic'] ?? null) ? $result['diagnostic'] : [];
$ldapStages = array_intersect_key($ldapDetail, array_flip(['search', 'user_bind', 'identity']));
$ldapAttributes = is_array($ldapDetail['attributes'] ?? null) ? $ldapDetail['attributes'] : [];
$oauth = ($report['kind'] ?? '') === 'oauth' ? $report : [];
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.auth.title')) ?></h1>
<p class="text-body-secondary small"><?= View::e($view->t('admin.auth.intro')) ?></p>
<?php if ($error !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<section class="mb-4" aria-labelledby="clara-auth-ldap">
    <h2 class="h6" id="clara-auth-ldap"><?= View::e($view->t('admin.auth.ldap')) ?></h2>
    <?php if ($servers === []): ?>
        <p class="small text-body-secondary"><?= View::e($view->t('admin.auth.none')) ?></p>
    <?php endif; ?>
    <?php foreach ($servers as $server): ?>
        <div class="card mb-3">
            <div class="card-body">
                <h3 class="h6"><?= View::e($view->t('admin.auth.source', ['index' => (string) $server['index']])) ?></h3>
                <dl class="row small mb-3">
                    <dt class="col-sm-3"><?= View::e($view->t('admin.auth.url')) ?></dt><dd class="col-sm-9"><?= View::e($server['url']) ?></dd>
                    <dt class="col-sm-3"><?= View::e($view->t('admin.auth.bind_dn')) ?></dt><dd class="col-sm-9"><?= View::e($server['bind_dn']) ?></dd>
                    <dt class="col-sm-3"><?= View::e($view->t('admin.auth.search_base')) ?></dt><dd class="col-sm-9"><?= View::e($server['search_base']) ?></dd>
                    <dt class="col-sm-3"><?= View::e($view->t('admin.auth.attributes')) ?></dt>
                    <dd class="col-sm-9"><?= View::e($server['uid_attr'] . ', ' . $server['email_attr'] . ', ' . $server['name_attr']) ?></dd>
                    <dt class="col-sm-3"><?= View::e($view->t('admin.auth.names')) ?></dt><dd class="col-sm-9"><?= View::e(implode(', ', $server['names'])) ?></dd>
                </dl>
                <form method="post" action="/admin?action=test_ldap" autocomplete="off">
                    <?= $view->csrfField() ?>
                    <input type="hidden" name="section" value="auth">
                    <input type="hidden" name="server" value="<?= View::e((string) $server['index']) ?>">
                    <div class="row g-2 align-items-end">
                        <div class="col-md-4">
                            <label class="form-label" for="ldap-user-<?= View::e((string) $server['index']) ?>"><?= View::e($view->t('admin.auth.username')) ?></label>
                            <input class="form-control form-control-sm" id="ldap-user-<?= View::e((string) $server['index']) ?>" name="username" required maxlength="256" autocomplete="off">
                        </div>
                        <div class="col-md-4">
                            <label class="form-label" for="ldap-password-<?= View::e((string) $server['index']) ?>"><?= View::e($view->t('admin.auth.password')) ?></label>
                            <input class="form-control form-control-sm" type="password" id="ldap-password-<?= View::e((string) $server['index']) ?>" name="password" required maxlength="1024" autocomplete="new-password">
                        </div>
                        <div class="col-md-4"><button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('admin.auth.test')) ?></button></div>
                    </div>
                </form>
            </div>
        </div>
    <?php endforeach; ?>
</section>

<section class="mb-4" aria-labelledby="clara-auth-oauth">
    <h2 class="h6" id="clara-auth-oauth"><?= View::e($view->t('admin.auth.oauth')) ?></h2>
    <?php if ($providers === []): ?>
        <p class="small text-body-secondary"><?= View::e($view->t('admin.auth.none')) ?></p>
    <?php endif; ?>
    <?php foreach ($providers as $provider): ?>
        <div class="card mb-3"><div class="card-body">
            <h3 class="h6"><?= View::e($provider['issuer']) ?></h3>
            <dl class="row small mb-3">
                <dt class="col-sm-3"><?= View::e($view->t('admin.auth.client_id')) ?></dt><dd class="col-sm-9"><?= View::e($provider['client_id']) ?></dd>
                <dt class="col-sm-3"><?= View::e($view->t('admin.auth.redirect_uri')) ?></dt><dd class="col-sm-9"><?= View::e($provider['redirect_uri']) ?></dd>
                <dt class="col-sm-3"><?= View::e($view->t('admin.auth.email_claim')) ?></dt><dd class="col-sm-9"><?= View::e($provider['email_attr']) ?></dd>
                <dt class="col-sm-3"><?= View::e($view->t('admin.auth.names')) ?></dt><dd class="col-sm-9"><?= View::e(implode(', ', $provider['names'])) ?></dd>
            </dl>
            <form method="post" action="/admin?action=test_oauth">
                <?= $view->csrfField() ?>
                <input type="hidden" name="section" value="auth">
                <input type="hidden" name="provider" value="<?= View::e((string) $provider['index']) ?>">
                <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('admin.auth.test_oauth')) ?></button>
            </form>
        </div></div>
    <?php endforeach; ?>
</section>

<?php if (($report['kind'] ?? '') === 'ldap'): ?>
    <section aria-labelledby="clara-auth-result">
        <h2 class="h6" id="clara-auth-result">
            <?= View::e($view->t('admin.auth.result')) ?>
            <?php if (isset($report['server'])): ?> — <?= View::e($view->t('admin.auth.source', ['index' => (string) $report['server']])) ?><?php endif; ?>
        </h2>
        <div class="alert <?= ($result['outcome'] ?? '') === 'ok' ? 'alert-success' : 'alert-warning' ?>" role="status">
            <?= View::e($view->t('admin.auth.outcome.' . ($result['outcome'] ?? 'unreachable'))) ?>
            <?php if (isset($result['stage'])): ?> — <?= View::e((string) $result['stage']) ?><?php endif; ?>
            <?php if (isset($result['reason'])): ?> — <?= View::e((string) $result['reason']) ?><?php endif; ?>
            <?php if (isset($result['errno'])): ?> (LDAP error <?= View::e((string) $result['errno']) ?>)<?php endif; ?>
        </div>
        <?php if ($ldapDetail !== []): ?>
            <pre class="small bg-body-tertiary p-3 rounded"><?= View::e((string) json_encode($ldapStages, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE)) ?></pre>
        <?php endif; ?>
        <?php if ($ldapAttributes !== []): ?>
            <h3 class="h6"><?= View::e($view->t('admin.auth.claims')) ?></h3>
            <pre class="small bg-body-tertiary p-3 rounded"><?= View::e((string) json_encode($ldapAttributes, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE)) ?></pre>
        <?php endif; ?>
    </section>
<?php endif; ?>

<?php if ($oauth !== []): ?>
    <section aria-labelledby="clara-auth-result">
        <h2 class="h6" id="clara-auth-result"><?= View::e($view->t('admin.auth.result')) ?></h2>
        <p class="small text-body-secondary"><?= View::e($view->t('admin.auth.oauth_notice')) ?></p>
        <?php if (empty($oauth['ok'])): ?>
            <div class="alert alert-warning" role="status"><?= View::e($view->t('admin.auth.oauth_failed')) ?></div>
            <p class="small"><?= View::e($view->t('admin.auth.oauth_status', [
                'discovery' => (string) ($oauth['discovery_status'] ?? 0),
                'token' => (string) ($oauth['token_status'] ?? 0),
                'userinfo' => (string) ($oauth['userinfo_status'] ?? 0),
            ])) ?></p>
            <?php if (isset($oauth['endpoints'])): ?>
                <pre class="small bg-body-tertiary p-3 rounded"><?= View::e((string) json_encode($oauth['endpoints'], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES)) ?></pre>
            <?php endif; ?>
        <?php else: ?>
            <p class="small"><?= View::e($view->t('admin.auth.oauth_status', [
                'discovery' => (string) $oauth['discovery_status'],
                'token' => (string) $oauth['token_status'],
                'userinfo' => (string) $oauth['userinfo_status'],
            ])) ?></p>
            <pre class="small bg-body-tertiary p-3 rounded"><?= View::e((string) json_encode([
                'issuer' => $oauth['issuer'],
                'endpoints' => $oauth['endpoints'],
                'userinfo_claims' => $oauth['userinfo_claims'],
                'id_token_claims' => $oauth['id_token_claims'],
            ], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES)) ?></pre>
        <?php endif; ?>
    </section>
<?php endif; ?>
