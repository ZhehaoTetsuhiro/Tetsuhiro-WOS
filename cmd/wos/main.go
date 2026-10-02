// Command wos runs the wave-optics simulator server with the embedded
// keyboard-operated web GUI.
//
//	go build -o wos ./cmd/wos
//	./wos -addr :1120        # then open http://localhost:1120
//
// Scripted elements (元件定义文件) live in JSON files under `elements/` next to
// the binary, next to the working directory and in ~/.wos/elements; extra
// directories can be named with -elements. The files are rescanned every couple
// of seconds while the server runs, so editing one shows up in the GUI without
// restarting anything. -check-elements validates them and exits, and -gen-go
// prints a native Go element equivalent to one definition.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"twos/optics"
	"twos/server"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":1120", "listen address")
	maxMB := flag.Int64("max-run-mb", 512, "in-memory budget for stored run data")
	extraDirs := flag.String("elements", "", "额外的元件定义目录（逗号分隔，优先级高于默认目录）")
	check := flag.Bool("check-elements", false, "只校验元件定义文件，打印结果后退出")
	genGo := flag.String("gen-go", "", "把元件定义文件导出为 Go 原生元件源码（打印到 stdout）")
	genName := flag.String("gen-name", "", "导出元件的类型名（默认使用定义文件里的 name）")
	noWatch := flag.Bool("no-elements-watch", false, "关闭元件定义文件的自动重载（默认每 2 秒检查一次）")
	flag.Parse()

	if *genGo != "" {
		if err := runGenGo(*genGo, *genName); err != nil {
			fmt.Fprintln(os.Stderr, "生成失败:", err)
			os.Exit(1)
		}
		return
	}

	dirs := optics.DefaultElementDirs()
	for _, d := range strings.Split(*extraDirs, ",") {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}
	optics.SetElementDirs(dirs)
	report := optics.ReloadElementDefinitions(nil)
	logElementReport(report)
	if *check {
		if len(report.Errors) > 0 {
			os.Exit(1)
		}
		return
	}
	if !*noWatch {
		go watchElements()
	}

	srv := server.New(*maxMB << 20)
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Handler())
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("Tetsuhiro WOS 波动光学模拟器 http://localhost%s", *addr)
	log.Printf("  内核: 角谱法/Fresnel/Fraunhofer 传播 + 可扩展元件注册表（详见 docs/）")
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// runGenGo prints the native Go element for one definition file.
func runGenGo(path, typeName string) error {
	def, err := optics.LoadElementDefinitionFile(path)
	if err != nil {
		return err
	}
	if typeName == "" {
		typeName = def.Name
	}
	src, err := optics.GenerateElementGo(def, typeName)
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(src)
	return err
}

// logElementReport prints one line per loaded definition plus every error and
// note, so both the startup log and a reload tell the same story.
func logElementReport(rep optics.DefinitionReport) {
	log.Printf("元件定义: 扫描 %d 个目录，已加载 %d 个脚本元件", len(rep.Dirs), len(rep.Loaded))
	for _, d := range rep.Loaded {
		log.Printf("  加载 %s（%s · %s · %d 个参数）← %s", d.Name, d.Label, d.Behavior, d.Params, d.Source)
	}
	for _, n := range rep.Skipped {
		log.Printf("  跳过 %s: %s", n.Name, n.Reason)
	}
	for _, e := range rep.Errors {
		log.Printf("  错误 %s: %s", e.Source, e.Message)
	}
}

// watchElements reloads the definition directories whenever a file changes.
// A plain modification-time poll keeps the kernel dependency-free.
func watchElements() {
	dirs := optics.ElementDirs()
	last := optics.DefinitionDirsMTime(dirs)
	for {
		time.Sleep(2 * time.Second)
		m := optics.DefinitionDirsMTime(dirs)
		if !m.After(last) {
			continue
		}
		last = m
		log.Printf("元件定义有变化，重新加载…")
		logElementReport(optics.ReloadElementDefinitions(nil))
	}
}
