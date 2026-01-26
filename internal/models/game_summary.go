package models

type PhaseStats struct {
    Blunders      int
    Mistakes      int
    Inaccuracies  int
    TotalCPL      int     
    MoveCount     int     
}

type GameSummaryData struct {
    Result      string  
    PlayerColor string  
    TimeClass   string  // "bullet", "blitz", "rapid" etc.
    OpeningName string
    ECOCode     string
    
    TotalMoves  int
    
    Opening    PhaseStats
    Middlegame PhaseStats
    Endgame    PhaseStats
    
    BiggestSwing     int    // largest single eval change against player
    BiggestSwingMove int    // which move number
    WasWinning       bool   // did player have winning position at some point?
    WasLosing        bool   // did player have losing position at some point?
}