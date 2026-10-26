package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type createOptionsFake struct {
	core.Service
	err error
}

func (f createOptionsFake) CreateOptions(context.Context) (core.CreateOptions, error) {
	return core.CreateOptions{DefaultDir: "/tmp/agents", Candidates: []core.AdoptCandidate{{Dir: "/tmp/agents/atlas", Name: "Atlas", Slug: "atlas", Harness: core.HarnessClaude}}}, f.err
}
func TestCreateOptionsRoute(t *testing.T) {
	for _, want := range []int{200, 422} {
		t.Run(http.StatusText(want), func(t *testing.T) {
			f := createOptionsFake{}
			if want == 422 {
				f.err = core.ErrInvalid
			}
			w := httptest.NewRecorder()
			New(f, Options{}).ServeHTTP(w, httptest.NewRequest("GET", "/api/create/options", nil))
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if want == 200 {
				var v core.CreateOptions
				if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v.DefaultDir != "/tmp/agents" || len(v.Candidates) != 1 {
					t.Fatal(v, err)
				}
			}
		})
	}
}
