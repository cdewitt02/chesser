package engine

import "github.com/cdewitt02/chesser/internal/models"

// Exported wrappers over the three arithmetic helpers that decide every stored
// `cpl` and `classification`. They exist so cmd/golden can capture a reference
// table for the Python port (docs/python-rewrite/00-plan.md, Phase 0) without
// moving the helpers themselves out of this file.
//
// These add no behavior. If they ever do, the goldens they produced are void.

// GetEvaluation is getEvaluation.
func GetEvaluation(analysis *models.MoveAnalysis) int { return getEvaluation(analysis) }

// NormalizeEval is normalizeEval.
func NormalizeEval(eval, moveIndex int) int { return normalizeEval(eval, moveIndex) }

// ClassifyMove is classifyMove.
func ClassifyMove(cpl int) string { return classifyMove(cpl) }
