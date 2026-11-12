package app

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"

	swaggerui "github.com/swaggest/swgui/v5/static"
)

//go:embed docs
var docsFiles embed.FS

var docsTemplate = template.Must(template.ParseFS(docsFiles, "docs/index.html"))

// docsCSP lets Swagger UI load its own scripts, styles, and spec, and nothing else.
const docsCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// registerDocs serves the spec and Swagger UI from files embedded in the binary, so /docs loads nothing from
// another origin.
func registerDocs(mux *http.ServeMux, title string, specJSON []byte) error {
	var page bytes.Buffer
	if err := docsTemplate.Execute(&page, title); err != nil {
		return fmt.Errorf("render the docs page: %w", err)
	}
	initScript, err := docsFiles.ReadFile("docs/init.js")
	if err != nil {
		return fmt.Errorf("read the docs script: %w", err)
	}
	mux.HandleFunc("GET /openapi.json", serveBytes("application/json", specJSON))
	mux.HandleFunc("GET /docs", serveBytes("text/html; charset=utf-8", page.Bytes()))
	mux.HandleFunc("GET /docs/init.js", serveBytes("text/javascript; charset=utf-8", initScript))
	for path, contentType := range map[string]string{
		"swagger-ui.css":       "text/css; charset=utf-8",
		"swagger-ui-bundle.js": "text/javascript; charset=utf-8",
	} {
		gzipped, err := fs.ReadFile(swaggerui.FS, path+".gz")
		if err != nil {
			return fmt.Errorf("read Swagger UI's %s: %w", path, err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(gzipped))
		if err != nil {
			return fmt.Errorf("unzip Swagger UI's %s: %w", path, err)
		}
		plain, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("unzip Swagger UI's %s: %w", path, err)
		}
		mux.HandleFunc("GET /docs/"+path, serveGzipped(contentType, gzipped, plain))
	}
	return nil
}

func serveBytes(contentType string, content []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(content)
	}
}

// serveGzipped sends the gzipped content when the client accepts gzip, and the plain content otherwise.
func serveGzipped(contentType string, gzipped, plain []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Cache-Control", "public, max-age=3600")
		h.Add("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.Set("Content-Encoding", "gzip")
			_, _ = w.Write(gzipped)
			return
		}
		_, _ = w.Write(plain)
	}
}
