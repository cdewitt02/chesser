package models


type GameAnalysis struct {
    AverageCentipawnLoss float64
    Accuracy             float64  // Chess.com already gives you this! (sometimes)
    Blunders             int      // moves where eval dropped significantly
    Mistakes             int
    Inaccuracies         int
    BestMovePercentage   float64
}