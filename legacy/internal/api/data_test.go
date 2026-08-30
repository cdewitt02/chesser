package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cdewitt02/chesser/internal/models"
)

var testDate = models.YearMonth{Year: 2026, Month: "01"}

// serve points the client at a fixture server for the duration of one test.
func serve(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	previous := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = previous })
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}
}

// The defect this fix exists for: every error status used to unmarshal into an
// empty game list and return a nil error, so the pipeline reported success on
// every failure.
func TestErrorStatusIsNeverSilentSuccess(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantText []string
	}{
		{
			// The most likely first-run mistake.
			name:     "404 names the username",
			status:   http.StatusNotFound,
			body:     `{"code":0,"message":"User not found."}`,
			wantText: []string{"magnus", "404", "check the username"},
		},
		{
			name:     "429 says how to recover",
			status:   http.StatusTooManyRequests,
			body:     "",
			wantText: []string{"rate limited", "429"},
		},
		{
			// chesser sends no User-Agent by default, which Chess.com is
			// known to block — reachable on a first run with a valid username.
			name:     "403 mentions the client identity",
			status:   http.StatusForbidden,
			body:     "Forbidden",
			wantText: []string{"403", "chesser/0.1"},
		},
		{
			name:     "other statuses carry the code and body",
			status:   http.StatusInternalServerError,
			body:     "upstream exploded",
			wantText: []string{"500", "upstream exploded"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serve(t, status(tc.status, tc.body))

			games, err := GetData(testDate, "magnus")
			if err == nil {
				t.Fatalf("got %d games and no error; a %d must be an error",
					len(games), tc.status)
			}
			if games != nil {
				t.Errorf("got %d games alongside an error, want none", len(games))
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

func TestRetryAfterIsSurfaced(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := GetData(testDate, "magnus")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "60") {
		t.Errorf("error = %q, want it to carry the Retry-After value", err)
	}
}

// Chess.com rejects clients that do not identify themselves.
func TestSendsUserAgent(t *testing.T) {
	var got string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		json.NewEncoder(w).Encode(Response{})
	})

	if _, err := GetData(testDate, "magnus"); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got != userAgent {
		t.Errorf("User-Agent = %q, want %q", got, userAgent)
	}
	if strings.Contains(got, "Go-http-client") {
		t.Error("requests still go out as the default Go client, which Chess.com blocks")
	}
}

func TestRequestsTheMonthPath(t *testing.T) {
	var got string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		json.NewEncoder(w).Encode(Response{})
	})

	if _, err := GetData(testDate, "magnus"); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	// The month keeps its leading zero, which is why YearMonth.Month is a
	// string rather than an int.
	if want := "/player/magnus/games/2026/01"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// A month with no games is a real, successful answer — it must stay
// distinguishable from a failure.
func TestEmptyMonthIsNotAnError(t *testing.T) {
	serve(t, status(http.StatusOK, `{"games":[]}`))

	games, err := GetData(testDate, "magnus")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if len(games) != 0 {
		t.Errorf("got %d games, want 0", len(games))
	}
}

func TestSuccessDecodesGames(t *testing.T) {
	serve(t, status(http.StatusOK, `{"games":[
		{"uuid":"abc","url":"https://chess.com/game/1"},
		{"uuid":"def","url":"https://chess.com/game/2"}
	]}`))

	games, err := GetData(testDate, "magnus")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("got %d games, want 2", len(games))
	}
	if games[0].UUID != "abc" {
		t.Errorf("games[0].UUID = %q, want %q", games[0].UUID, "abc")
	}
}

// A 200 carrying something other than the expected JSON must fail loudly, not
// decode into an empty list.
func TestMalformedBodyIsAnError(t *testing.T) {
	serve(t, status(http.StatusOK, "<html>maintenance</html>"))

	_, err := GetData(testDate, "magnus")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "maintenance") {
		t.Errorf("error = %q, want it to include the body it could not parse", err)
	}
}
