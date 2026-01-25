package models

import (
	"regexp"
	"strings"
)

var pgnHeaderRegex = regexp.MustCompile(`\[(\w+) "([^"]*)"\]`)
var openingURLRegex = regexp.MustCompile(`/openings/([^/]+)`)
var variationSplitRegex = regexp.MustCompile(`(-\d+\.|\.\.\.)`) // matches "-4." or "..."

type Game struct {
	UUID         string             `json:"uuid"`
	URL          string             `json:"url"`
	PGN          string             `json:"pgn"`
	TimeControl  string             `json:"time_control"`
	EndTime      int64              `json:"end_time"`
	Rated        bool               `json:"rated"`
	Accuracies   map[string]float32 `json:"accuracies"`
	TCN          string             `json:"tcn"`
	InitialSetup string             `json:"initial_setup"`
	FEN          string             `json:"fen"`
	TimeClass    string             `json:"time_class"`
	ECO          string             `json:"eco"`
	White        Player             `json:"white"`
	Black        Player             `json:"black"`
}

func (g *Game) Winner() string {
	if g.White.Result == "win" {
		return g.White.Username
	} else if g.Black.Result == "win" {
		return g.Black.Username
	} else {
		return ""
	}
}

func (g *Game) GameResult() string {
	if g.White.Result == "win" {
		return "white"
	} else if g.Black.Result == "win" {
		return "black"
	}
	return "draw"
}

func (g *Game) pgnHeader(headerName string) string {
	matches := pgnHeaderRegex.FindAllStringSubmatch(g.PGN, -1)
	for _, match := range matches {
		if len(match) >= 3 && match[1] == headerName {
			return match[2]
		}
	}
	return ""
}

func (g *Game) ECOCode() string {
	return g.pgnHeader("ECO")
}

func (g *Game) OpeningName() string {
	// Extract opening name from the ECO URL field
	// e.g., "https://www.chess.com/openings/Pirc-Defense-Main-Line-Kholmov-System-4...Bg7"
	// → "Pirc Defense Main Line Kholmov System"

	matches := openingURLRegex.FindStringSubmatch(g.ECO)
	if len(matches) < 2 {
		return ""
	}

	name := matches[1]

	// Remove variation details (e.g., "-4...Bg7" or "-2.Nf3-d5")
	// Split at pattern like "-4." or "-2."
	parts := variationSplitRegex.Split(name, 2)
	name = parts[0]

	// Replace dashes with spaces
	name = strings.ReplaceAll(name, "-", " ")

	return name
}
