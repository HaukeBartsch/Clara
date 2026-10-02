<?php
// Sequence B and the directory half of Sequence I: Authentication_Authorization_Design.md
// §2.2 and §2.9 (REQ-AUTH-004/063/065, DEV-AUTH-14).
//
// Three layers are tested apart because they fail apart. `LdapAttempt` holds the decisions
// about one bind — what may be attempted at all, how a login name becomes a filter, which
// directory error means what — and needs no directory. `LdapRace` decides who wins a set of
// attempts and runs against a launcher that answers from a fixture. And the child process
// that actually speaks LDAP is exercised through the real subprocess plumbing, because the
// rules that matter there (the job arrives on stdin, the answer is one JSON line, nothing of
// it sits on argv) only exist once processes are involved.

declare(strict_types=1);

use Clara\Config;
use Clara\LdapAttempt;
use Clara\LdapAttemptSet;
use Clara\LdapLauncher;
use Clara\LdapRace;
use Clara\Logger;
use Clara\Request;
use Clara\Router;

// --- the launcher the race runs against ---------------------------------------

/**
 * A set whose answers are known in advance. `settled` stands in for what real children
 * reported, in the order they would have reported it — which is the one property of a real
 * set the race's own rules depend on (first `ok` in arrival order wins, §2.9).
 */
final class FakeLdapSet implements LdapAttemptSet
{
    /** @var int how often the caller stopped a set it no longer waits for */
    public int $cancels = 0;

    /** @param array<string, array{outcome: string, email?: string, display_name?: string}> $settled */
    public function __construct(
        private readonly array $settled = [],
        private readonly ?Throwable $failure = null
    ) {}

    public function settle(int $deadlineUnixMs): array
    {
        if ($this->failure !== null) {
            throw $this->failure;
        }

        return $this->settled;
    }

    public function cancel(): void
    {
        $this->cancels++;
    }
}

/** The launcher seam LdapRace exists around (Plan/Web_Implementation.md §8). */
final class FakeLdapLauncher implements LdapLauncher
{
    /** @var list<array<string, scalar>> every job started, across every set */
    public array $jobs = [];

    /** @var list<list<string>> the ids handed over by each start() call — one call is concurrency */
    public array $startCalls = [];

    /** @var list<FakeLdapSet> */
    public array $sets = [];

    /** The per-attempt ceiling the race asked for (§2.9's own timeout). */
    public int $timeoutSeconds = 0;

    /** @param array<string, array{outcome: string}> $settled */
    public function __construct(
        private readonly array $settled = [],
        private readonly ?Throwable $failure = null
    ) {}

    public function start(array $jobs, int $perAttemptTimeoutSeconds): LdapAttemptSet
    {
        foreach ($jobs as $job) {
            $this->jobs[] = $job;
        }

        $this->startCalls[] = array_column($jobs, 'id');
        $this->timeoutSeconds = $perAttemptTimeoutSeconds;

        $set = new FakeLdapSet($this->settled, $this->failure);
        $this->sets[] = $set;

        return $set;
    }

    /** @return list<string> the source ids this race was asked to attempt, in order */
    public function ids(): array
    {
        return array_column($this->jobs, 'id');
    }
}

// --- fixtures -----------------------------------------------------------------

/** One configured directory, as Config parses `LDAP_SERVER_N_*`. */
function ldap_server(int $index, array $overrides = []): array
{
    return array_merge([
        'index' => $index,
        'url' => 'ldap://dir' . $index . '.example.org:389',
        'bind_dn' => 'cn=lookup' . $index . ',dc=example,dc=org',
        'bind_password' => 'lookup-secret-' . $index,
        'search_base' => 'dc=hospital' . $index . ',dc=example,dc=org',
        'uid_attr' => 'uid',
        'email_attr' => 'mail',
        'name_attr' => 'cn',
        'names' => [],
    ], $overrides);
}

/**
 * A configuration with two directories answering to "Hospital 1", one of them also to
 * "Hospital 2", and no local source under either name — the shape that makes it visible
 * which directory a submitted credential is sent to (REQ-AUTH-063).
 */
