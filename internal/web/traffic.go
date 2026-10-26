package web

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type trafficView struct {
	Mail   []core.Mail
	Peer   string        // filter: only mail from or to this participant
	People []participant // filter options
	Before time.Time     // this page starts before
	Next   time.Time     // cursor for the next (older) page; zero = no more
	Me     string
}

const trafficPage = 60

func (s *server) traffic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := trafficView{Peer: r.URL.Query().Get("peer"), Me: s.svc.Me().Name}
	if b := r.URL.Query().Get("before"); b != "" {
		v.Before, _ = time.Parse(time.RFC3339Nano, b)
	}
	// Traffic has no participant filter: page through it and filter here.
	cursor := v.Before
	for round := 0; round < 10 && len(v.Mail) <= trafficPage; round++ {
		batch, err := s.svc.Traffic(ctx, 200, cursor)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, m := range batch {
			if v.Peer == "" || m.FromName == v.Peer || m.ToName == v.Peer {
				v.Mail = append(v.Mail, m)
			}
		}
		if len(batch) < 200 {
			break
		}
		cursor = batch[len(batch)-1].CreatedAt
	}
	if len(v.Mail) > trafficPage {
		v.Mail = v.Mail[:trafficPage]
		v.Next = v.Mail[trafficPage-1].CreatedAt
	}

	me := s.svc.Me()
	v.People = []participant{{Name: me.Name, Display: me.Display, Kind: "user"}}
	if list, err := s.svc.List(ctx); err == nil {
		slices.SortFunc(list, func(a, b core.AgentStatus) int { return strings.Compare(a.Name, b.Name) })
		for _, a := range list {
			v.People = append(v.People, participant{Name: a.Name, Display: a.Display, Kind: "agent", State: a.State})
		}
	}
	v.People = append(v.People, participant{Name: "lolad", Display: "lolad (system)", Kind: "system"})
	if isHX(r) && r.URL.Query().Get("part") == "rows" {
		s.fragment(w, http.StatusOK, "traffic-rows", v)
		return
	}
	s.page(w, r, http.StatusOK, "traffic", layout{Title: "Traffic", Data: v})
}
