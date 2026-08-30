package models

import "time"

// PlayerStats holds pre-computed aggregate statistics for a player.
// Dimensional stats (by color, time class, etc.) are stored as maps
// that get serialized to JSON in the database.
type PlayerStats struct {
	Username   string
	TotalGames int
	Wins       int
	Losses     int
	Draws      int
	AvgCPL     float64

	// Dimensional breakdowns
	StatsByColor      map[string]*ColorStats
	StatsByTimeClass  map[string]*TimeClassStats
	StatsByOpening    map[string]*OpeningStats
	StatsByRatingBand map[string]*RatingBandStats

	// Termination stats
	StatsByTermination map[string]int

	// Period-based stats for trend analysis
	Last30Days *PeriodStats
	Last90Days *PeriodStats

	UpdatedAt time.Time
}

// PeriodStats holds stats for a specific time period.
type PeriodStats struct {
	Games   int     `json:"games"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	AvgCPL  float64 `json:"avg_cpl"`
	WinRate float64 `json:"win_rate"`
}

// ColorStats holds stats broken down by which color the player played.
type ColorStats struct {
	Games    int     `json:"games"`
	Wins     int     `json:"wins"`
	Losses   int     `json:"losses"`
	Draws    int     `json:"draws"`
	AvgCPL   float64 `json:"avg_cpl"`
	WinRate  float64 `json:"win_rate"` // computed: wins / games
}

// TimeClassStats holds stats broken down by time control.
type TimeClassStats struct {
	Games   int     `json:"games"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	AvgCPL  float64 `json:"avg_cpl"`
	WinRate float64 `json:"win_rate"`
}

// OpeningStats holds stats for a specific opening (ECO code).
type OpeningStats struct {
	ECOCode     string  `json:"eco_code"`
	OpeningName string  `json:"opening_name"`
	Games       int     `json:"games"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Draws       int     `json:"draws"`
	AvgCPL      float64 `json:"avg_cpl"`
	WinRate     float64 `json:"win_rate"`
}

// RatingBandStats holds stats broken down by opponent rating range.
type RatingBandStats struct {
	Games   int     `json:"games"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	AvgCPL  float64 `json:"avg_cpl"`
	WinRate float64 `json:"win_rate"`
}

// RatingBand returns the rating band string for a given rating.
// Bands: "<1000", "1000-1200", "1200-1400", "1400-1600", "1600-1800", "1800-2000", "2000+"
func RatingBand(rating int) string {
	switch {
	case rating < 1000:
		return "<1000"
	case rating < 1200:
		return "1000-1200"
	case rating < 1400:
		return "1200-1400"
	case rating < 1600:
		return "1400-1600"
	case rating < 1800:
		return "1600-1800"
	case rating < 2000:
		return "1800-2000"
	default:
		return "2000+"
	}
}
