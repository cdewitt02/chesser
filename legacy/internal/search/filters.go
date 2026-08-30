package search

import (
	"fmt"
	"strings"
	"time"
)

//structured filters for hybrid search.
type GameFilters struct {
	// String exact matches (nil = no filter)
	Result    *string // "win", "loss", "draw"
	UserColor *string // "white", "black"
	TimeClass *string // "bullet", "blitz", "rapid"
	WeakPhase *string // "opening", "middlegame", "endgame"

	// pattern matches
	ECOPrefix   *string // "B" for Sicilian, "E" for King's Indian
	OpeningName *string // Partial match: "Sicilian", "King's Indian"

	// ranges (inclusive)
	MinBlunders *int
	MaxBlunders *int
	MinMistakes *int
	MinRating   *int // opponent rating
	MaxRating   *int

	// Time ranges
	DateFrom *time.Time
	DateTo   *time.Time

	// Always applied (not optional)
	Username string
}

type FilterResult struct {
	Clause string
	Args   []any
}

// constructs WHERE clause from the filters.
func (f *GameFilters) BuildWHERE(startParam int) FilterResult {
	var conditions []string
	var args []any
	paramNum := startParam

	// Helper to add a condition and increment param number
	addCondition := func(condition string, arg any) {
		conditions = append(conditions, condition)
		args = append(args, arg)
		paramNum++
	}

	if f.Username != "" {
		conditions = append(conditions, fmt.Sprintf("(g.white_username = $%d OR g.black_username = $%d)", paramNum, paramNum))
		args = append(args, f.Username)
		paramNum++
	}

	if f.Result != nil {
		// if UserColor is set, we can translate; otherwise, we need to handle both cases
		if f.UserColor != nil {
			var dbResult string
			switch *f.Result {
			case "win":
				dbResult = *f.UserColor
			case "loss":
				if *f.UserColor == "white" {
					dbResult = "black"
				} else {
					dbResult = "white"
				}
			case "draw":
				dbResult = "draw"
			}
			addCondition(fmt.Sprintf("g.result = $%d", paramNum), dbResult)
		} else {
			//filter based on username position
			switch *f.Result {
			case "win":
				// Won means: (white_username = user AND result = white) OR (black_username = user AND result = black)
				conditions = append(conditions, fmt.Sprintf(
					"((g.white_username = $%d AND g.result = 'white') OR (g.black_username = $%d AND g.result = 'black'))",
					paramNum, paramNum))
				args = append(args, f.Username)
				paramNum++
			case "loss":
				// Lost means: (white_username = user AND result = black) OR (black_username = user AND result = white)
				conditions = append(conditions, fmt.Sprintf(
					"((g.white_username = $%d AND g.result = 'black') OR (g.black_username = $%d AND g.result = 'white'))",
					paramNum, paramNum))
				args = append(args, f.Username)
				paramNum++
			case "draw":
				addCondition(fmt.Sprintf("g.result = $%d", paramNum), "draw")
			}
		}
	}

	// User color filter
	if f.UserColor != nil {
		if *f.UserColor == "white" {
			addCondition(fmt.Sprintf("g.white_username = $%d", paramNum), f.Username)
		} else if *f.UserColor == "black" {
			addCondition(fmt.Sprintf("g.black_username = $%d", paramNum), f.Username)
		}
	}

	// Time class filter
	if f.TimeClass != nil {
		addCondition(fmt.Sprintf("g.time_class = $%d", paramNum), *f.TimeClass)
	}

	// ECO prefix filter (pattern match)
	if f.ECOPrefix != nil {
		addCondition(fmt.Sprintf("g.eco_code LIKE $%d", paramNum), *f.ECOPrefix+"%")
	}

	// Opening name filter (partial match, case-insensitive)
	if f.OpeningName != nil {
		addCondition(fmt.Sprintf("LOWER(g.eco_name) LIKE $%d", paramNum), "%"+strings.ToLower(*f.OpeningName)+"%")
	}

	// Blunder filters - need to handle based on user color
	if f.MinBlunders != nil || f.MaxBlunders != nil {
		// Sum blunders for the user's side based on their color position
		blunderExpr := fmt.Sprintf(`
			CASE 
				WHEN g.white_username = $%d THEN g.blunders_white
				ELSE g.blunders_black
			END`, paramNum)
		args = append(args, f.Username)
		paramNum++

		if f.MinBlunders != nil {
			conditions = append(conditions, fmt.Sprintf("(%s) >= $%d", blunderExpr, paramNum))
			args = append(args, *f.MinBlunders)
			paramNum++
		}
		if f.MaxBlunders != nil {
			conditions = append(conditions, fmt.Sprintf("(%s) <= $%d", blunderExpr, paramNum))
			args = append(args, *f.MaxBlunders)
			paramNum++
		}
	}

	// Mistake filter
	if f.MinMistakes != nil {
		mistakeExpr := fmt.Sprintf(`
			CASE 
				WHEN g.white_username = $%d THEN g.mistakes_white
				ELSE g.mistakes_black
			END`, paramNum)
		args = append(args, f.Username)
		paramNum++

		conditions = append(conditions, fmt.Sprintf("(%s) >= $%d", mistakeExpr, paramNum))
		args = append(args, *f.MinMistakes)
		paramNum++
	}

	// Opponent rating filters
	if f.MinRating != nil || f.MaxRating != nil {
		// Opponent rating is the opposite of user's color
		ratingExpr := fmt.Sprintf(`
			CASE 
				WHEN g.white_username = $%d THEN g.black_rating
				ELSE g.white_rating
			END`, paramNum)
		args = append(args, f.Username)
		paramNum++

		if f.MinRating != nil {
			conditions = append(conditions, fmt.Sprintf("(%s) >= $%d", ratingExpr, paramNum))
			args = append(args, *f.MinRating)
			paramNum++
		}
		if f.MaxRating != nil {
			conditions = append(conditions, fmt.Sprintf("(%s) <= $%d", ratingExpr, paramNum))
			args = append(args, *f.MaxRating)
			paramNum++
		}
	}

	// Date range filters
	if f.DateFrom != nil {
		addCondition(fmt.Sprintf("g.created_at >= $%d", paramNum), *f.DateFrom)
	}
	if f.DateTo != nil {
		addCondition(fmt.Sprintf("g.created_at <= $%d", paramNum), *f.DateTo)
	}

	// Build final clause
	clause := ""
	if len(conditions) > 0 {
		clause = strings.Join(conditions, " AND ")
	}

	return FilterResult{
		Clause: clause,
		Args:   args,
	}
}

