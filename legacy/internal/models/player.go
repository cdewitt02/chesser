package models

type Player struct {
	UUID string `json:"uuid"`
	Username string `json:"username"`
	Rating uint16 `json:"rating"`
	Result string `json:"result"`
}