// Package web отдаёт страницу чата. Она встроена в бинарник, поэтому сервису
// не нужны ни сборка фронтенда, ни отдельный статический сервер.
package web

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var index []byte

// Handler отдаёт страницу по корню. Скрипт и стили встроены в HTML, поэтому
// политика разрешает только свой источник и inline-код.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serve)
	mux.HandleFunc("GET /index.html", serve)
	return mux
}

func serve(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; base-uri 'none'; form-action 'none'; "+
			"connect-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(index)
}
