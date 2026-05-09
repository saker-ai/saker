package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// gzipPool reuses gzip.Writer instances. Level 5 is a good balance between
// CPU cost and ratio for JS/CSS/HTML — full compress/gzip default (6) costs
// noticeably more on large bundles without much extra savings.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, 5)
		return w
	},
}

// brotliPool reuses brotli.Writer instances. Level 4 keeps encoding cheap
// enough for on-the-fly serving while still beating gzip-5 on text payloads.
// Higher levels (6-11) get materially better ratios but spike CPU on large
// JS bundles, which is exactly what static asset traffic is.
var brotliPool = sync.Pool{
	New: func() any {
		return brotli.NewWriterLevel(io.Discard, 4)
	},
}

// compressibleExt lists file extensions whose payloads benefit from gzip/br.
// Already-compressed formats (.png, .woff2, .br, .gz, video/audio) are skipped
// because re-compressing them wastes CPU and usually grows the payload.
var compressibleExt = map[string]bool{
	".js":          true,
	".css":         true,
	".html":        true,
	".htm":         true,
	".json":        true,
	".svg":         true,
	".txt":         true,
	".xml":         true,
	".map":         true,
	".wasm":        true, // wasm is uncompressed by default and gzips to ~30%
	".woff":        true, // woff2 is already compressed; woff isn't
	".ttf":         true,
	".otf":         true,
	".ico":         true,
	".webmanifest": true,
}

// immutableCachePrefix is the path segment Next.js uses for content-hashed
// build artifacts. Anything under it is safe to cache forever because the
// filename itself changes whenever the file changes (e.g.
// `/_next/static/chunks/abc123.js`). We set the cache header before invoking
// the wrapped handler so it lands in the response regardless of whether
// gzip wrapping kicks in below.
const immutableCachePrefix = "/_next/static/"

// compressedWriter abstracts over gzip.Writer and brotli.Writer so the
// response wrapper doesn't have to branch on encoding for every Write.
type compressedWriter interface {
	io.Writer
	Close() error
}

// compressingResponseWriter buffers Write() calls through an underlying
// compressed writer (gzip or brotli). Headers are written lazily on the
// first Write so we can still drop compression if the upstream handler
// short-circuits with a non-200 or sets Content-Encoding itself.
type compressingResponseWriter struct {
	http.ResponseWriter
	cw       compressedWriter
	encoding string // "gzip" or "br"
	wroteHdr bool
	status   int
}

func (g *compressingResponseWriter) WriteHeader(status int) {
	g.status = status
	if g.wroteHdr {
		return
	}
	g.wroteHdr = true
	h := g.ResponseWriter.Header()
	// Don't double-encode if upstream already chose an encoding.
	if h.Get("Content-Encoding") == "" && status >= 200 && status < 300 {
		h.Set("Content-Encoding", g.encoding)
		// Content-Length is now wrong because the body is going through the
		// compressor; strip it so the client falls back to chunked transfer.
		h.Del("Content-Length")
		h.Add("Vary", "Accept-Encoding")
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *compressingResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHdr {
		g.WriteHeader(http.StatusOK)
	}
	if g.ResponseWriter.Header().Get("Content-Encoding") == g.encoding {
		return g.cw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// gzipStaticHandler wraps an http.Handler that serves static files and
// transparently compresses the response when:
//  1. The client signals support via Accept-Encoding (br preferred over gzip)
//  2. The URL points at a compressible file extension
//
// File extension is the cheap sniff — content-type detection would require
// reading the body. For a static dist this is enough because every file we
// care about (.js/.css/.wasm/...) has a stable extension.
//
// As a side effect this handler also stamps `Cache-Control: immutable` on
// any path under /_next/static/ — Next's content-hashed build artifacts
// never change in place, so a year-long browser cache is correct and saves
// the round-trip on every revisit.
//
// The function name is kept as `gzipStaticHandler` for backward compatibility
// with existing callers; brotli is added as a transparent upgrade path.
func gzipStaticHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, immutableCachePrefix) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		encoding := negotiateEncoding(r)
		if encoding == "" || !shouldCompress(r.URL.Path) {
			h.ServeHTTP(w, r)
			return
		}

		var cw compressedWriter
		switch encoding {
		case "br":
			bw := brotliPool.Get().(*brotli.Writer)
			defer brotliPool.Put(bw)
			bw.Reset(w)
			cw = bw
		case "gzip":
			gz := gzipPool.Get().(*gzip.Writer)
			defer gzipPool.Put(gz)
			gz.Reset(w)
			cw = gz
		}

		gw := &compressingResponseWriter{ResponseWriter: w, cw: cw, encoding: encoding}
		defer func() {
			if w.Header().Get("Content-Encoding") == encoding {
				_ = cw.Close()
			}
		}()
		h.ServeHTTP(gw, r)
	})
}

// negotiateEncoding picks the strongest encoding the client accepts, with
// brotli preferred over gzip. Returns "" when neither is acceptable. Quality
// values (q=...) are ignored — for static asset traffic the savings of br vs
// gzip vastly outweigh the rare client that demotes br via q=0.5.
func negotiateEncoding(r *http.Request) string {
	header := r.Header.Get("Accept-Encoding")
	if header == "" {
		return ""
	}
	hasBr, hasGzip := false, false
	for _, enc := range strings.Split(header, ",") {
		token := strings.TrimSpace(enc)
		if i := strings.Index(token, ";"); i >= 0 {
			token = token[:i]
		}
		switch token {
		case "br":
			hasBr = true
		case "gzip":
			hasGzip = true
		}
	}
	if hasBr {
		return "br"
	}
	if hasGzip {
		return "gzip"
	}
	return ""
}

// acceptsGzip is preserved for the existing test suite; new code should call
// negotiateEncoding instead.
func acceptsGzip(r *http.Request) bool {
	enc := negotiateEncoding(r)
	return enc == "gzip" || enc == "br"
}

func shouldCompress(p string) bool {
	return compressibleExt[strings.ToLower(path.Ext(p))]
}
