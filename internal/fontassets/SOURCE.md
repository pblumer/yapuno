# Font sources

The files in `files/` are **unmodified copies** of the Latin and Latin Extended variable-weight subsets published in the `@fontsource-variable` npm packages. Each file carries its copyright notice and the OFL URL in its `name` table. The licence text of each package is copied to `licenses/`.

| Font | Skin key | Package and version | Package tarball SHA-256 | Licence |
|---|---|---|---|---|
| Plus Jakarta Sans | `jakarta` | `@fontsource-variable/plus-jakarta-sans` 5.3.0 | `271d7863880f0de211d9d44534e16907fba50cf6d47245de1938ff392ab5a30a` | OFL-1.1 |
| Bricolage Grotesque | `bricolage` | `@fontsource-variable/bricolage-grotesque` 5.3.0 | `6e99e0d844f7bc152bb3b007f9b1493c53e60cc0744cc681bb744e0b5f14665e` | OFL-1.1 |
| Nunito | `nunito` | `@fontsource-variable/nunito` 5.3.0 | `be290c38a2fa3422e2b51c0c4992511a9483d6f6e0074fc24eb8968e6ac67330` | OFL-1.1 |
| JetBrains Mono | `mono` | `@fontsource-variable/jetbrains-mono` 5.3.0 | `996fe6368a480c9ce15d4de22a2682b7c40b403718fee2b15e7272f244fd993f` | OFL-1.1 |

From each package: `files/<name>-latin-wght-normal.woff2`, `files/<name>-latin-ext-wght-normal.woff2` and `LICENSE`. The SHA-256 of every font file is pinned in `fontassets_test.go`; the unicode ranges in `fontassets.go` are the ones the packages publish for these subsets (identical across the four packages).

## What was checked (2026-11-20)

- Each `LICENSE` is the SIL Open Font License 1.1 with a project copyright line and **no Reserved Font Name** declaration.
- With fontTools: family names match the names used in the skin font stacks; weight axes are 200–800 (Plus Jakarta Sans, Bricolage Grotesque), 200–1000 (Nunito) and 100–800 (JetBrains Mono); the Latin subsets contain German, French and Italian letters and common punctuation. `✓` and `←` are not in the subsets and fall back to a system font.
- The Bricolage Grotesque file is the weight-only variant; optical size and width are not variable in it.

## Updating

Replace the files and licences from a newer package version, update the checksums and the total size in the test, re-run the checks above, and update this table. Do not subset or rename fonts without reviewing the licences again.

This is a technical check, not legal advice.
