import { expect, test } from "@playwright/test"
import { existsSync, readFileSync } from "node:fs"
import path from "node:path"
import url from "node:url"

// The reverse proxy carries the boundary the application does not enforce itself
// (`Technology_Stack_Design.md` §5): only `POST /api/` reaches the Go API from outside, the
// administration API and the documentation endpoints are never routable (REQ-TECH-018,
// REQ-AUTH-014), `X-Internal-*` never crosses the edge, and `X-Real-IP` is the connection
// address so no caller picks the address it is rate-limited by or audited under (REQ-API-125,
// finding F9). nginx is the reference server; Apache httpd 2.4 is an approved alternative
// (`INSTALL.md` §11.3). Both configurations ship in `deploy/` as one rule set in two spellings.
//
// Three tiers, cheapest first:
//   1. The files agree — pure reads, no server, always run. This is the drift gate for the
//      "change one, change the other" rule of §5, including against `INSTALL.md` §11.2, which
//      documents the nginx file a third time.
//   2. The front-door contract over HTTP — INSTALL.md §13.1 as assertions, run against
//      CLARA_DEPLOY_URL when one is given and CLARA_WEB_URL otherwise. Only rules that hold with
//      no proxy in the path live here, because the dev stack is `php -S` (`e2e/dev-stack.sh`) —
//      which is exactly why they are stated as "the administration API is not reachable" rather
//      than "the proxy answered 404": on the dev stack PHP answers them (Router.php:406).
//   3. Reverse-proxy hardening — registered only when CLARA_PROXY_KIND names a server, since a
//      rule like "GET /api/ is refused at the edge" has no meaning in front of `php -S`. Against
//      a staging host:
//        CLARA_DEPLOY_URL=https://clara.example.org CLARA_PROXY_KIND=nginx npx playwright test
//      Add CLARA_DEPLOY_HTTP_URL to also check the redirect on port 80. Self-signed certificates
//      need `ignoreHTTPSErrors` in the project that runs these (`playwright.config.ts`).

const here = path.dirname(url.fileURLToPath(import.meta.url))
const read = (rel: string): string => readFileSync(path.resolve(here, rel), "utf8")
// Whitespace collapsed, so a rule reads as prose ("proxy_set_header X \"\"") and survives a
// reindent in either file — the rule is the directive, not its column.
const flat = (source: string): string => source.replace(/\s+/g, " ")
// Comments out (both servers use `#`). The files document the rules they enforce, including the
// ones they deliberately avoid — a guard that read prose would fail on its own explanation.
const codeOnly = (source: string): string =>
  flat(source.split("\n").filter((line) => !line.trim().startsWith("#")).join("\n"))

const NGINX_FILE = "../../deploy/nginx/clara.conf"
const APACHE_FILE = "../../deploy/apache2/clara.conf"

type Rule = { id: string; why: string; nginx: string[]; apache: string[] }

