package hub

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The operator console: server-rendered pages under /console, after the
// design in lolad-manager's landing-hub/console (People, Invites, Health).
// It shares the admin API's operations (admin.go) and holds no mail.

//go:embed console/templates/*.html console/static
var consoleFS embed.FS

type consoleUI struct {
	pages  map[string]*template.Template
	static http.Handler
}

func newConsoleUI() *consoleUI {
	ui := &consoleUI{pages: map[string]*template.Template{}}
	for _, name := range []string{"signin", "people", "invites", "health"} {
		ui.pages[name] = template.Must(template.ParseFS(consoleFS, "console/templates/layout.html", "console/templates/"+name+".html"))
	}
	sub, err := fs.Sub(consoleFS, "console/static")
	if err != nil {
		panic(err)
	}
	ui.static = http.StripPrefix("/console/static/", http.FileServerFS(sub))
	return ui
}

func (a *admin) consoleRoutes(m *http.ServeMux) {
	page := func(pattern string, h func(http.ResponseWriter, *http.Request, session)) {
		m.Handle(pattern, a.guard(a.consoleAuth(h)))
	}
	m.Handle("GET /console/static/", a.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		a.ui.static.ServeHTTP(w, r)
	})))
	m.Handle("GET /console/signin", a.guard(http.HandlerFunc(a.signinPage)))
	m.Handle("POST /console/signin", a.guard(http.HandlerFunc(a.signin)))
	page("POST /console/signout", a.signout)
	page("GET /console", func(w http.ResponseWriter, r *http.Request, _ session) {
		http.Redirect(w, r, "/console/people", http.StatusSeeOther)
	})
	page("GET /console/{$}", func(w http.ResponseWriter, r *http.Request, _ session) {
		http.Redirect(w, r, "/console/people", http.StatusSeeOther)
	})
	page("GET /console/people", a.peoplePage)
	page("POST /console/people/{handle}/{action}", a.peopleAction)
	page("GET /console/invites", a.invitesPage)
	page("POST /console/invites", a.inviteCreate)
	page("POST /console/invites/{id}/cancel", a.inviteCancel)
	page("GET /console/health", a.healthPage)
	page("POST /console/signup", a.signupSwitch)
	m.Handle("/console/", a.guard(a.consoleAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		http.NotFound(w, r)
	})))
}

// consoleAuth sends visitors without a session to the sign-in page and checks
// the CSRF token on every form post.
func (a *admin) consoleAuth(h func(http.ResponseWriter, *http.Request, session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.session(r)
		if !ok {
			if safeMethod(r.Method) {
				http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
				return
			}
			http.Error(w, "Sign in again.", http.StatusUnauthorized)
			return
		}
		if !safeMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
			if !csrfOK(r, s) {
				http.Error(w, "This form expired. Go back, reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		h(w, r, s)
	})
}

// ---------------------------------------------------------------- sign-in

type signinView struct {
	Title string
	Flash string
}

func (a *admin) signinPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.session(r); ok {
		http.Redirect(w, r, "/console/people", http.StatusSeeOther)
		return
	}
	a.render(w, 200, "signin", signinView{Title: "Sign in"})
}

func (a *admin) signin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ip := a.s.clientIP(r)
	if !a.signinAllowed(ip) {
		w.Header().Set("Retry-After", strconv.Itoa(int(signinWindow.Seconds())))
		a.render(w, 429, "signin", signinView{Title: "Sign in", Flash: "Too many tries. Wait 15 minutes, then try again."})
		return
	}
	if !a.tokenOK(r.PostFormValue("token")) {
		a.signinFailed(ip)
		a.render(w, 401, "signin", signinView{Title: "Sign in", Flash: "That is not this hub's admin secret."})
		return
	}
	id, _ := a.newSession()
	setSessionCookie(w, id, int(sessionTTL.Seconds()))
	a.s.o.Logger.Info("admin signed in", "ip", ip)
	http.Redirect(w, r, "/console/people", http.StatusSeeOther)
}

func (a *admin) signout(w http.ResponseWriter, r *http.Request, _ session) {
	a.endSession(r)
	setSessionCookie(w, "", -1)
	http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
}

// ---------------------------------------------------------------- page frame

type pageView struct {
	Title    string
	Nav      string
	CSRF     string
	Host     string
	HubURL   string // the public URL members join with
	Uptime   string
	Version  string
	Flash    string
	FlashBad bool
	Data     any
}

