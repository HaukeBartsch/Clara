<?php
/**
 * The project-mode badge (§6.6, REQ-UI-033): development / production / analysis, shown
 * beside the project name on Overview and in the header breadcrumb. `$badgeMode` is one of
 * the three modes of GD-20 or '' — and '' renders nothing, never a placeholder. The colour
 * is a Bootstrap contextual class, so every theme supplies its own (REQ-UI-040).
 */

use Clara\View;

$badgeClass = [
    'development' => 'text-bg-secondary',
    'production' => 'text-bg-success',
    'analysis' => 'text-bg-info',
][$badgeMode ?? ''] ?? '';
?>
<?php if ($badgeClass !== ''): ?>
    <span class="badge <?= $badgeClass ?> align-middle clara-mode-badge"
          data-mode="<?= View::e($badgeMode) ?>"><?= View::e($view->t('project.mode.' . $badgeMode)) ?></span>
<?php endif; ?>
