# Repository and archive size reduction plan

**Status:** Archive cleanup, Git LFS migration, push, and local Git object reclamation are complete. A fresh checkout retrieved and verified the LFS data. Browser indexes were rebuilt after that check exposed ignored `.DS_Store` metadata in the local archive.

## Goal

Reduce the size of the Git repository and the active local archive while retaining the useful raw listing captures and parsed listing records. For each confirmed image identity, retain one best rendition in the active data: either its JPEG or its WebP, at one resolution. Do not keep both encodings or multiple resolution variants of the same image in the active archive.

Keep the source HTML, source URLs, structured listing data, required browser indexes, and the best available copy of each distinct image. Treat the original files as authoritative until image matching and quality review are complete. Do not remove two files merely because they belong to the same listing or have similar names.

## Current evidence

Inspection on 2026-09-26 found:

- `main` is one commit ahead of `origin/main`; that commit is `918b14bd` and its parent is `7e4c2f59`.
- The failed push did not advance the remote branch. The remote rejected the incoming pack because it exceeded 2 GiB.
- The working tree is about 12 GiB: `.git` about 4.8 GiB, `JamesEdition` about 3.8 GiB, and `RealEstateArchive` about 3.2 GiB.
- The ahead commit introduces about 20,088 objects and 4.99 GiB of unique blob data. Its largest groups are JPGs (about 2.27 GiB), WebPs (about 1.87 GiB), and videos (about 0.58 GiB).
- The tracked tree currently has about 8,557 `.jpg` files and 11,021 `.webp` files. The tracked `.js` and `.css` files total about 0.85 GiB on disk, but contribute much less unique content to this push than the image and video files.
- The parsed `listing.json` records contain 10,748 image references with JamesEdition source image UUIDs. Across those records, 1,123 UUIDs have four to six local renditions each (760, 900, 1536, 2000/2200 widths and sometimes a crop rendition); 30 image references have no corresponding local file. Keep the highest-quality available rendition for each confirmed source image UUID, not every size variant.
- `RealEstateArchive` is the parsed archive. Its browser indexes and listing files are generated data used by the documented archive format. `JamesEdition` holds browser-saved raw pages and their `_files` resources.
- `.gitignore` currently does not exclude either archive directory. `.serena/` is untracked and must not be included in the cleaned commit.

Recompute all counts, byte totals, and commit comparisons immediately before execution; these values describe the inspected worktree and may change.

The user-provided `du -hs ./*` baseline on 2026-09-27 was:

| Path | Before |
| --- | ---: |
| `AGENTS.md` | 8.0 KiB |
| `cmd` | 4.0 KiB |
| `cookies.txt` | 4.0 KiB |
| `docs` | 4.0 KiB |
| `go.mod` | 4.0 KiB |
| `go.sum` | 8.0 KiB |
| `internal` | 1.0 MiB |
| `JamesEdition` | 3.8 GiB |
| `listing-archiver` | 11 MiB |
| `main.go` | 4.0 KiB |
| `Makefile` | 4.0 KiB |
| `README.md` | 16 KiB |
| `RealEstateArchive` | 3.2 GiB |
| `run.sh` | 4.0 KiB |
| `serve.sh` | 4.0 KiB |
| `tasks` | 20 KiB |
| `testdata` | 0 B |

Immediately after the archive cleanup, before the commit and Git object reclamation, `du -hs ./*` reported `JamesEdition` 2.8 GiB and `RealEstateArchive` 2.5 GiB. Final per-path measurements are recorded below after push and local Git cleanup.

## Implementation progress

