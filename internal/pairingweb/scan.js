"use strict";

(() => {
  const status = document.getElementById("status");
  const start = document.getElementById("start-camera");
  const stop = document.getElementById("stop-camera");
  const video = document.getElementById("camera");
  const form = document.getElementById("paste-link");
  const field = document.getElementById("invite-url");
  const validate = globalThis.YapunoPairScan?.validInvite;
  let stream = null;
  let scanning = false;
  let timer = null;

  function message(text) { status.textContent = text; }

  function releaseCamera() {
    scanning = false;
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    if (stream) {
      for (const track of stream.getTracks()) track.stop();
      stream = null;
    }
    video.pause();
    video.srcObject = null;
    video.hidden = true;
    stop.hidden = true;
    start.disabled = false;
  }

  function navigateToInvite(raw) {
    const target = validate && validate(raw, location.origin);
    if (!target) return false;
    releaseCamera();
    // Only an allowlisted same-origin /pair#token fragment is accepted.
    // A QR link cannot redirect to a phishing site or send the token in GET.
    location.assign(target);
    return true;
  }

  async function detectLoop(detector) {
    if (!scanning) return;
    try {
      if (video.readyState >= 2) {
        const codes = await detector.detect(video);
        if (!scanning) return;
        for (const code of codes) {
          if (navigateToInvite(code.rawValue)) return;
        }
        if (codes.length > 0) message("Das ist kein Yapuno-Einladungs-QR-Code. Versuche einen anderen.");
      }
    } catch (_) {
      // A transient camera frame failure must not expose the token or
      // disable the accessible native-camera and paste fallbacks.
      message("QR-Erkennung unterbrochen. Halte die Kamera ruhig oder nutze die normale Kamera-App.");
    }
    if (scanning) timer = setTimeout(() => detectLoop(detector), 300);
  }

  async function startCamera() {
    if (stream || scanning) return;
    if (!validate || !globalThis.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
      message("Diese Kamera-Funktion braucht HTTPS und einen kompatiblen Browser. Nutze die normale Kamera-App oder einen erhaltenen Link.");
      return;
    }
    if (typeof BarcodeDetector !== "function") {
      message("Dieser Browser hat keine eingebaute QR-Erkennung. Öffne die normale Kamera-App und scanne den QR-Code dort.");
      return;
    }
    start.disabled = true;
    try {
      const detector = new BarcodeDetector({formats: ["qr_code"]});
      stream = await navigator.mediaDevices.getUserMedia({
        audio: false,
        video: {facingMode: {ideal: "environment"}}
      });
      video.srcObject = stream;
      video.hidden = false;
      await video.play();
      scanning = true;
      stop.hidden = false;
      message("Kamera bereit. QR-Code in das Bild halten.");
      detectLoop(detector);
    } catch (_) {
      releaseCamera();
      message("Kamera nicht verfügbar oder Berechtigung verweigert. Die normale Kamera-App oder ein geteilter Link funktioniert weiterhin.");
    }
  }

  start.addEventListener("click", startCamera);
  stop.addEventListener("click", () => {
    releaseCamera();
    message("Kamera ausgeschaltet.");
  });
  form.addEventListener("submit", event => {
    event.preventDefault();
    if (!navigateToInvite(field.value)) {
      message("Dieser Link gehört nicht zu einer gültigen Yapuno-Geräteeinladung.");
    }
  });
  window.addEventListener("pagehide", releaseCamera);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) releaseCamera();
  });
})();
