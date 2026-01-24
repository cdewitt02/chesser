package models

type Move struct {
	Notation string
	Number int
	Time string
	Analysis *MoveAnalysis
}
