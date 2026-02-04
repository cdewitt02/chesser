package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chesser/internal/db"
	"github.com/chesser/internal/models"
	"github.com/chesser/internal/search"
)

// returns a pre-computed comparison string for win rates.
func formatWinRateComparison(rate, baseline float64) string {
	delta := rate - baseline
	if delta > 0.5 {
		return fmt.Sprintf("(%.1f%% ABOVE overall)", delta)
	} else if delta < -0.5 {
		return fmt.Sprintf("(%.1f%% BELOW overall)", -delta)
	}
	return "(≈ same as overall)"
}

// returns a pre-computed comparison string for centipawn loss.
func formatCPLComparison(cpl, baseline float64) string {
	delta := cpl - baseline
	if delta < -5 {
		return fmt.Sprintf("(%.1f BETTER than overall)", -delta)
	} else if delta > 5 {
		return fmt.Sprintf("(%.1f WORSE than overall)", delta)
	}
	return "(≈ same as overall)"
}

// routes questions to the appropriate handler based on query type.
type QueryRouter struct {
	db             *db.DB
	hybridSearcher *search.HybridSearcher
	promptBuilder  *PromptBuilder
	username       string
	numSimilar     int
}

// holds all the context needed to answer a question.
type QueryContext struct {
	QueryType         QueryType
	PlayerStats       *models.PlayerStats
	Games             []*db.SimilarGameResult
	Filters           []string
	MentionedOpenings []string // Opening names/ECO codes mentioned in the query
}

func NewQueryRouter(
	database *db.DB,
	searcher *search.HybridSearcher,
	promptBuilder *PromptBuilder,
	username string,
	numSimilar int,
) *QueryRouter {
	return &QueryRouter{
		db:             database,
		hybridSearcher: searcher,
		promptBuilder:  promptBuilder,
		username:       username,
		numSimilar:     numSimilar,
	}
}

// classifies the query and gathers appropriate context.
func (r *QueryRouter) Route(ctx context.Context, question string) (*QueryContext, error) {
	queryType := ClassifyQuery(question)

	qctx := &QueryContext{
		QueryType:         queryType,
		MentionedOpenings: ExtractMentionedOpenings(question),
	}

	stats, err := r.db.GetPlayerStats(ctx, r.username)
	if err != nil {
		return nil, fmt.Errorf("failed to get player stats: %w", err)
	}
	qctx.PlayerStats = stats

	switch queryType {
	case QueryTypeSpecificGames, QueryTypeRecommendation, QueryTypeTrend:
		games, filters, err := r.searchGames(ctx, question)
		if err != nil {
			return nil, fmt.Errorf("failed to search games: %w", err)
		}
		qctx.Games = games
		qctx.Filters = filters

	case QueryTypeAggregate, QueryTypeComparative:
		games, filters, err := r.searchGames(ctx, question)
		if err != nil {
			qctx.Games = nil
		} else {
			if len(games) > 3 {
				games = games[:3]
			}
			qctx.Games = games
			qctx.Filters = filters
		}
	}

	return qctx, nil
}

func (r *QueryRouter) searchGames(ctx context.Context, question string) ([]*db.SimilarGameResult, []string, error) {
	searchResult, err := r.hybridSearcher.Search(
		ctx,
		search.SearchQuery{
			Query: question,
			TopK:  r.numSimilar,
		},
		r.username,
	)
	if err != nil {
		return nil, nil, err
	}

	dbResults := make([]*db.SimilarGameResult, len(searchResult.Games))
	for i, g := range searchResult.Games {
		dbResults[i] = &db.SimilarGameResult{
			GameUUID:    g.GameUUID,
			SummaryText: g.SummaryText,
			Distance:    g.Distance,
			Game:        g.Game.(*db.GameRecord),
		}
	}

	return dbResults, searchResult.ExtractedFilters, nil
}

