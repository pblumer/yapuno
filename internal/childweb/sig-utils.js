// Encoding helpers for the child device login. No key material is handled
// here: these functions only re-encode public values and signatures.
(function (root) {
  "use strict";
  function url64(bytes) {
    const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    let binary = "";
    for (const byte of view) binary += String.fromCharCode(byte);
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  }
  // WebCrypto returns P-256 ECDSA signatures as IEEE-P1363 (r || s, 64 bytes).
  // The server verifies strict ASN.1 DER: SEQUENCE(INTEGER r, INTEGER s).
  function derFromP1363(raw) {
    const bytes = raw instanceof Uint8Array ? raw : new Uint8Array(raw);
    if (bytes.length !== 64) throw new Error("Unexpected signature length");
    function integer(part) {
      let start = 0;
      while (start < part.length - 1 && part[start] === 0) start++;
      const value = Array.from(part.slice(start));
      if (value[0] & 128) value.unshift(0);
      return [2, value.length, ...value];
    }
    const r = integer(bytes.slice(0, 32));
    const s = integer(bytes.slice(32, 64));
    return new Uint8Array([48, r.length + s.length, ...r, ...s]);
  }
  function validDeviceId(value) {
    return typeof value === "string" && /^[A-Za-z0-9_-]{22}$/.test(value);
  }
  function validNonce(value) {
    return typeof value === "string" && /^[A-Za-z0-9_-]{43}$/.test(value);
  }
  // The exact byte string the device signs. It must match the server.
  function loginMessage(deviceId, nonce) {
    if (!validDeviceId(deviceId) || !validNonce(nonce)) throw new Error("Invalid login challenge");
    return "yapuno-device-login-v1\0" + deviceId + "." + nonce;
  }
  const api = {url64, derFromP1363, validDeviceId, validNonce, loginMessage};
  root.YapunoSig = api;
  if (typeof module === "object" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
