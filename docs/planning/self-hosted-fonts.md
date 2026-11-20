# Self-hosted fonts

**Status:** implemented for all pages that use skin tokens. Decision context: [skin token contract ADR draft](../adr/draft-skin-token-contract.md), which required fonts to be self-hosted with verified licences before use.

## What changed

The skin contract names fonts by key (`system`, `jakarta`, `bricolage`, `nunito`, `mono`). Until now only the names were in the CSS, so every theme rendered in system fonts. `internal/fontassets` now embeds the font files in the binary and serves them from the application's own origin at `/assets/fonts/`.

- **No third party:** no page requests a font, stylesheet or anything else from another host. Font loading reveals nothing about who uses Yapuno.
- **Only what a theme uses:** a page declares `@font-face` rules only for its own skin's fonts. The default `classic` skin uses system fonts and loads **no font file**. The "Mein Look" picker declares all four because it previews every theme; a browser fetches a file only when text actually uses it.
- **Subsets:** each font has a Latin and a Latin Extended file with `unicode-range`; German text needs only the Latin file.
- **Caching:** file names contain a content hash and are served with `Cache-Control: public, max-age=31536000, immutable`. A changed file gets a new name.
- **Policy:** skinned pages send `font-src 'self'` next to `default-src 'none'`. Nothing else was relaxed.
- **Closed set:** the handler serves exactly the eight embedded files and `LICENSES.txt`; any other path is `404`, other methods are `405`. No cookie is read or set.
- `font-display: swap` shows text immediately in the fallback font and swaps when the font arrives.

Mounted in `yapuno-web` and `yapuno-demo`. No configuration is needed.

## Licences

All four families are under the **SIL Open Font License 1.1** without a Reserved Font Name. Provenance, checksums and what was verified: [`internal/fontassets/SOURCE.md`](../../internal/fontassets/SOURCE.md). The licence texts are in the repository and served at `/assets/fonts/LICENSES.txt`. This is a technical check, not legal advice; the repository's own licence is still undecided.

## Measured payload (headless Chromium, German text)

| Skin | Font files fetched | Bytes |
|---|---|---|
| classic | 0 | 0 |
| night | 1 | 27,348 |
| cloud, playful | 1 | 39,128 |
| terminal | 1 | 40,404 |
| vivid | 2 | 68,692 |

All eight files together are 239,404 bytes in the binary. These are measurements, not budgets; budgets should follow real-device baselines (MFE spec).

## Evidence

- Go tests: pinned SHA-256 of every file, WOFF2 signature, licence texts present without Reserved Font Name, only known keys produce rules, every declared URL is served with the right headers, nothing else is served, every skin's font has files under the family name its CSS requests, pages declare only their own skin's fonts, CSP allows fonts from the own origin on every skinned page. 11 mutations of the handler and CSS generation are each detected.
- Headless Chromium through the real server over TLS: for each of the six skins the fonts load from the own origin with status 200, no request leaves the origin and no CSP violation is reported; computed font families match; the picker fetches a font only when its theme is previewed; the child chat page renders in the hosted font.
- The browser check found that the local demo's own policy blocked fonts; fixed and covered by a test. Caching was confirmed on the demo (second visit transfers 0 bytes); Chromium does not cache under the self-signed test certificate.

**Not verified:** physical iPhone/Android, slow networks and the visible swap from fallback to web font, data-saver modes, real-device rendering quality.

## Limitations

- Device, pairing and login pages and the signed-out child shell still use fixed system-font styling.
- Characters outside the two subsets (for example `✓`, `←`, emoji, Cyrillic) fall back to system fonts.
- Fonts are not preloaded.

Related #16, #5.
