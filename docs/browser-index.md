# Browser index format

Every directory managed by `listing-archiver` contains:

- `index.html`: an exact copy of the embedded source page at
  `internal/archive/index.html`.
- `index.json`: a generated description of that directory.
- `index.js`: the same generated description assigned to a JavaScript variable
  for browsers that block local JSON reads.

The page contains all CSS and JavaScript it needs and reads the adjacent
`index.json`, falling back to `index.js` when needed. The page is intentionally
identical at every archive level.

For display, the page renders `path` as a breadcrumb-style label such as
`United States > New York > Albany > 10 Main Street`. The raw `path` value in
`index.json` remains slash-delimited.

## Schema version 1

```json
{
  "schema_version": 1,
  "path": "United States/New York",
  "parent": "../index.html",
  "directories": [
    {
      "name": "Albany",
      "href": "Albany/index.html"
    }
  ],
  "files": [
    {
      "name": "listing data.json",
      "href": "listing%20data.json",
      "size": 1234
    }
  ],
  "last_updated": "2026-06-10T13:14:23Z"
}
```

- `path` is relative to the archive root. The root uses `"."`.
- `parent` is omitted at the root.
- `directories` and `files` are sorted case-insensitively by name.
- `href` values are URL-escaped relative links. Directory and parent links
  explicitly target the generated `index.html`; removing `index.html` from an
  HTTP URL requests the underlying directory instead.
- `size` is the file size in bytes.
- `last_updated` is an RFC3339 UTC timestamp.
- Symlinks and generated `index.html`/`index.json`/`index.js`/`listing.js`
  files are not listed.

Indexes are updated atomically along an affected listing's path after a
successful archive run. Run `listing-archiver -reindex -root <archive>` to
rebuild every directory in an existing archive.

Open the root `index.html` directly through `file://`; the page falls back to
`index.js` if the browser blocks access to adjacent JSON. You can alternatively
serve the archive root through a local HTTP server:

```bash
python3 -m http.server --directory ./RealEstateArchive 8000
```
