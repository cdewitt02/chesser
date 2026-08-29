package summary

import (
	"fmt"

	"github.com/cdewitt02/chesser/internal/models"
)

const (
	OpeningEnd    = 10 // moves 1-10
	MiddlegameEnd = 25 // moves 11-25
	// 26+ is endgame
)

const (
	WinningThreshold = 200 // +2.00 pawns = winning
	LosingThreshold  = -200
)

// converts Stockfish's evaluation to the player's perspective.
func getPlayerEval(move *models.MoveAnalysis, playerColor string) int {
	var eval int
	if move.IsMate {
		if move.MateIn > 0 {
			eval = 10000 - move.MateIn
		} else {
			eval = -10000 - move.MateIn
		}
	} else {
		eval = move.Evaluation
	}

	if playerColor == "black" {
		eval = -eval
	}
	return eval
}

func ExtractSummaryData(
	game *models.Game,
	moves []*models.MoveAnalysis,
	username string,
) *models.GameSummaryData {

	var playerColor string
	if username == game.White.Username {
		playerColor = "white"
	} else {
		playerColor = "black"
	}

	winner := game.GameResult()
	var result string
	if winner == playerColor {
		result = "won"
	} else if winner == "" {
		result = "drew"
	} else {
		result = "lost"
	}

	var opponentRating int
	if playerColor == "white" {
		opponentRating = int(game.Black.Rating)
	} else {
		opponentRating = int(game.White.Rating)
	}

	var openingStats models.PhaseStats
	var middlegameStats models.PhaseStats
	var endgameStats models.PhaseStats
	var biggestSwing int
	var biggestSwingMove int
	var wasWinning bool
	var wasLosing bool

	for i, move := range moves {
		// Track position evaluation at every move (both players' moves)
		// to detect if player was ever winning or losing
		playerEval := getPlayerEval(move, playerColor)
		if playerEval > WinningThreshold {
			wasWinning = true
		}
		if playerEval < LosingThreshold {
			wasLosing = true
		}

		// Only track CPL and phase stats for the player's own moves
		isPlayerMove := (playerColor == "white" && i%2 == 0) ||
			(playerColor == "black" && i%2 == 1)

		if !isPlayerMove {
			continue
		}

		// Track biggest single-move blunder by the player
		if move.CentipawnLoss > biggestSwing {
			biggestSwing = move.CentipawnLoss
			biggestSwingMove = i + 1
		}

		// Categorize move into game phase and accumulate stats
		var phaseStats *models.PhaseStats
		if i < OpeningEnd {
			phaseStats = &openingStats
		} else if i < MiddlegameEnd {
			phaseStats = &middlegameStats
		} else {
			phaseStats = &endgameStats
		}

		phaseStats.MoveCount++
		phaseStats.TotalCPL += move.CentipawnLoss
		switch move.Classification {
		case "blunder":
			phaseStats.Blunders++
		case "mistake":
			phaseStats.Mistakes++
		case "inaccuracy":
			phaseStats.Inaccuracies++
		}
	}

	var gameSummaryData models.GameSummaryData
	gameSummaryData.Result = result
	gameSummaryData.PlayerColor = playerColor
	gameSummaryData.TimeClass = game.TimeClass
	gameSummaryData.OpeningName = game.OpeningName()
	gameSummaryData.ECOCode = game.ECOCode()
	gameSummaryData.TotalMoves = len(moves)
	gameSummaryData.Opening = openingStats
	gameSummaryData.Middlegame = middlegameStats
	gameSummaryData.Endgame = endgameStats
	gameSummaryData.BiggestSwing = biggestSwing
	gameSummaryData.BiggestSwingMove = biggestSwingMove
	gameSummaryData.WasWinning = wasWinning
	gameSummaryData.WasLosing = wasLosing
	gameSummaryData.TerminationType = game.TerminationType()
	gameSummaryData.OpponentRating = opponentRating
	
	return &gameSummaryData
}

func GenerateSummary(data *models.GameSummaryData) string {
	var summary string
	summary += fmt.Sprintf("%s as %s in %s.\n", data.Result, data.PlayerColor, data.TimeClass)
	summary += fmt.Sprintf("Played %s.\n", data.OpeningName)
	totalBlunders := data.Opening.Blunders + data.Middlegame.Blunders + data.Endgame.Blunders
	totalMistakes := data.Opening.Mistakes + data.Middlegame.Mistakes + data.Endgame.Mistakes
	totalInaccuracies := data.Opening.Inaccuracies + data.Middlegame.Inaccuracies + data.Endgame.Inaccuracies
	summary += fmt.Sprintf("%s performance with %d blunders, %d mistakes, and %d inaccuracies.\n", data.PlayerColor, totalBlunders, totalMistakes, totalInaccuracies)
	summary += fmt.Sprintf("%s.\n", weakestPhase(data.Opening, data.Middlegame, data.Endgame))
	summary += fmt.Sprintf("%s.\n", detectPattern(data))
	summary += fmt.Sprintf("Game length: %s.\n", classifyGameLength(data.TotalMoves))
	summary += fmt.Sprintf("Termination type: %s.\n", data.TerminationType)
	summary += fmt.Sprintf("Opponent rating: %d.\n", data.OpponentRating)

	return summary
}

func classifyGameLength(totalMoves int) string {
	if totalMoves < 20 {
		return "Short game"
	} else if totalMoves < 40 {
		return "Medium length game"
	} else {
		return "Long game"
	}
}

func weakestPhase(opening, middlegame, endgame models.PhaseStats) string {

	openingAvgCPL := 0.0
	middlegameAvgCPL := 0.0
	endgameAvgCPL := 0.0
	if opening.MoveCount > 0 {
		openingAvgCPL = float64(opening.TotalCPL) / float64(opening.MoveCount)
	}
	if middlegame.MoveCount > 0 {
		middlegameAvgCPL = float64(middlegame.TotalCPL) / float64(middlegame.MoveCount)
	}
	if endgame.MoveCount > 0 {
		endgameAvgCPL = float64(endgame.TotalCPL) / float64(endgame.MoveCount)
	}
	if openingAvgCPL > middlegameAvgCPL && openingAvgCPL > endgameAvgCPL {
		return "Opening was weakest"
	} else if middlegameAvgCPL > openingAvgCPL && middlegameAvgCPL > endgameAvgCPL {
		return "Middlegame was weakest"
	} else {
		return "Endgame was weakest"
	}
}

func detectPattern(data *models.GameSummaryData) string {
	// Logic matrix based on whether player was ever winning/losing:
	// WasWinning = player had eval > +200 at some point
	// WasLosing = player had eval < -200 at some point

	switch data.Result {
	case "won":
		if data.WasLosing {
			return "Came back from losing position"
		} else if data.WasWinning {
			return "Steady advantage throughout"
		}
		return "Converted a close game"

	case "lost":
		if data.WasWinning {
			return "Threw a winning position"
		} else if data.WasLosing {
			return "Was outplayed"
		}
		return "Lost a close game"

	case "drew":
		if data.WasWinning && !data.WasLosing {
			return "Missed winning opportunity"
		} else if data.WasLosing && !data.WasWinning {
			return "Saved a draw from worse position"
		} else if data.WasWinning && data.WasLosing {
			return "Wild game ended in draw"
		}
		return "Even game throughout"
	}

	return "Unknown pattern"
}
