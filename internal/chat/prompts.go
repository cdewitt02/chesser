package chat

import (
	"fmt"
	"strings"

	"github.com/chesser/internal/db"
)

type PromptBuilder struct {
	username string
}

func NewPromptBuilder(username string) *PromptBuilder {
	return &PromptBuilder{username: username}
}

type gameStats struct {
	totalGames      int
	wins            int
	losses          int
	draws           int
	asWhite         int
	asBlack         int
	openings        map[string]int
	weakestPhases   map[string]int
	patterns        map[string]int
}

func (p *PromptBuilder) BuildSystemPrompt(games []*db.SimilarGameResult, detailLimit int) string {
	var sb strings.Builder

	// Role definition
	sb.WriteString(fmt.Sprintf("You are a chess coach for %s. ", p.username))
	sb.WriteString("Your role is to provide insightful analysis based on the player's game history.\n\n")

	if len(games) == 0 {
		sb.WriteString("Note: No relevant games were found in the database.\n\n")
	} else {
		stats := aggregateGameStats(games)

		sb.WriteString(fmt.Sprintf("SUMMARY STATISTICS (based on %d relevant games):\n", stats.totalGames))
		sb.WriteString(fmt.Sprintf("- Win/Loss/Draw Record: %d wins, %d losses, %d draws\n", stats.wins, stats.losses, stats.draws))
		sb.WriteString(fmt.Sprintf("- Color Distribution: %d games as white, %d games as black\n", stats.asWhite, stats.asBlack))

		if len(stats.openings) > 0 {
			sb.WriteString("\nOPENINGS PLAYED (opening name and frequency):\n")
			for opening, count := range stats.openings {
				sb.WriteString(fmt.Sprintf("- %s: %d games\n", opening, count))
			}
		}

		if len(stats.weakestPhases) > 0 {
			sb.WriteString("\nWEAKEST GAME PHASES (phase where player made the most errors, independent of game result):\n")
			for phase, count := range stats.weakestPhases {
				sb.WriteString(fmt.Sprintf("- %s: %d games\n", phase, count))
			}
		}

		if len(stats.patterns) > 0 {
			sb.WriteString("\nGAME PATTERNS (how games unfolded, independent of weakest phase):\n")
			for pattern, count := range stats.patterns {
				sb.WriteString(fmt.Sprintf("- %s: %d games\n", pattern, count))
			}
		}

		// Show details for top N most relevant games
		numDetails := min(len(games), detailLimit)
		sb.WriteString(fmt.Sprintf("\nTOP %d MOST RELEVANT GAMES (of %d analyzed):\n", numDetails, len(games)))
		for i := 0; i < numDetails; i++ {
			sb.WriteString(fmt.Sprintf("%d. %s", i+1, strings.ReplaceAll(games[i].SummaryText, "\n", " ")))
			sb.WriteString("\n")
		}
	}

	// Response instructions
	sb.WriteString("\nINSTRUCTIONS:\n")

	sb.WriteString("- Interpret all questions in the context of chess and the player's games\n")
	sb.WriteString("- If a question seems unclear, assume it's about chess improvement, openings, tactics, or game patterns\n")

	sb.WriteString("- Use SUMMARY STATISTICS for overall trends and patterns (based on all analyzed games)\n")
	sb.WriteString("- Use TOP RELEVANT GAMES for specific examples and detailed analysis\n")
	sb.WriteString("- When discussing tendencies, cite the statistics; when giving examples, reference the detailed games\n")
	sb.WriteString("- If insufficient game data exists for a question, provide general chess principles relevant to the topic\n")

	sb.WriteString("- Identify patterns across multiple games rather than focusing on individual games\n")
	sb.WriteString("- Highlight both recurring weaknesses and consistent strengths\n")
	sb.WriteString("- Give specific, actionable recommendations\n")
	sb.WriteString("- Use proper chess notation and terminology\n")

	return sb.String()
}

func aggregateGameStats(games []*db.SimilarGameResult) gameStats {
	stats := gameStats{
		totalGames:    len(games),
		openings:      make(map[string]int),
		weakestPhases: make(map[string]int),
		patterns:      make(map[string]int),
	}

	for _, game := range games {
		summary := game.SummaryText

		if strings.HasPrefix(summary, "won") {
			stats.wins++
		} else if strings.HasPrefix(summary, "lost") {
			stats.losses++
		} else if strings.HasPrefix(summary, "drew") {
			stats.draws++
		}

		if strings.Contains(summary, "as white") {
			stats.asWhite++
		} else if strings.Contains(summary, "as black") {
			stats.asBlack++
		}

		lines := strings.Split(summary, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "Played ") {
				opening := strings.TrimPrefix(line, "Played ")
				opening = strings.TrimSuffix(opening, ".")
				stats.openings[opening]++
			}

			if strings.Contains(line, "was weakest") {
				// Extract just the phase name: "Opening was weakest." -> "Opening"
				phase := strings.TrimSuffix(line, ".")
				phase = strings.TrimSuffix(phase, " was weakest")
				stats.weakestPhases[phase]++
			}

			patterns := []string{
				"Came back from losing position",
				"Steady advantage throughout",
				"Converted a close game",
				"Threw a winning position",
				"Was outplayed",
				"Lost a close game",
				"Missed winning opportunity",
				"Saved a draw from worse position",
				"Wild game ended in draw",
				"Even game throughout",
			}
			for _, pattern := range patterns {
				if strings.Contains(line, pattern) {
					stats.patterns[pattern]++
				}
			}
		}
	}

	return stats
}

func (p *PromptBuilder) BuildFollowUpPrompt() string {
	return fmt.Sprintf(
		"Continue as the chess coach for %s. "+
			"Refer to the previously discussed game data and patterns when relevant. "+
			"Remember: only discuss chess-related topics, and base analysis on the player's actual game data. "+
			"Maintain the same helpful, insightful tone.",
		p.username,
	)
}

func (p *PromptBuilder) WrapUserQuestion(question string) string {
	return fmt.Sprintf(
		"[IMPORTANT: You are a chess coach for %s. "+
			"Only discuss chess and this player's games. "+
			"Base all analysis on the game data provided in the system message. "+
			"If this question is not about chess, politely decline and redirect to chess topics.]\n\n"+
			"User question: %s",
		p.username,
		question,
	)
}
