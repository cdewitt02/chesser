package search

import (
	"testing"
	"time"
)

func TestQueryParser_Parse(t *testing.T) {
	parser := NewQueryParser()
	username := "testuser"

	tests := []struct {
		name           string
		query          string
		wantResult     *string
		wantUserColor  *string
		wantTimeClass  *string
		wantECOPrefix  *string
		wantOpeningName *string
		wantWeakPhase  *string
		wantMinBlunders *int
		wantMaxBlunders *int
		wantDateFrom   bool // just check if set, not exact value
		wantExtracted  int  // expected number of extracted filters
	}{
		{
			name:       "simple loss query",
			query:      "my losses",
			wantResult: StringPtr("loss"),
			wantExtracted: 1,
		},
		{
			name:       "simple win query",
			query:      "games I won",
			wantResult: StringPtr("win"),
			wantExtracted: 1,
		},
		{
			name:       "draw query",
			query:      "show me draws",
			wantResult: StringPtr("draw"),
			wantExtracted: 1,
		},
		{
			name:          "color filter - as black",
			query:         "my games as black",
			wantUserColor: StringPtr("black"),
			wantExtracted: 1,
		},
		{
			name:          "color filter - with white",
			query:         "playing white",
			wantUserColor: StringPtr("white"),
			wantExtracted: 1,
		},
		{
			name:           "opening filter - sicilian",
			query:          "my Sicilian games",
			wantECOPrefix:  StringPtr("B"),
			wantOpeningName: StringPtr("Sicilian"),
			wantExtracted:  1,
		},
		{
			name:           "opening filter - king's indian",
			query:          "how do I play the King's Indian?",
			wantECOPrefix:  StringPtr("E"),
			wantOpeningName: StringPtr("King's Indian"),
			wantExtracted:  1,
		},
		{
			name:           "opening filter - french defense",
			query:          "French Defense analysis",
			wantECOPrefix:  StringPtr("C0"),
			wantOpeningName: StringPtr("French"),
			wantExtracted:  1,
		},
		{
			name:           "opening filter - caro-kann",
			query:          "Caro-Kann games",
			wantECOPrefix:  StringPtr("B1"),
			wantOpeningName: StringPtr("Caro-Kann"),
			wantExtracted:  1,
		},
		{
			name:          "time class filter - blitz",
			query:         "my blitz games",
			wantTimeClass: StringPtr("blitz"),
			wantExtracted: 1,
		},
		{
			name:          "time class filter - rapid",
			query:         "rapid time control",
			wantTimeClass: StringPtr("rapid"),
			wantExtracted: 1,
		},
		{
			name:          "phase filter - endgame",
			query:         "endgame problems",
			wantWeakPhase: StringPtr("endgame"),
			wantExtracted: 1,
		},
		{
			name:          "phase filter - middlegame",
			query:         "my middlegame tactics",
			wantWeakPhase: StringPtr("middlegame"),
			wantExtracted: 1,
		},
		{
			name:           "blunder filter - has blunders",
			query:          "games where I blundered",
			wantMinBlunders: IntPtr(1),
			wantExtracted:  1,
		},
		{
			name:           "blunder filter - no blunders",
			query:          "games without blunders",
			wantMaxBlunders: IntPtr(0),
			wantExtracted:  1,
		},
		{
			name:         "time filter - this week",
			query:        "my games this week",
			wantDateFrom: true,
			wantExtracted: 1,
		},
		{
			name:         "time filter - recent",
			query:        "recent games",
			wantDateFrom: true,
			wantExtracted: 1,
		},
		{
			name:           "combined - sicilian losses",
			query:          "Why do I keep losing in the Sicilian?",
			wantResult:     StringPtr("loss"),
			wantECOPrefix:  StringPtr("B"),
			wantOpeningName: StringPtr("Sicilian"),
			wantExtracted:  2,
		},
		{
			name:          "combined - blitz as black",
			query:         "my blitz games as black",
			wantTimeClass: StringPtr("blitz"),
			wantUserColor: StringPtr("black"),
			wantExtracted: 2,
		},
		{
			name:          "combined - endgame losses",
			query:         "endgame losses",
			wantWeakPhase: StringPtr("endgame"),
			wantResult:    StringPtr("loss"),
			wantExtracted: 2,
		},
		{
			name:          "no filters - general question",
			query:         "how can I improve?",
			wantExtracted: 0,
		},
		{
			name:          "no filters - analysis question",
			query:         "analyze my play",
			wantExtracted: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.query, username)

			// Check result filter
			if tt.wantResult != nil {
				if result.Filters.Result == nil {
					t.Errorf("expected Result filter to be set to %q, got nil", *tt.wantResult)
				} else if *result.Filters.Result != *tt.wantResult {
					t.Errorf("expected Result = %q, got %q", *tt.wantResult, *result.Filters.Result)
				}
			} else if result.Filters.Result != nil {
				t.Errorf("expected Result filter to be nil, got %q", *result.Filters.Result)
			}

			// Check user color filter
			if tt.wantUserColor != nil {
				if result.Filters.UserColor == nil {
					t.Errorf("expected UserColor filter to be set to %q, got nil", *tt.wantUserColor)
				} else if *result.Filters.UserColor != *tt.wantUserColor {
					t.Errorf("expected UserColor = %q, got %q", *tt.wantUserColor, *result.Filters.UserColor)
				}
			} else if result.Filters.UserColor != nil {
				t.Errorf("expected UserColor filter to be nil, got %q", *result.Filters.UserColor)
			}

			// Check time class filter
			if tt.wantTimeClass != nil {
				if result.Filters.TimeClass == nil {
					t.Errorf("expected TimeClass filter to be set to %q, got nil", *tt.wantTimeClass)
				} else if *result.Filters.TimeClass != *tt.wantTimeClass {
					t.Errorf("expected TimeClass = %q, got %q", *tt.wantTimeClass, *result.Filters.TimeClass)
				}
			} else if result.Filters.TimeClass != nil {
				t.Errorf("expected TimeClass filter to be nil, got %q", *result.Filters.TimeClass)
			}

			// Check ECO prefix filter
			if tt.wantECOPrefix != nil {
				if result.Filters.ECOPrefix == nil {
					t.Errorf("expected ECOPrefix filter to be set to %q, got nil", *tt.wantECOPrefix)
				} else if *result.Filters.ECOPrefix != *tt.wantECOPrefix {
					t.Errorf("expected ECOPrefix = %q, got %q", *tt.wantECOPrefix, *result.Filters.ECOPrefix)
				}
			} else if result.Filters.ECOPrefix != nil {
				t.Errorf("expected ECOPrefix filter to be nil, got %q", *result.Filters.ECOPrefix)
			}

			// Check opening name filter
			if tt.wantOpeningName != nil {
				if result.Filters.OpeningName == nil {
					t.Errorf("expected OpeningName filter to be set to %q, got nil", *tt.wantOpeningName)
				} else if *result.Filters.OpeningName != *tt.wantOpeningName {
					t.Errorf("expected OpeningName = %q, got %q", *tt.wantOpeningName, *result.Filters.OpeningName)
				}
			}

			// Check weak phase filter
			if tt.wantWeakPhase != nil {
				if result.Filters.WeakPhase == nil {
					t.Errorf("expected WeakPhase filter to be set to %q, got nil", *tt.wantWeakPhase)
				} else if *result.Filters.WeakPhase != *tt.wantWeakPhase {
					t.Errorf("expected WeakPhase = %q, got %q", *tt.wantWeakPhase, *result.Filters.WeakPhase)
				}
			} else if result.Filters.WeakPhase != nil {
				t.Errorf("expected WeakPhase filter to be nil, got %q", *result.Filters.WeakPhase)
			}

			// Check min blunders filter
			if tt.wantMinBlunders != nil {
				if result.Filters.MinBlunders == nil {
					t.Errorf("expected MinBlunders filter to be set to %d, got nil", *tt.wantMinBlunders)
				} else if *result.Filters.MinBlunders != *tt.wantMinBlunders {
					t.Errorf("expected MinBlunders = %d, got %d", *tt.wantMinBlunders, *result.Filters.MinBlunders)
				}
			}

			// Check max blunders filter
			if tt.wantMaxBlunders != nil {
				if result.Filters.MaxBlunders == nil {
					t.Errorf("expected MaxBlunders filter to be set to %d, got nil", *tt.wantMaxBlunders)
				} else if *result.Filters.MaxBlunders != *tt.wantMaxBlunders {
					t.Errorf("expected MaxBlunders = %d, got %d", *tt.wantMaxBlunders, *result.Filters.MaxBlunders)
				}
			}

			// Check date from filter (just existence)
			if tt.wantDateFrom {
				if result.Filters.DateFrom == nil {
					t.Errorf("expected DateFrom filter to be set, got nil")
				}
			}

			// Check extracted filters count
			if len(result.ExtractedFilters) != tt.wantExtracted {
				t.Errorf("expected %d extracted filters, got %d: %v", tt.wantExtracted, len(result.ExtractedFilters), result.ExtractedFilters)
			}

			// Verify username is always set
			if result.Filters.Username != username {
				t.Errorf("expected Username = %q, got %q", username, result.Filters.Username)
			}
		})
	}
}