function ldap_directory_config(array $overrides = []): Config
{
    return test_config(array_merge([
        'LDAP_SERVER_1_URL' => 'ldap://dir1.example.org:389',
        'LDAP_SERVER_1_SEARCH_BASE' => 'dc=hospital1,dc=example,dc=org',
        'LDAP_SERVER_1_NAMES' => 'Hospital 1',
        'LDAP_SERVER_2_URL' => 'ldaps://dir2.example.org:636',
        'LDAP_SERVER_2_SEARCH_BASE' => 'ou=people,dc=hospital2,dc=example,dc=org',
        'LDAP_SERVER_2_BIND_DN' => 'cn=lookup2,ou=people,dc=hospital2,dc=example,dc=org',
        'LDAP_SERVER_2_BIND_PASSWORD' => 'lookup-secret-2',
        'LDAP_SERVER_2_NAMES' => 'Hospital 1, Hospital 2',
    ], $overrides));
}

/** The race a login of this test runs: the real one, answering through `$launcher`. */
function ldap_login(Request $request, Config $config, LdapLauncher $launcher): Router
{
    $logger = new Logger('error', true);
    $transport = new FakeTransport();
    $api = new Clara\ApiClient($config, $logger, $request, $transport);

    return router_for($request, $config, new Clara\Auth(
        $api,
        $logger,
        $config,
        new Clara\Oauth($config, $logger, $transport),
        new LdapRace($config, $logger, $launcher)
    ));
}

/** The credential form submitted for a name, as the page posts it (§2.2). */
function ldap_submission(array $post = [], string $path = '/login'): Request
{
    return http_request('POST', $path, browser_headers(), array_merge([
        'csrf_token' => str_repeat('c', 64),
        'email' => 'typed@example.org',
        'password' => 'a typed secret',
    ], $post), ['action' => 'credentials']);
}

/** A job for the real child process: valid shape, nothing that can reach a directory. */
function subprocess_job(array $overrides = []): array
{
    return array_merge([
        'id' => 'ldap-1',
        'url' => 'ldap://dir.example.org:389',
        'bind_dn' => '',
        'bind_password' => '',
        'search_base' => 'dc=example,dc=org',
        'uid_attr' => 'uid',
        'email_attr' => 'mail',
        'name_attr' => 'cn',
        'login' => 'researcher',
        'password' => 'a typed secret',
        'timeout_seconds' => 2,
    ], $overrides);
}

/** Every command line this host can see — the view another user's process has. */
function host_cmdlines(): string
{
    $all = '';
    foreach (glob('/proc/[0-9]*/cmdline') ?: [] as $file) {
        $all .= (string) @file_get_contents($file);
    }

    return $all;
}

/**
 * Config refuses to start with a directory configured and no ext-ldap (REQ-CFG-005), so the
 * cases that need a parsed configuration are recorded as skipped on such a host instead of
 * reddening a suite about PHP's own decisions. LdapRace never calls ldap_* itself — only the
 * child does — so everything else runs everywhere.
 */
function it_needs_directory_support(string $name, callable $body): void
{
    if (function_exists('ldap_connect')) {
        it($name, $body);

        return;
    }

    $GLOBALS['cases'][] = [
        'suite' => $GLOBALS['currentSuite'],
        'name' => $name . ' [skipped: no ext-ldap]',
        'error' => null,
    ];
}

// --- one attempt's decisions (Sequence B, §2.2) --------------------------------

