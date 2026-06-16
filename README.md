# listing-archiver

`listing-archiver` creates reproducible local archives of real-estate listing
pages. JamesEdition is the initial supported site. The tool is intended for
personal archival of pages the user is authorized to access.

## Build

Go 1.23 or newer is required.

```bash
go build -o listing-archiver ./cmd/listing-archiver
```

The root compatibility command also remains available:

```bash
go run . -url 'https://www.jamesedition.com/real_estate/...'
```

## Usage

```bash
./listing-archiver \
  -url 'https://www.jamesedition.com/real_estate/...' \
  -cookies /path/to/cookies.txt \
  -root ./RealEstateArchive \
  -workers 6 \
  -timeout 45s
```

Archive several listings sequentially:

```bash
./listing-archiver \
  -url 'https://www.jamesedition.com/real_estate/...' \
  -url 'https://www.jamesedition.com/real_estate/...' \
  -root ./RealEstateArchive

./listing-archiver -input ./listing-urls.txt -root ./RealEstateArchive
printf '%s\n' 'https://www.jamesedition.com/real_estate/...' |
  ./listing-archiver -input - -root ./RealEstateArchive
```

Positional URLs are also accepted. Input files are newline-delimited; blank
lines and lines beginning with `#` are ignored. Duplicate input strings are
processed once. Batch runs continue after individual failures and return a
nonzero exit code if any listing failed.

Import HTML saved from an authorized browser without fetching the protected
listing page:

```bash
./listing-archiver \
  -html ./imports/listing.html \
  -source-url 'https://www.jamesedition.com/real_estate/...' \
  -root ./RealEstateArchive

./listing-archiver -html-dir ./imports -root ./RealEstateArchive
```

`-html-dir` processes top-level `.html` and `.htm` files in deterministic filename order.
Each file's source URL is discovered from a same-basename `.url` sidecar first,
then an absolute canonical link or `og:url`. Files without a usable HTTP(S)
source URL fail individually while the remaining batch continues. Imported
HTML is preserved exactly as `source.html`.

By default, `-html-dir` skips files whose listing already exists in the
archive, printing the existing browser page:

```text
Skipped existing: ./imports/listing.html [found: /Canada/Ontario/Oakville/2054 Lakeshore Rd E/index.html]
```

Use `-refresh` to intentionally reprocess existing imported HTML and rewrite
the manifest, retained source, browser page, and image status. If improved
extraction produces a cleaner address path, the existing archive directory is
moved before it is rewritten.

Plain-text `.url` sidecars and Windows `[InternetShortcut]` files with a
`URL=https://...` line are supported. Browser-saved HTML normally retains an
absolute canonical URL. Prefer saving the rendered listing page rather than a
Cloudflare challenge page; detected challenge HTML is rejected.

Use `-metadata-only` for a completely offline import. Otherwise, discovered
image URLs are downloaded normally; browser-challenged image failures are
recorded without discarding successfully extracted listing metadata.

Flags:

```text
-url          listing URL; may be repeated
-input        newline-delimited URL file, or - for standard input
-html         import one saved HTML file instead of fetching its page
-html-dir     import top-level .html files from a directory
-source-url   original listing URL required with -html
-root         archive root (default ./listings)
-cookies      optional JSON or Netscape-format cookie export
-workers      concurrent image downloads (default 6)
-timeout      per-request timeout (default 45s)
-overwrite    replace existing image files
-metadata-only archive metadata and source HTML without downloading images
-max-images   maximum images per listing; 0 means unlimited
-refresh      reprocess existing HTML imports instead of skipping them
-reindex      rebuild browser indexes beneath the archive root
-user-agent   HTTP User-Agent
```

Cookie files are credentials. Keep them outside the repository and archive
root. Cookie contents are loaded into memory and are never written into an
archive or printed by the application.

Cookie format is detected automatically. Supported formats:

- Browser-extension JSON arrays containing `domain`, `hostOnly`, `httpOnly`,
  `name`, `path`, `sameSite`, `secure`, `session`, and `value`.
- Netscape tab-delimited cookie exports.

JSON is preferred because it preserves host-only and SameSite metadata. Cookies
whose exact values cannot be represented by Go's standard cookie jar are sent
through a scoped transport only when their domain, path, and HTTPS rules match.
Partitioned cookies are preserved from JSON exports. Go's `http.Cookie`
supports the `Partitioned` attribute, while the standard cookie jar does not
retain the exported partition key. The archiver therefore keeps partitioned
cookies in its scoped transport and sends them only when the exported
top-level-site, domain, path, HTTPS, and cross-site-ancestor rules match.

## Archive format

