# Local family browser demo

Run `go run ./cmd/yapuno-demo` and open `http://127.0.0.1:8085` on the same computer. Use the Classic and Playful links to compare skins. The demo uses **synthetic** children and a hard-coded guardian grant projection. It has no login, persistence, device pairing or management actions. It listens on loopback only, is not available on another device, and is **not safe for real children or internet exposure**.

Next: authenticated guardian routes, persistent storage and explicit multi-guardian authorization decisions; then QR pairing. Browser testing on a phone will require a separate secured development deployment.
