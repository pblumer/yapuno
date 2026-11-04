# Skin rendering slice

First-party token definitions in `internal/skin` can now be rendered as fixed CSS custom properties for two distinct starter skins: `classic` and `playful`.

Use semantic CSS variables in future components, and apply `data-yp-skin` on the root application element. The CSS serializer accepts validated definitions only; it does not accept executable themes, arbitrary selectors, CSS/JS imports or remote assets.

This is a rendering contract and unit tests, **not** a complete working chat UI. Follow-up: CSS component library, accessibility/contrast validation, preference persistence and theme picker, responsive visual regression, parent controls, QR onboarding and messaging.

Security-critical affordances and consent requirements are not skin-configurable. Related #16.
