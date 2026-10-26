package api

import (
	"github.com/CamiloCaceres/lolad/internal/core"
	"net/http"
)

func settingsRoutes(m *http.ServeMux, s core.Service) {
	m.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		write(w, s.Models(core.Harness(r.URL.Query().Get("harness"))), nil)
	})
	m.HandleFunc("GET /api/agents/{name}/models", func(w http.ResponseWriter, r *http.Request) {
		a, e := s.Get(r.Context(), r.PathValue("name"))
		if e != nil {
			write(w, nil, e)
			return
		}
		write(w, s.Models(a.Runtime.Harness), nil)
	})
	m.HandleFunc("GET /api/agents/{name}/runtime", func(w http.ResponseWriter, r *http.Request) {
		a, e := s.Get(r.Context(), r.PathValue("name"))
		write(w, a.Runtime, e)
	})
	m.HandleFunc("PUT /api/agents/{name}/runtime", func(w http.ResponseWriter, r *http.Request) {
		var rt core.Runtime
		if e := decode(w, r, &rt); e != nil {
			write(w, nil, e)
			return
		}
		a, e := s.UpdateRuntime(r.Context(), r.PathValue("name"), rt)
		write(w, a, e)
	})
	m.HandleFunc("GET /api/agents/{name}/skills", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Skills(r.Context(), r.PathValue("name"))
		write(w, v, e)
	})
	m.HandleFunc("POST /api/agents/{name}/skills", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			From string `json:"from"`
			ID   string `json:"id"`
		}
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		if b.From == "" {
			b.From = b.ID
		}
		v, e := s.AddSkill(r.Context(), r.PathValue("name"), b.From)
		write(w, v, e)
	})
	m.HandleFunc("DELETE /api/agents/{name}/skills/{id}", func(w http.ResponseWriter, r *http.Request) {
		write(w, nil, s.RemoveSkill(r.Context(), r.PathValue("name"), r.PathValue("id")))
	})
	m.HandleFunc("GET /api/agents/{name}/env", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Env(r.Context(), r.PathValue("name"))
		write(w, v, e)
	})
	setEnv := func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		if k := r.PathValue("key"); k != "" {
			b.Key = k
		}
		write(w, nil, s.SetEnv(r.Context(), r.PathValue("name"), b.Key, b.Value))
	}
	m.HandleFunc("PUT /api/agents/{name}/env/{key}", setEnv)
	m.HandleFunc("PUT /api/agents/{name}/env", setEnv)
	m.HandleFunc("DELETE /api/agents/{name}/env/{key}", func(w http.ResponseWriter, r *http.Request) {
		write(w, nil, s.DeleteEnv(r.Context(), r.PathValue("name"), r.PathValue("key")))
	})
	m.HandleFunc("GET /api/agents/{name}/files", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Files(r.Context(), r.PathValue("name"))
		write(w, v, e)
	})
	m.HandleFunc("GET /api/agents/{name}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.ReadFile(r.Context(), r.PathValue("name"), r.PathValue("path"))
		write(w, v, e)
	})
	m.HandleFunc("PUT /api/agents/{name}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Content  string `json:"content"`
			BaseHash string `json:"base_hash"`
		}
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := s.WriteFile(r.Context(), r.PathValue("name"), r.PathValue("path"), b.Content, b.BaseHash)
		write(w, v, e)
	})
	m.HandleFunc("GET /api/agents/{name}/email", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Email(r.Context(), r.PathValue("name"))
		write(w, v, e)
	})
	m.HandleFunc("PUT /api/agents/{name}/email", func(w http.ResponseWriter, r *http.Request) {
		var b core.EmailSettings
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := s.SaveEmail(r.Context(), r.PathValue("name"), b)
		write(w, v, e)
	})
	m.HandleFunc("POST /api/agents/{name}/email/test", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Address string `json:"address"`
		}
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := s.TestEmail(r.Context(), r.PathValue("name"), b.Address)
		write(w, v, e)
	})
}