// creates the appropriate system prompt based on query context.
func (r *QueryRouter) BuildPrompt(qctx *QueryContext, detailLimit int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are a chess coach for %s. ", r.username))
	sb.WriteString("Your role is to provide insightful analysis based on the player's game history.\n\n")

	// Add player stats section if available
	if qctx.PlayerStats != nil && qctx.PlayerStats.TotalGames > 0 {
		r.writePlayerStats(&sb, qctx.PlayerStats, qctx.QueryType)

		// Add trend data for trend queries
		if qctx.QueryType == QueryTypeTrend {
			r.writeTrendStats(&sb, qctx.PlayerStats)
		}

		// Add specific opening stats if mentioned
		if len(qctx.MentionedOpenings) > 0 {
			r.writeMentionedOpeningStats(&sb, qctx.PlayerStats, qctx.MentionedOpenings)
		}
	}

	// Add game-specific context if available
	if len(qctx.Games) > 0 {
		r.writeGameContext(&sb, qctx.Games, detailLimit, qctx.QueryType)
	}

	// Add query-type specific instructions
	r.writeInstructions(&sb, qctx.QueryType)

	return sb.String()
}

func (r *QueryRouter) writePlayerStats(sb *strings.Builder, stats *models.PlayerStats, queryType QueryType) {
	sb.WriteString("PLAYER OVERVIEW (from all analyzed games):\n")
	sb.WriteString(fmt.Sprintf("- Total games analyzed: %d\n", stats.TotalGames))

	overallWinRate := 0.0
	if stats.TotalGames > 0 {
		overallWinRate = float64(stats.Wins) / float64(stats.TotalGames) * 100
	}
	sb.WriteString(fmt.Sprintf("- Overall record: %d wins, %d losses, %d draws (%.1f%% win rate)\n",
		stats.Wins, stats.Losses, stats.Draws, overallWinRate))
	sb.WriteString(fmt.Sprintf("- Average centipawn loss: %.1f\n", stats.AvgCPL))

	// Color stats with pre-computed comparisons
	if len(stats.StatsByColor) > 0 {
		sb.WriteString("\nPerformance by color:\n")
		for color, s := range stats.StatsByColor {
			winRateCmp := formatWinRateComparison(s.WinRate, overallWinRate)
			cplCmp := formatCPLComparison(s.AvgCPL, stats.AvgCPL)
			sb.WriteString(fmt.Sprintf("- As %s: %d games, %.1f%% win rate %s, %.1f avg CPL %s\n",
				color, s.Games, s.WinRate, winRateCmp, s.AvgCPL, cplCmp))
		}
		// Direct white vs black comparison if both exist
		if white, ok := stats.StatsByColor["white"]; ok {
			if black, ok := stats.StatsByColor["black"]; ok {
				r.writeColorComparison(sb, white, black)
			}
		}
	}

	// Time class stats with pre-computed comparisons
	if len(stats.StatsByTimeClass) > 0 {
		sb.WriteString("\nPerformance by time control:\n")
		for tc, s := range stats.StatsByTimeClass {
			winRateCmp := formatWinRateComparison(s.WinRate, overallWinRate)
			cplCmp := formatCPLComparison(s.AvgCPL, stats.AvgCPL)
			sb.WriteString(fmt.Sprintf("- %s: %d games, %.1f%% win rate %s, %.1f avg CPL %s\n",
				tc, s.Games, s.WinRate, winRateCmp, s.AvgCPL, cplCmp))
		}
		// Find best/worst time controls
		r.writeTimeControlInsights(sb, stats.StatsByTimeClass)
	}

	// For comparative/recommendation queries, show more detail
	if queryType == QueryTypeComparative || queryType == QueryTypeRecommendation {
		// Rating band stats with pre-computed comparisons
		if len(stats.StatsByRatingBand) > 0 {
			sb.WriteString("\nPerformance by opponent rating:\n")
			// Sort rating bands for consistent output
			bands := make([]string, 0, len(stats.StatsByRatingBand))
			for band := range stats.StatsByRatingBand {
				bands = append(bands, band)
			}
			sort.Strings(bands)
			for _, band := range bands {
				s := stats.StatsByRatingBand[band]
				winRateCmp := formatWinRateComparison(s.WinRate, overallWinRate)
				sb.WriteString(fmt.Sprintf("- vs %s: %d games, %.1f%% win rate %s\n",
					band, s.Games, s.WinRate, winRateCmp))
			}
			// Find best/worst rating bands (min 3 games)
			r.writeRatingBandInsights(sb, stats.StatsByRatingBand, overallWinRate)
		}

		// Top/bottom openings
		if len(stats.StatsByOpening) > 0 {
			r.writeOpeningStats(sb, stats.StatsByOpening, overallWinRate)
		}
	}

	// Termination stats (useful for questions about flagging, checkmates, etc)
	if len(stats.StatsByTermination) > 0 {
		sb.WriteString("\nGame endings:\n")
		for term, count := range stats.StatsByTermination {
			pct := float64(count) / float64(stats.TotalGames) * 100
			sb.WriteString(fmt.Sprintf("- %s: %d (%.1f%%)\n", term, count, pct))
		}
	}

	sb.WriteString("\n")
}