describe('directory bind mechanics (Sequence B, §2.2)', function (): void {
    it('refuses an empty password rather than binding anonymously', function (): void {
        // The property the whole preflight exists for: a simple bind with no password is not
        // "no credentials", it is an anonymous bind that many directories answer as success.
        $refused = LdapAttempt::preflight(subprocess_job(['password' => '']));
        assert_same('bad_password', $refused['outcome'] ?? null);

        // A login name of nothing cannot be searched for either, and answering it the same
        // way keeps the two empty-field cases one indistinguishable failure (§2.2 step 4).
        assert_same('bad_password', LdapAttempt::preflight(subprocess_job(['login' => '']))['outcome'] ?? null);
    });

    it('calls an unfinished server configuration an outage, not a wrong password', function (): void {
        foreach (['url' => '', 'search_base' => '   ', 'uid_attr' => ''] as $field => $blank) {
            $refused = LdapAttempt::preflight(subprocess_job([$field => $blank]));
            assert_same('unreachable', $refused['outcome'] ?? null, $field . ' missing');
        }
    });

    it('lets a complete job through to be attempted', function (): void {
        assert_same(null, LdapAttempt::preflight(subprocess_job()));

        // An anonymous search account is a supported configuration, not a missing one
        // (REQ-CFG-012, ASM-AUTH-2), so it must not trip the check above.
        assert_same(null, LdapAttempt::preflight(subprocess_job(['bind_dn' => '', 'bind_password' => ''])));
    });

    it('escapes a login name that is written as a filter', function (): void {
        // `*)(uid=*` is the classic injection: unescaped it turns the lookup into "every
        // entry", and the first entry's DN is the one the submitted password then binds.
        $filter = LdapAttempt::filter('uid', '*)(uid=*');

        assert_true(!str_contains($filter, ')('), 'no filter element can be opened from inside a value');
        assert_same(1, substr_count($filter, '('), 'the result is one assertion, not a compound');
        assert_same('(uid=\2a\29\28uid=\2a)', $filter);
    });

    it('escapes the characters RFC 4515 names, and nothing else', function (): void {
        assert_same('(uid=a\2ab)', LdapAttempt::filter('uid', 'a*b'));
        assert_same('(uid=a\\5cb)', LdapAttempt::filter('uid', 'a\\b'), 'the escape character escapes itself first');

        // Only the reserved octets are escaped; a name with a Norwegian ø in it travels as
        // the UTF-8 it already is. ext-ldap and the explicit fallback have to agree here, or
        // the escape rules would depend on whether this host installed php-ldap.
        assert_same('(uid=ø)', LdapAttempt::filter('uid', 'ø'));
        assert_same('(uid=ø\29å)', LdapAttempt::filter('uid', 'ø)å'), 'and one parenthesis in a real value is still escaped');
    });

    it('keeps a configured attribute name from becoming filter syntax', function (): void {
        // The attribute comes from configuration rather than the request, but a value with a
        // parenthesis in it would still break out of the assertion.
        assert_same('(uiduid=x)', LdapAttempt::filter('uid)(uid', 'x'));
        assert_same('(sAMAccountName=x)', LdapAttempt::filter('sAMAccountName', 'x'), 'a real Active Directory name survives');
    });

    it('reads a directory error as what it means for the race', function (): void {
        assert_same('bad_password', LdapAttempt::mapError(49), 'invalidCredentials is the user\'s');
        assert_same('no_entry', LdapAttempt::mapError(32), 'noSuchObject is a credential failure, phrased apart (§2.2 step 4)');
        assert_same('unreachable', LdapAttempt::mapError(14), 'serviceUnavailable is the directory\'s');
        assert_same('unreachable', LdapAttempt::mapError(-1), 'a connection-level failure never reads as a wrong password');
    });

    it('takes the identity from the entry, whatever case it came back in', function (): void {
        $entry = [
            'mail' => ['count' => 1, '0' => 'Researcher@Example.ORG'],
            'cn' => ['count' => 2, '0' => 'Rae Researcher', '1' => 'R. Searcher'],
        ];

        $identity = LdapAttempt::identity($entry, 'mail', 'cn');

        assert_same('researcher@example.org', $identity['email'], 'the address is normalised for lookup, lower-cased (§2.3)');
        assert_same('Rae Researcher', $identity['display_name'], 'a multi-valued attribute contributes its first value');

        // The claim's configured spelling is not the directory's: `mail` and `MAIL` are one
        // attribute, and an entry keyed in lower case must still answer a mixed-case claim.
        assert_same('researcher@example.org', LdapAttempt::identity($entry, 'MAIL', 'CN')['email']);
    });

    it('reports no identity rather than inventing one when the entry carries no address', function (): void {
        $identity = LdapAttempt::identity(['cn' => ['count' => 1, '0' => 'Rae Researcher']], 'mail', 'cn');

        assert_same('', $identity['email'], 'no claim, no account — the submitted login is not a substitute (§2.2 step 3)');
        assert_same('Rae Researcher', $identity['display_name']);
    });
});

