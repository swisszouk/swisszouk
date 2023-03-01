package main

import (
	"errors"
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
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/exp/maps"

	"github.com/fsnotify/fsnotify"
	yaml "gopkg.in/yaml.v2"
)

var tailwindBin = flag.String("tailwind", "tailwind/tailwindcss-windows-x64.exe", "tailwind binary")

var summaryCity = flag.String("summary_city", "zrh", "City to print summary for")
var showAll = flag.Bool("all", false, "Show events in the future")

type Event struct {
	Title                string `yaml:"title"`
	Location             string
	URL                  string `yaml:"URL"`
	City                 string
	DateString           string      `yaml:"date"`
	DateStringList       []string    `yaml:"dates"`
	Date                 time.Time   `yaml:"date_date"`
	DateList             []time.Time `yaml:"dates_date"`
	Hour                 string      `yaml:"time"`
	Image                string      `yaml:"image"`
	Hidden               bool        `yaml:"hidden"`
	Price                string      `yaml:"price"`
	CustomScheduleString string      `yaml:"custom_schedule_string"`

	SourceFileName string

	SeparatorBelow string
}

func (e Event) Domain() string {
	u, err := url.Parse(e.URL)
	if err != nil {
		return e.URL
	}
	var path string
	if u.Path != "" {
		path = "/..."
	}
	return strings.TrimPrefix(u.Hostname(), "www.") + path
}

func (e Event) ShortURL() string {
	url := strings.TrimPrefix(e.URL, "https://")
	url = strings.TrimPrefix(url, "http://")
	return strings.TrimPrefix(url, "www.")
}

var cityMap = map[string]string{
	"zrh":   "Zürich",
	"basel": "Basel",
	"bern":  "Bern",
}

type renderer struct {
	sourceGlob string
	outDir     string
}

var timeRe = regexp.MustCompile(`^\d?\d:\d\d$`)

func (r *renderer) renderEvent(yamlText string) ([]Event, error) {
	ev := &Event{}
	if err := yaml.UnmarshalStrict([]byte(yamlText), &ev); err != nil {
		return nil, fmt.Errorf("bad front matter: %v", err)
	}
	if ev.Title = strings.TrimSpace(ev.Title); ev.Title == "" {
		return nil, errors.New("no title")
	}
	if ev.Location = strings.TrimSpace(ev.Location); ev.Location == "" {
		return nil, errors.New("no location")
	}
	city, ok := cityMap[ev.City]
	if !ok {
		return nil, fmt.Errorf("unknown city %q, try one of: %v", ev.City, maps.Keys(cityMap))
	}
	ev.City = city
	if ev.DateString != "" {
		ev.DateStringList = append(ev.DateStringList, ev.DateString)
	}
	if len(ev.DateStringList) > 1 {
		r.warnf("multiple dates for %s (%d): %v", ev.Title, len(ev.DateStringList), ev.DateStringList)
	}
	for _, ds := range ev.DateStringList {
		loc, _ := time.LoadLocation("Europe/Berlin")
		t, err := time.ParseInLocation("2006-01-02", ds, loc)
		if err != nil {
			return nil, fmt.Errorf("bad date %q: %w", ev.DateString, err)
		}
		ev.DateList = append(ev.DateList, t)
	}
	if len(ev.DateList) == 0 {
		return nil, fmt.Errorf("no dates for %s", ev.Title)
	}
	if ev.Hour = strings.TrimSpace(ev.Hour); !timeRe.MatchString(ev.Hour) {
		return nil, fmt.Errorf("bad time %q; should be HH:MM", ev.Hour)
	}
	if ev.Price != "" && !strings.HasSuffix(ev.Price, "CHF") {
		ev.Price = strings.TrimSpace(ev.Price) + " CHF"
	}
	if !strings.HasPrefix(ev.URL, "http") {
		ev.URL = "https://" + ev.URL
	}
	if _, err := os.ReadFile(path.Join(r.outDir, ev.Image)); err != nil {
		return nil, fmt.Errorf("cannot read image %s: %w", ev.Image, err)
	}
	var ret []Event
	for i, date := range ev.DateList {
		e := *ev
		e.Date = date
		e.DateString = ev.DateStringList[i]
		ret = append(ret, e)
	}

	return ret, nil
}

func (r *renderer) warnf(format string, args ...any) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	format = time.Now().Format("15:04:05") + " " + format
	fmt.Printf(format, args...)
}

func (r *renderer) printf(format string, args ...any) {
	if !strings.HasSuffix(format, "\n") {
		format = format + "\n"
	}
	fmt.Printf(format, args...)
}