// adds a direct white vs black comparison.
func (r *QueryRouter) writeColorComparison(sb *strings.Builder, white, black *models.ColorStats) {
	winRateDelta := white.WinRate - black.WinRate
	cplDelta := white.AvgCPL - black.AvgCPL

	sb.WriteString("  → Direct comparison: ")
	if winRateDelta > 1 {
		sb.WriteString(fmt.Sprintf("White win rate is %.1f%% HIGHER than Black", winRateDelta))
	} else if winRateDelta < -1 {
		sb.WriteString(fmt.Sprintf("Black win rate is %.1f%% HIGHER than White", -winRateDelta))
	} else {
		sb.WriteString("Win rates approximately EQUAL between colors")
	}

	if cplDelta < -5 {
		sb.WriteString(fmt.Sprintf("; plays %.1f CPL BETTER as White\n", -cplDelta))
	} else if cplDelta > 5 {
		sb.WriteString(fmt.Sprintf("; plays %.1f CPL BETTER as Black\n", cplDelta))
	} else {
		sb.WriteString("; similar accuracy with both colors\n")
	}
}

// summarizes best/worst time controls.
func (r *QueryRouter) writeTimeControlInsights(sb *strings.Builder, timeClasses map[string]*models.TimeClassStats) {
	if len(timeClasses) < 2 {
		return
	}

	var bestTC, worstTC string
	var bestRate, worstRate float64 = -1, 101
	minGames := 3

	for tc, s := range timeClasses {
		if s.Games < minGames {
			continue
		}
		if s.WinRate > bestRate {
			bestRate = s.WinRate
			bestTC = tc
		}
		if s.WinRate < worstRate {
			worstRate = s.WinRate
			worstTC = tc
		}
	}

	if bestTC != "" && worstTC != "" && bestTC != worstTC {
		delta := bestRate - worstRate
		sb.WriteString(fmt.Sprintf("  → STRONGEST time control: %s (%.1f%% win rate)\n", bestTC, bestRate))
		sb.WriteString(fmt.Sprintf("  → WEAKEST time control: %s (%.1f%% win rate)\n", worstTC, worstRate))
		sb.WriteString(fmt.Sprintf("  → Difference: %.1f percentage points\n", delta))
	}
}

// summarizes performance patterns by opponent rating.
func (r *QueryRouter) writeRatingBandInsights(sb *strings.Builder, bands map[string]*models.RatingBandStats, overallWinRate float64) {
	if len(bands) < 2 {
		return
	}

	var bestBand, worstBand string
	var bestRate, worstRate float64 = -1, 101
	minGames := 3

	for band, s := range bands {
		if s.Games < minGames {
			continue
		}
		if s.WinRate > bestRate {
			bestRate = s.WinRate
			bestBand = band
		}
		if s.WinRate < worstRate {
			worstRate = s.WinRate
			worstBand = band
		}
	}

	if bestBand != "" && worstBand != "" {
		sb.WriteString(fmt.Sprintf("  → BEST performance vs: %s rated opponents (%.1f%% win rate)\n", bestBand, bestRate))
		sb.WriteString(fmt.Sprintf("  → WORST performance vs: %s rated opponents (%.1f%% win rate)\n", worstBand, worstRate))
	}
}

