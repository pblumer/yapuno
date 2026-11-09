# Family dashboard: HTML rendering slice

This PR adds a standalone, accessible and responsive HTML renderer for the existing least-privilege `familydashboard.Build` projection.

- All first-party skins from `internal/skin` (schema v2, see [skin contract](skin-contract.md)); unknown or invalid skin identifiers fall back to Classic. Component rules use semantic `--yp-*` variables only; a test rejects hard-coded colors and undeclared variables.
- Child names are HTML-escaped and untrusted skin strings cannot be injected into attributes or CSS.
- Touch-sized action indicators, responsive child cards, empty state and semantic headings.
- Action indicators are **not clickable** because no authenticated management routes exist yet. This avoids misleading or unsafe actions.
- RenderHTML is not an authorization boundary. The authenticated caller must use Build with trusted guardian grants. Future HTTP handlers must re-check authorization for every mutation.

## Next

Add authenticated dashboard route and trusted grant repository; integrate a real child switcher, device/contact approval requests, guardian roles and accessible forms. Do not enable management mutations until multi-guardian conflict rules are settled.

Related #13 #14 #16.
