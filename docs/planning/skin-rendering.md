# Skin rendering

`internal/skin` renders validated definitions as CSS custom properties (`--yp-bg`, `--yp-text`, `--yp-own-bubble`, `--yp-font-body`, `--yp-bubble-shadow`, `--yp-wallpaper`, …). Six first-party skins ship: `classic`, `playful`, `vivid`, `night`, `cloud`, `terminal`.

Components reference only these variables and apply `data-yp-skin` on the root application element. A test fails if the family dashboard references an undeclared variable or hard-codes a color. `Stylesheet()` emits every skin under a fixed attribute selector in deterministic order and forces `--yp-motion:none` under `prefers-reduced-motion:reduce`.

The serializer accepts validated definitions only; it never emits the display name, selectors, `url()`, imports, remote assets or scripts.

The family dashboard is the first consumer; try `?skin=<id>` in the local demo (`go run ./cmd/yapuno-demo`) or the engineering preview. This is a rendering contract, **not** a complete chat UI.

Security-critical affordances and consent requirements are not skin-configurable. Related #16.
