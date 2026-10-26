package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Traffic combines accepted AMP mail with recorded policy denials. A denial
// has no stored message body; never manufacture one from the current policy.
type trafficEntry struct {
	core.Mail
	Denied                                   bool
	Reason, Delivery, FromURL, ToURL, OrgURL string
}
type trafficPair struct {
	From, To       string
	Entries        []trafficEntry
	Denied, Queued int
	URL            string
}
type trafficView struct {
	Connection  string
	Connections []core.Connection

	Entries                                      []trafficEntry
	Pairs                                        []trafficPair
	Peer, Other, Direction, Status, Period, View string
	People                                       []participant
	Before, Next                                 time.Time
	Me                                           string
	HasAgents, Limited                           bool
	Total, Denied, Queued                        int
	Query                                        url.Values
}

const trafficPage = 60

func (v trafficView) URL(key, value string) string {
	q := url.Values{}
	for k, vs := range v.Query {
		q[k] = slices.Clone(vs)
	}
	q.Del("part")
	q.Del("before")
	if value == "" {
		q.Del(key)
	} else {
		q.Set(key, value)
	}
	return "/traffic?" + q.Encode()
}
func (v trafficView) Older() string {
	q := trafficQuery(v.Query)
	q.Del("part")
	q.Set("before", v.Next.Format(time.RFC3339Nano))
	return "/traffic?" + q.Encode()
}
func (v trafficView) LiveURL() string {
	q := trafficQuery(v.Query)
	q.Set("part", "rows")
	return "/traffic?" + q.Encode()
}