func (a *admin) frame(s session, nav, title string, data any) pageView {
	host := a.s.o.PublicURL
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		host = u.Host
	}
	return pageView{Title: title, Nav: nav, CSRF: s.csrf, Host: host, HubURL: a.s.o.PublicURL, Uptime: span(a.s.o.Clock().Sub(a.s.started)), Version: Version, Data: data}
}

func (a *admin) render(w http.ResponseWriter, status int, name string, v any) {
	var buf bytes.Buffer
	if err := a.ui.pages[name].ExecuteTemplate(&buf, "page", v); err != nil {
		a.s.o.Logger.Error("console render", "page", name, "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// ---------------------------------------------------------------- People

type skyPerson struct {
	Handle, Initial, State string
	Pos, Hue               template.CSS
}

type personRow struct {
	Handle, Note, Initial, State, StateText, Dot, Joined, Via, Today, Volume, Action, ActionText, Find string
	New                                                                                                bool
	Hue                                                                                                template.CSS
}

type chip struct {
	Label, Href, On string
	Count           int
}

type peopleView struct {
	Summary  string
	Stars    []template.CSS
	Sky      []skyPerson
	Chips    []chip
	Rows     []personRow
	Show, Q  string
	Filtered bool
}

const skyMax = 40

var hues = []string{"#6C9BFF", "#3DE8D0", "#A98BFF", "#FF6BD6", "#C4F25A", "#DCE6FF"}

func hashOf(s string) uint32 { h := fnv.New32a(); h.Write([]byte(s)); return h.Sum32() }

func hueCSS(handle string) template.CSS {
	return template.CSS("--h:" + hues[hashOf(handle)%uint32(len(hues))])
}

func initial(handle string) string {
	if handle == "" {
		return "?"
	}
	return strings.ToUpper(handle[:1])
}

func personState(p Person) string {
	switch {
	case p.Disabled:
		return "disabled"
	case p.Online:
		return "on"
	}
	return "off"
}

// stars is a fixed, pseudo-random sky.
var stars = func() []template.CSS {
	var out []template.CSS
	x := uint32(2463534242)
	next := func() uint32 { x ^= x << 13; x ^= x >> 17; x ^= x << 5; return x }
	for range 36 {
		out = append(out, template.CSS(fmt.Sprintf("left:%.1f%%;top:%.1f%%;animation-delay:-%.1fs", float64(next()%1000)/10, float64(next()%1000)/10, float64(next()%40)/10)))
	}
	return out
}()

func (a *admin) peoplePage(w http.ResponseWriter, r *http.Request, s session) {
	people, _, err := a.people(r)
	if err != nil {
		a.s.o.Logger.Error("console people", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	show := r.URL.Query().Get("show")
	if show != "on" && show != "off" && show != "disabled" {
		show = ""
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	v := peopleView{Stars: stars, Show: show, Q: q, Filtered: show != "" || q != ""}
	count := map[string]int{}
	now := a.s.o.Clock()
	for i, p := range people {
		st := personState(p)
		count[st]++
		if i < skyMax {
			// Spread people evenly left to right, alternating high and low
			// rows, with a little per-handle jitter so it reads as a sky.
			slots := min(len(people), skyMax)
			h := hashOf(p.Handle)
			x := 6 + (88*(2*i+1))/(2*slots)
			y := []int{40, 70, 52, 82}[i%4] + int(h%7) - 3
			v.Sky = append(v.Sky, skyPerson{Handle: p.Handle, Initial: initial(p.Handle), State: st, Hue: hueCSS(p.Handle),
				Pos: template.CSS(fmt.Sprintf("left:%d%%;top:%d%%;animation-delay:-%ds", x, y, x%9))})
		}
		find := strings.ToLower(p.Handle + " " + p.Note + " " + p.Display)
		if (show != "" && st != show) || (q != "" && !strings.Contains(find, strings.ToLower(q))) {
			continue
		}
		row := personRow{Handle: p.Handle, Note: p.Note, Initial: initial(p.Handle), State: st, Hue: hueCSS(p.Handle), Find: find,
			Joined: day(p.CreatedAt, now) + " " + p.CreatedAt.Local().Format("15:04"), Today: "—", Action: "disable", ActionText: "Turn off",
			New: now.Sub(p.CreatedAt) < 24*time.Hour, Via: "invite"}
		if p.JoinedVia == SignupOpen {
			row.Via = "open sign-up"
		} else if p.Note != "" {
			row.Via = "invite for " + p.Note
		}
		if p.Bytes24h > 0 {
			row.Volume = megabytes(p.Bytes24h)
		}
		if row.Note == "" {
			row.Note = p.Display
		}
		if p.CarriedToday > 0 {
			row.Today = plural(p.CarriedToday, "message")
		}
		switch st {
		case "on":
			row.StateText, row.Dot = "Connected", "dot-on"
		case "off":
			row.StateText, row.Dot = "Offline", "dot-off"
			if !p.LastSeen.IsZero() {
				row.StateText = "Offline, last seen " + ago(now.Sub(p.LastSeen))
			} else {
				row.StateText = "Offline, never connected"
			}
		default:
			row.StateText, row.Dot, row.Action, row.ActionText = "Turned off", "dot-dis", "enable", "Turn back on"
		}
		v.Rows = append(v.Rows, row)
	}
	n := len(people)
	v.Summary = fmt.Sprintf("%s joined this hub. %s connected right now.", peopleCount(n), areCount(count["on"]))
	mk := func(key, label string, c int) chip {
		href := "/console/people"
		if key != "" {
			href += "?show=" + key
		}
		on := "false"
		if key == show {
			on = "true"
		}
		return chip{Label: label, Href: href, On: on, Count: c}
	}
	v.Chips = []chip{mk("", "Everyone", n), mk("on", "Connected", count["on"]), mk("off", "Offline", count["off"]), mk("disabled", "Turned off", count["disabled"])}
	a.render(w, 200, "people", a.frame(s, "people", "People", v))
}

func (a *admin) peopleAction(w http.ResponseWriter, r *http.Request, s session) {
	handle, action := r.PathValue("handle"), r.PathValue("action")
	var err error
	switch action {
	case "disable", "enable":
		err = a.setDisabled(r, handle, action == "disable")
	case "block":
		err = a.block(r, handle)
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.s.o.Logger.Error("console set account", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	http.Redirect(w, r, "/console/people", http.StatusSeeOther)
}

// signupSwitch opens or pauses open sign-up from the Health page.
func (a *admin) signupSwitch(w http.ResponseWriter, r *http.Request, s session) {
	if err := a.setSignup(r, r.PostFormValue("mode")); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "Unknown mode", 400)
			return
		}
		a.s.o.Logger.Error("console sign-up mode", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	http.Redirect(w, r, "/console/health", http.StatusSeeOther)
}

// ---------------------------------------------------------------- Invites

type pendingRow struct {
	ID         int64
	Note, When string
}

type usedRow struct {
	Handle, Initial, Note, When string
	Hue                         template.CSS
}

type invitesView struct {
	Code, MadeFor, Until string
	Pending              []pendingRow
	Used                 []usedRow
}

func noteOr(n string) string {
	if strings.TrimSpace(n) == "" {
		return "someone new"
	}
	return n
}

func (a *admin) invitesView(r *http.Request) (invitesView, error) {
	pending, used, err := a.s.store.Invites(r.Context())
	if err != nil {
		return invitesView{}, err
	}
	var v invitesView
	for _, p := range pending {
		v.Pending = append(v.Pending, pendingRow{ID: p.ID, Note: noteOr(p.Note),
			When: "Made " + p.CreatedAt.Local().Format("2 Jan") + " · valid until " + p.ExpiresAt.Local().Format("2 Jan")})
	}
	for _, u := range used {
		v.Used = append(v.Used, usedRow{Handle: u.UsedBy, Initial: initial(u.UsedBy), Note: noteOr(u.Note), Hue: hueCSS(u.UsedBy), When: u.UsedAt.Local().Format("2 Jan")})
	}
	return v, nil
}

func (a *admin) invitesPage(w http.ResponseWriter, r *http.Request, s session) {
	v, err := a.invitesView(r)
	if err != nil {
		a.s.o.Logger.Error("console invites", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	a.render(w, 200, "invites", a.frame(s, "invites", "Invite someone", v))
}

// inviteCreate answers with the page itself, not a redirect, so the code is
// shown once and never lands in a URL or the history.
func (a *admin) inviteCreate(w http.ResponseWriter, r *http.Request, s session) {
	inv, code, err := a.createInvite(r, r.PostFormValue("note"), InviteTTL)
	if err != nil {
		a.s.o.Logger.Error("console create invite", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	v, err := a.invitesView(r)
	if err != nil {
		a.s.o.Logger.Error("console invites", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	v.Code, v.MadeFor, v.Until = code, noteOr(inv.Note), inv.ExpiresAt.Local().Format("Monday 2 January")
	a.render(w, 200, "invites", a.frame(s, "invites", "Invite someone", v))
}

func (a *admin) inviteCancel(w http.ResponseWriter, r *http.Request, s session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = a.s.store.CancelInvite(r.Context(), id)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		a.s.o.Logger.Error("console cancel invite", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	if err == nil {
		a.s.o.Logger.Info("admin cancelled invite", "id", id)
	}
	http.Redirect(w, r, "/console/invites", http.StatusSeeOther)
}

// ---------------------------------------------------------------- Health

type logRow struct {
	Time, To, Path, Label, Class, Took string
	Status                             int
}

type healthView struct {
	Lede, UpFor, Connected, Carried, Limited, Version string
	Signups, SignupState, PlacesLeft, SignupAction    string
	LastHour                                          int
	Chart                                             template.HTML
	Log                                               []logRow
}

func (a *admin) healthPage(w http.ResponseWriter, r *http.Request, s session) {
	st, err := a.status(r)
	if err != nil {
		a.s.o.Logger.Error("console health", "err", err)
		http.Error(w, "Console error", 500)
		return
	}
	t := st.Traffic
	v := healthView{Version: st.Version, LastHour: t.LastHour, Chart: chartSVG(t.Hours),
		Connected: fmt.Sprintf("%d of %s", st.Online, plural(st.Accounts, "person")),
		Carried:   plural(t.Carried24h, "message"),
		UpFor:     span(a.s.o.Clock().Sub(a.s.started)) + ", since " + a.s.started.Local().Format("2 Jan 15:04"),
		Limited:   "None"}
	if t.Limited24h > 0 {
		v.Limited = plural(t.Limited24h, "request") + ", " + plural(t.LimitedIPs, "address")
	}
	v.Signups = plural(st.Signups24h, "new person")
	switch {
	case st.SignupMode == SignupInvite:
		v.SignupState, v.SignupAction = "Invite only", SignupOpen
	case st.SignupState == SignupClosed:
		v.SignupState, v.SignupAction = "Open, but full for now", SignupInvite
	default:
		v.SignupState, v.SignupAction = "Open to anyone", SignupInvite
	}
	if st.AccountsMax > 0 {
		v.PlacesLeft = fmt.Sprintf("%s of %s", commas(max(st.AccountsMax-st.Accounts, 0)), commas(st.AccountsMax))
	}
	v.Lede = fmt.Sprintf("%s connected.", lolads(st.Online))
	switch {
	case t.TypicalMs < 0:
		v.Lede += " No messages in the last hour."
	case t.TypicalMs < 100:
		v.Lede += " Messages are getting through in under a tenth of a second."
	case t.TypicalMs < 1000:
		v.Lede += fmt.Sprintf(" Messages are getting through in about %d ms.", t.TypicalMs)
	default:
		v.Lede += fmt.Sprintf(" Messages are taking about %.1f seconds to get through.", float64(t.TypicalMs)/1000)
	}
	for _, q := range a.s.traffic.recent(30) {
		row := logRow{Time: q.Time.Local().Format("15:04:05"), To: q.Handle, Path: q.Path, Status: q.Status, Took: fmt.Sprintf("%d ms", q.Ms)}
		if row.To == "" {
			row.To = "—"
		}
		row.Label, row.Class = resultLabel(q)
		v.Log = append(v.Log, row)
	}
	a.render(w, 200, "health", a.frame(s, "health", "Health", v))
}

func resultLabel(q Request) (label, class string) {
	switch {
	case q.Status >= 200 && q.Status < 300:
		if q.Handle == "" {
			return "Joined", "ok"
		}
		return "Carried", "ok"
	case q.Status == 429:
		return "Slowed down", ""
	case q.Status == 503:
		return "Not connected", ""
	case q.Status == 403 && q.Handle != "":
		return "Turned off", ""
	case q.Status == 403:
		return "Refused", ""
	case q.Status == 404:
		return "Not found", ""
	case q.Status == 504:
		return "No answer", "bad"
	case q.Status == 499:
		return "Caller left", ""
	case q.Status >= 500:
		return "Failed", "bad"
	}
	return "Refused", ""
}

// chartSVG draws carried-per-hour as an area chart, after the Health design.
func chartSVG(hours []Hour) template.HTML {
	const x0, x1, y0, y1 = 44.0, 704.0, 188.0, 16.0
	peak, busiest, quiet := 0, 0, -1
	for i, h := range hours {
		if h.Carried > peak {
			peak, busiest = h.Carried, i
		}
		if quiet < 0 || h.Carried < hours[quiet].Carried {
			quiet = i
		}
	}
	top := niceTop(peak)
	n := len(hours)
	xAt := func(i int) float64 {
		if n < 2 {
			return x1
		}
		return x0 + (x1-x0)*float64(i)/float64(n-1)
	}
	yAt := func(c int) float64 { return y0 - (y0-y1)*float64(c)/float64(top) }
	var b strings.Builder
	label := "Messages carried per hour over the last 24 hours."
	if n > 0 {
		label += fmt.Sprintf(" Busiest at %s with %d. Quietest at %s with %d. %d in the last hour.",
			hours[busiest].Start.Local().Format("15:04"), hours[busiest].Carried, hours[quiet].Start.Local().Format("15:04"), hours[quiet].Carried, hours[n-1].Carried)
	}
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 720 220" width="100%%" role="img" aria-label="%s">`, template.HTMLEscapeString(label))
	b.WriteString(`<defs><linearGradient id="ar" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#7AA2FF" stop-opacity="0.35"/><stop offset="1" stop-color="#7AA2FF" stop-opacity="0"/></linearGradient></defs>`)
	for i := 0; i <= 3; i++ {
		v := top * i / 3
		y := yAt(v)
		fmt.Fprintf(&b, `<line x1="44" x2="704" y1="%.1f" y2="%.1f" stroke="#F2F4FF" stroke-opacity="0.07"/><text x="34" y="%.1f" text-anchor="end" fill="#8A91AE" font-size="11" font-family="Manrope,sans-serif">%d</text>`, y, y, y+4, v)
	}
	if n > 0 {
		var line strings.Builder
		for i, h := range hours {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			fmt.Fprintf(&line, "%s%.1f %.1f ", cmd, xAt(i), yAt(h.Carried))
		}
		p := strings.TrimSpace(line.String())
		fmt.Fprintf(&b, `<path d="%s L%.1f %.1f L%.1f %.1f Z" fill="url(#ar)"/>`, p, x1, y0, x0, y0)
		fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="#9FB8FF" stroke-width="2" stroke-linejoin="round"/>`, p)
		ly := yAt(hours[n-1].Carried)
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="9" fill="#9FB8FF" fill-opacity="0.18"/><circle cx="%.1f" cy="%.1f" r="4" fill="#DCE6FF"/>`, x1, ly, x1, ly)
		for _, i := range []int{0, 6, 12, 18} {
			if i >= n-1 {
				break
			}
			anchor := "middle"
			if i == 0 {
				anchor = "start"
			}
			fmt.Fprintf(&b, `<text x="%.1f" y="212" text-anchor="%s" fill="#8A91AE" font-size="11" font-family="Manrope,sans-serif">%s</text>`, xAt(i), anchor, hours[i].Start.Local().Format("15:04"))
		}
		b.WriteString(`<text x="704" y="212" text-anchor="end" fill="#8A91AE" font-size="11" font-family="Manrope,sans-serif">Now</text>`)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// niceTop rounds a chart's peak up to a value that splits into 3 round steps.
func niceTop(peak int) int {
	if peak <= 0 {
		return 3
	}
	for _, step := range []int{1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000} {
		if step*3 >= peak {
			return step * 3
		}
	}
	return ((peak + 2999) / 3000) * 3000
}

// ---------------------------------------------------------------- words

// megabytes is "0.4 MB", "12 MB".
func megabytes(n int64) string {
	mb := float64(n) / 1e6
	if mb < 10 {
		return fmt.Sprintf("%.1f MB", mb)
	}
	return fmt.Sprintf("%s MB", commas(int(mb+0.5)))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "person") {
		return commas(n) + " " + strings.TrimSuffix(word, "person") + "people"
	}
	if strings.HasSuffix(word, "ss") {
		return fmt.Sprintf("%s %ses", commas(n), word)
	}
	return fmt.Sprintf("%s %ss", commas(n), word)
}

func commas(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func peopleCount(n int) string {
	switch n {
	case 0:
		return "Nobody has"
	case 1:
		return "1 person has"
	}
	return commas(n) + " people have"
}

func areCount(n int) string {
	switch n {
	case 0:
		return "None are"
	case 1:
		return "1 is"
	}
	return commas(n) + " are"
}

func lolads(n int) string {
	switch n {
	case 0:
		return "No lolads are"
	case 1:
		return "1 lolad is"
	}
	return commas(n) + " lolads are"
}

// span is a rough duration: "12 days", "3 hours", "5 minutes".
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 24*time.Hour:
		return "1 day"
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= time.Hour:
		return "1 hour"
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return "a minute"
}

// day is "23 Nov", with the year when it is not this year.
func day(t, now time.Time) string {
	t = t.Local()
	if t.Year() == now.Local().Year() {
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

func ago(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return span(d) + " ago"
}
