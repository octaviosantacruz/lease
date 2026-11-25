package identity

import (
	"context"
	"log/slog"
	"net"
	"net/http"
)

// Actor is the authenticated caller of one request.
type Actor struct {
	Principal string // the Lease principal the policy names
	Person    Person // what Ozal asserted
}

// En Go se usa un tipo propio, no exportado, como clave de context: así
// ningún otro paquete puede pisar o leer este valor por accidente.
type actorKey struct{}

// From returns the actor Middleware accepted.
func From(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// WithActor is for tests of handlers behind Middleware.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// Middleware admits a request only from a loopback peer (Ozal on this host)
// carrying one valid assertion for a mapped person. The peer check closes
// direct access; the assertion is the identity; the mapping is the
// authorization to use Lease at all (the policy then decides each action).
// Failures answer 401/403 with a stable code and no data.
func Middleware(v *Verifier, logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackPeer(r.RemoteAddr) {
			deny(w, r, logger, http.StatusUnauthorized, "not_through_ozal")
			return
		}
		values := r.Header.Values(Header)
		if len(values) > 1 {
			deny(w, r, logger, http.StatusUnauthorized, Malformed)
			return
		}
		token := ""
		if len(values) == 1 {
			token = values[0]
		}
		person, code := v.Verify(token)
		if code != "" {
			deny(w, r, logger, http.StatusUnauthorized, code)
			return
		}
		principal := v.Principal(person)
		if principal == "" {
			deny(w, r, logger, http.StatusForbidden, Unmapped)
			return
		}
		// The assertion has served its purpose; nothing downstream needs it.
		r.Header.Del(Header)
		next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), Actor{Principal: principal, Person: person})))
	})
}

func deny(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, code string) {
	logger.LogAttrs(r.Context(), slog.LevelWarn, "identity.denied", slog.String("code", code), slog.String("path", r.URL.Path))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
}

func loopbackPeer(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
