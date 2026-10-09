# Authenticated SDK transport verification

Issue #17 is independently based on main. No live credentials or network endpoints are used by security fixtures.

1. Use Node 24.21.0 and Go 1.27.2. Run npm run test, npm run lint, npm run typecheck and npm run build.
2. Run npm run sdk:check and npm run sdk:test for generated contract consistency and actual authenticated API integration.
3. Run go test -race ./... from packages/clients/go. Synthetic origin matrices cover HTTPS, supported loopback forms, misleading host suffixes, mapped IPv6, malformed schemes and embedded credentials. Go redirect tests use disposable HTTP/TLS servers and verify the destination receives no request; TypeScript verifies the actual Request redirect policy.

Factories reject remote HTTP before requests, require origin-only URLs and use safe fixed error messages. Exactly localhost, 127.0.0.1 and ::1 are supported HTTP development hosts. HTTPS is allowed. Default redirects are stopped; supplied Go *http.Client instances are cloned before applying this rule. A custom fetch or arbitrary HttpRequestDoer is caller-owned and must enforce the same policy. No production authentication, key rotation or deployment is performed.