func TestQueryParser_SemanticRemainder(t *testing.T) {
	parser := NewQueryParser()
	username := "testuser"

	tests := []struct {
		name           string
		query          string
		wantContains   string
		wantNotContain string
	}{
		{
			name:           "removes opening keyword",
			query:          "Why do I keep losing in the Sicilian?",
			wantContains:   "Why do I keep",
			wantNotContain: "Sicilian",
		},
		{
			name:           "removes result keyword",
			query:          "my recent losses",
			wantNotContain: "losses",
		},
		{
			name:           "removes time keyword",
			query:          "games this week",
			wantNotContain: "this week",
		},
		{
			name:         "preserves semantic content",
			query:        "how can I improve my play?",
			wantContains: "how can I improve my play",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.query, username)

			if tt.wantContains != "" {
				if !containsSubstring(result.SemanticQuery, tt.wantContains) {
					t.Errorf("expected SemanticQuery to contain %q, got %q", tt.wantContains, result.SemanticQuery)
				}
			}

			if tt.wantNotContain != "" {
				if containsSubstring(result.SemanticQuery, tt.wantNotContain) {
					t.Errorf("expected SemanticQuery to NOT contain %q, got %q", tt.wantNotContain, result.SemanticQuery)
				}
			}
		})
	}
}

