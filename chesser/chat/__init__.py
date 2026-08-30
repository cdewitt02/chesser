"""Query classification, prompt assembly, and the chat service."""

from chesser.chat.classifier import QueryType, classify_query, extract_mentioned_openings
from chesser.chat.prompts import PromptBuilder, aggregate_game_stats
from chesser.chat.router import QueryContext, QueryRouter
from chesser.chat.service import NO_DATA_ANSWER, Config, Service

__all__ = [
    "NO_DATA_ANSWER",
    "Config",
    "PromptBuilder",
    "QueryContext",
    "QueryRouter",
    "QueryType",
    "Service",
    "aggregate_game_stats",
    "classify_query",
    "extract_mentioned_openings",
]
