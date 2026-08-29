package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"github.com/cdewitt02/chesser/internal/models"
)

type Response struct {
	Games []models.Game `json:"games"`
}

func GetData(date models.YearMonth, username string) ([]models.Game, error) {
	url := fmt.Sprintf("https://api.chess.com/pub/player/%s/games/%d/%s", username, date.Year, date.Month)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}	

	var response Response
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}

	return response.Games, nil
}

