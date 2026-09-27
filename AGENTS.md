# AGENTS.md

## Project

`listing-archiver` is a Go CLI for creating reproducible local archives of real-estate listing pages that the user is authorized to access.

The project currently begins with JamesEdition support but must be designed around independent site adapters so additional listing websites can be added without contaminating core archive logic with site-specific selectors.

## Primary goals

1. Reliably archive listing metadata and images.
2. Retain source HTML for reproducibility.
3. Produce stable, understandable local directory layouts.
4. Remain safe to rerun and resume.
5. Keep extraction logic testable without live network access.
6. Protect cookie and authorization data.
7. Avoid mechanisms intended to bypass access controls or anti-bot systems.

## Archive layout

```text
<root>/<country>/<state-or-region>/<municipality>/<address>/
├── index.html
├── index.json
├── index.js
├── listing.json
├── listing.js
├── README.md
├── source.html
├── source.url
└── images/
    ├── index.html
    ├── index.json
    └── index.js
```

The root and every intermediate archive directory also contain `index.html`,
`index.json`, and `index.js`. `internal/archive/index.html` is the single source page and
must be copied byte-for-byte. Browser indexes are generated data and must be
updated atomically after successful archive changes.

Do not change this layout casually. Any breaking archive-format change requires:

- A schema version change
- Documentation
- Migration or backward-compatibility consideration
- Tests

## Go conventions

- Use the Go version declared in `go.mod`.
- Run `gofmt` on all changed Go files.
- Prefer standard-library packages where they are sufficient.
- Wrap errors with useful context using `%w`.
- Pass `context.Context` through network and long-running operations.
- Avoid global mutable state.
- Bound all concurrency.
- Close response bodies and files promptly.
- Never ignore meaningful errors.
- Keep exported APIs minimal.
- Add package comments only where they add value.
- Do not create interfaces solely for mocking; create them around genuine boundaries.

## Suggested package boundaries

```text
cmd/listing-archiver
internal/archive
internal/config
internal/cookies
internal/downloader
internal/extract
internal/model
internal/pathutil
internal/site
internal/site/jamesedition
```

These boundaries are guidance, not an immutable requirement. Improve them when the code provides a clear reason.

## Extraction rules

Prefer extraction sources in this order:

1. JSON-LD
2. Embedded application-state JSON
3. Metadata and Open Graph tags
4. Stable semantic HTML
5. Text fallback

Keep site-specific parsing in its site adapter.

Offline HTML imports must use the same extraction and archive-writing pipeline
as fetched pages. Preserve imported HTML byte-for-byte. A source URL is still
required for adapter selection, relative URL resolution, and archive identity.

Do not silently invent missing listing values. Missing information should remain absent, null, or empty according to the schema.

Preserve original displayed values when normalization may lose useful information.

## Image rules

- Deduplicate normalized image URLs.
- Prefer highest-resolution variants.
- Validate image responses.
- Use bounded parallelism.
- Maintain deterministic ordering.
- Use stable filenames.
- Support safe reruns.
- Record individual failures.
- Do not fail the entire archive solely because one image failed.
- Do not save HTML challenge pages with image extensions.
- Do not fetch arbitrary unrelated assets.

## Filesystem safety

- Sanitize every path component.
- Prevent `..`, slash, backslash, control-character, device-name, and empty-component issues.
- Account for Windows reserved filenames.
- Avoid trailing dots and spaces.
- Avoid directory collisions where different listings normalize to the same path.
- Never write outside the configured root.
- Prefer atomic temporary-file-and-rename writes.

## Cookie and secret handling

- Support browser-extension JSON and Netscape-format cookie files.
- Never commit cookie files.
- Never copy cookies into archives.
- Never log cookie values.
- Never log authorization headers.
- Ensure errors do not accidentally include secrets.
- Add common cookie filenames and local archive directories to `.gitignore`.

## Responsible-use constraints

This project must not add:

- CAPTCHA bypass
- Anti-bot challenge bypass
- Proxy rotation for evasion
- Browser fingerprint spoofing
- Account credential harvesting
- Paywall circumvention
- Rate-limit evasion
- Access-control bypass

Normal authenticated requests using cookies supplied by the user are acceptable when the user is authorized to access the page.

## Testing

Tests must not require live JamesEdition access.

Use:

- `testdata` fixtures
- `httptest.Server`
- Temporary directories
- Deterministic clocks or injectable time sources where needed

Before completing a task, run:

```bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

When a command cannot run, state exactly why.

## Dependencies

Before adding a dependency:

1. Confirm the standard library is insufficient.
2. Prefer mature, narrowly scoped modules.
3. Avoid dependencies for trivial helpers.
4. Document major architectural dependencies.
5. Run `go mod tidy`.
6. Review resulting indirect dependencies.

## Documentation

Update `README.md` whenever behavior, flags, archive layout, schema, or setup changes.

Examples must be runnable and must not contain real cookies, credentials, personal paths, or private listing information.

## Change discipline

- Inspect existing behavior before refactoring.
- Add characterization tests before risky changes.
- Keep changes scoped to the requested task.
- Do not rewrite functioning components without a concrete benefit.
- Do not leave dead code or commented-out implementations.
- Do not claim successful validation without running it.
- Clearly report remaining assumptions and limitations.
