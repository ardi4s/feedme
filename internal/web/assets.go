package web

import (
	_ "embed"
	"net/http"
	"strconv"
)

// The brand images live inside the binary so the distroless runtime image can
// stay a single file. They are the logo shown next to the wordmark and the
// favicon browsers ask for.
//
//go:embed assets/feedme.png
var logoPNG []byte

//go:embed assets/favicon.png
var faviconPNG []byte

//go:embed assets/favicon.ico
var faviconICO []byte

// iconLinksHTML points a page's head at the embedded brand images. It is shared
// by the pages whose heads are assembled in Go; indexHTMLHead carries the same
// tags inline because it is a large raw string.
const iconLinksHTML = `<link rel="icon" href="/favicon.ico" sizes="any">` +
	`<link rel="icon" type="image/png" href="/favicon.png">` +
	`<link rel="apple-touch-icon" href="/feedme.png">`

// writeAsset serves an embedded image. The bytes never change while the process
// runs, so they are cached aggressively by the browser and the response is
// cheap to produce.
func writeAsset(w http.ResponseWriter, r *http.Request, body []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(body); err != nil {
		return
	}
}
