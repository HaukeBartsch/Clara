<?php
// Permission gating for rendering (REQ-UI-003, REQ-API-126): the levels come from the
// project read's `permissions` block, an ungranted arm is always no_access rather than
// an absent row (REQ-AUTH-019), and the rank tables mirror api/internal/authz.

declare(strict_types=1);

use Clara\Permissions;

/** The §4.5 shape the API returns for GET /api/v1/projects/{id}. */
function project_detail(bool $projectAdmin, array $arms): array
{
    return ['id' => 3, 'permissions' => [
        'project_admin' => $projectAdmin,
        'arms' => $arms,
    ]];
}

describe('permission ranks (authz parity)', function (): void {
    it('orders the data ladder as the API does', function (): void {
        assert_same(0, Permissions::dataRank('no_access'));
        assert_same(1, Permissions::dataRank('read_only'));
        assert_same(2, Permissions::dataRank('view_edit'));
        assert_same(3, Permissions::dataRank('delete'));
        assert_same(4, Permissions::dataRank('edit_survey_responses'));
    });

    it('orders the export ladder as the API does', function (): void {
        assert_same(0, Permissions::exportRank('export_none'));
        assert_same(1, Permissions::exportRank('export_de_identified'));
        assert_same(2, Permissions::exportRank('export_no_identifiers'));
        assert_same(3, Permissions::exportRank('export_full'));
    });

    it('ranks an unknown level as no access, never as something permissive', function (): void {
        assert_same(0, Permissions::dataRank('owner'));
        assert_same(0, Permissions::exportRank('everything'));
    });
});

describe('gating decisions (REQ-UI-003)', function (): void {
    it('grants a read_only member nothing but reading', function (): void {
        $permissions = Permissions::fromProjectDetail(project_detail(false, [
            ['arm_num' => 1, 'data_access_level' => 'read_only', 'export_level' => 'export_de_identified'],
        ]));

        assert_true($permissions->canView(1));
        assert_true(!$permissions->canEdit(1));
        assert_true(!$permissions->canDelete(1));
        assert_true(!$permissions->projectAdmin);
        assert_true($permissions->canExportAny());
    });

    it('lets delete reach view_edit', function (): void {
        $permissions = Permissions::fromProjectDetail(project_detail(false, [
            ['arm_num' => 1, 'data_access_level' => 'delete', 'export_level' => 'export_none'],
        ]));

        assert_true($permissions->canDelete(1));
        assert_true($permissions->canEdit(1));
        assert_true(!$permissions->canExportAny(), 'export_none on every arm hides the export card (§6.4)');
    });

    it('reports no_access for an arm the user was never granted (REQ-AUTH-019)', function (): void {
        $permissions = Permissions::fromProjectDetail(project_detail(false, [
            ['arm_num' => 1, 'data_access_level' => 'view_edit', 'export_level' => 'export_full'],
            ['arm_num' => 2, 'data_access_level' => 'no_access', 'export_level' => 'export_none'],
        ]));

        assert_true($permissions->canEdit(1));
        assert_true(!$permissions->canView(2), 'a level is never inferred from a missing grant');
        assert_same('no_access', $permissions->dataLevel(2));
    });

    it('answers no_access for an arm the project does not have', function (): void {
        $permissions = Permissions::fromProjectDetail(project_detail(false, []));

        assert_same('no_access', $permissions->dataLevel(99));
        assert_true(!$permissions->canView(99));
    });

    it('gives an administrator the full levels the API reports (REQ-AUTH-023)', function (): void {
        $permissions = Permissions::fromProjectDetail(project_detail(true, [
            ['arm_num' => 1, 'data_access_level' => 'edit_survey_responses', 'export_level' => 'export_full'],
        ]));

        assert_true($permissions->projectAdmin);
        assert_true($permissions->canDelete(1));
        assert_same('export_full', $permissions->exportLevel(1));
    });

    it('degrades to nothing at all when the read carried no block', function (): void {
        $permissions = Permissions::fromProjectDetail(['id' => 3]);

        assert_true(!$permissions->projectAdmin);
        assert_true(!$permissions->canView(1));
        assert_true(!$permissions->canExportAny());
    });

    it('lists arms in order for the arm tabs (§6.2)', function (): void {
        $permissions = Permissions::fromProjectDetail(project_detail(false, [
            ['arm_num' => 2, 'data_access_level' => 'read_only', 'export_level' => 'export_none'],
            ['arm_num' => 1, 'data_access_level' => 'view_edit', 'export_level' => 'export_full'],
        ]));

        assert_same([1, 2], array_column($permissions->arms(), 'arm_num'));
    });
});
