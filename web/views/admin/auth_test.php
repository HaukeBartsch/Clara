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
                <dt class="col-sm-3"><?= View::e($view->t('admin.auth.roles_claim')) ?></dt><dd class="col-sm-9"><?= View::e($provider['roles_attr'] !== '' ? $provider['roles_attr'] : $view->t('admin.auth.roles_claim.none')) ?></dd>
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
    <?php
    // Stage and reason arrive as machine identifiers and are translated here. One this page
    // has never seen renders as itself rather than as a missing-string fault, because the API
    // and the web layer are deployed independently (REQ-AUTH-085).
    $oauthStages = ['discovery', 'exchange', 'token_exchange', 'assertion'];
    $oauthReasons = [
        'unknown_provider', 'provider_unavailable', 'provider_issuer_mismatch',
        'assertion_invalid', 'email_unverified', 'assurance_insufficient', 'nonce_mismatch',
        'state_mismatch',
    ];
    $oauthStage = (string) ($oauth['stage'] ?? '');
    $oauthReason = (string) ($oauth['error'] ?? '');
    $oauthClaimSets = [
        'userinfo_claims' => (string) $view->t('admin.auth.userinfo_claims'),
        'id_token_claims' => (string) $view->t('admin.auth.id_token_claims'),
    ];
    ?>
    <section aria-labelledby="clara-auth-result">
        <h2 class="h6" id="clara-auth-result"><?= View::e($view->t('admin.auth.result')) ?></h2>
        <p class="small text-body-secondary"><?= View::e($view->t('admin.auth.oauth_notice')) ?></p>
        <?php if (empty($oauth['ok'])): ?>
            <div class="alert alert-warning" role="status">
                <?= View::e($view->t('admin.auth.oauth_failed')) ?>
                <?php if ($oauthStage !== '' || $oauthReason !== ''): ?>
                    <div class="small mt-1">
                        <?php if ($oauthStage !== ''): ?>
                            <strong><?= View::e(in_array($oauthStage, $oauthStages, true)
                                ? $view->t('admin.auth.stage.' . $oauthStage)
                                : $oauthStage) ?></strong>
                        <?php endif; ?>
                        <?php if ($oauthReason !== ''): ?>
                            &mdash; <?= View::e(in_array($oauthReason, $oauthReasons, true)
                                ? $view->t('admin.auth.reason.' . $oauthReason)
                                : $oauthReason) ?>
                        <?php endif; ?>
                    </div>
                <?php endif; ?>
            </div>
        <?php endif; ?>
        <p class="small"><?= View::e($view->t('admin.auth.oauth_status', [
            'discovery' => (string) ($oauth['discovery_status'] ?? 0),
            'token' => (string) ($oauth['token_status'] ?? 0),
            'userinfo' => (string) ($oauth['userinfo_status'] ?? 0),
        ])) ?></p>
        <?php $oauthEndpoints = is_array($oauth['endpoints'] ?? null) ? $oauth['endpoints'] : []; ?>
        <?php if ($oauthEndpoints !== []): ?>
            <pre class="small bg-body-tertiary p-3 rounded"><?= View::e((string) json_encode($oauthEndpoints, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE)) ?></pre>
        <?php endif; ?>
        <?php foreach ($oauthClaimSets as $oauthClaimKey => $oauthClaimLabel): ?>
            <?php
            // Claims render whether or not the test ended well: the run that fails at
            // verification after a userinfo endpoint answered is exactly the split claim
            // release an administrator needs to see, and hiding it there was the bug
            // REQ-AUTH-078 was written against.
            $oauthClaims = is_array($oauth[$oauthClaimKey] ?? null) ? $oauth[$oauthClaimKey] : [];
            ?>
            <?php if ($oauthClaims !== []): ?>
                <h3 class="h6"><?= View::e($oauthClaimLabel) ?></h3>
                <pre class="small bg-body-tertiary p-3 rounded"><?= View::e((string) json_encode($oauthClaims, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE)) ?></pre>
            <?php endif; ?>
        <?php endforeach; ?>
        <?php
        // The region preview (§5.12, Authentication_Authorization_Design.md §2.11/§2.12): what the
        // mapping says about each role value and the region a sign-in would set. Nothing was recorded.
        $preview = is_array($oauth['region_preview'] ?? null) ? $oauth['region_preview'] : [];
        $previewValues = is_array($preview['values'] ?? null) ? array_filter($preview['values'], 'is_array') : [];
        ?>
        <?php if ($preview !== []): ?>
            <h3 class="h6"><?= View::e($view->t('admin.auth.region_preview')) ?></h3>
            <p class="small mb-1"><?= View::e(empty($preview['claim_present'])
                ? $view->t('admin.auth.region_preview.absent', ['claim' => (string) ($preview['claim'] ?? '')])
                : $view->t('admin.auth.region_preview.claim', ['claim' => (string) ($preview['claim'] ?? '')])) ?></p>
            <?php if ($previewValues !== []): ?>
                <div class="table-responsive">
                    <table class="table table-sm small align-middle mb-2">
                        <thead><tr>
                            <th scope="col"><?= View::e($view->t('admin.regions.value')) ?></th>
                            <th scope="col"><?= View::e($view->t('admin.regions.region')) ?></th>
                        </tr></thead>
                        <tbody>
                        <?php foreach ($previewValues as $row): ?>
                            <?php
                            $rowRegion = is_string($row['region'] ?? null) ? $row['region'] : '';
                            $mapped = match (true) {
                                empty($row['listed']) => $view->t('admin.auth.region_preview.unlisted'),
                                empty($row['reviewed']) => $view->t('admin.regions.undecided'),
                                $rowRegion === '' => $view->t('admin.regions.no_region'),
                                default => $rowRegion,
                            };
                            ?>
                            <tr><td><?= View::e((string) ($row['value'] ?? '')) ?></td><td><?= View::e($mapped) ?></td></tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endif; ?>
            <p class="small"><?= View::e(is_string($preview['region'] ?? null)
                ? $view->t('admin.auth.region_preview.result', ['region' => $regionLabel($preview['region'])])
                : $view->t('admin.auth.region_preview.none')) ?></p>
        <?php endif; ?>
    </section>
<?php endif; ?>
