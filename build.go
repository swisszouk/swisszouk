package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	blackfriday "github.com/russross/blackfriday/v2"
	yaml "gopkg.in/yaml.v2"
)

var tailwindBin = flag.String("tailwind", "tailwind/tailwindcss-windows-x64.exe", "tailwind binary")

type Event struct {
	Title      string   `yaml:"title"`
	Slots      []string `yaml:"slots"`
	Location   string
	URL        string `yaml:"URL"`
	City       string
	DateString string    `yaml:"date"`
	Date       time.Time `yaml:"date_date"`
	Hour       string    `yaml:"time"`
	Image      string    `yaml:"image"`
	Hidden     bool      `yaml:"hidden"`
	Price      string    `yaml:"price"`

	DescriptionHTML template.HTML
	SourceFileName  string

	SeparatorBelow string
}

func (e Event) TimeAndDate() string {
	return e.Hour + ", " + e.Date.Format("Jan 2")
}
func (e Event) NiceDate() string {
	return e.Date.Format("Jan 2")
}
func (e Event) Domain() string {
	u, err := url.Parse(e.URL)
	if err != nil {
		return e.URL
	}
	return u.Hostname()
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

	var yamlText, mdText string
	switch parts := strings.Split(sourceContent, "---"); len(parts) {
	case 1:
		yamlText = sourceContent
	case 2:
		yamlText = parts[0]
		mdText = strings.TrimSpace(parts[1])
	}

	ev := &Event{}
	if err := yaml.Unmarshal([]byte(yamlText), &ev); err != nil {
		return nil, fmt.Errorf("bad front matter: %v", err)
	}
	city, ok := cityMap[ev.City]
	if ev.City != "" && !ok {
		return nil, fmt.Errorf("unknown city %q, try one of: %v", ev.City, cityMap) // TODO maps.keys
	}
	ev.City = city
	if ev.DateString != "" {
		loc, _ := time.LoadLocation("Europe/Berlin")
		t, err := time.ParseInLocation("2006-01-02", ev.DateString, loc)
		if err != nil {
			return nil, fmt.Errorf("bad date %q: %w", ev.DateString, err)
		}
		ev.Date = t
	}
	if !strings.HasPrefix(ev.URL, "http") {
		ev.URL = "https://" + ev.URL
	}
	if ev.Price != "" && !strings.HasSuffix(ev.Price, "CHF") {
		ev.Price = strings.TrimSpace(ev.Price) + " CHF"
	}

	if mdText == "" {
		return ev, nil
	}

	parser := blackfriday.New(
		blackfriday.WithRenderer(r.htmlRenderer),
		blackfriday.WithExtensions(blackfriday.CommonExtensions),
	)

	mdSource := strings.ReplaceAll(mdText, "\r\n", "\n")
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
	sort.Slice(events, func(i, j int) bool { return events[i].Date.Before(events[j].Date) })
	for i := 0; i+1 < len(events); i++ {
		_, prevM, _ := events[i].Date.Date()
		_, nextM, _ := events[i+1].Date.Date()
		if prevM != nextM {
			events[i].SeparatorBelow = nextM.String()
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
	cmd := exec.Command(*tailwindBin, "-i", "app.css", "-o", path.Join(r.outDir, "compiled_style.css"))
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
		outDir:         "docs",
		htmlRenderer: blackfriday.NewHTMLRenderer(blackfriday.HTMLRendererParameters{
			Flags: blackfriday.CommonHTMLFlags,
		}),
	}
	r.renderAll()
	go watch(r.renderAll)

	s := http.FileServer(http.Dir(r.outDir))
	fmt.Println("preview server at :9000")
	http.ListenAndServe(":9000", s)

}