- Copied `JamesEdition` and `RealEstateArchive` to `/private/tmp/ListingArchiver-before-20260927` and verified all 14,540 and 15,248 files respectively by size and SHA-256, excluding the untracked macOS `.DS_Store` file.
- The matching report found 1,123 listing/image UUID groups with multiple parsed renditions. Kept each group's 2,200-width WebP and removed 4,188 lower-resolution/crop variants (770,762,740 bytes).
- Matched 1,119 raw sidecar JPEGs to parsed images by listing ID and the same source UUID in raw `<picture>` markup; the raw resources were 1100-width renditions. Removed those redundant files (221,472,022 bytes).
- Removed 4,534 raw sidecar `.js` and `.css` files (810,851,751 bytes). The original raw tree is retained in the verified backup.
- Refreshed all 226 listing manifests/README/listing scripts and rebuilt indexes for 733 directories using archive package routines. `-verify` passes: 733 directories, 226 listings, 8,207 file entries.
- The regular `-migrate` command encountered an existing destination collision at the Corfu archive path. It moved no listing directories. The direct refresh preserved existing listing paths and regenerated required data successfully.
- A broader perceptual-hash scan was stopped after a slow batch; no files were removed based on visual similarity alone. The removals above rely on matching source UUIDs and resolution metadata.
- Git LFS 3.8.0 was downloaded to temporary storage and checked against the official SHA-256, then initialized for this repository. Path-scoped attributes cover the retained source pages, images, and videos; README.md documents the full-data and code-only checkout flows.
- Before commit, the staged tree had 20,292 changed paths: 15,914 paths used the new LFS attributes, with 37,893,889 bytes across 2,726 unique ordinary Git blobs. The staged LFS objects passed `git lfs fsck --pointers --objects --dry-run`.
- `go fmt ./...` and `go vet ./...` passed. The first normal `go test ./...` attempt was blocked by sandbox loopback restrictions; rerunning with local loopback access passed. `go test -race ./...` also passed.
- `main` was reset to the verified `origin/main` parent while leaving the cleaned working tree intact. Replacement commit `35a6e17b` was pushed as a normal fast-forward from `7e4c2f59`; the rejected commit `918b14bd` is not in the new ancestry. The ordinary Git tree contains about 36.2 MiB of unique blobs before pack compression.
- Git LFS uploaded 11,671 objects totaling 4.4 GB. A temporary fresh clone retrieved and checked out all 15,914 LFS pointers; `git lfs fsck` passed there. GitHub reported that it validated a random sample of 10,000 objects during push.
- The fresh checkout revealed three ignored macOS `.DS_Store` files in the local archive; two had been incorporated into generated browser indexes. Removed this OS metadata and rebuilt indexes. `-verify` now passes for all 733 directories, 226 listings, and 8,207 file entries.
- After confirming the remote branch and verified backup, expired local reflogs and ran `git gc --prune=now`. Git object storage fell from 4.75 GiB of packed objects plus unreachable data to one 7.98 MiB pack. `.git` is now 4.1 GiB, mostly the retained local LFS object cache; the LFS cache was not pruned.
- Final `du -hs ./*` was:

  ```text
  8.0K  ./AGENTS.md
  2.8G  ./JamesEdition
  4.0K  ./Makefile
   16K  ./README.md
  2.5G  ./RealEstateArchive
  4.0K  ./cmd
  4.0K  ./cookies.txt
  4.0K  ./docs
  4.0K  ./go.mod
  8.0K  ./go.sum
  1.0M  ./internal
   11M  ./listing-archiver
  4.0K  ./main.go
  4.0K  ./run.sh
  4.0K  ./serve.sh
   24K  ./tasks
    0B  ./testdata
  ```

  `.git` is hidden from the `./*` glob; its separate post-GC measurement is 4.1 GiB.

The central delivery gates are complete. The original inventory records 30 listing image references without a matching local image; these were already missing before cleanup and remain unresolved rather than guessed or silently rewritten.

## Target state

1. Git history contains application source, documentation, small sanitized fixtures, and LFS pointers for any retained archive data that needs to live on the enabled remote LFS store. Bulk media is not stored as ordinary Git blobs.
2. Raw captures and parsed archives remain available through the remote LFS objects plus a verified independent backup, or in a separately managed data location outside the Git repository if local checkout size is a concern.
3. Each confirmed image identity has one active image file and one chosen format/resolution: JPEG or WebP, with the highest-quality suitable rendition. Preserve the higher-fidelity source when formats differ; if quality is equivalent, prefer the smaller file, with WebP as the tie-breaker.
4. Parsed archive records, source pages, URLs, manifests, required generated indexes, and media references agree after cleanup.
5. The branch to push is rebuilt from `origin/main`, so the rejected large-data commit is not an ancestor of the new commit.
6. Original data is retained in a verified recovery copy until all matching, archive checks, and push checks pass. After final approval of the deduplication report, the recovery copy must also be reduced to the selected single format per confirmed image if the one-format rule is intended to apply to all retained copies, not just the active archive.
7. Git LFS is enabled on the remote and is the route for retained large collection files that need remote versioning. Verify the remote repository's LFS storage, bandwidth, and per-file limits before upload. LFS keeps binary payloads out of ordinary Git packs; it does not reduce downloaded working-tree size or the remote storage needed for the media.

