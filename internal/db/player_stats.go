package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chesser/internal/models"
	"github.com/jackc/pgx/v5"
)

// SavePlayerStats saves or updates the player stats in the database.
func (db *DB) SavePlayerStats(ctx context.Context, stats *models.PlayerStats) error {
	// Serialize maps to JSON
	statsByColor, err := json.Marshal(stats.StatsByColor)
	if err != nil {
		return fmt.Errorf("failed to marshal stats_by_color: %w", err)
	}

	statsByTimeClass, err := json.Marshal(stats.StatsByTimeClass)
	if err != nil {
		return fmt.Errorf("failed to marshal stats_by_time_class: %w", err)
	}

	statsByOpening, err := json.Marshal(stats.StatsByOpening)
	if err != nil {
		return fmt.Errorf("failed to marshal stats_by_opening: %w", err)
	}

	statsByRatingBand, err := json.Marshal(stats.StatsByRatingBand)
	if err != nil {
		return fmt.Errorf("failed to marshal stats_by_rating_band: %w", err)
	}

	statsByTermination, err := json.Marshal(stats.StatsByTermination)
	if err != nil {
		return fmt.Errorf("failed to marshal stats_by_termination: %w", err)
	}

	last30Days, err := json.Marshal(stats.Last30Days)
	if err != nil {
		return fmt.Errorf("failed to marshal last_30_days: %w", err)
	}

	last90Days, err := json.Marshal(stats.Last90Days)
	if err != nil {
		return fmt.Errorf("failed to marshal last_90_days: %w", err)
	}

	query := `
		INSERT INTO player_stats (
			username, total_games, wins, losses, draws, avg_cpl,
			stats_by_color, stats_by_time_class, stats_by_opening,
			stats_by_rating_band, stats_by_termination,
			last_30_days, last_90_days, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (username) DO UPDATE SET
			total_games = EXCLUDED.total_games,
			wins = EXCLUDED.wins,
			losses = EXCLUDED.losses,
			draws = EXCLUDED.draws,
			avg_cpl = EXCLUDED.avg_cpl,
			stats_by_color = EXCLUDED.stats_by_color,
			stats_by_time_class = EXCLUDED.stats_by_time_class,
			stats_by_opening = EXCLUDED.stats_by_opening,
			stats_by_rating_band = EXCLUDED.stats_by_rating_band,
			stats_by_termination = EXCLUDED.stats_by_termination,
			last_30_days = EXCLUDED.last_30_days,
			last_90_days = EXCLUDED.last_90_days,
			updated_at = EXCLUDED.updated_at
	`

	_, err = db.pool.Exec(ctx, query,
		stats.Username,
		stats.TotalGames,
		stats.Wins,
		stats.Losses,
		stats.Draws,
		stats.AvgCPL,
		string(statsByColor),
		string(statsByTimeClass),
		string(statsByOpening),
		string(statsByRatingBand),
		string(statsByTermination),
		string(last30Days),
		string(last90Days),
		stats.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save player stats: %w", err)
	}

	return nil
}

// GetPlayerStats retrieves the player stats from the database.
func (db *DB) GetPlayerStats(ctx context.Context, username string) (*models.PlayerStats, error) {
	query := `
		SELECT username, total_games, wins, losses, draws, avg_cpl,
			   stats_by_color, stats_by_time_class, stats_by_opening,
			   stats_by_rating_band, stats_by_termination,
			   last_30_days, last_90_days, updated_at
		FROM player_stats
		WHERE username = $1
	`

	var stats models.PlayerStats
	var statsByColor, statsByTimeClass, statsByOpening, statsByRatingBand, statsByTermination string
	var last30Days, last90Days *string

	err := db.pool.QueryRow(ctx, query, username).Scan(
		&stats.Username,
		&stats.TotalGames,
		&stats.Wins,
		&stats.Losses,
		&stats.Draws,
		&stats.AvgCPL,
		&statsByColor,
		&statsByTimeClass,
		&statsByOpening,
		&statsByRatingBand,
		&statsByTermination,
		&last30Days,
		&last90Days,
		&stats.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get player stats: %w", err)
	}

	// Deserialize JSON fields
	if statsByColor != "" {
		if err := json.Unmarshal([]byte(statsByColor), &stats.StatsByColor); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stats_by_color: %w", err)
		}
	}
	if statsByTimeClass != "" {
		if err := json.Unmarshal([]byte(statsByTimeClass), &stats.StatsByTimeClass); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stats_by_time_class: %w", err)
		}
	}
	if statsByOpening != "" {
		if err := json.Unmarshal([]byte(statsByOpening), &stats.StatsByOpening); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stats_by_opening: %w", err)
		}
	}
	if statsByRatingBand != "" {
		if err := json.Unmarshal([]byte(statsByRatingBand), &stats.StatsByRatingBand); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stats_by_rating_band: %w", err)
		}
	}
	if statsByTermination != "" {
		if err := json.Unmarshal([]byte(statsByTermination), &stats.StatsByTermination); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stats_by_termination: %w", err)
		}
	}
	if last30Days != nil && *last30Days != "" {
		if err := json.Unmarshal([]byte(*last30Days), &stats.Last30Days); err != nil {
			return nil, fmt.Errorf("failed to unmarshal last_30_days: %w", err)
		}
	}
	if last90Days != nil && *last90Days != "" {
		if err := json.Unmarshal([]byte(*last90Days), &stats.Last90Days); err != nil {
			return nil, fmt.Errorf("failed to unmarshal last_90_days: %w", err)
		}
	}

	return &stats, nil
}