// Every entry is a routing rule of §5. A missing one means the edge stopped enforcing something
// a requirement says it enforces — not a formatting change.
const RULES: Rule[] = [
  {
    id: "tls-termination-and-redirect",
    why: "TLS terminates here and plain HTTP redirects (§11.2)",
    nginx: ["listen 80;", "return 301 https://$host$request_uri;"],
    apache: ["RewriteCond %{HTTPS} !=on", "[R=301,L]"],
  },
  {
    id: "hsts-on-every-response",
    why: "HSTS, including on error responses",
    nginx: ['add_header Strict-Transport-Security "max-age=31536000" always;'],
    apache: ['Header always set Strict-Transport-Security "max-age=31536000"'],
  },
  {
    id: "data-api-is-post-only",
    why: "GET /api/ is refused; the data endpoint speaks POST (REQ-API-001)",
    nginx: ["limit_except POST { deny all; }"],
    apache: ["RewriteCond %{REQUEST_METHOD} !=POST", 'RewriteRule "^/api/$" - [R=403,L]'],
  },
  {
    id: "data-api-is-proxied",
    why: "POST /api/ reaches the Go API and nothing else does (REQ-TECH-018)",
    nginx: ["location = /api/ {", "proxy_pass http://clara_api;"],
    apache: ['RewriteRule "^/api/$" "http://127.0.0.1:8080/api/" [P,L]'],
  },
  {
    id: "administration-api-never-routable",
    why: "/api/v1/* is never reachable from the network (REQ-TECH-018, REQ-AUTH-014)",
    nginx: ["location /api/ { return 404; }"],
    apache: ['RewriteRule "^/api/v1(/.*)?$" - [R=404,L]'],
  },
  {
    id: "internal-headers-stripped",
    why: "no external request may carry the internal identity headers (REQ-AUTH-014, §5)",
    nginx: [
      'proxy_set_header X-Internal-Service-Token "";',
      'proxy_set_header X-Internal-User-Id "";',
      'proxy_set_header X-Internal-Session-Epoch "";',
    ],
    apache: [
      "RequestHeader unset X-Internal-Service-Token",
      "RequestHeader unset X-Internal-User-Id",
      "RequestHeader unset X-Internal-Session-Epoch",
    ],
  },
  {
    id: "caller-address-is-the-connection",
    why: "X-Real-IP is set, never believed (REQ-API-125, finding F9)",
    nginx: ["proxy_set_header X-Real-IP $remote_addr;"],
    apache: ["RequestHeader set X-Real-IP expr=%{REMOTE_ADDR}"],
  },
  {
    id: "access-logs-omit-query-credentials",
    why: "OAuth callback codes and other query-string credentials must not enter access logs",
    nginx: [
      'log_format clara_safe',
      '"$request_method $uri $server_protocol"',
      "access_log /var/log/nginx/clara.access.log clara_safe;",
    ],
    apache: [
      "LogFormat",
      "%m %U %H",
      "CustomLog /var/log/httpd/clara_access.log clara_safe",
    ],
  },
  {
    id: "no-forwarded-for-chain",
    why: "one address per caller and no chain to argue about (§5)",
    nginx: ['proxy_set_header X-Forwarded-For "";'],
    apache: ["ProxyAddHeaders Off", "RequestHeader unset X-Forwarded-For"],
  },
  {
    id: "assets-served-outside-document-root",
    why: "web/assets stays outside web/public (§4 layout, Technology_Stack_Design.md)",
    nginx: ["alias /opt/clara/web/assets/;", "alias /opt/clara/web/assets/vendor/;"],
    apache: [
      "Alias /assets/vendor/ /opt/clara/web/assets/vendor/",
      "Alias /assets/ /opt/clara/web/assets/",
    ],
  },
  {
    id: "vendor-cached-app-revalidated",
    why: "vendored libraries for a day, the application's own files always revalidated (§11.2)",
    nginx: ["expires 1d;", 'add_header Cache-Control "no-cache" always;'],
    apache: ['Cache-Control "public, max-age=86400"', 'Cache-Control "no-cache"'],
  },
  {
    id: "pages-go-to-front-controller",
    why: "every page is index.php; a real file is itself (REQ-UI-044 routing)",
    nginx: ["try_files $uri /index.php$is_args$args;"],
    apache: ["RewriteCond %{REQUEST_FILENAME} !-f", "RewriteRule .* index.php [L]"],
  },
  {
    id: "only-front-controller-executed",
    why: "no other PHP file runs and none is served as text (REQ-AUTH-014 defence in depth)",
    nginx: ["location ~ \\.php$ { return 404;", "fastcgi_param SCRIPT_FILENAME $document_root/index.php;"],
    apache: ['FilesMatch "\\.php$"', "Require all denied", '<Files "index.php">'],
  },
  {
    id: "php-fpm-over-the-pool-socket",
    why: "PHP-FPM pool of §10, over its unix socket — never a network listener",
    nginx: ["fastcgi_pass unix:/run/php-fpm/clara.sock;"],
    apache: ["proxy:unix:/run/php-fpm/clara.sock|fcgi://localhost"],
  },
  {
    id: "import-body-limit",
    why: "a 32 MiB REDCap import arrives and nothing larger (REQ-API-019, §10 post_max_size)",
    nginx: ["client_max_body_size 32m;"],
    apache: ["LimitRequestBody 33554432"],
  },
  {
    id: "exports-stream",
    why: "a long export is delivered as it is produced, not buffered (§11.2)",
    nginx: ["proxy_buffering off;", "proxy_read_timeout 600s;"],
    apache: ["ProxyTimeout 600", "SetEnv no-gzip 1"],
  },
]

