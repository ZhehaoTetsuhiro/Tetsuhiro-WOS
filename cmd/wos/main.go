// Command wos runs the wave-optics simulator server with the embedded
// keyboard-operated web GUI.
//
//	go build -o wos ./cmd/wos
//	./wos -addr :8080        # then open http://localhost:8080
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"

	"wos/server"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	maxMB := flag.Int64("max-run-mb", 512, "in-memory budget for stored run data")
	flag.Parse()

	srv := server.New(*maxMB << 20)
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Handler())
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("WOS 波动光学模拟器 http://localhost%s", *addr)
	log.Printf("  内核: 角谱法/Fresnel/Fraunhofer 传播 + 可扩展元件注册表（详见 docs/）")
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
