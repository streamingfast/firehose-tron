package rpc

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Endpoint is a parsed RPC endpoint with its control parameters separated from
// the URL. URL is the final value used to dial: it never contains the apiKey or
// insecure query parameters. The key travels in request headers instead, and
// insecure is applied as a dial option.
type Endpoint struct {
	URL      *url.URL
	APIKey   string
	Insecure bool
}

// ParseEndpoint expands ${VAR}/$VAR environment references in rawURL, extracts
// and removes the apiKey and insecure query parameters (matched
// case-insensitively), and defaults the scheme to https:// when none is present
// (use an http:// endpoint for plaintext). defaultAPIKey is used when the URL
// carries no apiKey of its own; an empty apiKey value is treated as absent.
//
// An empty rawURL, unresolved environment variables, and a non-boolean insecure
// value are errors.
func ParseEndpoint(rawURL, defaultAPIKey string) (Endpoint, error) {
	expanded, err := expandEnv(rawURL)
	if err != nil {
		return Endpoint{}, err
	}

	if strings.TrimSpace(expanded) == "" {
		return Endpoint{}, fmt.Errorf("endpoint URL is empty")
	}

	if !schemePrefixRegexp.MatchString(expanded) {
		expanded = "https://" + expanded
	}

	parsed, err := url.Parse(expanded)
	if err != nil {
		// url.Error.Error() embeds the raw URL a second time (e.g. `parse
		// "<raw>": invalid port ...`), so redacting only our own %q above is
		// not enough: the underlying error's message must be scrubbed too.
		return Endpoint{}, fmt.Errorf("parse endpoint %q: %s", RedactRawURL(expanded), strings.ReplaceAll(err.Error(), expanded, RedactRawURL(expanded)))
	}

	query := parsed.Query()

	// Query parameter names are case-sensitive per the URL spec and url.Values
	// does not fold case, so match apiKey/insecure case-insensitively ourselves
	// to accept apikey, APIKEY, Insecure, etc.
	apiKey := defaultAPIKey
	if v, ok := popQueryParam(query, "apiKey"); ok && v != "" {
		apiKey = v
	}

	insecure := false
	if v, ok := popQueryParam(query, "insecure"); ok {
		insecure, err = strconv.ParseBool(v)
		if err != nil {
			return Endpoint{}, fmt.Errorf("invalid insecure value %q: want a boolean: %w", v, err)
		}
	}

	parsed.RawQuery = query.Encode()

	return Endpoint{URL: parsed, APIKey: apiKey, Insecure: insecure}, nil
}

// schemePrefixRegexp matches an http:// or https:// scheme prefix
// case-insensitively, so an endpoint like HTTP://host is not double-prefixed.
var schemePrefixRegexp = regexp.MustCompile(`(?i)^https?://`)

// popQueryParam removes the first query parameter whose name case-insensitively
// matches name and returns its value and whether it was present.
func popQueryParam(query url.Values, name string) (string, bool) {
	for k := range query {
		if strings.EqualFold(k, name) {
			v := query.Get(k)
			query.Del(k)
			return v, true
		}
	}
	return "", false
}

// expandEnv replaces $VAR and ${VAR} references, returning an error that names
// any variable that is not set in the environment.
func expandEnv(in string) (string, error) {
	var missing []string
	out := os.Expand(in, func(name string) string {
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unresolved environment variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// DialTarget returns the host:port used to dial, filling in the scheme default
// port when none is present.
func (e Endpoint) DialTarget() string {
	port := e.URL.Port()
	if port == "" {
		if e.Plaintext() {
			port = "80"
		} else {
			port = "443"
		}
	}
	return e.URL.Hostname() + ":" + port
}

// Plaintext reports whether the resolved scheme is http (no TLS).
func (e Endpoint) Plaintext() bool {
	return e.URL.Scheme == "http"
}

// RedactRawURL masks the apiKey query value in a raw (possibly unparseable)
// URL string so it can appear safely in error messages and logs.
func RedactRawURL(raw string) string {
	return apiKeyRedactRegexp.ReplaceAllString(raw, "${1}<redacted>")
}

// Case-insensitive so a key passed as ?APIKEY= or ?ApiKey= is redacted too.
var apiKeyRedactRegexp = regexp.MustCompile(`(?i)([?&]apiKey=)[^&]*`)

// String renders the endpoint for logging with the API key redacted.
func (e Endpoint) String() string {
	if e.APIKey == "" {
		return e.URL.String()
	}
	redacted := *e.URL
	query := redacted.Query()
	query.Set("apiKey", "<redacted>")
	redacted.RawQuery = query.Encode()
	// url.Values.Encode escapes the angle brackets; decode them back so the
	// marker reads plainly in logs.
	return strings.ReplaceAll(redacted.String(), "apiKey=%3Credacted%3E", "apiKey=<redacted>")
}
