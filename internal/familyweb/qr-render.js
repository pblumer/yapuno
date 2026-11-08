"use strict";

// QR encoding stays entirely in this browser; the temporary invitation never
// leaves Yapuno for a remote QR rendering service.
(() => {
  const root = document.getElementById("pairing-qr");
  const link = root && root.dataset.link;
  if (!root || !link) return;
  try {
    if (typeof qrcode !== "function") throw new Error("QR renderer unavailable");
    const qr = qrcode(0, "M");
    qr.addData(link, "Byte");
    qr.make();
    // SVG has quiet zone and sufficient module size for physical scanning.
    root.innerHTML = qr.createSvgTag({cellSize: 7, margin: 28});
    const svg = root.querySelector("svg");
    if (svg) {
      svg.setAttribute("role", "img");
      svg.setAttribute("aria-label", "QR-Code für das Kindergerät");
      svg.setAttribute("style", "max-width:100%;height:auto;background:white");
    }
  } catch (_) {
    // The normal HTML link remains as a safe accessible fallback.
    root.textContent = "QR-Code konnte nicht angezeigt werden. Verwende den Link darunter.";
  }

  const copy = document.getElementById("copy-pair-link");
  const share = document.getElementById("share-pair-link");
  const note = document.getElementById("pair-link-note");
  if (copy) copy.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(link);
      if (note) note.textContent = "Link kopiert – sicher an das Kindergerät senden.";
    } catch (_) {
      if (note) note.textContent = "Kopieren nicht verfügbar: Link darunter auswählen.";
    }
  });
  if (share) {
    if (typeof navigator.share !== "function") share.hidden = true;
    share.addEventListener("click", async () => {
      try {
        await navigator.share({title: "Yapuno – Gerät verbinden", url: link});
        if (note) note.textContent = "Einladungslink geteilt.";
      } catch (_) {
        if (note) note.textContent = "Teilen abgebrochen – du kannst den Link kopieren.";
      }
    });
  }
})();
