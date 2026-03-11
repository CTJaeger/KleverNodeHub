package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/version"
	"github.com/CTJaeger/KleverNodeHub/web"
)

func main() {
	info := version.Get()
	fmt.Printf("Klever Node Hub - Dashboard %s (%s)\n", info.Version, info.GitCommit)

	staticFS, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		log.Fatalf("failed to load static assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := web.StaticFS.ReadFile("templates/index.html")
		if err != nil {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(tmpl)
	})

	addr := ":9443"
	fmt.Printf("Starting dashboard on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