// --- the race over a launcher (Sequence I, §2.9) -------------------------------

describe('the credential race over its launcher (Sequence I, §2.9)', function (): void {
    it('contributes nothing when there is no directory to try', function (): void {
        $race = new LdapRace(test_config(), new Logger('error', true), new FakeLdapLauncher());

        assert_same(null, $race->start([], 'researcher@example.org', 'pw'), 'no set is started at all');
        assert_same(['outcomes' => [], 'winner' => null], $race->finish(null));
        $race->abandon(null); // stopping nothing is not an error — the local path calls it unconditionally
    });

    it('starts one attempt per directory, named as the API names a source', function (): void {
        $launcher = new FakeLdapLauncher();
        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);

        $running = $race->start(
            [ldap_server(1), ldap_server(2, ['url' => 'ldaps://dir2.example.org:636'])],
            'researcher@example.org',
            'the password'
        );

        // "ldap-N" is the source id the audit details and `provider` use (API_Endpoints_Design.md §4.3).
        assert_same(['ldap-1', 'ldap-2'], $launcher->ids());

        // One start call for the whole name, not one per server: that hand-over is what makes
        // the attempts concurrent, and a loop over sources is exactly what DEV-AUTH-14
        // replaced. (That real processes overlap is shown against live children below.)
        assert_same([['ldap-1', 'ldap-2']], $launcher->startCalls);

        $job = $launcher->jobs[1];
        assert_same('ldaps://dir2.example.org:636', $job['url']);
        assert_same('cn=lookup2,dc=example,dc=org', $job['bind_dn'], 'each attempt carries its own read account');
        assert_same('the password', $job['password'], 'and the credential the user submitted, for the bind-as-user');
        assert_true($launcher->timeoutSeconds > 0, 'every directory gets its own ceiling so one cannot stall the race (§2.9)');
        assert_true($running['deadline_unix_ms'] > microtime(true) * 1000, 'the deadline is in the future');
    });

    it('takes the first attempt to report ok, whoever it was', function (): void {
        $launcher = new FakeLdapLauncher([
            'ldap-1' => ['outcome' => 'unreachable'],
            'ldap-2' => ['outcome' => 'ok', 'email' => 'from@dir2.example', 'display_name' => 'Rae Researcher'],
        ]);

        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);
        $result = $race->finish($race->start([ldap_server(1), ldap_server(2)], 'researcher@example.org', 'pw'));

        assert_same('ldap-2', $result['winner']['provider'] ?? null, 'the winner names the concrete source (§2.3)');
        assert_same('from@dir2.example', $result['winner']['email']);
        assert_same('Rae Researcher', $result['winner']['display_name']);
        assert_same('unreachable', $result['outcomes']['ldap-1'], 'the attempt that could not take part is still reported');
    });

    it('discards a later success rather than finalizing twice (REQ-AUTH-065)', function (): void {
        $launcher = new FakeLdapLauncher([
            'ldap-1' => ['outcome' => 'ok', 'email' => 'first@dir.example'],
            'ldap-2' => ['outcome' => 'ok', 'email' => 'second@dir.example'],
        ]);

        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);
        $result = $race->finish($race->start([ldap_server(1), ldap_server(2)], 'researcher@example.org', 'pw'));

        assert_same('first@dir.example', $result['winner']['email'], 'one winner, the first to answer');
        assert_same('ok_ignored', $result['outcomes']['ldap-2'], 'the second agreement is recorded as discarded, never used');
    });

    it('treats a bind that produced no address as an outage, not as a wrong password', function (): void {
        // The password matched but the entry names nobody: §2.2 step 3 forbids PHP deciding
        // the identity from the submitted login, and reporting it as `bad_password` would
        // tell the user their credential was wrong when the directory's claim is the problem.
        $launcher = new FakeLdapLauncher(['ldap-1' => ['outcome' => 'ok', 'email' => '']]);

        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);
        $result = $race->finish($race->start([ldap_server(1)], 'researcher@example.org', 'pw'));

        assert_same(null, $result['winner']);
        assert_same('unreachable', $result['outcomes']['ldap-1']);
    });

    it('carries every failure through as its own word', function (): void {
        $launcher = new FakeLdapLauncher([
            'ldap-1' => ['outcome' => 'bad_password'],
            'ldap-2' => ['outcome' => 'no_entry'],
            'ldap-3' => ['outcome' => 'unreachable'],
        ]);

        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);
        $result = $race->finish($race->start(
            [ldap_server(1), ldap_server(2), ldap_server(3)],
            'researcher@example.org',
            'pw'
        ));

        assert_same(null, $result['winner']);
        assert_same(
            ['ldap-1' => 'bad_password', 'ldap-2' => 'no_entry', 'ldap-3' => 'unreachable'],
            $result['outcomes'],
            'the attempts map the failure report carries (§2.9 step 5)'
        );
    });

    it('survives a launcher that cannot run attempts at all', function (): void {
        // A broken deployment is not a credential failure: the race contributes no outcome
        // and the login is decided by whatever else was in flight.
        $launcher = new FakeLdapLauncher([], new RuntimeException('proc_open disabled'));

        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);
        $result = $race->finish($race->start([ldap_server(1)], 'researcher@example.org', 'pw'));

        assert_same([], $result['outcomes']);
        assert_same(null, $result['winner']);
    });

    it('stops a set the winner has made irrelevant', function (): void {
        // Abandoning is what keeps a second directory's later success from ever arriving as
        // another finalization (REQ-AUTH-065): nothing is left running to report it.
        $launcher = new FakeLdapLauncher();
        $race = new LdapRace(test_config(), new Logger('error', true), $launcher);

        $running = $race->start([ldap_server(1)], 'researcher@example.org', 'pw');
        $race->abandon($running);

        assert_same(1, $launcher->sets[0]->cancels);
    });
});

