package models

import "github.com/notnil/chess"


type MoveAnalysis struct {  
    Evaluation  int      // centipawns (+150 = white up 1.5 pawns)
    IsMate      bool     // is this a forced mate?
    MateIn      int      // if IsMate, how many moves?
    BestMove    *chess.Move   // what Stockfish recommends
    PV          []*chess.Move // principal variation (expected line)
    Depth       int      // how deep did stockfish search?
    PlayedMove  *chess.Move // the move that was played
    CentipawnLoss int // the centipawn loss of the played move
    Classification string // the classification of the played move
}