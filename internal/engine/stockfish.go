package engine

import (
	"strings"

	"github.com/cdewitt02/chesser/internal/models"
	"github.com/notnil/chess"
	"github.com/notnil/chess/uci"
)

func StartEngine() (*uci.Engine, error) {
	engine, err := uci.New("stockfish")
	if err != nil {
		return nil, err
	}
	return engine, nil
}

func StopEngine(engine *uci.Engine) error {
	return engine.Close()
}

func readPGN(pgnString string) (*chess.Game, error) {
	reader := strings.NewReader(pgnString)
	pgnOption, err := chess.PGN(reader)
	if err != nil {
		return nil, err
	}

	game := chess.NewGame(pgnOption)
	return game, nil
}

func AnalyzePosition(engine *uci.Engine, pos *chess.Position, depth int) (*models.MoveAnalysis, error) {
	cmdPos := uci.CmdPosition{Position: pos}
	cmdGo := uci.CmdGo{Depth: depth}

	if err := engine.Run(cmdPos, cmdGo); err != nil {
		return nil, err
	}

	searchResults := engine.SearchResults()

	return &models.MoveAnalysis{
		BestMove:   searchResults.BestMove,
		Evaluation: searchResults.Info.Score.CP,
		IsMate:     searchResults.Info.Score.Mate != 0,
		MateIn:     searchResults.Info.Score.Mate,
		PV:         searchResults.Info.PV,
		Depth:      searchResults.Info.Depth,
	}, nil
}

func getEvaluation(analysis *models.MoveAnalysis) int {
	if analysis.IsMate {
		if analysis.MateIn > 0 {
			return 10000 - analysis.MateIn
		} else {
			return -10000 - analysis.MateIn
		}
	}
	return analysis.Evaluation
}

func normalizeEval(eval int, moveIndex int) int {
	if moveIndex%2 == 1 { 
		return -eval
	}
	return eval
}

func classifyMove(cpl int) string {
	switch {
	case cpl <= 0:
		return "best"
	case cpl <= 50:
		return "good"
	case cpl <= 100:
		return "inaccuracy"
	case cpl <= 200:
		return "mistake"
	default:
		return "blunder"
	}
}

func AnalyzeGame(engine *uci.Engine, pgnString string, depth int) ([]*models.MoveAnalysis, error) {
	game, err := readPGN(pgnString)
	if err != nil {
		return nil, err
	}

	gamePositions := game.Positions()
	gameMoves := game.Moves()

	moveAnalyses := make([]*models.MoveAnalysis, len(gameMoves))

	for i := range len(gameMoves) {
		beforeAnalysis, err := AnalyzePosition(engine, gamePositions[i], depth)
		if err != nil {
			return nil, err
		}

		playedMove := gameMoves[i]
		afterPos := gamePositions[i+1]

		var actualEval int
		isTerminal := afterPos.Status() != chess.NoMethod

		if isTerminal {
	
			if afterPos.Status() == chess.Checkmate {
				if i%2 == 0 { // White delivered checkmate
					actualEval = 10000
				} else { // Black delivered checkmate
					actualEval = -10000
				}
			} else {
				// draw
				actualEval = 0
			}
		} else {
			afterAnalysis, err := AnalyzePosition(engine, afterPos, depth)
			if err != nil {
				return nil, err
			}
			actualEval = normalizeEval(getEvaluation(afterAnalysis), i+1)
		}

		bestEval := normalizeEval(getEvaluation(beforeAnalysis), i)

		var cpl int
		if i%2 == 0 { // White's move
			cpl = bestEval - actualEval
		} else { // Black's move
			cpl = actualEval - bestEval
		}

		if cpl < 0 {
			cpl = 0
		}

		classification := classifyMove(cpl)

		if beforeAnalysis.BestMove != nil && playedMove.String() == beforeAnalysis.BestMove.String() {
			classification = "best"
			cpl = 0
		}

		moveAnalyses[i] = &models.MoveAnalysis{
			BestMove:       beforeAnalysis.BestMove,
			Evaluation:     beforeAnalysis.Evaluation,
			IsMate:         beforeAnalysis.IsMate,
			MateIn:         beforeAnalysis.MateIn,
			PV:             beforeAnalysis.PV,
			Depth:          beforeAnalysis.Depth,
			PlayedMove:     playedMove,
			CentipawnLoss:  cpl,
			Classification: classification,
			FENBefore:      gamePositions[i].String(),
		}
	}

	return moveAnalyses, nil
}

/* FUTURE:
-concurrent analysis of multiple games
	-consider a worker pool or mutex protection.
-introduce heuristics / two pass analysis
*/