// --- login through the race (Sequence I end to end) ----------------------------

describe('login with a directory source (Sequence I, §2.9)', function (): void {
    it_needs_directory_support('sends the credential to no directory outside the selected name', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $launcher = new FakeLdapLauncher(['ldap-2' => ['outcome' => 'ok', 'email' => 'from@dir2.example']]);
        api_route('/api/v1/auth/login', [
            'id' => 21, 'email' => 'from@dir2.example', 'display_name' => 'Rae Researcher',
            'is_admin' => false, 'auth_source' => 'ldap',
        ]);

        ldap_login(ldap_submission(['source' => 'Hospital 2']), ldap_directory_config(), $launcher)->dispatch();

        assert_same(['ldap-2'], $launcher->ids(), 'the submitted credential went only to the sources under that name (REQ-AUTH-063)');
        assert_same(0, api_calls_to('/auth/verify-password'), 'no local source stands under this name to verify against');
    });

    it_needs_directory_support('logs in as the address the directory holds, not the one typed', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $launcher = new FakeLdapLauncher(['ldap-2' => [
            'outcome' => 'ok', 'email' => 'from@dir2.example', 'display_name' => 'Rae Researcher',
        ]]);
        api_route('/api/v1/auth/login', [
            'id' => 21, 'email' => 'from@dir2.example', 'display_name' => 'Rae Researcher',
            'is_admin' => false, 'auth_source' => 'ldap', 'ui_language' => 'en', 'ui_theme' => null,
        ]);

        $response = ldap_login(ldap_submission(['source' => 'Hospital 2']), ldap_directory_config(), $launcher)->dispatch();

        assert_same(302, $response->status());
        assert_true(Clara\Session::isAuthenticated());
        assert_same('from@dir2.example', Clara\Session::email(), 'the directory is the authority on identity (REQ-AUTH-004)');

        $body = api_request_body('/api/v1/auth/login');
        assert_same('ldap', $body['source']);
        assert_same('ldap-2', $body['provider'], 'Sequence C names the concrete winning source (§2.3)');
        assert_same('Hospital 2', $body['source_name'], 'and records the name the user selected (REQ-AUTH-067)');
        assert_true(!array_key_exists('password', $body), 'the directory password is never forwarded to the API');
        assert_same(1, api_calls_to('/api/v1/auth/login'), 'one finalization for one login (REQ-AUTH-065)');
    });

    it_needs_directory_support('settles the login locally and stops the directories mid-flight', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $launcher = new FakeLdapLauncher();
        $config = ldap_directory_config(['LOCAL_LOGIN_NAMES' => 'Hospital 2']);
        api_route('/api/v1/auth/verify-password', ['status' => 'ok', 'first_factor' => 'ff1-handle']);
        api_route('/api/v1/auth/login', [
            'id' => 7, 'email' => 'typed@example.org', 'display_name' => 'T', 'is_admin' => false,
        ]);

        $response = ldap_login(ldap_submission(['source' => 'Hospital 2']), $config, $launcher)->dispatch();

        assert_same(302, $response->status());
        assert_same(['ldap-2'], $launcher->ids(), 'the directories under the name were started all the same — that is the race');
        assert_same(1, $launcher->sets[0]->cancels, 'and stopped as soon as the local source answered (§2.9)');

        $body = api_request_body('/api/v1/auth/login');
        assert_same('local', $body['source']);
        assert_same('ff1-handle', $body['first_factor'] ?? null);
        assert_true(!array_key_exists('attempts', $body), 'a success carries no failure report');
    });

    it_needs_directory_support('reports one failed race once, with the per-source outcomes', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        // A name that carries the local source and both directories: three attempts, one
        // failure report. (A name carrying only directories is the case below.)
        $config = ldap_directory_config(['LOCAL_LOGIN_NAMES' => 'Hospital 1']);
        $launcher = new FakeLdapLauncher([
            'ldap-1' => ['outcome' => 'no_entry'],
            'ldap-2' => ['outcome' => 'unreachable'],
        ]);
        api_route('/api/v1/auth/verify-password', ['error' => 'bad_password', 'message' => '', 'status' => 401], 401);
        // The wire code is bad_password; bad_credentials is the audit reason it maps to
        // (api/internal/admin/auth.go:131), and only the first reaches this layer.
        api_route('/api/v1/auth/login', ['error' => 'bad_password', 'message' => '', 'status' => 401], 401);

        $response = ldap_login(ldap_submission(['source' => 'Hospital 1']), $config, $launcher)->dispatch();

        assert_same(['ldap-1', 'ldap-2'], $launcher->ids());
        assert_same(1, api_calls_to('/api/v1/auth/login'), 'exactly one login_failure for the submission (§2.9 step 5)');
        assert_contains('not recognised', $response->body(), 'one translated line, no reason the API did not give (§2.2)');
        assert_true(!Clara\Session::isAuthenticated(), 'a reported failure authenticates nothing (REQ-API-135)');

        $body = api_request_body('/api/v1/auth/login');
        assert_same('bad_password', $body['attempts']['local'] ?? null);
        assert_same('no_entry', $body['attempts']['ldap-1'] ?? null);
        assert_same('unreachable', $body['attempts']['ldap-2'] ?? null);
        assert_true(!array_key_exists('password', $body), 'the report of a failure carries no credential to try again');
    });

    it_needs_directory_support('shows an account state rather than a credential failure', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $config = ldap_directory_config(['LOCAL_LOGIN_NAMES' => 'Hospital 1']);
        $launcher = new FakeLdapLauncher([
            'ldap-1' => ['outcome' => 'bad_password'],
            'ldap-2' => ['outcome' => 'unreachable'],
        ]);
        api_route('/api/v1/auth/verify-password', ['error' => 'account_disabled', 'message' => '', 'status' => 403], 403);
        api_route('/api/v1/auth/login', ['error' => 'account_disabled', 'message' => '', 'status' => 403], 403);

        $response = ldap_login(ldap_submission(['source' => 'Hospital 1']), $config, $launcher)->dispatch();

        assert_contains('disabled', $response->body(), 'the account state outranks the generic line (§2.6 step 4)');
        assert_same('account_disabled', api_request_body('/api/v1/auth/login')['attempts']['local'] ?? null);
    });

    it_needs_directory_support('answers an unreachable directory as an outage, not as a wrong password', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $launcher = new FakeLdapLauncher(['ldap-2' => ['outcome' => 'unreachable']]);
        api_route('/api/v1/auth/login', ['error' => 'provider_unavailable', 'message' => '', 'status' => 503], 503);

        $response = ldap_login(ldap_submission(['source' => 'Hospital 2']), ldap_directory_config(), $launcher)->dispatch();

        assert_contains('could not be reached', $response->body(), 'a directory that was down is never reported as a bad password (§2.2 step 4)');
        assert_true(!Clara\Session::isAuthenticated());
    });

    it_needs_directory_support('still asks the second factor of a directory winner (§2.9)', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $launcher = new FakeLdapLauncher(['ldap-2' => ['outcome' => 'ok', 'email' => 'from@dir2.example']]);
        api_route('/api/v1/auth/login', [
            'error' => 'mfa_required', 'method' => 'totp', 'user_id' => 21, 'message' => '', 'status' => 401,
        ], 401);

        $response = ldap_login(ldap_submission(['source' => 'Hospital 2']), ldap_directory_config(), $launcher)->dispatch();

        assert_true(!Clara\Session::isAuthenticated(), 'the race answers who you are; the factor decides whether you get in (§2.7)');
        assert_true(Clara\Session::hasPendingSecondFactor());

        $pending = Clara\Session::pendingSecondFactor();
        assert_same('ldap', $pending['source']);
        assert_same('ldap-2', $pending['provider']);
        assert_same('', $pending['first_factor'], 'a directory winner has no first-factor handle — the bind was the factor');
        assert_contains('authenticator app', $response->body());
    });

    it_needs_directory_support('keeps the typed password out of the session and the page (REQ-AUTH-036)', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        $launcher = new FakeLdapLauncher(['ldap-2' => ['outcome' => 'bad_password']]);
        // The wire code is bad_password; bad_credentials is the audit reason it maps to
        // (api/internal/admin/auth.go:131), and only the first reaches this layer.
        api_route('/api/v1/auth/login', ['error' => 'bad_password', 'message' => '', 'status' => 401], 401);

        $response = ldap_login(
            ldap_submission(['source' => 'Hospital 2', 'password' => 'a typed secret']),
            ldap_directory_config(),
            $launcher
        )->dispatch();

        assert_true(!str_contains(serialize($_SESSION), 'a typed secret'), 'nothing a directory verifies against is remembered');
        assert_not_contains('a typed secret', $response->body());
        assert_not_contains('lookup-secret-2', $response->body(), 'nor is the read account of the directory it failed at');
    });
});

