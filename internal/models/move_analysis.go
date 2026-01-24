package models


type MoveAnalysis struct {  
    Evaluation  int      // centipawns (+150 = white up 1.5 pawns)
    IsMate      bool     // is this a forced mate?
    MateIn      int      // if IsMate, how many moves?
    BestMove    string   // what Stockfish recommends
    PV          []string // principal variation (expected line)
    Depth       int      // how deep did stockfish search?
}