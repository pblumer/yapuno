// Parse a Yapuno QR invitation without trusting arbitrary QR URL targets.
// No link or token ever goes to a third-party service.
(function (root) {
  "use strict";
  function validInvite(value, expectedOrigin) {
    if (typeof value !== "string" || value.length > 2048) return null;
    let link;
    try {
      link = new URL(value.trim(), expectedOrigin + "/");
    } catch (_) {
      return null;
    }
    if (link.origin !== expectedOrigin || link.protocol !== "https:" ||
        link.username || link.password || link.pathname !== "/pair" ||
        link.search || !/^#[A-Za-z0-9_-]{43}$/.test(link.hash)) {
      return null;
    }
    return "/pair" + link.hash;
  }
  const api = {validInvite};
  root.YapunoPairScan = api;
  if (typeof module === "object" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