// --- the child process that speaks LDAP ---------------------------------------

describe('the directory attempt as a child process (SubprocessLdapAttempts)', function (): void {
    it('answers one JSON line per child, refusing an anonymous bind', function (): void {
        // The refusal LdapAttempt::preflight implements is checked here in the process that
        // would otherwise have run the bind: whatever the directory thinks, a child started
        // for an empty password never reports a successful login.
        $set = new Clara\SubprocessLdapAttempts(
            [subprocess_job(['id' => 'ldap-1', 'password' => ''])],
            2,
            new Logger('error', true)
        );

        $settled = $set->settle((int) (microtime(true) * 1000) + 8000);

        assert_same('bad_password', $settled['ldap-1']['outcome'] ?? null, 'one answer, read as the outcome it is');
    });

    it('calls an unfinished server configuration unreachable from inside the child', function (): void {
        $set = new Clara\SubprocessLdapAttempts(
            [subprocess_job(['id' => 'ldap-1', 'search_base' => ''])],
            2,
            new Logger('error', true)
        );

        assert_same('unreachable', $set->settle((int) (microtime(true) * 1000) + 8000)['ldap-1']['outcome'] ?? null);
    });

    it('does not wait for a directory that never answers, and keeps its siblings', function (): void {
        // A socket that accepts and says nothing: the child blocks in its bind until its own
        // ceiling, which is exactly the stall §2.9 forbids from holding up a login.
        $blackhole = @stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
        if ($blackhole === false) {
            $GLOBALS['cases'][] = [
                'suite' => $GLOBALS['currentSuite'],
                'name' => 'stalled directory [skipped: cannot listen on loopback]',
                'error' => null,
            ];

            return;
        }

        $address = (string) stream_socket_get_name($blackhole, false);
        $started = microtime(true);

        $set = new Clara\SubprocessLdapAttempts([
            subprocess_job(['id' => 'ldap-1', 'url' => 'ldap://' . $address, 'timeout_seconds' => 30]),
            subprocess_job(['id' => 'ldap-2', 'password' => '']),
        ], 30, new Logger('error', true));

        // A quarter of a second: long enough for the child that can answer to have answered,
        // far shorter than the 30 s ceiling the stalled one was given.
        $settled = $set->settle((int) ((microtime(true) + 0.25) * 1000));
        $elapsed = microtime(true) - $started;

        fclose($blackhole);

        assert_same('bad_password', $settled['ldap-2']['outcome'] ?? null, 'the attempt that could answer was read');
        assert_same('unreachable', $settled['ldap-1']['outcome'] ?? null, 'the one that could not is settled, not left unknown');
        assert_true($elapsed < 5.0, 'the race closed at its deadline rather than waiting out a directory (took ' . round($elapsed, 2) . 's)');
    });

    it('carries the job on stdin, where no other process can read it (REQ-AUTH-036)', function (): void {
        if (!is_dir('/proc')) {
            $GLOBALS['cases'][] = [
                'suite' => $GLOBALS['currentSuite'],
                'name' => 'secrets off the command line [skipped: no /proc]',
                'error' => null,
            ];

            return;
        }

        // A child that stays alive while we look: its command line is what every other user
        // of this host can see through /proc, so neither password may appear there.
        $blackhole = @stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
        if ($blackhole === false) {
            $GLOBALS['cases'][] = [
                'suite' => $GLOBALS['currentSuite'],
                'name' => 'secrets off the command line [skipped: cannot listen on loopback]',
                'error' => null,
            ];

            return;
        }

        $address = (string) stream_socket_get_name($blackhole, false);
        $set = new Clara\SubprocessLdapAttempts([
            subprocess_job([
                'url' => 'ldap://' . $address,
                'timeout_seconds' => 30,
                'password' => 'clara-test-user-secret',
                'bind_dn' => 'cn=lookup,dc=example,dc=org',
                'bind_password' => 'clara-test-lookup-secret',
            ]),
        ], 30, new Logger('error', true));

        usleep(250_000); // let the child reach its bind before the host's process list is read
        $cmdlines = host_cmdlines();
        // A short deadline on purpose: this attempt is expected to settle by timeout, which is
        // also the answer it would report if the directory were merely slow.
        $settled = $set->settle((int) (microtime(true) * 1000) + 300);
        fclose($blackhole);

        assert_not_contains('clara-test-user-secret', $cmdlines, 'the submitted password is not on any argv');
        assert_not_contains('clara-test-lookup-secret', $cmdlines, 'nor is the directory read account');
        assert_same('unreachable', $settled['ldap-1']['outcome'] ?? null, 'and the attempt still settles normally');
    });
});