## Data retention rules

### Keep

- Raw saved listing HTML and the associated source URL for adapter selection, URL resolution, identity, and later offline imports.
- Parsed `listing.json`, manifests, README files, source HTML and URLs, and all required `index.html`, `index.json`, and `index.js` browser indexes.
- One best-quality local image per confirmed image identity, with stable listing references and source URL/hash information retained in the listing metadata or inventory.
- Distinct images, including images from the same listing that are different photos or crops.
- Direct videos and any unique media that is not duplicated elsewhere.
- Small sanitized fixtures needed to develop and test extraction offline.

### Review before excluding from the active collection

- Browser-saved `_files` content, especially JavaScript, CSS, fonts, icons, analytics, tracking pixels, and ancillary HTML. These files can affect how a saved raw page renders, so document that effect before excluding them from the active raw capture. The exact original capture should remain in the verified recovery copy until the review is accepted.
- Images with uncertain identity, different crops, different visible content, materially different dimensions, or uncertain quality. Keep both temporarily and classify them; do not count an uncertain pair as a confirmed duplicate.
- Any videos or images referenced by a listing manifest. Update the references through the archive pipeline before removing a file.

## Execution plan

### Phase 1: Freeze, inventory, and establish recovery

1. Do not retry the current push and do not delete or convert files.
2. Record the current branch, `HEAD`, `origin/main`, remotes, status, and the exact parent/commit relationship. Save a list of files changed by `918b14bd` outside the repository.
3. Produce a machine-readable inventory for both data trees with relative path, byte size, SHA-256, extension, listing identity, image dimensions where applicable, and media type. Keep raw and parsed namespaces distinct.
4. Copy both trees to a separate storage location. Verify file counts, total bytes, and SHA-256 values against the source inventory. Open representative HTML files and parsed listings; decode representative images and videos from the copy.
5. Preserve the failed commit ID and the inventory with the recovery copy. Do not make the recovery copy a long-term duplicate store after deduplication is approved.

**Gate:** No pruning or history rewrite until both tree copies pass checksum verification and the inventory can identify every source file.

### Phase 2: Build an image identity and format-pair report

Generate a report that groups image candidates across `JamesEdition` and `RealEstateArchive`. Record, for every candidate:

- Listing identity and path in each tree.
- Original source URL and normalized URL, when available from the HTML or listing manifest.
- SHA-256, extension, MIME type, dimensions, byte size, and any original filename or image ID.
- Whether the candidate is an exact-byte duplicate, a confirmed JPEG/WebP rendition of the same image, a likely match needing review, or an unrelated/different image.
- Proposed retained path, chosen encoding, and reason for the decision.

Use evidence in this order:

1. Exact content hash: identical bytes under different names or extensions are exact duplicates.
2. Matching JamesEdition source image UUID in source URLs, listing manifests, or raw page markup. Treat different size/format URLs for the same UUID as renditions of one image; compare dimensions and retain only the best suitable rendition. Prefer the largest original source dimensions, and drop smaller responsive sizes. If a same-UUID crop differs materially in framing, review it against the largest uncropped image before selecting one.
3. Visual comparison with perceptual hashes or image-diff tooling, alongside dimensions and aspect ratio. Use perceptual matching to suggest candidates, not to delete automatically.
4. Manual review for ambiguous cases. Similar property photos, resized variants, alternate crops, and different photos of the same property can look alike while containing different information.

Do not use listing membership alone as proof that two files are the same image. Do not auto-pair files solely from matching sequence numbers such as `image(1).jpg` and `image(1).webp`.

For confirmed renditions, select one file deterministically. Prefer the highest original pixel dimensions and best visible detail. If two source renditions are equal in quality, choose the smaller byte representation; prefer WebP on an otherwise equal tie. Record the retained and removed hashes, dimensions, source UUID, and URL mapping in a deduplication manifest. Never recompress or convert the selected original as part of this cleanup.

