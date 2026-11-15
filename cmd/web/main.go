// Command web serves a browser interface for the pricer's products.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed static
var staticFiles embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	flag.Parse()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	log.Printf("pricer web interface on http://%s", *addr)
	log.Fatal(srv.ListenAndServe())
}

func newHandler() http.Handler {
	catalog := products()
	byID := make(map[string]product, len(catalog))
	for _, p := range catalog {
		byID[p.ID] = p
	}
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/products", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, catalog)
	})
	mux.HandleFunc("POST /api/price", handle(byID, func(p product, e env) (any, error) {
		resp, err := p.price(e)
		resp.Product = p.ID
		return resp, err
	}))
	mux.HandleFunc("POST /api/curve", handle(byID, func(p product, e env) (any, error) {
		c, err := p.curve(e)
		return c, err
	}))
	return mux
}

// handle decodes the request, validates it, and writes fn's result as JSON.
func handle(byID map[string]product, fn func(product, env) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		req, p, err := decodeRequest(body, byID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		e, err := newEnv(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		out, err := fn(p, e)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// decodeRequest applies the body on top of the product's defaults, so a
// client may send only the inputs it wants to change.
func decodeRequest(body []byte, byID map[string]product) (request, product, error) {
	var head struct {
		Product string `json:"product"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return request{}, product{}, fmt.Errorf("invalid JSON: %w", err)
	}
	p, ok := byID[head.Product]
	if !ok {
		return request{}, product{}, fmt.Errorf("unknown product %q", head.Product)
	}
	req := p.Defaults.clone()
	if err := json.Unmarshal(body, &req); err != nil {
		return request{}, product{}, fmt.Errorf("invalid request: %w", err)
	}
	if p.Basket && (len(req.Underliers) < 1 || len(req.Underliers) > 10) {
		return request{}, product{}, errors.New("a basket needs between 1 and 10 underliers")
	}
	return req, p, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		writeError(w, http.StatusUnprocessableEntity, errors.New("the result is not a finite number; check the inputs"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
