package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/store"
)

// rateLimitPolicy is one quota, named as in the RateLimit-Policy header of draft-ietf-httpapi-ratelimit-headers.
type rateLimitPolicy struct {
	name          string
	quota         int
	windowSeconds int32
}

func (p rateLimitPolicy) item() string {
	return fmt.Sprintf("%q;q=%d;w=%d", p.name, p.quota, p.windowSeconds)
}

// rateLimitHeaders collects the policies that applied to a request, so each response lists all of them.
type rateLimitHeaders struct{ policies []string }

type rateLimitKey struct{}

func withRateLimitHeaders(ctx context.Context) context.Context {
	return context.WithValue(ctx, rateLimitKey{}, &rateLimitHeaders{})
}

func rateLimitHeadersFrom(ctx context.Context) *rateLimitHeaders {
	if headers, ok := ctx.Value(rateLimitKey{}).(*rateLimitHeaders); ok {
		return headers
	}
	return &rateLimitHeaders{}
}

type rateLimiter struct {
	queries        *store.Queries
	trustProxyHops int
	global         rateLimitPolicy
	auth           rateLimitPolicy
	login          rateLimitPolicy
}

// rateLimitClient keys an address the way rate limits count it: an IPv4 address alone, and an IPv6 address
// by its /64, because one client usually holds a whole /64.
func rateLimitClient(address string) string {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	ip = ip.Unmap().WithZone("")
	if ip.Is4() {
		return ip.String()
	}
	return netip.PrefixFrom(ip, 64).Masked().String()
}

func secondsUntil(t time.Time) int {
	return max(1, int(math.Ceil(time.Until(t).Seconds())))
}

func (l rateLimiter) hit(ctx context.Context, key string, policy rateLimitPolicy) (store.HitRateLimitRow, error) {
	hit, err := l.queries.HitRateLimit(ctx, store.HitRateLimitParams{Key: key, WindowSeconds: policy.windowSeconds})
	if err != nil {
		return hit, fmt.Errorf("count %s rate limit hit: %w", policy.name, err)
	}
	return hit, nil
}

// check counts the request against a per-client policy, adds the policy to the RateLimit headers, and
// returns a problem once the client is over the quota.
func (l rateLimiter) check(w http.ResponseWriter, r *http.Request, policy rateLimitPolicy) error {
	hit, err := l.hit(r.Context(), policy.name+":"+rateLimitClient(clientIP(r, l.trustProxyHops)), policy)
	if err != nil {
		return err
	}
	headers := rateLimitHeadersFrom(r.Context())
	headers.policies = append(headers.policies, policy.item())
	resetIn := secondsUntil(hit.ResetAt)
	state := fmt.Sprintf("%q;r=%d;t=%d", policy.name, max(0, policy.quota-int(hit.Count)), resetIn)
	if int(hit.Count) > policy.quota {
		return rateLimited(headers.policies, policy, resetIn,
			fmt.Sprintf("You've reached the limit of %d requests per minute. Retry in %d seconds.", policy.quota, resetIn))
	}
	w.Header().Set("RateLimit-Policy", strings.Join(headers.policies, ", "))
	appendHeader(w.Header(), "RateLimit", state)
	return nil
}

func appendHeader(h http.Header, name, item string) {
	if existing := h.Get(name); existing != "" {
		item = existing + ", " + item
	}
	h.Set(name, item)
}

// rateLimited builds a 429 that lists every policy, while RateLimit and Retry-After describe only the one that ran out.
func rateLimited(policies []string, policy rateLimitPolicy, resetIn int, detail string) *problem.Error {
	return &problem.Error{
		Slug:   problem.RateLimited,
		Detail: detail,
		Headers: map[string]string{
			"RateLimit-Policy": strings.Join(policies, ", "),
			"RateLimit":        fmt.Sprintf("%q;r=0;t=%d", policy.name, resetIn),
			"Retry-After":      strconv.Itoa(resetIn),
		},
	}
}

func loginKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "login:" + hex.EncodeToString(sum[:])
}

// startLogin counts a login attempt for the account as failed before the password is checked, so concurrent
// guesses can't all slip under the limit. Call the returned undo when the login succeeds.
func (l rateLimiter) startLogin(ctx context.Context, email string) (func(context.Context) error, error) {
	key := loginKey(email)
	hit, err := l.hit(ctx, key, l.login)
	if err != nil {
		return nil, err
	}
	if int(hit.Count) > l.login.quota {
		resetIn := secondsUntil(hit.ResetAt)
		policies := append(slices.Clone(rateLimitHeadersFrom(ctx).policies), l.login.item())
		return nil, rateLimited(policies, l.login, resetIn,
			fmt.Sprintf("Too many failed logins for this account. Retry in %d seconds.", resetIn))
	}
	return func(ctx context.Context) error {
		if err := l.queries.UndoRateLimitHit(ctx, key); err != nil {
			return fmt.Errorf("undo login failure count: %w", err)
		}
		return nil
	}, nil
}
