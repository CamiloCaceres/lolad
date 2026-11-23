package hub

import (
	"slices"
	"sync"
	"time"
)

// The traffic recorder keeps what the operator console shows about requests:
// metadata only (handle, path, status, time taken), in memory, for the last
// 24 hours. Never a body, a header or an invite code. It resets when the hub
// restarts.

const (
	logKeep   = 500 // recent requests kept for the console
	hoursKept = 25  // hourly buckets: the last 24 full hours plus the current one
)

// Request is one request the hub handled, as the console shows it.
type Request struct {
	Time   time.Time `json:"time"`
	Handle string    `json:"handle,omitempty"` // empty for hub endpoints (register)
	Path   string    `json:"path"`             // without the query string
	Status int       `json:"status"`
	Ms     int64     `json:"ms"`
	ip     string    // for counting addresses that were slowed down; never shown
}

// Carried reports whether the request reached a lolad and got a 2xx answer.
func (r Request) Carried() bool { return r.Handle != "" && r.Status >= 200 && r.Status < 300 }

type hourBucket struct {
	start    time.Time
	carried  map[string]int // handle → requests carried
	limited  int            // 429s
	limitIPs map[string]bool
	ms       []int64 // durations of carried requests (capped), for the typical latency
}

type traffic struct {
	mu    sync.Mutex
	now   func() time.Time
	ring  []Request // newest last
	hours []*hourBucket
}

func newTraffic(now func() time.Time) *traffic { return &traffic{now: now} }

func (t *traffic) bucket(at time.Time) *hourBucket {
	start := at.Truncate(time.Hour)
	if n := len(t.hours); n > 0 && t.hours[n-1].start.Equal(start) {
		return t.hours[n-1]
	}
	for _, b := range t.hours {
		if b.start.Equal(start) {
			return b
		}
	}
	b := &hourBucket{start: start, carried: map[string]int{}, limitIPs: map[string]bool{}}
	t.hours = append(t.hours, b)
	slices.SortFunc(t.hours, func(a, b *hourBucket) int { return a.start.Compare(b.start) })
	cut := t.now().Truncate(time.Hour).Add(-(hoursKept - 1) * time.Hour)
	t.hours = slices.DeleteFunc(t.hours, func(b *hourBucket) bool { return b.start.Before(cut) })
	return b
}

func (t *traffic) record(r Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ring = append(t.ring, r)
	if len(t.ring) > logKeep {
		t.ring = slices.Delete(t.ring, 0, len(t.ring)-logKeep)
	}
	b := t.bucket(r.Time)
	if r.Carried() {
		b.carried[r.Handle]++
		if len(b.ms) < 2000 {
			b.ms = append(b.ms, r.Ms)
		}
	}
	if r.Status == 429 {
		b.limited++
		if r.ip != "" {
			b.limitIPs[r.ip] = true
		}
	}
}

// recent returns up to n requests, newest first.
func (t *traffic) recent(n int) []Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n <= 0 || n > len(t.ring) {
		n = len(t.ring)
	}
	out := make([]Request, 0, n)
	for i := len(t.ring) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, t.ring[i])
	}
	return out
}

// Hour is the number of requests carried in one clock hour.
type Hour struct {
	Start   time.Time `json:"start"`
	Carried int       `json:"carried"`
}

// TrafficSummary is the console's view of the last 24 hours.
type TrafficSummary struct {
	Hours        []Hour         `json:"hours"`       // 24 entries, oldest first; the last is the current hour
	Carried24h   int            `json:"carried_24h"` // requests carried in those hours
	LastHour     int            `json:"carried_last_hour"`
	Limited24h   int            `json:"limited_24h"`     // requests refused with 429
	LimitedIPs   int            `json:"limited_ips_24h"` // distinct source addresses among them
	TypicalMs    int64          `json:"typical_ms"`      // median time to carry a request in the last hour, -1 when none
	CarriedToday map[string]int `json:"carried_today"`   // handle → carried since local midnight
}

func (t *traffic) summary() TrafficSummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	cur := now.Truncate(time.Hour)
	first := cur.Add(-23 * time.Hour)
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	s := TrafficSummary{CarriedToday: map[string]int{}, TypicalMs: -1}
	ips := map[string]bool{}
	byStart := map[int64]*hourBucket{}
	for _, b := range t.hours {
		byStart[b.start.Unix()] = b
	}
	for i := range 24 {
		at := first.Add(time.Duration(i) * time.Hour)
		h := Hour{Start: at.UTC()}
		if b := byStart[at.Unix()]; b != nil {
			for handle, n := range b.carried {
				h.Carried += n
				if b.start.Add(time.Hour).After(midnight) { // the hour overlaps today
					s.CarriedToday[handle] += n
				}
			}
			s.Limited24h += b.limited
			for ip := range b.limitIPs {
				ips[ip] = true
			}
			if i == 23 && len(b.ms) > 0 {
				ms := slices.Clone(b.ms)
				slices.Sort(ms)
				s.TypicalMs = ms[len(ms)/2]
			}
		}
		s.Carried24h += h.Carried
		s.Hours = append(s.Hours, h)
	}
	s.LimitedIPs = len(ips)
	s.LastHour = s.Hours[23].Carried
	return s
}