```text
<root>/
├── index.html
├── index.json
├── index.js
└── <country>/<region>/<municipality>/<address>/
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

Every managed directory receives an identical browser page copied from
`internal/archive/index.html`. Its matching local `index.json` and `index.js`
contain the same current path, parent, child-directory, and file data. The page
tries `index.json` first and dynamically loads `index.js` when a browser blocks
local JSON access through `file://`. Generated browser-index files are omitted
from their own file listings. Indexes are updated atomically after successful
archives.

At listing directories, the same page also renders the structured manifest.
It loads `listing.json` first and falls back to its generated `listing.js`
counterpart for direct `file://` browsing. Directory and parent links
explicitly target each generated `index.html`; removing `index.html` from an
HTTP URL requests the underlying directory instead. Listing pages render all
available images in a full-viewport-width gallery at the bottom. The page
header displays the archive path as `country > region > municipality > address`;
the listing title remains in the listing detail heading.

Open the root `index.html` directly, or serve the archive root locally:

```bash
python3 -m http.server --directory ./RealEstateArchive 8000
# or:
make serve ARCHIVE_ROOT=./RealEstateArchive PORT=8000
```

Rebuild browsing indexes for an archive created by an older version:

```bash
./listing-archiver -reindex -root ./RealEstateArchive
```

`listing.json` is an explicit, versioned manifest. Schema version 1 contains:

- source URL, canonical URL, site listing ID, public listing reference,
  first-listed/last-updated display values, and retrieval timestamp;
- structured location and property data, including a Google Maps URL when the
  source page provides one, plus typed coordinates;
- typed numeric prices, price per area, bedroom/bathroom/floor counts, years,
  interior and lot measurements, photo count, availability, and video URL;
- public broker/agent names, profile URLs, license, and agency address, when
  available;
- image source URLs, local filenames, media types, byte counts, and failures;
- retained JSON-LD evidence and extraction/download warnings.

Map URLs may contain precise coordinates published by the source listing.
Review archive contents before sharing them.

Browser-saved `source.html` can also contain authenticated account details,
pre-filled inquiry forms, tokens, or other private browser-session data. The
extractor does not promote that data into `listing.json`, but the unchanged raw
HTML must still be treated as sensitive.

Repeated URL runs against the same source reuse valid existing images and
update the manifest and retained source atomically. Repeated `-html-dir` runs
skip already archived listings unless `-refresh` is set. The tool refuses to
overwrite an archive directory whose manifest identifies a different source.
When separate listings resolve to the same address path, the later listing
receives a stable listing-ID or URL-hash suffix.

## Development

```bash
make fmt
make vet
make test
make race
make build
make check
make serve ARCHIVE_ROOT=./RealEstateArchive
```

Tests use sanitized fixtures and local `httptest.Server` instances. They do not
require or permit live JamesEdition access.

## Responsible use

Use the tool only for pages you are authorized to access. Respect applicable
site terms, reasonable request rates, privacy obligations, and copyright law.
Archived HTML and images may contain personal information and copyrighted
material; protect and retain them accordingly.

The project does not bypass CAPTCHAs, anti-bot challenges, paywalls, access
controls, or authorization barriers. It does not implement stealth,
fingerprint spoofing, proxy rotation, or rate-limit evasion.

## Troubleshooting

- `unsupported listing site`: the final redirected URL does not match an
  installed site adapter.
- `load cookies`: verify the file is a supported JSON browser-extension export
  or tab-delimited Netscape export. Cookie values are intentionally omitted
  from errors.
- `HTTP 401` or `HTTP 403`: the server refused access. Export a fresh cookie
  file from a browser session authorized to view the listing and retry. The
  archiver will not bypass access controls or anti-bot challenges.
- `Cloudflare returned a browser challenge`: the site requires JavaScript and
  browser-specific challenge state beyond exported cookies. Use a normal
  authorized browser, save the listing HTML, and import it with `-html` or
  `-html-dir`; the archiver intentionally does not bypass the challenge.
- `no usable source URL found for imported HTML`: add a same-basename `.url`
  sidecar containing the original absolute listing URL.
- `Unknown State` or `Unknown Municipality`: the saved page did not expose
  enough location evidence. The JamesEdition adapter checks structured data,
  semantic breadcrumbs, the listing title, and the canonical URL. Reimport
  after updating the archiver; existing incorrectly located archive
  directories are not moved automatically.
- `response is not a recognized image`: the image URL returned non-image data,
  often an error or challenge page. The failure remains in `listing.json`.
- `refuse to overwrite archive for a different source`: two listings resolved
  to the same sanitized fallback path. Choose another root or move the existing
  archive.
- An imported HTML file is skipped unexpectedly: rerun with `-refresh` to
  reprocess existing listings. This may move older title-based archive
  directories to cleaner address-based paths.
- Timeouts or partial failures: rerun the same command. Completed images are
  reused and `.part` downloads are resumed when the server supports ranges.