func (r *QueryRouter) writeOpeningStats(sb *strings.Builder, openings map[string]*models.OpeningStats, overallWinRate float64) {
	// Convert to slice for sorting
	type openingEntry struct {
		eco   string
		stats *models.OpeningStats
	}
	entries := make([]openingEntry, 0, len(openings))
	for eco, stats := range openings {
		entries = append(entries, openingEntry{eco, stats})
	}

	// Sort by games played
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stats.Games > entries[j].stats.Games
	})

	// Show top 5 most played with comparisons to overall
	sb.WriteString("\nMost played openings:\n")
	for i := 0; i < min(5, len(entries)); i++ {
		e := entries[i]
		name := e.stats.OpeningName
		if name == "" {
			name = e.eco
		}
		winRateCmp := formatWinRateComparison(e.stats.WinRate, overallWinRate)
		sb.WriteString(fmt.Sprintf("- %s (%s): %d games, %.1f%% win rate %s, %.1f CPL\n",
			name, e.eco, e.stats.Games, e.stats.WinRate, winRateCmp, e.stats.AvgCPL))
	}

	// Find best and worst openings (min 3 games)
	var best, worst *openingEntry
	for i := range entries {
		e := &entries[i]
		if e.stats.Games < 3 {
			continue
		}
		if best == nil || e.stats.WinRate > best.stats.WinRate {
			best = e
		}
		if worst == nil || e.stats.WinRate < worst.stats.WinRate {
			worst = e
		}
	}

	if best != nil && worst != nil {
		bestName := best.stats.OpeningName
		if bestName == "" {
			bestName = best.eco
		}
		worstName := worst.stats.OpeningName
		if worstName == "" {
			worstName = worst.eco
		}
		delta := best.stats.WinRate - worst.stats.WinRate
		sb.WriteString(fmt.Sprintf("\n  → STRONGEST opening (min 3 games): %s - %.1f%% win rate\n", bestName, best.stats.WinRate))
		sb.WriteString(fmt.Sprintf("  → WEAKEST opening (min 3 games): %s - %.1f%% win rate\n", worstName, worst.stats.WinRate))
		sb.WriteString(fmt.Sprintf("  → Spread: %.1f percentage points between best and worst\n", delta))
	}
}

func (r *QueryRouter) writeGameContext(sb *strings.Builder, games []*db.SimilarGameResult, detailLimit int, queryType QueryType) {
	numDetails := min(len(games), detailLimit)

	switch queryType {
	case QueryTypeAggregate, QueryTypeComparative:
		sb.WriteString(fmt.Sprintf("EXAMPLE GAMES (showing %d relevant games for context):\n", numDetails))
	default:
		sb.WriteString(fmt.Sprintf("RELEVANT GAMES (top %d matches):\n", numDetails))
	}

	for i := 0; i < numDetails; i++ {
		game := games[i]
		gameRecord := game.Game

		// Determine opponent username
		var opponent string
		if gameRecord.WhiteUsername == r.username {
			opponent = gameRecord.BlackUsername
		} else {
			opponent = gameRecord.WhiteUsername
		}

		// Format: "vs opponent_name: summary"
		summary := strings.ReplaceAll(game.SummaryText, "\n", " ")
		sb.WriteString(fmt.Sprintf("%d. [vs %s] %s\n", i+1, opponent, summary))
	}
	sb.WriteString("\n")
}

func (r *QueryRouter) writeInstructions(sb *strings.Builder, queryType QueryType) {
	sb.WriteString("INSTRUCTIONS:\n")
	sb.WriteString("- Interpret all questions in the context of chess and the player's games\n")

	switch queryType {
	case QueryTypeAggregate:
		sb.WriteString("- This is a STATISTICS question - use the PLAYER OVERVIEW data primarily\n")
		sb.WriteString("- Provide specific numbers and percentages from the stats\n")
		sb.WriteString("- Example games are for illustration only\n")

	case QueryTypeComparative:
		sb.WriteString("- This is a COMPARISON question - compare the relevant dimensions from PLAYER OVERVIEW\n")
		sb.WriteString("- Clearly state which option is better and by how much\n")
		sb.WriteString("- Use specific numbers to support comparisons\n")

	case QueryTypeSpecificGames:
		sb.WriteString("- Use RELEVANT GAMES for specific examples and patterns\n")
		sb.WriteString("- Reference PLAYER OVERVIEW for context on how typical these games are\n")
		sb.WriteString("- When citing specific games, use the actual opponent username shown in brackets [vs USERNAME] to identify the game\n")
		sb.WriteString("- Quote specific details from game summaries when relevant\n")

	case QueryTypeRecommendation:
		sb.WriteString("- Analyze PLAYER OVERVIEW to identify weaknesses and areas for improvement\n")
		sb.WriteString("- Use RELEVANT GAMES as concrete examples of the issues\n")
		sb.WriteString("- When citing specific games, use the actual opponent username shown in brackets [vs USERNAME] to identify the game\n")
		sb.WriteString("- Provide specific, actionable recommendations\n")
		sb.WriteString("- Prioritize the most impactful areas for improvement\n")

	case QueryTypeTrend:
		sb.WriteString("- This is a TREND question - focus on comparing RECENT PERFORMANCE to ALL-TIME stats\n")
		sb.WriteString("- Highlight specific improvements or regressions with numbers\n")
		sb.WriteString("- If recent data is limited, say so and explain what more data would show\n")
		sb.WriteString("- Use RELEVANT GAMES to illustrate specific changes in play\n")
		sb.WriteString("- When citing specific games, use the actual opponent username shown in brackets [vs USERNAME] to identify the game\n")
	}

	sb.WriteString("- Use proper chess notation and terminology\n")
	sb.WriteString("- If insufficient data exists for a question, say so clearly\n")
}

