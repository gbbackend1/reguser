package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/gbbackend1/reguser/api/openapi"
	"github.com/gbbackend1/reguser/app/repos/user"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Router struct {
	chi.Router
	us *user.Users
}

func NewRouter(us *user.Users) *Router {
	r := chi.NewRouter()
	r.Use(AuthMiddleware)

	rt := &Router{
		Router: r,
		us:     us,
	}

	swg, err := openapi.GetSwagger()
	if err != nil {
		log.Fatal("swagger fail")
	}

	r.Mount("/", openapi.Handler(rt))

	// r.HandleFunc("/delete", r.AuthMiddleware(http.HandlerFunc(r.DeleteUser)).ServeHTTP)
	// r.HandleFunc("/search", r.AuthMiddleware(http.HandlerFunc(r.SearchUser)).ServeHTTP)

	rt.Get("/swagger.json", func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		_ = enc.Encode(swg)
	})

	return rt
}

type User struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Data       string    `json:"data"`
	Permission int       `json:"perms"`
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if u, p, ok := r.BasicAuth(); !ok || !(u == "admin" && p == "admin") {
				http.Error(w, "unautorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		},
	)
}

func (rt *Router) PostCreate(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	u := User{}
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	bu := user.User{
		Name: u.Name,
		Data: u.Data,
	}

	nbu, err := rt.us.Create(r.Context(), bu)
	if err != nil {
		http.Error(w, "error when creating", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)

	_ = json.NewEncoder(w).Encode(
		User{
			ID:         nbu.ID,
			Name:       nbu.Name,
			Data:       nbu.Data,
			Permission: nbu.Permissions,
		},
	)
}

// read/{uid}
func (rt *Router) GetReadId(w http.ResponseWriter, r *http.Request, suid string) {
	uid, err := uuid.Parse(suid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (uid == uuid.UUID{}) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	nbu, err := rt.us.Read(r.Context(), uid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "error when reading", http.StatusInternalServerError)
		}
		return
	}

	_ = json.NewEncoder(w).Encode(
		User{
			ID:         nbu.ID,
			Name:       nbu.Name,
			Data:       nbu.Data,
			Permission: nbu.Permissions,
		},
	)
}

func (rt *Router) DeleteDeleteId(w http.ResponseWriter, r *http.Request, suid string) {
	uid, err := uuid.Parse(suid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (uid == uuid.UUID{}) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	nbu, err := rt.us.Delete(r.Context(), uid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "error when reading", http.StatusInternalServerError)
		}
		return
	}

	_ = json.NewEncoder(w).Encode(
		User{
			ID:         nbu.ID,
			Name:       nbu.Name,
			Data:       nbu.Data,
			Permission: nbu.Permissions,
		},
	)
}

// /search?q=...
func (rt *Router) FindUsers(w http.ResponseWriter, r *http.Request, q string) {
	ch, err := rt.us.SearchUsers(r.Context(), q)
	if err != nil {
		http.Error(w, "error when reading", http.StatusInternalServerError)
		return
	}

	enc := json.NewEncoder(w)

	first := true
	fmt.Fprintf(w, "[")
	defer fmt.Fprintf(w, "]")

	for {
		select {
		case <-r.Context().Done():
			return
		case u, ok := <-ch:
			if !ok {
				return
			}
			if first {
				first = false
			} else {
				fmt.Fprintf(w, ",")
			}
			_ = enc.Encode(
				User{
					ID:         u.ID,
					Name:       u.Name,
					Data:       u.Data,
					Permission: u.Permissions,
				},
			)
			w.(http.Flusher).Flush()
		}
	}
}
