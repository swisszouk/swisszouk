package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	blackfriday "github.com/russross/blackfriday/v2"
	yaml "gopkg.in/yaml.v2"
)

var tailwindBin = flag.String("tailwind", "tailwind/tailwindcss-windows-x64.exe", "tailwind binary")

type Event struct {
	Title    string   `yaml:"title"`
	Slots    []string `yaml:"slots"`
	Location string
	URL      string
	City     string

	DescriptionHTML template.HTML
	SourceFileName  string
}

var cityMap = map[string]string{
	"zrh":   "Zürich",
	"basel": "Basel",
	"bern":  "Bern",
}

type renderer struct {
	sourceGlob     string
	assetSourceDir string
	outDir         string
	htmlRenderer   blackfriday.Renderer
}

func (r *renderer) renderEvent(sourceContent string) (*Event, error) {

	parts := strings.Split(sourceContent, "---")
	if len(parts) != 2 {
		return nil, fmt.Errorf("no front matter marker found")
	}
	ev := &Event{}
	if err := yaml.Unmarshal([]byte(parts[0]), &ev); err != nil {
		return nil, fmt.Errorf("bad front matter: %v", err)
	}
	city, ok := cityMap[ev.City]
	if ev.City != "" && !ok {
		return nil, fmt.Errorf("unknown city %q, try one of: %v", ev.City, cityMap) // TODO maps.keys
	}
	ev.City = city

	parser := blackfriday.New(
		blackfriday.WithRenderer(r.htmlRenderer),
		blackfriday.WithExtensions(blackfriday.CommonExtensions),
	)
	mdSource := strings.TrimSpace(string(parts[1]))
	mdSource = strings.ReplaceAll(mdSource, "\r\n", "\n")
	ast := parser.Parse([]byte(mdSource))
	h := ast.FirstChild
	if h.Type != blackfriday.Heading || h.HeadingData.Level != 1 {
		return nil, fmt.Errorf("Expected a top-level heading at the beginning (a line starting with #)")
	}
	ev.Title = strings.TrimSpace(string(h.FirstChild.Literal))
	var buf bytes.Buffer
	r.htmlRenderer.RenderHeader(&buf, ast)
	ast.Walk(func(node *blackfriday.Node, entering bool) blackfriday.WalkStatus {
		return r.htmlRenderer.RenderNode(&buf, node, entering)
	})
	r.htmlRenderer.RenderFooter(&buf, ast)
	ev.DescriptionHTML = template.HTML(buf.String())
	return ev, nil
}

func (r *renderer) warnf(format string, args ...any) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	format = time.Now().Format("15:04:05") + " " + format
	fmt.Printf(format, args...)
}

func (r *renderer) renderAll() {
	files, err := filepath.Glob(r.sourceGlob)
	if err != nil {
		r.warnf("%v", err)
	}
	var events []*Event
	for _, fpath := range files {
		content, err := os.ReadFile(fpath)
		if err != nil {
			r.warnf("Skipping %s: %v", fpath, err)
			continue
		}
		ev, err := r.renderEvent(string(content))
		if err != nil {
			r.warnf("Skipping %s: %v", fpath, err)
		} else {
			r.warnf("Loaded %s (%s).", fpath, ev.Title)
			events = append(events, ev)
		}
	}

	dest := filepath.Join(r.outDir, "index.html")
	sink, err := os.Create(dest)
	if err != nil {
		r.warnf("Open %s for writing: %w", dest, err)
		return
	}

	var mainT = template.Must(template.ParseFiles("template.html"))
	if err := mainT.Execute(sink, events); err != nil {
		r.warnf("Write %s: %v", dest, err)
		return
	}
	sink.Close()
	r.warnf("Regenerated %s, %d events.", dest, len(events))
	cmd := exec.Command(*tailwindBin, "-i", "app.css", "-o", "_build/compiled_style.css")
	if err := cmd.Run(); err != nil {
		r.warnf("Tailwind failed: %v", err)
		return
	}
	r.warnf("Tailwind done.")
}

func watch(f func()) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	w.Add(".")
	w.Add("events")
	w.Add("img")

	for {
		select {
		// watch for events
		case <-w.Events:
			f()

			// watch for errors
		case err := <-w.Errors:
			log.Fatal(err)
		}

	}
}

func main() {

	r := renderer{
		sourceGlob:     "events/*.md",
		assetSourceDir: "img",
		outDir:         "_build",
		htmlRenderer: blackfriday.NewHTMLRenderer(blackfriday.HTMLRendererParameters{
			Flags: blackfriday.CommonHTMLFlags,
		}),
	}
	r.renderAll()
	go watch(r.renderAll)

	s := http.FileServer(http.Dir("_build"))
	fmt.Println("preview server at :9000")
	http.ListenAndServe(":9000", s)

}