// IsEmpty returns true if no filters are set (except Username).
func (f *GameFilters) IsEmpty() bool {
	return f.Result == nil &&
		f.UserColor == nil &&
		f.TimeClass == nil &&
		f.WeakPhase == nil &&
		f.ECOPrefix == nil &&
		f.OpeningName == nil &&
		f.MinBlunders == nil &&
		f.MaxBlunders == nil &&
		f.MinMistakes == nil &&
		f.MinRating == nil &&
		f.MaxRating == nil &&
		f.DateFrom == nil &&
		f.DateTo == nil
}

// Clone creates a deep copy of the filters.
func (f *GameFilters) Clone() *GameFilters {
	clone := &GameFilters{
		Username: f.Username,
	}

	if f.Result != nil {
		v := *f.Result
		clone.Result = &v
	}
	if f.UserColor != nil {
		v := *f.UserColor
		clone.UserColor = &v
	}
	if f.TimeClass != nil {
		v := *f.TimeClass
		clone.TimeClass = &v
	}
	if f.WeakPhase != nil {
		v := *f.WeakPhase
		clone.WeakPhase = &v
	}
	if f.ECOPrefix != nil {
		v := *f.ECOPrefix
		clone.ECOPrefix = &v
	}
	if f.OpeningName != nil {
		v := *f.OpeningName
		clone.OpeningName = &v
	}
	if f.MinBlunders != nil {
		v := *f.MinBlunders
		clone.MinBlunders = &v
	}
	if f.MaxBlunders != nil {
		v := *f.MaxBlunders
		clone.MaxBlunders = &v
	}
	if f.MinMistakes != nil {
		v := *f.MinMistakes
		clone.MinMistakes = &v
	}
	if f.MinRating != nil {
		v := *f.MinRating
		clone.MinRating = &v
	}
	if f.MaxRating != nil {
		v := *f.MaxRating
		clone.MaxRating = &v
	}
	if f.DateFrom != nil {
		v := *f.DateFrom
		clone.DateFrom = &v
	}
	if f.DateTo != nil {
		v := *f.DateTo
		clone.DateTo = &v
	}

	return clone
}

func (f *GameFilters) String() string {
	var parts []string

	if f.Result != nil {
		parts = append(parts, fmt.Sprintf("result=%s", *f.Result))
	}
	if f.UserColor != nil {
		parts = append(parts, fmt.Sprintf("color=%s", *f.UserColor))
	}
	if f.TimeClass != nil {
		parts = append(parts, fmt.Sprintf("time=%s", *f.TimeClass))
	}
	if f.WeakPhase != nil {
		parts = append(parts, fmt.Sprintf("phase=%s", *f.WeakPhase))
	}
	if f.ECOPrefix != nil {
		parts = append(parts, fmt.Sprintf("eco=%s*", *f.ECOPrefix))
	}
	if f.OpeningName != nil {
		parts = append(parts, fmt.Sprintf("opening~%s", *f.OpeningName))
	}
	if f.MinBlunders != nil {
		parts = append(parts, fmt.Sprintf("blunders>=%d", *f.MinBlunders))
	}
	if f.MaxBlunders != nil {
		parts = append(parts, fmt.Sprintf("blunders<=%d", *f.MaxBlunders))
	}
	if f.MinMistakes != nil {
		parts = append(parts, fmt.Sprintf("mistakes>=%d", *f.MinMistakes))
	}
	if f.MinRating != nil {
		parts = append(parts, fmt.Sprintf("opponent>=%d", *f.MinRating))
	}
	if f.MaxRating != nil {
		parts = append(parts, fmt.Sprintf("opponent<=%d", *f.MaxRating))
	}
	if f.DateFrom != nil {
		parts = append(parts, fmt.Sprintf("from=%s", f.DateFrom.Format("2006-01-02")))
	}
	if f.DateTo != nil {
		parts = append(parts, fmt.Sprintf("to=%s", f.DateTo.Format("2006-01-02")))
	}

	if len(parts) == 0 {
		return "no filters"
	}
	return strings.Join(parts, ", ")
}

func StringPtr(s string) *string {
	return &s
}

func IntPtr(i int) *int {
	return &i
}

func TimePtr(t time.Time) *time.Time {
	return &t
}
