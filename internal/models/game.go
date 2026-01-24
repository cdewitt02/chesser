package models

type Game struct {
    ID string
    URL string
    PGN string
    TimeControl string
    EndTime int64
    Rated bool
    Accuracies map[string]float64
    TCN string
    UUID string
    InitialSetup string
	FEN string
	TimeClass string
	ECO string
	White Player
	Black Player
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
