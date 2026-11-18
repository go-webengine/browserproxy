module github.com/go-webengine/browserproxy

// ⛔ THE PATCH, AND THIS REPOSITORY IS ONE OF THE NINE THE FLEET SWEEPS FOR
// EXPOSURE: it is the browser proxy, network in, untrusted HTML parsed. This
// line is the only one of the three Go pins a CONSUMER honours — `go install`
// with GOTOOLCHAIN=local obeys it, while the CI pin merely builds our artefacts
// and the toolchain stamped in a binary describes one executable.
//
// Raised to 1.27.4 on 2026-11-15 for GO-2026-6859 — published 2026-11-11,
// CVE-2026-97054, "HTTP/1 server crash due to MaxBytesReader header race in
// net/http", stdlib, fixed in 1.26.11 and 1.27.4. Read at
// https://vuln.go.dev/ID/GO-2026-6859.json rather than quoted from notes.
//
// ⛔ AND THIS ONE IS A PROXY: it SERVES HTTP as well as fetching it, so an
// advisory about an HTTP/1 server is this program's own class rather than a
// contract detail. (Whether its handlers reach the racing path is a
// REACHABILITY question, which govulncheck answers and this line does not.)
//
// Measured, not assumed: `modfloor --floor 1.27.4` read the directive this
// module PUBLISHED at v0.2.1 as `go 1.27.3`. Eight of the nine network-facing
// modules read BELOW; only go-webengine/engine was already at 1.27.4. This is
// the second-to-last of the eight.
//
// Previous raise, kept because the record is the point: 1.27.2 -> 1.27.3 on
// 2026-10-31 for GO-2026-6809 (crypto/x509 Certificate.Verify), which was
// REACHABLE from cmd/browserproxy — not merely required.
go 1.27.4

require (
	github.com/go-webengine/engine v0.48.0
	github.com/grpc-transports/websocket v0.4.0
	google.golang.org/grpc v1.86.0-dev
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/andybalholm/brotli v1.2.8 // indirect
	github.com/breml/rootcerts v0.3.7 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/dop251/goja v0.0.0-20260926152631-39ec2650adc9 // indirect
	github.com/dsnet/compress v0.0.2-0.20230904184137-39efe44ab707 // indirect
	github.com/ebitengine/purego v0.11.3 // indirect
	github.com/evanw/esbuild v0.28.2 // indirect
	github.com/go-browserhttp/browserhttp v0.2.0 // indirect
	github.com/go-gfx/gfx v0.37.0 // indirect
	github.com/go-gfx/oksvg v0.4.0 // indirect
	github.com/go-gfx/wgsl v0.4.0 // indirect
	github.com/go-images/gif v0.2.0 // indirect
	github.com/go-images/images v0.1.1-0.20261026203936-4ac989d310a1 // indirect
	github.com/go-images/jpeg v0.3.0 // indirect
	github.com/go-images/jpeg2000 v0.14.0 // indirect
	github.com/go-images/png v0.2.0 // indirect
	github.com/go-macos/metal v0.7.0 // indirect
	github.com/go-macos/objc v0.12.0 // indirect
	github.com/go-opentype/fonts v0.14.0 // indirect
	github.com/go-opentype/opentype v0.21.1 // indirect
	github.com/go-simd/highway v0.0.14 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/go-webengine/esbuildsandbox v0.1.0 // indirect
	github.com/go-widgets/painter v0.16.0 // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	github.com/klauspost/compress v1.20.2 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/tannevaled/gobig2 v0.2.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
)
