package integration

import (
	"net/http"
	"net/http/cookiejar"
)

func newJar() http.CookieJar {
	jar, _ := cookiejar.New(nil)
	return jar
}