func trafficDenial(e core.Event) trafficEntry {
	route, reason, ok := strings.Cut(strings.TrimPrefix(e.Detail, "denied "), ": ")
	from, to, hasRoute := strings.Cut(route, " → ")
	if !ok || !hasRoute {
		from, to, reason = e.Agent, "", e.Detail
	}
	orgURL := "/org#org-defaults"
	var rule int
	if _, err := fmt.Sscanf(reason, "rule %d", &rule); err == nil && rule > 0 {
		orgURL = fmt.Sprintf("/org#org-rule-%d", rule)
	}
	return trafficEntry{OrgURL: orgURL, Mail: core.Mail{ID: fmt.Sprintf("policy-%d", e.ID), FromName: from, ToName: to, CreatedAt: e.At, Subject: "Message blocked by policy", Type: "policy"}, Denied: true, Reason: reason, Delivery: "Denied"}
}
func (s *server) traffic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	v := trafficView{Peer: q.Get("peer"), Other: q.Get("other"), Direction: q.Get("direction"), Status: q.Get("status"), Period: q.Get("period"), View: q.Get("view"), Me: s.svc.Me().Name, Query: q}
	v.Connection = q.Get("connection")
	v.Connections, _ = s.svc.Connections(ctx)
	v.Before, _ = time.Parse(time.RFC3339Nano, q.Get("before"))
	var entries []trafficEntry
	cursor := v.Before
	// Service deliberately has no audit filter. Bound the scan and disclose it;
	// filters never silently claim to cover the entire mail archive.
	for round := 0; round < 10; round++ {
		batch, err := s.svc.Traffic(ctx, 200, cursor)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, m := range batch {
			if v.Connection != "" && m.Remote != v.Connection {
				continue
			}
			delivery := "Queued"
			if m.Status == core.MailNotified {
				delivery = "Notified"
			}
			if m.Status == core.MailRead {
				delivery = "Read"
			}
			if m.Remote != "" && m.Status == core.MailUnread {
				delivery = "Received"
			}
			if m.Delivery != "" {
				delivery = deliveryLabel(m.Delivery)
			} else if m.Status == core.MailHeld {
				delivery = "Awaiting approval"
			}
			entries = append(entries, trafficEntry{Mail: m, Delivery: delivery})
		}
		if len(batch) < 200 {
			break
		}
		cursor = batch[len(batch)-1].CreatedAt
		if round == 9 {
			v.Limited = true
		}
	}
	events, err := s.svc.Events(ctx, "", 2000)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(events) == 2000 {
		v.Limited = true
	}
	for _, e := range events {
		if e.Kind == "policy" {
			entries = append(entries, trafficDenial(e))
		}
	}
	me := s.svc.Me()
	v.People = []participant{{Name: me.Name, Display: me.Display, Kind: "user"}}
	list, err := s.svc.List(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slices.SortFunc(list, func(a, b core.AgentStatus) int { return strings.Compare(a.Name, b.Name) })
	links := map[string]string{}
	v.HasAgents = len(list) > 0
	for _, a := range list {
		v.People = append(v.People, participant{Name: a.Name, Display: a.Display, Kind: "agent", State: a.State})
		links[a.Name] = "/agents/" + url.PathEscape(a.Name)
	}
	v.People = append(v.People, participant{Name: "lolad", Display: "lolad (system)", Kind: "system"})
	// Retain former agents and outside AMP participants in audit filters even
	// when they are absent from the current registry.
	known := map[string]bool{}
	for _, p := range v.People {
		known[p.Name] = true
	}
	var outside []string
	for _, entry := range entries {
		for _, name := range []string{entry.FromName, entry.ToName} {
			if name != "" && !known[name] {
				known[name] = true
				outside = append(outside, name)
			}
		}
	}
	slices.Sort(outside)
	for _, name := range outside {
		v.People = append(v.People, participant{Name: name, Display: name, Kind: "external"})
	}

	var since time.Time
	switch v.Period {
	case "1h":
		since = time.Now().Add(-time.Hour)
	case "24h":
		since = time.Now().Add(-24 * time.Hour)
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
	}
	focus := v.Peer
	if focus == "" {
		focus = v.Me
	}
	for _, e := range entries {
		if v.Connection != "" && e.Remote != v.Connection {
			continue
		}
		if !since.IsZero() && e.CreatedAt.Before(since) {
			continue
		}
		if v.Peer != "" && e.FromName != v.Peer && e.ToName != v.Peer {
			continue
		}
		if v.Other != "" && e.FromName != v.Other && e.ToName != v.Other {
			continue
		}
		if v.Direction == "out" && e.FromName != focus {
			continue
		}
		if v.Direction == "in" && e.ToName != focus {
			continue
		}
		v.Total++
		if e.Denied {
			v.Denied++
		}
		if e.Delivery == "Queued" {
			v.Queued++
		}
		if v.Status == "denied" && !e.Denied {
			continue
		}
		if v.Status == "queued" && e.Delivery != "Queued" {
			continue
		}
		if !v.Before.IsZero() && !e.CreatedAt.Before(v.Before) {
			continue
		}
		e.FromURL = links[e.FromName]
		e.ToURL = links[e.ToName]
		v.Entries = append(v.Entries, e)
	}
	slices.SortStableFunc(v.Entries, func(a, b trafficEntry) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(v.Entries) > trafficPage {
		v.Entries = v.Entries[:trafficPage]
		v.Next = v.Entries[trafficPage-1].CreatedAt
	}
	pairs := map[string]int{}
	for _, e := range v.Entries {
		a, b := e.FromName, e.ToName
		if a > b {
			a, b = b, a
		}
		key := a + "\x00" + b
		i, ok := pairs[key]
		if !ok {
			i = len(v.Pairs)
			pairs[key] = i
			pq := trafficQuery(q)
			pq.Del("before")
			pq.Set("peer", a)
			pq.Set("other", b)
			pq.Del("view")
			v.Pairs = append(v.Pairs, trafficPair{From: a, To: b, URL: "/traffic?" + pq.Encode()})
		}
		p := &v.Pairs[i]
		p.Entries = append(p.Entries, e)
		if e.Denied {
			p.Denied++
		}
		if e.Delivery == "Queued" {
			p.Queued++
		}
	}
	slices.SortStableFunc(v.Pairs, func(a, b trafficPair) int { return len(b.Entries) - len(a.Entries) })
	if isHX(r) && q.Get("part") == "rows" {
		s.fragment(w, http.StatusOK, "traffic-rows", v)
		return
	}
	s.page(w, r, http.StatusOK, "traffic", layout{Title: "Traffic", Data: v})
}

func trafficQuery(q url.Values) url.Values {
	out := url.Values{}
	for k, vs := range q {
		out[k] = slices.Clone(vs)
	}
	return out
}
