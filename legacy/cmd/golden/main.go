// Command golden captures reference outputs from the Go implementation into
// testdata/golden/, so the Python port can be diffed against them rather than
// trusted (docs/python-rewrite/00-plan.md, Phase 0).
//
// It is a capture tool, not a test. Running it overwrites the reference, which
// is exactly what must not happen casually: a golden regenerated from the
// current tree always matches the current tree and proves nothing. Regenerate
// only when a behavior change is deliberate, and update the recorded SHA in
// MANIFEST.md when you do.
//
//	go run ./cmd/golden <username>
//
// Two golden sets with different validity conditions come out of this:
//
//   - Per-game (summaries.json, moves are read straight from the database).
//     Keyed by game UUID; each game's values depend only on that game, so they
//     survive a growing corpus.
//   - Whole-corpus (prompts/). Every added game shifts win rates and the
//     comparison strings built from them, so these carry a corpus fingerprint
//     and the harness refuses to compare across a change in it.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/cdewitt02/chesser/internal/chat"
	"github.com/cdewitt02/chesser/internal/config"
	"github.com/cdewitt02/chesser/internal/db"
	"github.com/cdewitt02/chesser/internal/engine"
	"github.com/cdewitt02/chesser/internal/models"
	"github.com/cdewitt02/chesser/internal/search"
	"github.com/cdewitt02/chesser/internal/summary"
)

// goldenDir is relative to this directory, so the tool writes into the
// repository root's testdata/ from legacy/. Run it as:
//
//	cd legacy && . ../.env && go run ./cmd/golden <username>
const goldenDir = "../testdata/golden"

// detailLimit and numSimilar mirror cmd/chat's constants. The assembled prompt
// depends on both, so capturing at different values would produce a reference
// no real session ever sees.
const (
	numSimilar  = 100
	detailLimit = 10
)

// analysisDepth mirrors cmd/data's ANALYSIS_DEPTH. It is frozen for the
// duration of the rewrite: changing it rewrites every cpl and classification.
const analysisDepth = 12

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/golden <username>")
		os.Exit(1)
	}
	username := os.Args[1]

	ctx := context.Background()

	database, err := db.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer database.Close()

	if err := os.MkdirAll(filepath.Join(goldenDir, "prompts"), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}

	fingerprint, gameCount, err := corpusFingerprint(ctx, database)
	if err != nil {
		log.Fatalf("fingerprint: %v", err)
	}

	if err := captureEvalHelpers(); err != nil {
		log.Fatalf("eval helpers: %v", err)
	}
	fmt.Println("wrote eval_helpers.json")

	if err := captureAnalysis(ctx, database); err != nil {
		log.Fatalf("analysis: %v", err)
	}
	fmt.Println("wrote analysis.json")

	if err := captureSummaries(ctx, database, username); err != nil {
		log.Fatalf("summaries: %v", err)
	}
	fmt.Println("wrote summaries.json")

	if err := captureClassification(); err != nil {
		log.Fatalf("classification: %v", err)
	}
	fmt.Println("wrote classification.json")

	if err := captureParsing(username); err != nil {
		log.Fatalf("parsing: %v", err)
	}
	fmt.Println("wrote parsing.json")

	if err := capturePrompts(ctx, database, username, fingerprint, gameCount); err != nil {
		log.Fatalf("prompts: %v", err)
	}
	fmt.Println("wrote prompts/")

	fmt.Printf("\ncorpus fingerprint: %s (%d games)\n", fingerprint, gameCount)
	fmt.Println("Record the capture commit SHA in testdata/golden/MANIFEST.md.")
}

// ---------- corpus fingerprint ----------

// corpusFingerprint is the game count plus a hash of the sorted game UUIDs. It
// is enough to tell a stale whole-corpus reference from a real regression, and
// it commits no database contents.
func corpusFingerprint(ctx context.Context, database *db.DB) (string, int, error) {
	rows, err := database.Pool().Query(ctx, `SELECT uuid::text FROM games`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()

	var uuids []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return "", 0, err
		}
		uuids = append(uuids, u)
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	sort.Strings(uuids)

	h := sha256.New()
	for _, u := range uuids {
		h.Write([]byte(u))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16], len(uuids), nil
}

// ---------- eval helpers ----------

