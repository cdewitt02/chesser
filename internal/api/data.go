// Package api is the Chess.com public API client.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cdewitt02/chesser/internal/models"
)

// baseURL is a variable so tests can point it at an httptest.Server. Nothing
// else reassigns it.
var baseURL = "https://api.chess.com/pub"

// userAgent identifies chesser to Chess.com. Requests without one go out as
// "Go-http-client/1.1", which the public API is known to reject or throttle —
// a 403 on a new user's very first run with a perfectly valid username. The
// API docs ask for contact information, which the repository URL provides.
const userAgent = "chesser/0.1 (+https://github.com/cdewitt02/chesser)"

// httpClient carries an explicit timeout. http.DefaultClient has none, so a
// hung connection would hang the whole ingestion pipeline indefinitely.
var httpClient = &http.Client{Timeout: 30 * time.Second}

type Response struct {
	Games []models.Game `json:"games"`
}

// GetData fetches one month of games for a player.
//
// Every non-2xx status is an error. This matters more than it looks: an error
// body unmarshals cleanly into a zero-valued Response, because encoding/json
// does not error on absent fields, so returning it as an empty game list made
// the program print "Fetched 0 games" and exit 0 on every failure.
func GetData(date models.YearMonth, username string) ([]models.Game, error) {
	url := fmt.Sprintf("%s/player/%s/games/%d/%s", baseURL, username, date.Year, date.Month)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the Chess.com request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the Chess.com API: %w (check your network connection)", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the Chess.com response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(resp, body, date, username)
	}

	var response Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("Chess.com returned a body that is not the expected JSON: %w: %s",
			err, snippet(body))
	}

	return response.Games, nil
}

// statusError turns a status code into a message that names the remedy, since
// each class here has a different one and the user is usually about to spend
// minutes on Stockfish analysis.
func statusError(resp *http.Response, body []byte, date models.YearMonth, username string) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf(
			"no games found for user %q in %d/%s: Chess.com returned 404 — check the username spelling",
			username, date.Year, date.Month)
	case http.StatusTooManyRequests:
		if after := strings.TrimSpace(resp.Header.Get("Retry-After")); after != "" {
			return fmt.Errorf(
				"rate limited by Chess.com (429); retry after %s seconds, or ingest fewer months at a time",
				after)
		}
		return fmt.Errorf(
			"rate limited by Chess.com (429); wait a minute and retry, or ingest fewer months at a time")
	case http.StatusForbidden:
		return fmt.Errorf(
			"Chess.com refused the request (403). This usually means the client was blocked; "+
				"chesser identifies itself as %q. Response: %s", userAgent, snippet(body))
	default:
		return fmt.Errorf("Chess.com returned status %d: %s", resp.StatusCode, snippet(body))
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(empty body)"
	}
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