// ComputePlayerStats calculates aggregate statistics for a player from all their games.
// This performs a full recomputation - it queries all games and aggregates from scratch.
func (db *DB) ComputePlayerStats(ctx context.Context, username string) (*models.PlayerStats, error) {
	stats := &models.PlayerStats{
		Username:           username,
		StatsByColor:       make(map[string]*models.ColorStats),
		StatsByTimeClass:   make(map[string]*models.TimeClassStats),
		StatsByOpening:     make(map[string]*models.OpeningStats),
		StatsByRatingBand:  make(map[string]*models.RatingBandStats),
		StatsByTermination: make(map[string]int),
		UpdatedAt:          time.Now(),
	}

	// Initialize color stats
	stats.StatsByColor["white"] = &models.ColorStats{}
	stats.StatsByColor["black"] = &models.ColorStats{}

	// Query all games for this player
	query := `
		SELECT 
			white_username, black_username,
			white_rating, black_rating,
			result, termination_type, time_class,
			eco_code, eco_name,
			avg_cpl_white, avg_cpl_black
		FROM games
		WHERE white_username = $1 OR black_username = $1
	`

	rows, err := db.pool.Query(ctx, query, username)
	if err != nil {
		return nil, fmt.Errorf("failed to query games: %w", err)
	}
	defer rows.Close()

	var totalCPL float64
	var gamesWithCPL int

	for rows.Next() {
		var whiteUsername, blackUsername string
		var whiteRating, blackRating int
		var result string
		var terminationType, timeClass, ecoCode, ecoName *string
		var avgCPLWhite, avgCPLBlack float64

		err := rows.Scan(
			&whiteUsername, &blackUsername,
			&whiteRating, &blackRating,
			&result, &terminationType, &timeClass,
			&ecoCode, &ecoName,
			&avgCPLWhite, &avgCPLBlack,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan game row: %w", err)
		}

		// Determine player's color and opponent rating
		var playerColor string
		var opponentRating int
		var playerCPL float64

		if whiteUsername == username {
			playerColor = "white"
			opponentRating = blackRating
			playerCPL = avgCPLWhite
		} else {
			playerColor = "black"
			opponentRating = whiteRating
			playerCPL = avgCPLBlack
		}

		// Determine result from player's perspective
		var playerWon, playerLost bool
		switch result {
		case "white":
			playerWon = (playerColor == "white")
			playerLost = (playerColor == "black")
		case "black":
			playerWon = (playerColor == "black")
			playerLost = (playerColor == "white")
		}

		// Update overall stats
		stats.TotalGames++
		if playerWon {
			stats.Wins++
		} else if playerLost {
			stats.Losses++
		} else {
			stats.Draws++
		}

		if playerCPL > 0 {
			totalCPL += playerCPL
			gamesWithCPL++
		}

		// Update color stats
		colorStats := stats.StatsByColor[playerColor]
		colorStats.Games++
		if playerWon {
			colorStats.Wins++
		} else if playerLost {
			colorStats.Losses++
		} else {
			colorStats.Draws++
		}
		if playerCPL > 0 {
			// Running average: new_avg = old_avg + (new_value - old_avg) / n
			colorStats.AvgCPL += (playerCPL - colorStats.AvgCPL) / float64(colorStats.Games)
		}

		// Update time class stats
		if timeClass != nil && *timeClass != "" {
			tc := *timeClass
			if stats.StatsByTimeClass[tc] == nil {
				stats.StatsByTimeClass[tc] = &models.TimeClassStats{}
			}
			tcStats := stats.StatsByTimeClass[tc]
			tcStats.Games++
			if playerWon {
				tcStats.Wins++
			} else if playerLost {
				tcStats.Losses++
			} else {
				tcStats.Draws++
			}
			if playerCPL > 0 {
				tcStats.AvgCPL += (playerCPL - tcStats.AvgCPL) / float64(tcStats.Games)
			}
		}

		// Update opening stats
		if ecoCode != nil && *ecoCode != "" {
			eco := *ecoCode
			if stats.StatsByOpening[eco] == nil {
				openingName := ""
				if ecoName != nil {
					openingName = *ecoName
				}
				stats.StatsByOpening[eco] = &models.OpeningStats{
					ECOCode:     eco,
					OpeningName: openingName,
				}
			}
			openingStats := stats.StatsByOpening[eco]
			openingStats.Games++
			if playerWon {
				openingStats.Wins++
			} else if playerLost {
				openingStats.Losses++
			} else {
				openingStats.Draws++
			}
			if playerCPL > 0 {
				openingStats.AvgCPL += (playerCPL - openingStats.AvgCPL) / float64(openingStats.Games)
			}
		}

		// Update rating band stats
		ratingBand := models.RatingBand(opponentRating)
		if stats.StatsByRatingBand[ratingBand] == nil {
			stats.StatsByRatingBand[ratingBand] = &models.RatingBandStats{}
		}
		rbStats := stats.StatsByRatingBand[ratingBand]
		rbStats.Games++
		if playerWon {
			rbStats.Wins++
		} else if playerLost {
			rbStats.Losses++
		} else {
			rbStats.Draws++
		}
		if playerCPL > 0 {
			rbStats.AvgCPL += (playerCPL - rbStats.AvgCPL) / float64(rbStats.Games)
		}

		// Update termination stats
		if terminationType != nil && *terminationType != "" {
			stats.StatsByTermination[*terminationType]++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating game rows: %w", err)
	}

	// Calculate overall average CPL
	if gamesWithCPL > 0 {
		stats.AvgCPL = totalCPL / float64(gamesWithCPL)
	}

	// Calculate win rates for all dimensional stats
	for _, colorStats := range stats.StatsByColor {
		if colorStats.Games > 0 {
			colorStats.WinRate = float64(colorStats.Wins) / float64(colorStats.Games) * 100
		}
	}
	for _, tcStats := range stats.StatsByTimeClass {
		if tcStats.Games > 0 {
			tcStats.WinRate = float64(tcStats.Wins) / float64(tcStats.Games) * 100
		}
	}
	for _, openingStats := range stats.StatsByOpening {
		if openingStats.Games > 0 {
			openingStats.WinRate = float64(openingStats.Wins) / float64(openingStats.Games) * 100
		}
	}
	for _, rbStats := range stats.StatsByRatingBand {
		if rbStats.Games > 0 {
			rbStats.WinRate = float64(rbStats.Wins) / float64(rbStats.Games) * 100
		}
	}

	return stats, nil
}

// RefreshPlayerStats computes and saves the player stats in one operation.
func (db *DB) RefreshPlayerStats(ctx context.Context, username string) (*models.PlayerStats, error) {
	stats, err := db.ComputePlayerStats(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to compute player stats: %w", err)
	}

	// Compute period-based stats
	stats.Last30Days, err = db.ComputePeriodStats(ctx, username, 30)
	if err != nil {
		return nil, fmt.Errorf("failed to compute 30-day stats: %w", err)
	}

	stats.Last90Days, err = db.ComputePeriodStats(ctx, username, 90)
	if err != nil {
		return nil, fmt.Errorf("failed to compute 90-day stats: %w", err)
	}

	if err := db.SavePlayerStats(ctx, stats); err != nil {
		return nil, fmt.Errorf("failed to save player stats: %w", err)
	}

	return stats, nil
}

// ComputePeriodStats calculates stats for games played in the last N days.
func (db *DB) ComputePeriodStats(ctx context.Context, username string, days int) (*models.PeriodStats, error) {
	query := `
		SELECT 
			COUNT(*) as games,
			COUNT(*) FILTER (WHERE 
				(white_username = $1 AND result = 'white') OR 
				(black_username = $1 AND result = 'black')
			) as wins,
			COUNT(*) FILTER (WHERE 
				(white_username = $1 AND result = 'black') OR 
				(black_username = $1 AND result = 'white')
			) as losses,
			COUNT(*) FILTER (WHERE result = 'draw') as draws,
			COALESCE(AVG(
				CASE 
					WHEN white_username = $1 THEN avg_cpl_white
					ELSE avg_cpl_black
				END
			) FILTER (WHERE 
				CASE 
					WHEN white_username = $1 THEN avg_cpl_white
					ELSE avg_cpl_black
				END > 0
			), 0) as avg_cpl
		FROM games
		WHERE (white_username = $1 OR black_username = $1)
		  AND played_at >= NOW() - INTERVAL '1 day' * $2
	`

	var stats models.PeriodStats
	err := db.pool.QueryRow(ctx, query, username, days).Scan(
		&stats.Games,
		&stats.Wins,
		&stats.Losses,
		&stats.Draws,
		&stats.AvgCPL,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to compute period stats: %w", err)
	}

	if stats.Games > 0 {
		stats.WinRate = float64(stats.Wins) / float64(stats.Games) * 100
	}

	return &stats, nil
}