// The Apache front-controller probe only works inside <Directory>: at vhost level mod_rewrite
// runs during translate_name, before the URL is mapped to a file, where -f always matches and
// every asset would be routed to PHP. Returns the block's body, or "" when it is absent.
function directoryBlock(source: string, marker: string): string {
  const start = source.indexOf(marker)
  if (start === -1) return ""
  const end = source.indexOf("</Directory>", start)
  return source.slice(start, end === -1 ? source.length : end)
}

test.describe("reverse-proxy reference configurations", () => {
  const remedy = "both servers' files ship in deploy/ — restore them (Technology_Stack_Design.md §5)"

  test("nginx and Apache each ship a configuration file", () => {
    expect(existsSync(path.resolve(here, NGINX_FILE)), `deploy/nginx/clara.conf: ${remedy}`).toBe(true)
    expect(existsSync(path.resolve(here, APACHE_FILE)), `deploy/apache2/clara.conf: ${remedy}`).toBe(true)
  })

  test("every §5 routing rule holds in both spellings", () => {
    const nginx = codeOnly(read(NGINX_FILE))
    const apache = codeOnly(read(APACHE_FILE))
    const missing: string[] = []

    for (const rule of RULES) {
      for (const [kind, source] of [["nginx", nginx], ["apache", apache]] as const) {
        const absent = rule[kind].filter((needle) => !source.includes(needle))
        if (absent.length > 0) {
          missing.push(`${rule.id} (${rule.why}) — ${kind} is missing: ${absent.join(" | ")}`)
        }
      }
    }

    // Aggregated so one run names every rule that broke, not just the first.
    expect(missing, `rules no longer enforced:\n  ${missing.join("\n  ")}`).toEqual([])
    expect(nginx).not.toContain("access_log /var/log/nginx/clara.access.log;")
    expect(apache).not.toContain("CustomLog /var/log/httpd/clara_access.log combined")
  })

  test("Apache keeps the file-system probe inside <Directory>, where it works", () => {
    const block = directoryBlock(read(APACHE_FILE), "<Directory /opt/clara/web/public>")
    expect(block, "no <Directory /opt/clara/web/public> block").toContain("!-f")
    expect(block).toContain("RewriteRule .* index.php [L]")
  })

  test("Apache proxies exactly /api/ and declares no ProxyPass prefix", () => {
    const code = codeOnly(read(APACHE_FILE))
    // A `ProxyPass /api/` prefix answers /api/v1/* from mod_proxy's map_to_storage hook, which
    // runs before the rewrite rule that refuses it — the administration API becomes routable.
    expect(code).not.toMatch(/ProxyPass \/(api|v1)/)
    expect(code).toContain("[P,L]")
  })

  test("each file names the other, so a change lands in both", () => {
    expect(read(NGINX_FILE)).toContain("deploy/apache2/clara.conf")
    expect(read(APACHE_FILE)).toContain("deploy/nginx/clara.conf")
  })

  test("the nginx file is still the configuration INSTALL.md §11.2 documents", () => {
    const guide = read("../../INSTALL.md")
    const documented = guide.match(/```nginx\n([\s\S]*?)```/)
    expect(documented, "INSTALL.md no longer holds an nginx block").not.toBeNull()

    const shipped = read(NGINX_FILE)
    const anchor = "# /etc/nginx/conf.d/clara.conf — CLARA reverse proxy"
    expect(shipped, `deploy/nginx/clara.conf lost its ${anchor} line`).toContain(anchor)

    // The guide quotes the file; two copies of one configuration drift silently unless something
    // compares them (the same stance as tools/build_relevance_sankey.php --check).
    expect(flat(shipped.slice(shipped.indexOf(anchor)))).toBe(flat(documented?.[1] ?? ""))
  })
})

// --- tier 2: the same contract over HTTP -----------------------------------------------------
// INSTALL.md §13.1 as assertions. Runs against a deployed front door when one is named, and
// otherwise against the dev stack, where PHP answers these paths itself — which still proves a
// caller cannot reach the internal surface, the property that matters.
const BASE = process.env.CLARA_DEPLOY_URL ?? process.env.CLARA_WEB_URL
// Playwright's configured baseURL is the dev stack; a deployment under test is named by
// CLARA_DEPLOY_URL, so requests here are built absolute whenever a front door is set.
const at = (route: string): string => `${(BASE ?? '').replace(/\/+$/, '')}${route}`

