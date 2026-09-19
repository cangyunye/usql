// Package dsnparse implements lenient parsing of database connection strings
// (DSNs), allowing passwords to contain unencoded special characters, or to be
// wrapped in single or double quotes.
//
// DSNs are scanned right-to-left: query parameters first, then the database
// name, then the host/port, then the credentials -- so the last "@" separates
// the userinfo from the host, and everything between the first ":" and that
// "@" is the password. This makes DSNs like the following work without
// percent-encoding the password:
//
//	postgres://superme:AA@1122#@172.20.208.1:5432/test?sslmode=disable
//	postgres://superme:"AA@1122#"@172.20.208.1:5432/test
//
// Percent-encoded DSNs keep working: extracted components are decoded before
// the URL is rebuilt, so "p%40ss" and "p@ss" (and quoted "\"p@ss\"") all yield
// the same password.
package dsnparse

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/xo/dburl"
)

// schemeRE matches a leading URL scheme followed by "://".
var schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// Parse parses a database URL the way dburl.Parse does, but tolerates
// unencoded special characters ("@", ":", "/", "?", "#", spaces, ...) in the
// password, and passwords wrapped in single or double quotes.
//
// DSNs that are not scheme:// URLs (bare file paths, opaque "scheme:path"
// forms, plain database names, ...) are passed to dburl.Parse unchanged.
func Parse(raw string) (*dburl.URL, error) {
	if s, ok := rebuild(raw); ok {
		raw = s
	}
	return dburl.Parse(raw)
}

// rebuild re-encodes a scheme:// DSN into a strictly valid, percent-encoded
// URL, returning it and true when raw is a scheme:// URL, and raw, false
// otherwise.
func rebuild(raw string) (string, bool) {
	m := schemeRE.FindString(raw)
	if m == "" {
		return raw, false
	}
	scheme, rest := m[:len(m)-3], raw[len(m):]

	firstSlash := strings.IndexByte(rest, '/')
	at := strings.LastIndexByte(rest, '@')

	// split the credentials from the host/path/query, scanning from the
	// right so the last "@" separates the userinfo from the host. When the
	// "@" occurs only after the first "/" (eg, "host/db@name"), the authority
	// instead ends at the first "/", like net/url does.
	var creds, hostport, tail string
	var hasCreds bool
	switch {
	case at >= 0 && (firstSlash < 0 || at < firstSlash || strings.Contains(rest[at+1:], "/")):
		creds, hasCreds = rest[:at], true
		if i := strings.IndexAny(rest[at+1:], "/?#"); i >= 0 {
			hostport, tail = rest[at+1:at+1+i], rest[at+1+i:]
		} else {
			hostport = rest[at+1:]
		}
	case at >= 0:
		authority := rest[:firstSlash]
		tail = rest[firstSlash:]
		if a := strings.LastIndexByte(authority, '@'); a >= 0 {
			creds, hasCreds, hostport = authority[:a], true, authority[a+1:]
		} else {
			hostport = authority
		}
	default:
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			hostport, tail = rest[:i], rest[i:]
		} else {
			hostport = rest
		}
	}

	// split the tail into path/query/fragment using standard URL semantics:
	// the fragment starts at the first "#", the query at the first "?" before
	// it, and whatever remains is the (possibly multi-segment) path
	path, query, frag := tail, "", ""
	if i := strings.IndexByte(path, '#'); i >= 0 {
		frag, path = path[i+1:], path[:i]
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		query, path = path[i+1:], path[:i]
	}

	user, pass, hasPass := splitCreds(creds)

	u := &url.URL{
		Scheme:   scheme,
		Host:     hostport,
		Path:     unescapeLenient(path),
		RawQuery: query,
		Fragment: unescapeLenient(frag),
	}
	if hasCreds {
		if hasPass {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	}
	// net/url omits the "//" when scheme, host, path and userinfo are all
	// empty, so rebuild "scheme://" forms by hand to keep them intact
	if u.Host == "" && u.Path == "" && u.User == nil {
		s := scheme + "://"
		if query != "" {
			s += "?" + query
		}
		if frag != "" {
			s += "#" + frag
		}
		return s, true
	}
	return u.String(), true
}

// splitCreds splits a "user[:password]" string. A leading single/double
// quoted section is a quoted username ("user":password, or "user" alone).
// Quoted values are taken literally; unquoted values are percent-decoded when
// possible.
func splitCreds(creds string) (user, pass string, hasPass bool) {
	if len(creds) >= 2 && (creds[0] == '"' || creds[0] == '\'') {
		if i := strings.IndexByte(creds[1:], creds[0]) + 1; i > 0 {
			user = creds[1:i]
			if r := creds[i+1:]; r == "" {
				return user, "", false
			} else if r[0] == ':' {
				pass, hasPass = parsePass(r[1:])
				return user, pass, hasPass
			}
			// trailing garbage after the closing quote: fall through
		}
	}
	i := strings.IndexByte(creds, ':')
	if i < 0 {
		return unescapeLenient(creds), "", false
	}
	pass, hasPass = parsePass(creds[i+1:])
	return unescapeLenient(creds[:i]), pass, hasPass
}

// parsePass parses a password, unwrapping a matching pair of single/double
// quotes; quoted passwords are taken literally, unquoted ones are
// percent-decoded when possible.
func parsePass(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return unescapeLenient(s), true
}

// unescapeLenient percent-decodes s, returning s unchanged when it contains
// no escapes or an invalid escape (eg, "p%zz").
func unescapeLenient(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}
