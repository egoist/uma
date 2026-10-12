package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// cloudEndpoint is the API of Umami Cloud.
const cloudEndpoint = "https://api.umami.is/v1"

// client calls the Umami API, of Umami Cloud or of a server of one's own,
// with an API key.
type client struct {
	endpoint string // without a trailing slash, as https://api.umami.is/v1
	key      string
	http     *http.Client
}

func newClient(endpoint, key string) *client {
	return &client{
		endpoint: strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		key:      strings.TrimSpace(key),
		http:     &http.Client{Timeout: 20 * time.Second},
	}
}

// apiError is a response other than 2xx.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string {
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "The API key was rejected."
	case http.StatusNotFound:
		return "Nothing found at this endpoint."
	case http.StatusTooManyRequests:
		return "Too many requests. Try again in a minute."
	}
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("The server answered %d.", e.status)
}

func (cl *client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := cl.endpoint + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	// Umami takes a bearer token; Umami Cloud's first API took this header.
	req.Header.Set("Authorization", "Bearer "+cl.key)
	req.Header.Set("x-umami-api-key", cl.key)
	res, err := cl.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Timeout() {
			return errors.New("The server took too long to answer.")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Couldn't reach the server.")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		e := &apiError{status: res.StatusCode}
		var m struct {
			Message string `json:"message"`
			Error   any    `json:"error"`
		}
		if json.Unmarshal(body, &m) == nil {
			e.msg = m.Message
		}
		return e
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("The server's answer isn't Umami's.")
	}
	return nil
}

// website is one of the account's websites.
type website struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

// websites returns every website the key can see, its teams' included.
func (cl *client) websites(ctx context.Context) ([]website, error) {
	var all []website
	for page := 1; ; page++ {
		var res struct {
			Data     []website `json:"data"`
			Count    int       `json:"count"`
			PageSize int       `json:"pageSize"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "pageSize": {"100"}, "includeTeams": {"true"}}
		if err := cl.get(ctx, "/websites", q, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Data...)
		if len(res.Data) == 0 || len(all) >= res.Count || page >= 20 {
			return all, nil
		}
	}
}

// num decodes a count however the server writes it: a number, a string (as
// Postgres's bigints come), or {"value": n} as Umami 2 wrote stats.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*n = 0
		return nil
	}
	if b[0] == '{' {
		var v struct {
			Value num `json:"value"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*n = v.Value
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = num(f)
	return nil
}

// stats are a website's totals over a range.
type stats struct {
	Pageviews num `json:"pageviews"`
	Visitors  num `json:"visitors"`
	Visits    num `json:"visits"`
	Bounces   num `json:"bounces"`
	Totaltime num `json:"totaltime"` // seconds
}

// bounceRate is the share of visits that saw one page, from 0 to 1.
func (s stats) bounceRate() float64 {
	if s.Visits == 0 {
		return 0
	}
	return min(1, float64(s.Bounces)/float64(s.Visits))
}

// visitDuration is the time an average visit lasted.
func (s stats) visitDuration() time.Duration {
	if s.Visits == 0 {
		return 0
	}
	return time.Duration(float64(s.Totaltime) / float64(s.Visits) * float64(time.Second))
}

// stats returns a website's totals over [from, to] and over the range
// before it, of the same length.
func (cl *client) stats(ctx context.Context, id string, from, to time.Time) (cur, prev stats, err error) {
	var raw map[string]json.RawMessage
	if err := cl.get(ctx, "/websites/"+url.PathEscape(id)+"/stats", rangeQuery(from, to), &raw); err != nil {
		return cur, prev, err
	}
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &cur); err != nil {
		return cur, prev, err
	}
	if c, ok := raw["comparison"]; ok {
		// Umami 3: the previous range's totals beside the current ones.
		err = json.Unmarshal(c, &prev)
		return cur, prev, err
	}
	// Umami 2: {"pageviews": {"value": 10, "prev": 8}, ...}
	var old map[string]struct {
		Prev num `json:"prev"`
	}
	if json.Unmarshal(b, &old) == nil {
		prev = stats{
			Pageviews: old["pageviews"].Prev,
			Visitors:  old["visitors"].Prev,
			Visits:    old["visits"].Prev,
			Bounces:   old["bounces"].Prev,
			Totaltime: old["totaltime"].Prev,
		}
	}
	return cur, prev, nil
}

// active returns how many people are on a website now: in the last five
// minutes.
func (cl *client) active(ctx context.Context, id string) (int, error) {
	var res struct {
		Visitors *num `json:"visitors"`
		X        *num `json:"x"` // Umami 2
	}
	if err := cl.get(ctx, "/websites/"+url.PathEscape(id)+"/active", nil, &res); err != nil {
		return 0, err
	}
	switch {
	case res.Visitors != nil:
		return int(*res.Visitors), nil
	case res.X != nil:
		return int(*res.X), nil
	}
	return 0, nil
}

// point is a value of a series, at a time the server formats.
type point struct {
	X string `json:"x"`
	Y num    `json:"y"`
}

// series returns the pageviews and the visitors of a website in each unit
// of time from from to to, in the time zone tz. Units without any are
// missing.
func (cl *client) series(ctx context.Context, id string, from, to time.Time, unit, tz string) (views, visitors []point, err error) {
	q := rangeQuery(from, to)
	q.Set("unit", unit)
	if tz != "" {
		q.Set("timezone", tz)
	}
	var res struct {
		Pageviews []point `json:"pageviews"`
		Sessions  []point `json:"sessions"`
	}
	if err := cl.get(ctx, "/websites/"+url.PathEscape(id)+"/pageviews", q, &res); err != nil {
		return nil, nil, err
	}
	return res.Pageviews, res.Sessions, nil
}

// metric is a value of a breakdown, as a page and its views.
type metric struct {
	X string `json:"x"`
	Y num    `json:"y"`
}

// metrics returns the top values of a breakdown: "path", "referrer",
// "country", "browser" and so on.
func (cl *client) metrics(ctx context.Context, id, kind string, from, to time.Time, limit int) ([]metric, error) {
	q := rangeQuery(from, to)
	q.Set("type", kind)
	q.Set("limit", strconv.Itoa(limit))
	var res []metric
	err := cl.get(ctx, "/websites/"+url.PathEscape(id)+"/metrics", q, &res)
	var ae *apiError
	if kind == "path" && errors.As(err, &ae) && ae.status == http.StatusBadRequest {
		// Umami 2 named it "url".
		return cl.metrics(ctx, id, "url", from, to, limit)
	}
	return res, err
}

func rangeQuery(from, to time.Time) url.Values {
	return url.Values{
		"startAt": {strconv.FormatInt(from.UnixMilli(), 10)},
		"endAt":   {strconv.FormatInt(to.UnixMilli(), 10)},
	}
}

// dashboardURL is where a website's page is in Umami's own interface.
func dashboardURL(endpoint, id string) string {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Host == "api.umami.is" {
		return "https://cloud.umami.is/websites/" + id
	}
	u.Path = strings.TrimSuffix(u.Path, "/api")
	return u.String() + "/websites/" + id
}