test.describe("front-door contract (INSTALL.md §13.1)", () => {
  test.skip(!BASE, "no front door: set CLARA_DEPLOY_URL (a deployment) or CLARA_WEB_URL (dev stack)")

  const INTERNAL_ONLY = [
    "/api/v1/health",
    "/api/v1/system/metrics",
    "/api/v1/system/check",
    "/api/v1/projects/1/fields",
  ]
  const API_OWN_MARKERS = ['"status":"ok"', '"openapi"', "swagger-ui", "Swagger UI"]

  for (const target of INTERNAL_ONLY) {
    test(`${target} is not the administration API`, async ({ request }) => {
      const response = await request.get(at(target))
      const body = await response.text()
      expect(response.status(), `${target} answered ${response.status()}: ${body.slice(0, 160)}`)
        .toBe(404)
      for (const marker of API_OWN_MARKERS) {
        expect(body, `${target} leaked "${marker}"`).not.toContain(marker)
      }
    })
  }

  for (const target of ["/docs", "/openapi.json", "/healthz"]) {
    test(`${target} never reaches the API's own documentation`, async ({ request }) => {
      const response = await request.get(at(target))
      const body = await response.text()
      expect(body, `${target} served the OpenAPI document`).not.toContain('"openapi"')
      expect(body, `${target} served Swagger UI`).not.toContain("swagger-ui")
    })
  }

  test(".env is unreachable by any path", async ({ request }) => {
    // --path-as-is equivalents: a client that refuses to normalise, and one that encodes.
    for (const target of ["/.env", "/assets/../.env", "/%2e%2e/.env", "/../.env"]) {
      const body = await request.get(at(target)).then((r) => r.text())
      expect(body, `${target} served the configuration file`).not.toContain("INTERNAL_SERVICE_TOKEN")
    }
  })

  test("vendored assets are cacheable and the application's own revalidate", async ({ request }) => {
    const vendored = await request.get(at("/assets/vendor/bootstrap/bootstrap.bundle.min.js"))
    expect(vendored.status()).toBe(200)
    expect(vendored.headers()["cache-control"] ?? "").toContain("max-age=86400")

    const own = await request.get(at("/assets/app.js"))
    expect(own.status()).toBe(200)
    expect(own.headers()["cache-control"] ?? "").toContain("no-cache")
  })
})

// --- tier 3: what only a reverse proxy can get right -----------------------------------------
// Registered per server kind, so nothing is skipped on the dev stack (a skip would be an
// untested rule, and ci/run.sh treats a skipped browser spec as uncovered — REQ-TECH-028).
const PROXY = process.env.CLARA_PROXY_KIND

if (PROXY && BASE) {
  test.describe(`reverse-proxy hardening (${PROXY})`, () => {
    test("GET /api/ is refused at the edge", async ({ request }) => {
      expect(await request.get(at("/api/")).then((r) => r.status())).toBe(403)
    })

    test("POST /api/ arrives at the API, which refuses a bad token", async ({ request }) => {
      const response = await request.post(at("/api/"), { data: "token=0000&content=version" })
      // 401 from the API, not a proxy page: the body is REDCap's own line (§13.1).
      expect(response.status()).toBe(401)
      expect(await response.text()).toContain("Invalid token")
    })

    test("a caller cannot claim an internal identity", async ({ request }) => {
      const headers = {
        "X-Internal-Service-Token": "not-the-real-one",
        "X-Internal-User-Id": "1",
      }
      const admin = await request.get(at("/api/v1/system/metrics"), { headers })
      expect(admin.status(), "internal headers survived the edge").toBe(404)

      // And no session is opened for a caller that presented them to a page.
      const page = await request.get(at("/"), { headers, maxRedirects: 0 })
      expect([302, 303]).toContain(page.status())
      expect(page.headers().location ?? "").toContain("/login")
    })

    test("HSTS is set on the response a browser actually gets", async ({ request }) => {
      const response = await request.get(at("/login"))
      expect(response.headers()["strict-transport-security"] ?? "").toContain("max-age=31536000")
    })
  })
}

if (process.env.CLARA_DEPLOY_HTTP_URL) {
  test("plain HTTP redirects to HTTPS", async ({ request }) => {
    const response = await request.get(process.env.CLARA_DEPLOY_HTTP_URL as string, { maxRedirects: 0 })
    expect([301, 308]).toContain(response.status())
    expect(response.headers().location ?? "").toMatch(/^https:\/\//)
  })
}
