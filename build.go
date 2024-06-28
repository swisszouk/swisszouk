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
	"golang.org/x/exp/slices"

	texttemplate "text/template"

	"github.com/fsnotify/fsnotify"
	"github.com/teambition/rrule-go"
	yaml "gopkg.in/yaml.v2"
)

var tailwindBin = flag.String("tailwind", "tailwind/tailwindcss-windows-x64.exe", "tailwind binary")

var summaryCity = flag.String("summary_city", "zrh", "City to print summary for")
var showAll = flag.Bool("all", false, "Show events in the future")

type Event struct {
	Title          string `yaml:"title"`
	Location       string
	URL            string `yaml:"URL"`
	City           string
	DateString     string    `yaml:"date"`
	DateStringList []string  `yaml:"dates"`
	Date           time.Time `yaml:"date_date"`

	// See https://github.com/teambition/rrule-go/blob/master/rrule.go :
	DateSpec string `yaml:"date_spec"`

	DateList             []time.Time `yaml:"dates_date"`
	Hour                 string      `yaml:"time"`
	Image                string      `yaml:"image"`
	Hidden               bool        `yaml:"hidden"`
	Price                string      `yaml:"price"`
	CustomScheduleString string      `yaml:"custom_schedule_string"`
	Bullet               string
	Size                 string `yaml:"size"`

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
func (e Event) IsBig() bool {
	return e.Size == "big"
}
func (e Event) ScheduleString() string {
	if e.CustomScheduleString != "" {
		return e.CustomScheduleString
	}
	return e.Date.Format("Mon, Jan 2")
}

var cityMap = map[string]string{
	"zrh":   "Zürich",
	"basel": "Basel",
	"bern":  "Bern",
	"ge":    "Genève",
}

type renderer struct {
	sourceGlob string
	outDir     string
}

var timeRe = regexp.MustCompile(`^\d?\d:\d\d$`)

func (r *renderer) renderEvent(fpath string, yamlText string) ([]Event, error) {
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
	if ev.City == "" {
		ev.City = filepath.Base(filepath.Dir(fpath))
	}
	city, ok := cityMap[ev.City]
	if !ok {
		return nil, fmt.Errorf("unknown city %q, try one of: %v", ev.City, maps.Keys(cityMap))
	}
	ev.City = city
	if ev.DateString != "" {
		ev.DateStringList = append(ev.DateStringList, ev.DateString)
	}
	if len(ev.DateStringList) > 0 && ev.DateSpec != "" {
		return nil, fmt.Errorf("cannot set both date_list and date_spec")
	}
	if ev.DateSpec != "" {
		rrs, err := rrule.StrToRRuleSet(ev.DateSpec)
		if err != nil {
			return nil, fmt.Errorf("bad rrule %q: %w", ev.DateSpec, err)
		}
		//rr.Dtstart = time.Now()
		ev.DateList = rrs.All()
		for _, d := range ev.DateList {
			ev.DateStringList = append(ev.DateStringList, d.Format("2006-01-02"))
		}
	} else {
		for _, ds := range ev.DateStringList {
			loc, _ := time.LoadLocation("Europe/Berlin")
			t, err := time.ParseInLocation("2006-01-02", ds, loc)
			if err != nil {
				return nil, fmt.Errorf("bad date %q: %w", ev.DateString, err)
			}
			ev.DateList = append(ev.DateList, t)
		}
	}
	if len(ev.DateList) == 0 {
		return nil, fmt.Errorf("no dates for %s", ev.Title)
	}
	if ev.Hour = strings.TrimSpace(ev.Hour); !timeRe.MatchString(ev.Hour) {
		return nil, fmt.Errorf("bad time %q; should be HH:MM", ev.Hour)
	}
	if ev.Price != "" && ev.Price != "donations" && !strings.HasSuffix(ev.Price, "CHF") {
		ev.Price = strings.TrimSpace(ev.Price) + " CHF"
	}
	if !strings.HasPrefix(ev.URL, "http") {
		ev.URL = "https://" + ev.URL
	}
	if ev.Size == "" {
		ev.Size = "big"
		if ev.CustomScheduleString != "" {
			ev.Size = "small"
		}
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

type SummaryKey struct {
	Title    string
	Schedule string
}
type SummaryEntry struct {
	SummaryKey
	FirstDate time.Time
	IsBig     bool
	Img       string
}

func (se SummaryEntry) LessThan(other SummaryEntry) bool {
	if se.IsBig != other.IsBig {
		return se.IsBig
	}
	return se.FirstDate.Before(other.FirstDate)
}

type monthSummary struct {
	Month     time.Month
	EvsByCity map[string]map[SummaryKey]SummaryEntry
}

func (ms monthSummary) EvsByCitySorted() map[string][]SummaryEntry {
	ret := make(map[string][]SummaryEntry)
	for city, evmap := range ms.EvsByCity {
		ret[city] = maps.Values(evmap)
		slices.SortFunc(ret[city], SummaryEntry.LessThan)
	}
	return ret
}

func (ms monthSummary) Images() []string {
	set := make(map[string]bool)
	for _, city := range ms.EvsByCity {
		for _, evs := range city {
			set[evs.Img] = true
		}
	}
	delete(set, "dancezouk-right.png")
	return maps.Keys(set)
}

func (r *renderer) summarizeEvent(ms *monthSummary, ev Event) {
	if ms.Month != ev.Date.Month() {
		ms.EvsByCity = make(map[string]map[SummaryKey]SummaryEntry)
		ms.Month = ev.Date.Month()
	}
	se := SummaryEntry{
		SummaryKey: SummaryKey{
			Title:    ev.Title,
			Schedule: ev.ScheduleString(),
		},
		IsBig:     ev.Size == "big",
		Img:       ev.Image,
		FirstDate: ev.Date,
	}
	if strings.Contains(se.SummaryKey.Schedule, "#SUMMARY_SKIP") {
		return
	}
	if _, ok := ms.EvsByCity[ev.City]; !ok {
		ms.EvsByCity[ev.City] = make(map[SummaryKey]SummaryEntry)
	}
	if old, ok := ms.EvsByCity[ev.City][se.SummaryKey]; !ok || old.FirstDate.Before(se.FirstDate) {
		ms.EvsByCity[ev.City][se.SummaryKey] = se
	}
}

type School struct {
	URL         string
	ShortName   string
	Description string
	Quote       string
}

func (s School) HumanURL() string {
	if s.ShortName != "" {
		return s.ShortName
	}
	short := strings.TrimPrefix(s.URL, "https://")
	return strings.TrimPrefix(short, "www.")
}

func (r *renderer) renderAll() {
	future := time.Date(time.Now().Year(), time.Now().Month()+4, 1, 0, 0, 0, 0, time.Local)
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
		evs, err := r.renderEvent(fpath, string(content))
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
				// skip future event
			case ev.Date.Before(time.Now().Truncate(24 * time.Hour)):
				// skip past event
			default:
				events = append(events, ev)
			}
		}
	}
	var allSummaries []monthSummary
	var ms monthSummary
	sort.Slice(events, func(i, j int) bool { return events[i].Date.Before(events[j].Date) })
	for i := 0; i+1 < len(events); i++ {
		r.summarizeEvent(&ms, events[i])
		_, prevM, _ := events[i].Date.Date()
		_, nextM, _ := events[i+1].Date.Date()
		if prevM != nextM {
			allSummaries = append(allSummaries, ms)

			ms = monthSummary{}
			events[i].SeparatorBelow = nextM.String()
		}
	}
	r.summarizeEvent(&ms, events[len(events)-1])
	allSummaries = append(allSummaries, ms)

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

	summarySink, err := os.Create(filepath.Join(r.outDir, "summary.html"))
	if err != nil {
		r.warnf("Open %s for writing: %v", "summary.html", err)
		return
	}
	if err := mainT.ExecuteTemplate(summarySink, "summarypage", allSummaries); err != nil {
		r.warnf("write summary.html: %v", err)
		return
	}
	summarySink.Close()

	sitemapSink, err := os.Create(filepath.Join(r.outDir, "sitemap.xml"))
	if err != nil {
		r.warnf("create sitemap.xml: %v", err)
		return
	}
	sitemapT := texttemplate.Must(texttemplate.ParseFiles("sitemap.template.xml"))
	if err := sitemapT.Execute(sitemapSink, time.Now().Format("2006-01-02")); err != nil {
		r.warnf("write sitemap: %w", err)
		return
	}
	sitemapSink.Close()

	schools := []School{
		{"https://zoukessence.ch", "", "School for Brazilian zouk in Zürich.",
			"Neben Kursen und Workshops haben wir mehrere regelmässige Partys und internationale Lehrer besuchen uns oft und gerne für Wochenende oder gleich mehrere Wochen."},
		{"https://lambaswiss.com", "", "School for lambada and zouk in Zürich.",
			"In unseren Workshops, Kursen und Privatstunden lernst du durch Lambada & Zouk Tanztechniken, die wichtigsten Grundlagen und dein Rhythmusgefühl wird trainiert."},
		{"https://www.zoukies.ch", "", "Dance embodiment, zouk and meditation. Zürich, Basel, Bern and Winterthur.",
			"A dance school and community for people who want to free their mind and awaken their body."},
		{"https://dancezouk.ch", "", "Brazilian zouk in Zürich.",
			"Join our DanceZouk Family and together we will take your dancing to the next level."},
		{"https://www.zoukarium.com", "", "Brazilian zouk in Basel and Freiburg.",
			"Wir fokussieren uns vor allem auf die Improvisation, das Gefühl der Freiheit und die Beziehung zueinander."},
		{"https://studiokehl.my.canva.site/brazilian-zouk-in-basel", "BaselZouk", "Brazilian zouk in Basel.",
			"Our team is full of passion for dance. Our goal is to build up the Brazilian Zouk community in Basel."},
	}
	sort.Slice(schools, func(i, j int) bool { return schools[i].HumanURL() < schools[j].HumanURL() })
	schoolsSink, err := os.Create(filepath.Join(r.outDir, "schools.html"))
	if err != nil {
		r.warnf("open schools.html: %w", err)
		return
	}
	if err := mainT.ExecuteTemplate(schoolsSink, "schools", schools); err != nil {
		r.warnf("write schools: %v", err)
		return
	}
	schoolsSink.Close()

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
	w.Add("events/zrh")
	w.Add("events/bern")
	w.Add("events/bsl")
	w.Add("events/ge")

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
		sourceGlob: "events/*/*.yaml",
		outDir:     "docs",
	}
	r.renderAll()
	go watch(r.renderAll)

	s := http.FileServer(http.Dir(r.outDir))
	fmt.Println("preview server at :9000")
	http.ListenAndServe(":9000", s)

}
