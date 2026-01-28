package search

import (
	"regexp"
	"strings"
	"time"
)

// extracts structured filters from natural language queries
// uses pattern matching to identify filterable criteria while preserving
// the semantic remainder for embedding search.
type QueryParser struct {
	openingPatterns map[string]OpeningPattern
	resultKeywords  map[string]string
	colorKeywords   map[string]string
	timeClassKeys   map[string]string
	phaseKeywords   map[string]string
	timePatterns    []TimePattern
}

type OpeningPattern struct {
	ECOPrefix   string
	OpeningName string
}

type TimePattern struct {
	Pattern  *regexp.Regexp
	Duration time.Duration
	Relative bool
}

type ParseResult struct {
	Filters          *GameFilters
	SemanticQuery    string  
	ExtractedFilters []string
}

func NewQueryParser() *QueryParser {
	p := &QueryParser{
		openingPatterns: make(map[string]OpeningPattern),
		resultKeywords:  make(map[string]string),
		colorKeywords:   make(map[string]string),
		timeClassKeys:   make(map[string]string),
		phaseKeywords:   make(map[string]string),
	}

	p.openingPatterns = map[string]OpeningPattern{
		// Sicilian variations (B20-B99)
		"sicilian":       {ECOPrefix: "B", OpeningName: "Sicilian"},
		"sicilian najdorf": {ECOPrefix: "B9", OpeningName: "Sicilian"},
		"najdorf":        {ECOPrefix: "B9", OpeningName: "Najdorf"},
		"dragon":         {ECOPrefix: "B7", OpeningName: "Dragon"},
		"sicilian dragon": {ECOPrefix: "B7", OpeningName: "Dragon"},

		// King's Indian (E60-E99)
		"king's indian":  {ECOPrefix: "E", OpeningName: "King's Indian"},
		"kings indian":   {ECOPrefix: "E", OpeningName: "King's Indian"},
		"kid":            {ECOPrefix: "E", OpeningName: "King's Indian"},

		// Queen's Gambit (D06-D69)
		"queen's gambit": {ECOPrefix: "D", OpeningName: "Queen's Gambit"},
		"queens gambit":  {ECOPrefix: "D", OpeningName: "Queen's Gambit"},
		"qgd":            {ECOPrefix: "D", OpeningName: "Queen's Gambit"},
		"qga":            {ECOPrefix: "D", OpeningName: "Queen's Gambit Accepted"},

		// Ruy Lopez (C60-C99)
		"ruy lopez":      {ECOPrefix: "C6", OpeningName: "Ruy Lopez"},
		"spanish":        {ECOPrefix: "C6", OpeningName: "Ruy Lopez"},
		"spanish game":   {ECOPrefix: "C6", OpeningName: "Ruy Lopez"},

		// French Defense (C00-C19)
		"french":         {ECOPrefix: "C0", OpeningName: "French"},
		"french defense": {ECOPrefix: "C0", OpeningName: "French"},
		"french defence": {ECOPrefix: "C0", OpeningName: "French"},

		// Caro-Kann (B10-B19)
		"caro-kann":      {ECOPrefix: "B1", OpeningName: "Caro-Kann"},
		"caro kann":      {ECOPrefix: "B1", OpeningName: "Caro-Kann"},

		// Italian Game (C50-C59)
		"italian":        {ECOPrefix: "C5", OpeningName: "Italian"},
		"italian game":   {ECOPrefix: "C5", OpeningName: "Italian"},
		"giuoco piano":   {ECOPrefix: "C5", OpeningName: "Italian"},

		// English Opening (A10-A39)
		"english":        {ECOPrefix: "A1", OpeningName: "English"},
		"english opening": {ECOPrefix: "A1", OpeningName: "English"},

		// London System (D00)
		"london":         {ECOPrefix: "D00", OpeningName: "London"},
		"london system":  {ECOPrefix: "D00", OpeningName: "London"},

		// Scandinavian (B01)
		"scandinavian":   {ECOPrefix: "B01", OpeningName: "Scandinavian"},

		// Pirc Defense (B07-B09)
		"pirc":           {ECOPrefix: "B0", OpeningName: "Pirc"},
		"pirc defense":   {ECOPrefix: "B0", OpeningName: "Pirc"},

		// Dutch Defense (A80-A99)
		"dutch":          {ECOPrefix: "A8", OpeningName: "Dutch"},
		"dutch defense":  {ECOPrefix: "A8", OpeningName: "Dutch"},

		// Nimzo-Indian (E20-E59)
		"nimzo-indian":   {ECOPrefix: "E", OpeningName: "Nimzo-Indian"},
		"nimzo indian":   {ECOPrefix: "E", OpeningName: "Nimzo-Indian"},
		"nimzo":          {ECOPrefix: "E", OpeningName: "Nimzo-Indian"},

		// Grunfeld (D70-D99)
		"grunfeld":       {ECOPrefix: "D7", OpeningName: "Grunfeld"},
		"grünfeld":       {ECOPrefix: "D7", OpeningName: "Grunfeld"},
	}

	p.resultKeywords = map[string]string{
		// Win
		"win":     "win",
		"wins":    "win",
		"won":     "win",
		"winning": "win",
		"victory": "win",
		"victories": "win",

		// Loss
		"loss":   "loss",
		"losses": "loss",
		"lost":   "loss",
		"losing": "loss",
		"defeat": "loss",
		"defeats": "loss",

		// Draw
		"draw":  "draw",
		"draws": "draw",
		"drew":  "draw",
		"tie":   "draw",
		"ties":  "draw",
	}

	// Color keywords
	p.colorKeywords = map[string]string{
		"as white":    "white",
		"with white":  "white",
		"playing white": "white",
		"white pieces": "white",
		"as black":    "black",
		"with black":  "black",
		"playing black": "black",
		"black pieces": "black",
	}

	// Time class keywords
	p.timeClassKeys = map[string]string{
		"bullet":    "bullet",
		"blitz":     "blitz",
		"rapid":     "rapid",
		"classical": "classical",
		"daily":     "daily",
	}

	// Phase keywords
	p.phaseKeywords = map[string]string{
		"opening":    "opening",
		"openings":   "opening",
		"middlegame": "middlegame",
		"middle game": "middlegame",
		"midgame":    "middlegame",
		"endgame":    "endgame",
		"end game":   "endgame",
		"endings":    "endgame",
	}

	// Time patterns (for relative date filtering)
	p.timePatterns = []TimePattern{
		{Pattern: regexp.MustCompile(`(?i)\b(today|this day)\b`), Duration: 24 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(yesterday)\b`), Duration: 48 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(this week|past week|last 7 days)\b`), Duration: 7 * 24 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(last week)\b`), Duration: 14 * 24 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(this month|past month|last 30 days)\b`), Duration: 30 * 24 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(last month)\b`), Duration: 60 * 24 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(this year|past year|last 365 days)\b`), Duration: 365 * 24 * time.Hour, Relative: true},
		{Pattern: regexp.MustCompile(`(?i)\b(recent|recently|lately)\b`), Duration: 14 * 24 * time.Hour, Relative: true},
	}

	return p
}

