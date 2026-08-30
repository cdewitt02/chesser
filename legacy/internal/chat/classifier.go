package chat

import (
	"regexp"
	"strings"
)
type QueryType int

const (
	// QueryTypeAggregate - Questions about overall statistics
	// Examples: "What's my average CPL?", "How many games have I played?"
	QueryTypeAggregate QueryType = iota

	// QueryTypeComparative - Questions comparing dimensions
	// Examples: "Am I better with white or black?", "What's my best time control?"
	QueryTypeComparative

	// QueryTypeSpecificGames - Questions about particular games or examples
	// Examples: "Show me games where I threw", "What openings do I lose with?"
	QueryTypeSpecificGames

	// QueryTypeRecommendation - Questions asking for advice or next steps
	// Examples: "What should I study?", "How can I improve?"
	QueryTypeRecommendation

	// QueryTypeTrend - Questions about changes over time
	// Examples: "Have I improved?", "Am I getting better?"
	QueryTypeTrend
)

func (q QueryType) String() string {
	switch q {
	case QueryTypeAggregate:
		return "aggregate"
	case QueryTypeComparative:
		return "comparative"
	case QueryTypeSpecificGames:
		return "specific_games"
	case QueryTypeRecommendation:
		return "recommendation"
	case QueryTypeTrend:
		return "trend"
	default:
		return "unknown"
	}
}

// determines the type of question being asked.
func ClassifyQuery(question string) QueryType {
	q := strings.ToLower(question)

	if isRecommendationQuery(q) {
		return QueryTypeRecommendation
	}

	if isTrendQuery(q) {
		return QueryTypeTrend
	}

	if isComparativeQuery(q) {
		return QueryTypeComparative
	}

	if isAggregateQuery(q) {
		return QueryTypeAggregate
	}

	return QueryTypeSpecificGames
}

// checks for questions about improvement over time.
func isTrendQuery(q string) bool {
	trendKeywords := []string{
		"improved",
		"improving",
		"getting better",
		"getting worse",
		"progress",
		"trend",
		"over time",
		"recently",
		"lately",
		"last month",
		"last week",
		"past few",
		"compared to before",
		"used to",
		"changed",
		"changing",
	}

	for _, keyword := range trendKeywords {
		if strings.Contains(q, keyword) {
			return true
		}
	}
	return false
}

// checks for advice/improvement questions.
func isRecommendationQuery(q string) bool {
	recommendationKeywords := []string{
		"should i study",
		"should i focus",
		"should i learn",
		"should i practice",
		"how can i improve",
		"how do i improve",
		"how to improve",
		"what to study",
		"what to practice",
		"what to focus",
		"recommend",
		"suggestion",
		"advice",
		"tips for",
		"help me get better",
		"what's my biggest weakness",
		"what are my weaknesses",
		"where should i",
		"what should i work on",
	}

	for _, keyword := range recommendationKeywords {
		if strings.Contains(q, keyword) {
			return true
		}
	}
	return false
}

// checks for comparison questions.
func isComparativeQuery(q string) bool {
	comparativeKeywords := []string{
		"better with white or black",
		"better as white or black",
		"white or black",
		"better at bullet or blitz",
		"best time control",
		"worst time control",
		"best opening",
		"worst opening",
		"weakest opening",
		"strongest opening",
		"compare",
		"versus",
		" vs ",
		"difference between",
		"which is better",
		"which is worse",
		"do i perform better",
		"am i better",
		"am i worse",
		"higher rated or lower rated",
		"against higher",
		"against lower",
	}

	for _, keyword := range comparativeKeywords {
		if strings.Contains(q, keyword) {
			return true
		}
	}
	return false
}

// checks for statistical/aggregate questions.
func isAggregateQuery(q string) bool {
	aggregateKeywords := []string{
		"average",
		"total games",
		"how many games",
		"win rate",
		"winning percentage",
		"win percentage",
		"loss rate",
		"draw rate",
		"how often do i",
		"what percentage",
		"what's my record",
		"what is my record",
		"overall",
		"statistics",
		"stats",
		"centipawn loss",
		"cpl",
		"accuracy",
		"how many wins",
		"how many losses",
		"how many draws",
		"most common",
		"most played",
		"how frequently",
	}

	for _, keyword := range aggregateKeywords {
		if strings.Contains(q, keyword) {
			return true
		}
	}
	return false
}

// Common chess openings for detection
var openingPatterns = []string{
	"sicilian",
	"italian",
	"spanish",
	"ruy lopez",
	"french",
	"caro-kann",
	"caro kann",
	"scandinavian",
	"pirc",
	"modern",
	"king's indian",
	"kings indian",
	"queen's gambit",
	"queens gambit",
	"london",
	"english",
	"catalan",
	"nimzo",
	"grunfeld",
	"dutch",
	"scotch",
	"vienna",
	"petroff",
	"philidor",
	"alekhine",
	"benoni",
	"slav",
	"budapest",
	"benko",
	"trompowsky",
	"bird",
	"ponziani",
	"evan's gambit",
	"evans gambit",
	"king's gambit",
	"kings gambit",
}

var ecoPattern = regexp.MustCompile(`\b[A-E]\d{2}\b`)

// finds any chess openings mentioned in the query.
func ExtractMentionedOpenings(question string) []string {
	q := strings.ToLower(question)
	var found []string

	for _, opening := range openingPatterns {
		if strings.Contains(q, opening) {
			found = append(found, opening)
		}
	}

	ecoMatches := ecoPattern.FindAllString(strings.ToUpper(question), -1)
	found = append(found, ecoMatches...)

	return found
}