**Gate:** Review counts and byte savings by confirmed exact duplicates, confirmed alternate encodings, uncertain candidates, and unpaired images. No uncertain candidate is deleted. Review visual contact sheets for all ambiguous pairs and a sample of automatic matches.

### Phase 3: Choose the durable locations and reduce duplicate media

1. Keep a verified recovery copy of the complete raw and parsed collections outside the Git working tree, using separate raw-capture and parsed-archive roots. The reduced active collections may remain under `JamesEdition` and `RealEstateArchive` for upload through LFS. If local checkout size is also a concern, use an LFS skip-smudge workflow or keep the active collection in the external roots and upload from a dedicated LFS staging checkout.
2. Decide whether the active collection needs the raw browser `_files` directories. If the active raw capture is for byte-preserved HTML and future extraction, retain the HTML and URL; exclude ancillary resources only after the offline-rendering effect is documented. The verified full copy remains available through this decision.
3. Apply the image identity report to the active collections. For each confirmed UUID/rendition family, retain only its chosen format and highest-quality suitable resolution. Remove smaller responsive versions and duplicate cross-tree encodings only after their references have been updated and the retained file passes hash and decode checks.
4. Where the raw page and parsed archive both carry copies of the same photo, keep one active canonical media copy in the parsed listing and retain the raw HTML and URL. Do not alter the raw HTML bytes. If the raw `_files` layout must remain usable offline, document how it references the retained media and avoid leaving broken local links; otherwise preserve the original sidecar only in the temporary recovery copy, then remove it from active storage after acceptance.
5. Keep distinct source image UUIDs, even when they depict the same property. For one source UUID, keep one rendition only; do not retain multiple sizes of the same photo. Use review for materially different crop variants before choosing the single retained rendition.
6. Rebuild or update listing manifests, image statuses, local filenames, and browser indexes through the archive tool/pipeline where possible. Do not hand-edit generated indexes as a substitute for their generator.

**Gate:** The retained active media paths decode successfully; every parsed listing reference resolves; every confirmed identity has exactly one active image encoding; all unmatched files remain accounted for.

### Phase 4: Trim browser-capture extras safely

1. Measure the size and unique hashes of `_files` JavaScript, CSS, fonts, trackers, and other resources by listing. Exact repeated blobs are already stored once by Git, so estimate savings from unique content, not the sum of repeated file sizes.
2. Keep any asset demonstrated to contain unique listing data or needed for the intended offline raw-page experience. Exclude analytics, tracking, duplicate vendor bundles, challenge artifacts, and unrelated page resources from the active collection after confirming they are not the only copy of valuable listing content.
3. Retain `source.html` byte-for-byte in the parsed archive, along with its source URL. Do not remove required parsed archive files or the single source template `internal/archive/index.html`.
4. Record the excluded asset classes and their byte totals in the inventory. Preserve the full original raw capture outside the repo until the final retention decision.

### Phase 5: Put retained remote archive payloads in Git LFS

1. Git LFS is enabled on the remote, but this checkout does not have the `git lfs` command installed. Install/configure Git LFS locally before staging LFS-managed files; no `.gitattributes` or `.lfsconfig` files are currently tracked.
2. Check the remote LFS quota, bandwidth, per-file limit, and any organization policy. Decide which retained files must be downloadable from the GitHub remote. Keep a verified independent backup regardless of that decision.
3. Add reviewed, path-scoped `.gitattributes` entries for retained large archive payloads. Likely candidates are the single selected image format and retained videos under `JamesEdition` and `RealEstateArchive`; raw listing HTML may also use LFS if it is needed remotely. Keep small source files, JSON manifests, browser indexes, and code as ordinary Git text unless measured size justifies otherwise. Avoid broad global patterns that would unexpectedly put source fixtures or unrelated repository assets in LFS.
4. Do not put pruned JPEG/WebP renditions, browser analytics bundles, CSS duplicates, or temporary backups into LFS. LFS objects consume remote storage even though Git commits contain small pointer files.
5. Verify `git check-attr` for representative files, `git lfs ls-files` for the intended tracked set, and pointer contents for the staged result before creating the cleaned commit. Confirm each retained image identity has only the selected format staged.
6. Add ignore rules for local-only backup/export directories, credentials, `.serena/`, and any archive data intentionally kept outside the repository. Keep archive roots trackable when their selected contents are intentionally stored through LFS.
7. If full collections should not be downloaded on every checkout, document a `GIT_LFS_SKIP_SMUDGE=1` clone/fetch workflow and the explicit command to retrieve the archive paths when needed. LFS shrinks the Git pack and regular Git history; it does not shrink a fully materialized archive on disk.

