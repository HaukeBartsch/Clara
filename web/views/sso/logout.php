<?php

/**
 * Sign out at a client's request (Sequence K, REQ-AUTH-094, User_Interface_Design.md §2.1).
 *
 * A registered application's "sign out" link lands here. The page only asks: signing out is
 * the POST below, carrying the session's CSRF token, so no other page can sign a user out by
 * embedding the link. Signing out of CLARA ends every application sign-in made through it,
 * and the page says so before the user chooses.
 */

use Clara\View;

$client_name = $client_name ?? '';
?>
<h1 class="h4 mb-1"><?= View::e($view->t('sso.logout.title')) ?></h1>
<p class="text-body-secondary small mb-3">
    <?= View::e($client_name !== ''
        ? $view->t('sso.logout.body_client', ['client' => $client_name])
        : $view->t('sso.logout.body')) ?>
</p>

<form method="post" action="/sso/logout">
    <?= $view->csrfField() ?>
    <input type="hidden" name="client_id" value="<?= View::e($client_id ?? '') ?>">
    <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('sso.logout.submit')) ?></button>
</form>

<p class="text-center mt-3 mb-0">
    <a class="small" href="<?= View::e($stay_href ?? '/') ?>"><?= View::e($view->t('sso.logout.stay')) ?></a>
</p>
