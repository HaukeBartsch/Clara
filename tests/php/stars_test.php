<?php
// Starred projects and the overview's filters on the web side (User_Interface_Design.md §4,
// REQ-UI-086/087): the data region passes each project's star through, the page renders the search
// and the switch beside the project count and names the account the switch is remembered for, and POST /?action=star_project stars or
// unstars through the API and answers JSON — the shape dashboard.js reads — including when it is
// refused. The search and the filtering run in the browser (dashboard.js) and are covered end to
// end (e2e/tests/dashboard.spec.ts).

declare(strict_types=1);

const ST_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/** A POST of the star action as dashboard.js sends it: the CSRF header and a JSON Accept. */
function st_post(array $fields, string $csrf = ST_CSRF): \Clara\Response
{
    queue_shell();
    $headers = json_headers() + ['x-csrf-token' => $csrf];

    return router_for(http_request('POST', '/', $headers, $fields, ['action' => 'star_project']))->dispatch();
}

/** The method and path of every API call to the star endpoint. */
function st_calls(): array
{
    return array_values(array_map(
        static fn (array $c): string => $c['method'] . ' ' . parse_url($c['url'], PHP_URL_PATH),
        array_filter($GLOBALS['api_calls'], static fn (array $c): bool => str_contains($c['url'], '/starred-projects/'))
    ));
}

/** @return array<string, mixed> */
function st_json(\Clara\Response $response): array
{
    $decoded = json_decode($response->body(), true);
    assert_true(is_array($decoded), 'the answer is JSON: ' . $response->body());

    return $decoded;
}

describe('overview — starred projects (§4, REQ-UI-087)', function (): void {
    it('passes each project\'s star through the data region', function (): void {
        resetApi();
        sign_in();
        api_route('/api/v1/projects', [
            ['id' => 1, 'project_name' => 'Alpha', 'organization' => 'OUS', 'starred' => true],
            ['id' => 2, 'project_name' => 'Beta', 'organization' => 'OUS'],
        ]);

        $rows = json_decode(router_for(http_request('GET', '/', json_headers()))->dispatch()->body(), true);

        assert_same([true, false], array_column($rows, 'starred'));
    });

    it('renders the search and the switch beside the project count, and names the account', function (): void {
        resetApi();
        sign_in(['user_id' => 42]);
        queue_shell();
        api_route('/api/v1/projects', [['id' => 1, 'project_name' => 'Alpha', 'organization' => 'OUS']]);

        $body = router_for(http_request('GET', '/', browser_headers()))->dispatch()->body();

        $row = substr($body, (int) strpos($body, 'data-clara-project-count'));
        $row = substr($row, 0, (int) strpos($row, 'data-region="projects"'));
        assert_contains('aria-label="Search projects by name"', $row, 'the search sits in the heading row, outside the grid');
        assert_contains('<label class="form-check-label" for="clara-starred-only">Starred only</label>', $row);
        assert_not_contains('data-clara-project-tools hidden', $body, 'a list with a project shows the controls');
        assert_contains('data-clara-account="42"', $body);
        assert_contains('"dashboard.project.star":"Star {project}"', $body);
        assert_contains('"dashboard.search.none":', $body);
    });

    it('hides the controls while an administrator\'s installation has no project', function (): void {
        resetApi();
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', []);

        $body = router_for(http_request('GET', '/', browser_headers()))->dispatch()->body();

        assert_contains('data-clara-project-tools hidden', $body);
    });

    it('stars with PUT and unstars with DELETE, answering what the API stored', function (): void {
        resetApi();
        sign_in();
        api_route('/api/v1/users/me/starred-projects/33', ['project_id' => 33, 'starred' => true]);

        $response = st_post(['project_id' => '33', 'starred' => '1']);

        assert_same(200, $response->status());
        assert_contains('application/json', $response->headers()['Content-Type']);
        assert_same(['project_id' => 33, 'starred' => true], st_json($response));
        assert_same(['PUT /api/v1/users/me/starred-projects/33'], st_calls());

        resetApi();
        sign_in();
        api_route('/api/v1/users/me/starred-projects/33', ['project_id' => 33, 'starred' => false]);

        $response = st_post(['project_id' => '33', 'starred' => '0']);

        assert_same(['project_id' => 33, 'starred' => false], st_json($response));
        assert_same(['DELETE /api/v1/users/me/starred-projects/33'], st_calls());
    });

    it('refuses a malformed project or state before the API is called', function (): void {
        foreach ([
            ['project_id' => 'abc', 'starred' => '1'],
            ['project_id' => '0', 'starred' => '1'],
            ['project_id' => '-3', 'starred' => '1'],
            ['project_id' => '33', 'starred' => 'yes'],
            ['project_id' => '33'],
        ] as $fields) {
            resetApi();
            sign_in();

            $response = st_post($fields);

            assert_same(400, $response->status(), json_encode($fields));
            assert_same('invalid_request', st_json($response)['error']);
            assert_same([], st_calls());
        }
    });

    it('answers the API\'s refusal as JSON with its status', function (): void {
        resetApi();
        sign_in();
        api_route('/api/v1/users/me/starred-projects/34', ['error' => 'forbidden', 'message' => 'forbidden'], 403);

        $response = st_post(['project_id' => '34', 'starred' => '1']);

        assert_same(403, $response->status());
        assert_same('forbidden', st_json($response)['error']);
    });

    it('refuses a stale CSRF token as JSON rather than with a redirect', function (): void {
        resetApi();
        sign_in();

        $response = st_post(['project_id' => '33', 'starred' => '1'], str_repeat('b', 64));

        assert_same(403, $response->status());
        assert_same('forbidden', st_json($response)['error']);
        assert_true(!isset($response->headers()['Location']), 'no redirect for a script to follow');
        assert_same([], st_calls());
    });
});