// adds period comparison data for trend queries.
func (r *QueryRouter) writeTrendStats(sb *strings.Builder, stats *models.PlayerStats) {
	sb.WriteString("RECENT PERFORMANCE (trend analysis):\n")

	// All-time baseline
	allTimeWinRate := 0.0
	if stats.TotalGames > 0 {
		allTimeWinRate = float64(stats.Wins) / float64(stats.TotalGames) * 100
	}
	sb.WriteString(fmt.Sprintf("All-time: %d games, %.1f%% win rate, %.1f avg CPL\n",
		stats.TotalGames, allTimeWinRate, stats.AvgCPL))

	// Last 30 days
	if stats.Last30Days != nil && stats.Last30Days.Games > 0 {
		sb.WriteString(fmt.Sprintf("Last 30 days: %d games, %.1f%% win rate, %.1f avg CPL\n",
			stats.Last30Days.Games, stats.Last30Days.WinRate, stats.Last30Days.AvgCPL))

		// Calculate deltas
		winRateDelta := stats.Last30Days.WinRate - allTimeWinRate
		cplDelta := stats.Last30Days.AvgCPL - stats.AvgCPL

		if winRateDelta > 0 {
			sb.WriteString(fmt.Sprintf("  → Win rate UP %.1f%% vs all-time\n", winRateDelta))
		} else if winRateDelta < 0 {
			sb.WriteString(fmt.Sprintf("  → Win rate DOWN %.1f%% vs all-time\n", -winRateDelta))
		}

		if cplDelta < 0 {
			sb.WriteString(fmt.Sprintf("  → CPL improved by %.1f (lower is better)\n", -cplDelta))
		} else if cplDelta > 0 {
			sb.WriteString(fmt.Sprintf("  → CPL worse by %.1f (lower is better)\n", cplDelta))
		}
	} else {
		sb.WriteString("Last 30 days: No games with recorded dates in this period\n")
	}

	// Last 90 days
	if stats.Last90Days != nil && stats.Last90Days.Games > 0 {
		sb.WriteString(fmt.Sprintf("Last 90 days: %d games, %.1f%% win rate, %.1f avg CPL\n",
			stats.Last90Days.Games, stats.Last90Days.WinRate, stats.Last90Days.AvgCPL))
	}

	sb.WriteString("\n")
}

// adds detailed stats for specifically mentioned openings.
func (r *QueryRouter) writeMentionedOpeningStats(sb *strings.Builder, stats *models.PlayerStats, mentioned []string) {
	if len(stats.StatsByOpening) == 0 {
		return
	}

	sb.WriteString("OPENING-SPECIFIC STATS (for openings mentioned in your question):\n")

	for _, opening := range mentioned {
		openingLower := strings.ToLower(opening)

		// Search for matching openings (by ECO code or name)
		for eco, oStats := range stats.StatsByOpening {
			ecoLower := strings.ToLower(eco)
			nameLower := strings.ToLower(oStats.OpeningName)

			if ecoLower == openingLower || strings.Contains(nameLower, openingLower) {
				name := oStats.OpeningName
				if name == "" {
					name = eco
				}
				sb.WriteString(fmt.Sprintf("\n%s (%s):\n", name, eco))
				sb.WriteString(fmt.Sprintf("  - Games: %d\n", oStats.Games))
				sb.WriteString(fmt.Sprintf("  - Record: %d wins, %d losses, %d draws\n",
					oStats.Wins, oStats.Losses, oStats.Draws))
				sb.WriteString(fmt.Sprintf("  - Win rate: %.1f%%\n", oStats.WinRate))
				sb.WriteString(fmt.Sprintf("  - Average CPL: %.1f\n", oStats.AvgCPL))
			}
		}
	}

	sb.WriteString("\n")
}
