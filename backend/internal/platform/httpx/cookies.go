package httpx

import (
	"net/http"
	"time"
)

const (
	AccessCookieName  = "wp_access"
	RefreshCookieName = "wp_refresh"
)

type CookieConfig struct {
	Secure     bool
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// Cookies are HttpOnly so JavaScript cannot exfiltrate the session. The Next.js
// app talks to the API through a same-origin proxy, keeping SameSite effective.
func SetAuthCookies(w http.ResponseWriter, cfg CookieConfig, accessToken string, refreshToken string, accessExpires time.Time, refreshExpires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     AccessCookieName,
		Value:    accessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  accessExpires,
		MaxAge:   int(time.Until(accessExpires).Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    refreshToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  refreshExpires,
		MaxAge:   int(time.Until(refreshExpires).Seconds()),
	})
}

func ClearAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{AccessCookieName, RefreshCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
		})
	}
}

func TokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	if c, err := r.Cookie(AccessCookieName); err == nil {
		return c.Value
	}
	return ""
}

func RefreshTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(RefreshCookieName); err == nil {
		return c.Value
	}
	return ""
}