type evalCase struct {
	Evaluation     int    `json:"evaluation"`
	IsMate         bool   `json:"is_mate"`
	MateIn         int    `json:"mate_in"`
	MoveIndex      int    `json:"move_index"`
	GetEvaluation  int    `json:"get_evaluation"`
	NormalizeEval  int    `json:"normalize_eval"`
	ClassifyMoveIn int    `json:"classify_move_cpl"`
	ClassifyMove   string `json:"classify_move"`
}

// captureEvalHelpers walks a fixed grid rather than sampling the corpus: these
// three functions decide every stored cpl and classification, and the branches
// that matter (mate in either direction, the four classification boundaries,
// odd and even move indices) are not all reached by 74 games.
func captureEvalHelpers() error {
	evals := []int{-10000, -1200, -201, -200, -101, -100, -51, -50, -1, 0, 1, 50, 51, 100, 101, 200, 201, 1200, 10000}
	mates := []int{-5, -1, 0, 1, 5}

	var cases []evalCase
	for _, ev := range evals {
		for _, mate := range mates {
			for _, isMate := range []bool{false, true} {
				for _, idx := range []int{0, 1, 2, 3} {
					a := &models.MoveAnalysis{Evaluation: ev, IsMate: isMate, MateIn: mate}
					got := engine.GetEvaluation(a)
					cases = append(cases, evalCase{
						Evaluation:     ev,
						IsMate:         isMate,
						MateIn:         mate,
						MoveIndex:      idx,
						GetEvaluation:  got,
						NormalizeEval:  engine.NormalizeEval(got, idx),
						ClassifyMoveIn: ev,
						ClassifyMove:   engine.ClassifyMove(ev),
					})
				}
			}
		}
	}
	return writeJSON("eval_helpers.json", cases)
}

// ---------- engine re-analysis ----------

type analysisGolden struct {
	GameUUID string         `json:"game_uuid"`
	Moves    []analysisMove `json:"moves"`
}

type analysisMove struct {
	PlayedMove     string `json:"played_move"`
	BestMove       string `json:"best_move"`
	Evaluation     int    `json:"evaluation"`
	IsMate         bool   `json:"is_mate"`
	MateIn         int    `json:"mate_in"`
	CPL            int    `json:"cpl"`
	Classification string `json:"classification"`
	FENBefore      string `json:"fen_before"`
}

// analysisGames caps how many games are re-analyzed. Two Stockfish searches per
// move at depth 12 means the whole corpus takes many minutes; the shortest five
// are enough to catch a systematic arithmetic error, which is the only kind the
// port can have. A per-move bug shows up on the first game.
const analysisGames = 5