func allOld(evs []Event) bool {
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, ev := range evs {
		if ev.Date.After(cutoff) || ev.Date.IsZero() {
			return false
		}
	}
	return true
}

type monthSummary struct {
	month time.Month
	evs   map[string][]Event
}

func (r *renderer) summarizeMonth(ms *monthSummary) {
	if len(ms.evs) > 0 {
		r.printf("📅 Hello! Upcoming events in %s, courtesy of http://parties.swisszouk.ch :", ms.month)
		packs := maps.Values(ms.evs)
		sort.Slice(packs, func(i, j int) bool { return packs[i][0].Date.Before(packs[j][0].Date) })
		for _, p := range packs {
			schedule := p[0].CustomScheduleString
			if schedule == "" {
				var dates []string
				for _, e := range p {
					dates = append(dates, e.Date.Format("Monday, Jan 2"))
				}
				schedule = strings.Join(dates, ", ")
			}
			r.printf("• %s (%s)", p[0].Title, schedule)
		}
		r.printf("")
	}

}

func (r *renderer) summarizeEvent(ms *monthSummary, ev Event) {
	if ms.month != ev.Date.Month() {
		r.summarizeMonth(ms)
		ms.evs = make(map[string][]Event)
		ms.month = ev.Date.Month()
	}
	if ev.City == *summaryCity {
		ms.evs[ev.Title] = append(ms.evs[ev.Title], ev)
	}
}

func (r *renderer) renderAll() {
	future := time.Date(time.Now().Year(), time.Now().Month()+3, 1, 0, 0, 0, 0, time.Local)
	files, err := filepath.Glob(r.sourceGlob)
	if err != nil {
		r.warnf("%v", err)
	}
	var events []Event
	for _, fpath := range files {
		content, err := os.ReadFile(fpath)
		if err != nil {
			r.warnf("Skipping %s: %v", fpath, err)
			continue
		}
		evs, err := r.renderEvent(string(content))
		if err != nil {
			r.warnf("Skipping %s: %v", fpath, err)
			continue
		}
		if allOld(evs) {
			r.warnf("removing %s, because too old", fpath)
			os.Remove(fpath)
			continue
		}
		for _, ev := range evs {
			switch {
			case ev.Date.After(future) && !*showAll:
				r.warnf("skipping %s, because %v is too far in the future", ev.Title, ev.Date)
			case ev.Date.Before(time.Now()):
				// skip past event
			default:
				events = append(events, ev)
			}
		}
	}
	var ms monthSummary
	sort.Slice(events, func(i, j int) bool { return events[i].Date.Before(events[j].Date) })
	for i := 0; i+1 < len(events); i++ {
		_, prevM, _ := events[i].Date.Date()
		_, nextM, _ := events[i+1].Date.Date()
		if prevM != nextM {
			events[i].SeparatorBelow = nextM.String()
		}
		r.summarizeEvent(&ms, events[i])
	}
	r.summarizeEvent(&ms, events[len(events)-1])
	r.summarizeMonth(&ms)

	dest := filepath.Join(r.outDir, "index.html")
	sink, err := os.Create(dest)
	if err != nil {
		r.warnf("Open %s for writing: %w", dest, err)
		return
	}

	var mainT = template.Must(template.ParseFiles("template.html"))
	if err := mainT.ExecuteTemplate(sink, "indexpage", events); err != nil {
		r.warnf("Write %s: %v", dest, err)
		return
	}
	sink.Close()
	r.warnf("Regenerated %s, %d events.", dest, len(events))

	aboutSink, err := os.Create(filepath.Join(r.outDir, "about.html"))
	if err != nil {
		r.warnf("Open %s for writing: %v", "about.html", err)
		return
	}
	if err := mainT.ExecuteTemplate(aboutSink, "aboutpage", nil); err != nil {
		r.warnf("write about.html: %v", err)
		return
	}
	aboutSink.Close()

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
	w.Add("template.html")
	w.Add("tailwind.config.js")
	w.Add("app.css")
	w.Add("events")

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
	flag.Parse()
	if city := cityMap[*summaryCity]; city != "" {
		*summaryCity = city
	}

	r := renderer{
		sourceGlob: "events/*.yaml",
		outDir:     "docs",
	}
	r.renderAll()
	go watch(r.renderAll)

	s := http.FileServer(http.Dir(r.outDir))
	fmt.Println("preview server at :9000")
	http.ListenAndServe(":9000", s)

}