### Phase 6: Rebuild the pending Git commit without the large data

1. Verify that `origin/main` still points to the expected parent. Fetch first if repository access is available and re-check the remote relationship.
2. Preserve the small intended changes from `918b14bd`: `.gitignore`, `AGENTS.md`, `run.sh`, and `serve.sh`. Recreate them on a new commit based directly on `origin/main`.
3. Add only the selected collection data intended for remote distribution, with the reviewed LFS attributes active before files are staged. Keep local-only collections and backups out of the index; confirm `.serena/` is excluded.
4. Review the full staged path list and byte summary. Confirm media files are represented by LFS pointers in ordinary Git, and that the LFS upload set contains no discarded image formats or unneeded capture assets.
5. Review `git rev-list --objects origin/main..HEAD`. Confirm the rejected commit `918b14bd` is not an ancestor of the new branch tip and the normal Git push pack is comfortably below the 2 GiB remote limit.
6. Push the clean branch as a normal fast-forward update and verify the LFS object upload completed. Do not force-push unless the remote relationship has changed and the required history rewrite has been reviewed separately.

**Gate:** The new commit contains only intended code/documentation changes plus intended LFS pointer files; the remote branch advances successfully; both data collections remain readable from their active archive paths and verified recovery roots.

### Phase 7: Reclaim Git storage and close out deduplication

1. After the recovery copy, selected media, archive references, and successful Git/LFS push are verified, remove local safety references to the rejected commit if they are no longer needed.
2. Expire reflog references and run Git garbage collection only after confirming the old commit and removed payloads have recoverable copies. Check `.git` size and object counts afterward.
3. LFS payloads may remain in the local `.git/lfs` cache after normal Git garbage collection. Use Git LFS's own prune operation only after successful remote upload, verification from a clean checkout, and confirmation that the independent backup is complete.
4. Once the JPEG/WebP decision is accepted and no rollback is needed, apply the same one-format-per-confirmed-image rule to any retained backup or mirror that is intended to remain authoritative. Retain the deduplication manifest and checksums so the selected source and discarded rendition are auditable.
5. Document the LFS retrieval workflow and any external backup restore procedure without adding private paths, listing details, or credentials to the public repository.

## Completion checklist

- [x] Source HTML, URLs, parsed listing records, browser indexes, and retained parsed images/videos remain in the archive and a SHA-256-verified recovery copy. Raw saved HTML still references some deleted sidecar assets, so offline rendering of those raw pages is degraded.
- [x] Pre-cleanup inventory and checksum manifests reconcile with the recovery copy; the removal manifest records the cleanup choices and byte totals.
- [x] For each confirmed UUID rendition family, one largest available parsed rendition is retained; matching 1100-width raw JPEG sidecars were removed where the parsed image with the same listing ID and source UUID remains.
- [x] No removals were based only on perceptual similarity. The broader visual-hash scan was stopped; uncertain matches were preserved. The retained recovery copy still contains the original pre-cleanup formats and renditions.
- [x] Generated archive indexes and manifests verify after excluding ignored `.DS_Store` metadata. The initial inventory's 30 unmatched image references were pre-existing and remain unresolved.
- [x] Retained remote archive media is LFS-managed, and local-only `.serena/` and credential files are excluded from Git. Unneeded raw sidecar JS/CSS and confirmed duplicate JPEGs are absent from the active tree.
- [x] Replacement branch was based on `origin/main`; rejected commit `918b14bd` is not an ancestor of the pushed branch.
- [x] GitHub accepted the normal Git push and LFS upload. A fresh clone retrieved and checked out all 15,914 LFS pointers, and LFS integrity checks passed.
- [x] Reflogs were expired and Git object cleanup ran only after the recovery copy and remote push were verified.