// captureAnalysis re-runs AnalyzeGame over a few stored games with the engine
// that is on PATH *now*, and records what it produced.
//
// This exists because the stored `moves` rows are **not** reproducible: they
// were written by a different Stockfish build, and the current Go tree diverges
// from them on every evaluation (verified 2026-08-29 — 12 of 12 and 17 of 17
// evaluations differ on the two shortest games, and 3 of 17 classifications).
// So "does Python match the database?" is unanswerable, and the question that
// can be answered — and is the one the port is actually on the hook for — is
// "does Python match Go, given the same engine?"
//
// Consequence: this golden is only valid for the Stockfish version that
// produced it. That version is recorded in MANIFEST.md, and this is the one
// golden that a machine with a different engine cannot check.
func captureAnalysis(ctx context.Context, database *db.DB) error {
	rows, err := database.Pool().Query(ctx, `
		SELECT g.uuid::text, g.pgn
		FROM games g
		JOIN moves m ON m.game_uuid = g.uuid
		GROUP BY g.uuid, g.pgn
		ORDER BY COUNT(*) ASC, g.uuid ASC
		LIMIT $1`, analysisGames)
	if err != nil {
		return err
	}
	type gameRow struct{ uuid, pgn string }
	var games []gameRow
	for rows.Next() {
		var g gameRow
		if err := rows.Scan(&g.uuid, &g.pgn); err != nil {
			rows.Close()
			return err
		}
		games = append(games, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	eng, err := engine.StartEngine()
	if err != nil {
		return fmt.Errorf("start stockfish: %w", err)
	}
	defer engine.StopEngine(eng)

	out := make([]analysisGolden, 0, len(games))
	for _, g := range games {
		analyses, err := engine.AnalyzeGame(eng, g.pgn, analysisDepth)
		if err != nil {
			return fmt.Errorf("analyze %s: %w", g.uuid, err)
		}
		moves := make([]analysisMove, 0, len(analyses))
		for _, a := range analyses {
			best, played := "", ""
			if a.BestMove != nil {
				best = a.BestMove.String()
			}
			if a.PlayedMove != nil {
				played = a.PlayedMove.String()
			}
			moves = append(moves, analysisMove{
				PlayedMove:     played,
				BestMove:       best,
				Evaluation:     a.Evaluation,
				IsMate:         a.IsMate,
				MateIn:         a.MateIn,
				CPL:            a.CentipawnLoss,
				Classification: a.Classification,
				FENBefore:      a.FENBefore,
			})
		}
		out = append(out, analysisGolden{GameUUID: g.uuid, Moves: moves})
	}
	return writeJSON("analysis.json", out)
}

// ---------- summaries ----------

type summaryGolden struct {
	GameUUID string                  `json:"game_uuid"`
	Data     *models.GameSummaryData `json:"data"`
	Text     string                  `json:"text"`
	Stored   string                  `json:"stored_text"`
	Matches  bool                    `json:"matches_stored"`
}

var ecoURLHeader = regexp.MustCompile(`\[ECOUrl "([^"]*)"\]`)

// captureSummaries regenerates every stored game's summary from the stored
// games and moves rows.
//
// It also compares against the summary_text the corpus already holds. A
// mismatch there is not a port problem — it means the Go tree itself has
// drifted from what produced the stored embeddings, and the goldens would
// freeze that drift.
func captureSummaries(ctx context.Context, database *db.DB, username string) error {
	rows, err := database.Pool().Query(ctx, `
		SELECT g.uuid::text, g.pgn, g.result, g.time_class,
		       g.white_username, g.white_rating, g.black_username, g.black_rating,
		       COALESCE(gs.summary_text, '')
		FROM games g
		LEFT JOIN game_summaries gs ON gs.game_uuid = g.uuid
		ORDER BY g.uuid`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type gameRow struct {
		uuid, pgn, result, timeClass string
		whiteUser, blackUser, stored string
		whiteRating, blackRating     int
	}
	var games []gameRow
	for rows.Next() {
		var g gameRow
		if err := rows.Scan(&g.uuid, &g.pgn, &g.result, &g.timeClass,
			&g.whiteUser, &g.whiteRating, &g.blackUser, &g.blackRating, &g.stored); err != nil {
			return err
		}
		games = append(games, g)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	out := make([]summaryGolden, 0, len(games))
	drift := 0
	for _, g := range games {
		moveRows, err := database.GetMovesForGame(ctx, g.uuid)
		if err != nil {
			return err
		}
		analyses := make([]*models.MoveAnalysis, len(moveRows))
		for i, m := range moveRows {
			analyses[i] = &models.MoveAnalysis{
				Evaluation:     m.Evaluation,
				IsMate:         m.IsMate,
				MateIn:         m.MateIn,
				CentipawnLoss:  m.CPL,
				Classification: m.Classification,
				FENBefore:      m.FENBefore,
			}
		}

		game := &models.Game{
			UUID:      g.uuid,
			PGN:       g.pgn,
			TimeClass: g.timeClass,
			White:     models.Player{Username: g.whiteUser, Rating: uint16(g.whiteRating)},
			Black:     models.Player{Username: g.blackUser, Rating: uint16(g.blackRating)},
		}
		// Game.ECO holds the Chess.com openings URL, which is not a column but
		// is carried in the PGN as [ECOUrl "..."]. Reconstructing it here is
		// what lets OpeningName() run unchanged.
		if m := ecoURLHeader.FindStringSubmatch(g.pgn); len(m) == 2 {
			game.ECO = m[1]
		}
		switch g.result {
		case "white":
			game.White.Result, game.Black.Result = "win", "lost"
		case "black":
			game.White.Result, game.Black.Result = "lost", "win"
		default:
			game.White.Result, game.Black.Result = "draw", "draw"
		}

		data := summary.ExtractSummaryData(game, analyses, username)
		text := summary.GenerateSummary(data)
		matches := g.stored == "" || text == g.stored
		if !matches {
			drift++
		}
		out = append(out, summaryGolden{
			GameUUID: g.uuid,
			Data:     data,
			Text:     text,
			Stored:   g.stored,
			Matches:  matches,
		})
	}

	if drift > 0 {
		// Loud, and not fatal: the capture is still worth having, but the
		// operator has to know the reference no longer describes the corpus.
		fmt.Fprintf(os.Stderr,
			"WARNING: %d of %d regenerated summaries differ from the stored summary_text.\n"+
				"The Go tree has drifted from what produced the stored embeddings.\n"+
				"Resolve that before trusting these goldens.\n", drift, len(out))
	}
	return writeJSON("summaries.json", out)
}

// ---------- query classification and parsing ----------

// questions is the frozen set from docs/multi-provider/03-eval-plan.md §2, plus
// the two adversarial extras. Phase 7 gates on prompt parity for exactly these.
var questions = []string{
	"What's my average centipawn loss?",
	"How many games have I played and what's my win rate?",
	"Am I better with white or black?",
	"Which time control is my best?",
	"Show me games where I threw a winning position",
	"What openings do I lose with most often?",
	"What should I study to improve fastest?",
	"What's my biggest weakness?",
	"Have I improved over the last month?",
	"Is my accuracy getting better or worse?",
	"What's a good recipe for risotto?",
	"How would I do against Magnus Carlsen?",
}

// probeQuestions exercise classifier and parser branches the frozen set does
// not reach. They are classification/parsing goldens only — no prompt is
// captured for them, because prompts are the expensive whole-corpus artifact.
var probeQuestions = []string{
	"my losses",
	"games I won",
	"show me draws",
	"my games as black",
	"playing white",
	"my Sicilian games",
	"how do I play the King's Indian?",
	"French Defense analysis",
	"Caro-Kann games",
	"my blitz games",
	"rapid time control",
	"endgame problems",
	"my middlegame tactics",
	"games where I blundered",
	"games without blunders",
	"my games this week",
	"recent games",
	"Why do I keep losing in the Sicilian?",
	"my blitz games as black",
	"endgame losses",
	"how can I improve?",
	"analyze my play",
	"Compare my B20 and C50 results",
	"Am I getting better at the Queen's Gambit lately?",
	"no blunders in my rapid wins as white",
}

type classifyGolden struct {
	Question          string   `json:"question"`
	QueryType         string   `json:"query_type"`
	MentionedOpenings []string `json:"mentioned_openings"`
}

func captureClassification() error {
	var out []classifyGolden
	for _, q := range append(append([]string{}, questions...), probeQuestions...) {
		openings := chat.ExtractMentionedOpenings(q)
		if openings == nil {
			openings = []string{}
		}
		out = append(out, classifyGolden{
			Question:          q,
			QueryType:         chat.ClassifyQuery(q).String(),
			MentionedOpenings: openings,
		})
	}
	return writeJSON("classification.json", out)
}

type parseGolden struct {
	Question         string   `json:"question"`
	SemanticQuery    string   `json:"semantic_query"`
	ExtractedFilters []string `json:"extracted_filters"`
	Result           *string  `json:"result"`
	UserColor        *string  `json:"user_color"`
	TimeClass        *string  `json:"time_class"`
	WeakPhase        *string  `json:"weak_phase"`
	ECOPrefix        *string  `json:"eco_prefix"`
	OpeningName      *string  `json:"opening_name"`
	MinBlunders      *int     `json:"min_blunders"`
	MaxBlunders      *int     `json:"max_blunders"`
	MinMistakes      *int     `json:"min_mistakes"`
	MinRating        *int     `json:"min_rating"`
	MaxRating        *int     `json:"max_rating"`
	// DateFrom is recorded as a flag, never a value: it is now() minus a
	// duration, so a captured timestamp would fail one second after capture and
	// tell you nothing about the port.
	DateFromSet bool `json:"date_from_set"`
	DateToSet   bool `json:"date_to_set"`
}

func captureParsing(username string) error {
	parser := search.NewQueryParser()
	var out []parseGolden
	for _, q := range append(append([]string{}, questions...), probeQuestions...) {
		r := parser.Parse(q, username)
		f := r.Filters
		extracted := r.ExtractedFilters
		if extracted == nil {
			extracted = []string{}
		}
		out = append(out, parseGolden{
			Question:         q,
			SemanticQuery:    r.SemanticQuery,
			ExtractedFilters: extracted,
			Result:           f.Result,
			UserColor:        f.UserColor,
			TimeClass:        f.TimeClass,
			WeakPhase:        f.WeakPhase,
			ECOPrefix:        f.ECOPrefix,
			OpeningName:      f.OpeningName,
			MinBlunders:      f.MinBlunders,
			MaxBlunders:      f.MaxBlunders,
			MinMistakes:      f.MinMistakes,
			MinRating:        f.MinRating,
			MaxRating:        f.MaxRating,
			DateFromSet:      f.DateFrom != nil,
			DateToSet:        f.DateTo != nil,
		})
	}
	return writeJSON("parsing.json", out)
}

// ---------- assembled prompts ----------

type promptManifest struct {
	CorpusFingerprint string   `json:"corpus_fingerprint"`
	GameCount         int      `json:"game_count"`
	Username          string   `json:"username"`
	NumSimilar        int      `json:"num_similar"`
	DetailLimit       int      `json:"detail_limit"`
	Questions         []string `json:"questions"`
}

// capturePrompts builds one Assembled Prompt per frozen question, against the
// live corpus. This is the artifact Phase 7 gates on: every input the chat
// provider receives is the prompt, so a matching prompt leaves nothing
// downstream for the port to have gotten wrong.
func capturePrompts(ctx context.Context, database *db.DB, username, fingerprint string, gameCount int) error {
	cfg, err := config.Resolve(config.OSEnv, "")
	if err != nil {
		return err
	}
	embedder, err := cfg.NewEmbedder()
	if err != nil {
		return err
	}
	if err := config.CheckIndex(ctx, database, embedder, false, os.Stderr); err != nil {
		return err
	}

	router := chat.NewQueryRouter(
		database,
		search.NewHybridSearcher(embedder, &dbSearchAdapter{db: database}),
		chat.NewPromptBuilder(username),
		username,
		numSimilar,
	)

	for i, q := range questions {
		qctx, err := router.Route(ctx, q)
		if err != nil {
			return fmt.Errorf("question %d: %w", i+1, err)
		}
		prompt := router.BuildPrompt(qctx, detailLimit)
		// chat.Service appends this after BuildPrompt; the golden has to carry
		// it too, or the reference is not the prompt the provider receives.
		if len(qctx.Filters) > 0 {
			prompt += fmt.Sprintf("\n\nNote: The search was filtered by: %v", qctx.Filters)
		}
		name := filepath.Join(goldenDir, "prompts", fmt.Sprintf("%02d.txt", i+1))
		if err := os.WriteFile(name, []byte(prompt), 0o644); err != nil {
			return err
		}
	}

	return writeJSON(filepath.Join("prompts", "manifest.json"), promptManifest{
		CorpusFingerprint: fingerprint,
		GameCount:         gameCount,
		Username:          username,
		NumSimilar:        numSimilar,
		DetailLimit:       detailLimit,
		Questions:         questions,
	})
}

// dbSearchAdapter mirrors the unexported adapter in internal/chat. It exists
// only because that one is not exported; the two must stay identical.
type dbSearchAdapter struct{ db *db.DB }

func (a *dbSearchAdapter) FindSimilarGamesWithFilters(
	ctx context.Context, queryEmbedding []float32, filters *search.GameFilters, limit int,
) ([]*search.SimilarGameResult, error) {
	results, err := a.db.FindSimilarGamesWithFilters(ctx, queryEmbedding, filters, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*search.SimilarGameResult, len(results))
	for i, r := range results {
		out[i] = &search.SimilarGameResult{
			GameUUID:    r.GameUUID,
			SummaryText: r.SummaryText,
			Distance:    r.Distance,
			Game:        r.Game,
		}
	}
	return out, nil
}

func (a *dbSearchAdapter) CountGamesMatchingFilters(ctx context.Context, filters *search.GameFilters) (int, error) {
	return a.db.CountGamesMatchingFilters(ctx, filters)
}

// ---------- io ----------

func writeJSON(name string, v any) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	return os.WriteFile(filepath.Join(goldenDir, name), buf, 0o644)
}
