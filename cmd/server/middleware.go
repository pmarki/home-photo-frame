package main

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type userCtxKey struct{}

// authMiddleware enforces the YAML-configured user list. It is a no-op when
// no config file is loaded. Skipped routes (the bootstrap endpoints and SPA
// shell) always pass through so the frontend can render the user-select modal.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appConfig == nil || !requiresAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		id := r.Header.Get("X-User")
		if id == "" {
			// <img> and direct browser navigation can't add headers; fall back
			// to the cookie set by the frontend so thumbnail/original URLs work.
			if c, err := r.Cookie("user"); err == nil {
				id = c.Value
			}
		}
		u, ok := usersByID[id]
		if !ok {
			http.Error(w, "unknown user", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requiresAuth returns true for /api/* routes that should be gated by the
// X-User header. Bootstrap endpoints (/api/config, /api/users) and non-API
// routes are exempt so the SPA can load and prompt for a user.
func requiresAuth(p string) bool {
	if !strings.HasPrefix(p, "/api/") {
		return false
	}
	switch p {
	case "/api/config", "/api/users":
		return false
	}
	return true
}

// userFromCtx returns the *ConfigUser stashed by authMiddleware. Returns nil
// when the feature is disabled or the request is on an unauthenticated route.
func userFromCtx(r *http.Request) *ConfigUser {
	if v, ok := r.Context().Value(userCtxKey{}).(*ConfigUser); ok {
		return v
	}
	return nil
}

// recoveryMiddleware catches panics in HTTP handlers, logs them with a stack
// trace, and returns 500. net/http already recovers handler panics but logs
// only a single line; this gives us the full trace.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// safeGo launches fn in a new goroutine. If fn panics the panic is logged with
// a stack trace and the goroutine exits (use for one-shot operations).
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic in %s: %v\n%s", name, rec, debug.Stack())
			}
		}()
		fn()
	}()
}

// safeLoop launches fn in a goroutine that restarts fn after any panic, with a
// short back-off, so long-running background loops survive unexpected errors.
func safeLoop(name string, fn func()) {
	go func() {
		for {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("panic in %s: %v\n%s", name, rec, debug.Stack())
					}
				}()
				fn()
			}()
			log.Printf("%s: restarting in 5s after unexpected exit", name)
			time.Sleep(5 * time.Second)
		}
	}()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