func TestGameFilters_BuildWHERE(t *testing.T) {
	tests := []struct {
		name        string
		filters     *GameFilters
		wantClause  bool   // whether a WHERE clause should be generated
		wantArgs    int    // expected number of args
	}{
		{
			name: "username only",
			filters: &GameFilters{
				Username: "testuser",
			},
			wantClause: true,
			wantArgs:   1,
		},
		{
			name: "username and result",
			filters: &GameFilters{
				Username: "testuser",
				Result:   StringPtr("loss"),
			},
			wantClause: true,
			wantArgs:   2, // username twice (once for base filter, once for result translation)
		},
		{
			name: "eco prefix filter",
			filters: &GameFilters{
				Username:  "testuser",
				ECOPrefix: StringPtr("B"),
			},
			wantClause: true,
			wantArgs:   2,
		},
		{
			name: "date range filter",
			filters: &GameFilters{
				Username: "testuser",
				DateFrom: TimePtr(time.Now().Add(-7 * 24 * time.Hour)),
			},
			wantClause: true,
			wantArgs:   2,
		},
		{
			name:       "empty filters",
			filters:    &GameFilters{},
			wantClause: false,
			wantArgs:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.filters.BuildWHERE(1)

			if tt.wantClause && result.Clause == "" {
				t.Errorf("expected WHERE clause to be generated, got empty")
			}
			if !tt.wantClause && result.Clause != "" {
				t.Errorf("expected empty WHERE clause, got %q", result.Clause)
			}
			if len(result.Args) != tt.wantArgs {
				t.Errorf("expected %d args, got %d", tt.wantArgs, len(result.Args))
			}
		})
	}
}

func TestGameFilters_Clone(t *testing.T) {
	original := &GameFilters{
		Username:    "testuser",
		Result:      StringPtr("win"),
		UserColor:   StringPtr("white"),
		TimeClass:   StringPtr("blitz"),
		ECOPrefix:   StringPtr("B"),
		MinBlunders: IntPtr(1),
	}

	clone := original.Clone()

	// Verify values are equal
	if clone.Username != original.Username {
		t.Errorf("Username mismatch")
	}
	if *clone.Result != *original.Result {
		t.Errorf("Result mismatch")
	}
	if *clone.UserColor != *original.UserColor {
		t.Errorf("UserColor mismatch")
	}

	// Verify it's a deep copy (modifying clone doesn't affect original)
	*clone.Result = "loss"
	if *original.Result == "loss" {
		t.Errorf("Clone modified original - not a deep copy")
	}
}

func TestGameFilters_IsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		filters *GameFilters
		want    bool
	}{
		{
			name:    "empty filters",
			filters: &GameFilters{},
			want:    true,
		},
		{
			name: "username only - still empty",
			filters: &GameFilters{
				Username: "testuser",
			},
			want: true,
		},
		{
			name: "with result filter",
			filters: &GameFilters{
				Result: StringPtr("win"),
			},
			want: false,
		},
		{
			name: "with eco prefix",
			filters: &GameFilters{
				ECOPrefix: StringPtr("B"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filters.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGameFilters_String(t *testing.T) {
	filters := &GameFilters{
		Result:    StringPtr("loss"),
		UserColor: StringPtr("black"),
		TimeClass: StringPtr("blitz"),
	}

	str := filters.String()

	if str == "" {
		t.Errorf("expected non-empty string representation")
	}
	if !containsSubstring(str, "result=loss") {
		t.Errorf("expected string to contain result=loss, got %q", str)
	}
	if !containsSubstring(str, "color=black") {
		t.Errorf("expected string to contain color=black, got %q", str)
	}
	if !containsSubstring(str, "time=blitz") {
		t.Errorf("expected string to contain time=blitz, got %q", str)
	}
}

// Helper function for substring check
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