// extracts filters from a natural language query.
func (p *QueryParser) Parse(query string, username string) *ParseResult {
	filters := &GameFilters{
		Username: username,
	}
	var extracted []string
	remainingQuery := query
	lowerQuery := strings.ToLower(query)

	// extract opening patterns
	type openingMatch struct {
		keyword string
		pattern OpeningPattern
	}
	var openingMatches []openingMatch
	for keyword, pattern := range p.openingPatterns {
		if strings.Contains(lowerQuery, keyword) {
			openingMatches = append(openingMatches, openingMatch{keyword, pattern})
		}
	}
	// find the longest match
	if len(openingMatches) > 0 {
		longest := openingMatches[0]
		for _, m := range openingMatches {
			if len(m.keyword) > len(longest.keyword) {
				longest = m
			}
		}
		filters.ECOPrefix = StringPtr(longest.pattern.ECOPrefix)
		filters.OpeningName = StringPtr(longest.pattern.OpeningName)
		extracted = append(extracted, "opening: "+longest.pattern.OpeningName)
		remainingQuery = removeKeyword(remainingQuery, longest.keyword)
	}

	// extract color keywords
	for keyword, color := range p.colorKeywords {
		if strings.Contains(lowerQuery, keyword) {
			filters.UserColor = StringPtr(color)
			extracted = append(extracted, "color: "+color)
			remainingQuery = removeKeyword(remainingQuery, keyword)
			break
		}
	}

	// extract result keywords
	for keyword, result := range p.resultKeywords {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
		if pattern.MatchString(lowerQuery) {
			filters.Result = StringPtr(result)
			extracted = append(extracted, "result: "+result)
			remainingQuery = pattern.ReplaceAllString(remainingQuery, "")
			break
		}
	}

	// extract time class keywords
	for keyword, timeClass := range p.timeClassKeys {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
		if pattern.MatchString(lowerQuery) {
			filters.TimeClass = StringPtr(timeClass)
			extracted = append(extracted, "time control: "+timeClass)
			remainingQuery = pattern.ReplaceAllString(remainingQuery, "")
			break
		}
	}

	// extract phase keywords
	for keyword, phase := range p.phaseKeywords {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
		if pattern.MatchString(lowerQuery) {
			filters.WeakPhase = StringPtr(phase)
			extracted = append(extracted, "phase: "+phase)
			remainingQuery = pattern.ReplaceAllString(remainingQuery, "")
			break
		}
	}

	// extract time patterns
	now := time.Now()
	for _, tp := range p.timePatterns {
		if tp.Pattern.MatchString(lowerQuery) {
			dateFrom := now.Add(-tp.Duration)
			filters.DateFrom = TimePtr(dateFrom)
			extracted = append(extracted, "date: "+tp.Pattern.String())
			remainingQuery = tp.Pattern.ReplaceAllString(remainingQuery, "")
			break
		}
	}

	// extract blunder-related patterns
	blunderPatterns := []struct {
		pattern *regexp.Regexp
		extract func(matches []string) (*int, *int)
	}{
		{
			pattern: regexp.MustCompile(`(?i)\bno blunders?\b`),
			extract: func(matches []string) (*int, *int) {
				return nil, IntPtr(0)
			},
		},
		{
			pattern: regexp.MustCompile(`(?i)\bwithout blunders?\b`),
			extract: func(matches []string) (*int, *int) {
				return nil, IntPtr(0)
			},
		},
		{
			pattern: regexp.MustCompile(`(?i)\bdidn'?t blunder\b`),
			extract: func(matches []string) (*int, *int) {
				return nil, IntPtr(0)
			},
		},
		{
			pattern: regexp.MustCompile(`(?i)\b(blunder|blunders|blundered)\b`),
			extract: func(matches []string) (*int, *int) {
				return IntPtr(1), nil
			},
		},
	}

	for _, bp := range blunderPatterns {
		if bp.pattern.MatchString(query) {
			min, max := bp.extract(bp.pattern.FindStringSubmatch(query))
			if min != nil {
				filters.MinBlunders = min
				extracted = append(extracted, "min blunders: "+string(rune('0'+*min)))
			}
			if max != nil {
				filters.MaxBlunders = max
				extracted = append(extracted, "max blunders: "+string(rune('0'+*max)))
			}
			remainingQuery = bp.pattern.ReplaceAllString(remainingQuery, "")
			break
		}
	}

	// clean up the remaining query
	remainingQuery = cleanQuery(remainingQuery)

	return &ParseResult{
		Filters:          filters,
		SemanticQuery:    remainingQuery,
		ExtractedFilters: extracted,
	}
}

// removeKeyword removes a keyword from the query (case-insensitive).
func removeKeyword(query, keyword string) string {
	pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(keyword))
	return pattern.ReplaceAllString(query, "")
}

// cleanQuery removes extra whitespace and cleans up the query.
func cleanQuery(query string) string {
	spacePattern := regexp.MustCompile(`\s+`)
	query = spacePattern.ReplaceAllString(query, " ")
	
	query = strings.Trim(query, " .,!?")
	
	return query
}