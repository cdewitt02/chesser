package api

import "github.com/chesser/internal/models"

func GetData(date models.YearMonth) (int, error) {
	return date.Year, nil
}
