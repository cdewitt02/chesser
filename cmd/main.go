package main

import (
	"fmt"
	"github.com/chesser/internal/api"
	"github.com/chesser/internal/models"
)

func main() {
	fmt.Println(api.GetData(models.YearMonth{Year: 2025, Month: "01"}))
}