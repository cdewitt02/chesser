"""Resolves provider selection from the environment.

Both entrypoints — `chesser chat` and `chesser data` — resolve through here, so
they cannot drift into the split-brain where chat runs on one provider and
ingestion silently runs on another.
"""

from __future__ import annotations

import os
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any, TextIO

from chesser.llm.base import Embedder, Preflighter
from chesser.llm.errors import ErrorKind, LLMError
from chesser.llm.providers import (
    ANTHROPIC,
    ANTHROPIC_API_KEY_ENV,
    ANTHROPIC_DEFAULT_MODEL,
    CHAT_PROVIDERS,
    EMBED_PROVIDERS,
    OLLAMA,
    OLLAMA_DEFAULT_BASE_URL,
    OLLAMA_DEFAULT_CHAT_MODEL,
    OLLAMA_DEFAULT_EMBED_MODEL,
    OPENAI,
    OPENAI_API_KEY_ENV,
    OPENAI_DEFAULT_CHAT_MODEL,
    OPENAI_DEFAULT_EMBED_MODEL,
)

if TYPE_CHECKING:  # pragma: no cover - import cycle avoidance only
    from chesser.db import DB
    from chesser.llm.base import ChatModel

# Env reads one environment variable. Tests pass a dict lookup.
Env = Callable[[str], str]


def os_env(key: str) -> str:
    """Read the process environment."""
    return os.environ.get(key, "")


class ConfigError(Exception):
    """An unusable provider selection. Raised by `resolve`, never later."""


@dataclass(slots=True)
class Config:
    """The resolved provider selection.

    Both defaults are ollama, so an existing setup with only DATABASE_URL and
    OLLAMA_URL behaves identically and the tool never starts spending money
    because a default moved.
    """

    chat_provider: str
    chat_model: str
    embed_provider: str
    embed_model: str
    ollama_url: str

    # API keys are never printed, only passed to the adapter that needs them.
    _anthropic_api_key: str = field(default="", repr=False)
    _openai_api_key: str = field(default="", repr=False)

    def uses_hosted_provider(self) -> bool:
        """Whether any selected provider sends data off-machine."""
        return self.chat_provider != OLLAMA or self.embed_provider != OLLAMA

    def new_chat_model(self) -> ChatModel:
        # Imported here rather than at module scope so `resolve` — and its
        # error messages about unknown providers — never depend on a vendor SDK
        # being importable.
        if self.chat_provider == OLLAMA:
            from chesser.llm.ollama import OllamaChatModel

            return OllamaChatModel(base_url=self.ollama_url, model=self.chat_model)
        if self.chat_provider == ANTHROPIC:
            from chesser.llm.anthropic import AnthropicChatModel

            return AnthropicChatModel(api_key=self._anthropic_api_key, model=self.chat_model)
        if self.chat_provider == OPENAI:
            from chesser.llm.openai import OpenAIChatModel

            return OpenAIChatModel(api_key=self._openai_api_key, model=self.chat_model)
        raise ConfigError(f"unknown chat provider {self.chat_provider!r}")

    def new_embedder(self) -> Embedder:
        if self.embed_provider == OLLAMA:
            from chesser.llm.ollama import OllamaEmbedder

            return OllamaEmbedder(base_url=self.ollama_url, model=self.embed_model)
        if self.embed_provider == OPENAI:
            # dimensions is left at its default, which is the width the
            # game_summaries column already declares. An index built by another
            # embedder is caught by check_index, not here.
            from chesser.llm.openai import OpenAIEmbedder

            return OpenAIEmbedder(api_key=self._openai_api_key, model=self.embed_model)
        raise ConfigError(f"unknown embed provider {self.embed_provider!r}")

    def summary(self) -> str:
        """The resolved configuration, printed at startup so a user who set only
        CHAT_PROVIDER can see that embeddings stayed local."""
        lines = [
            f"Chat:       {self.chat_provider} / {self.chat_model}",
            f"Embeddings: {self.embed_provider} / {self.embed_model}",
        ]
        text = "\n".join(lines)
        if self.uses_hosted_provider():
            text += (
                "\nNote: a hosted provider is selected — game summaries and the "
                "username are sent to a third party."
            )
        return text


def resolve(env: Env | None = None, chat_model_override: str = "") -> Config:
    """Build a Config from the environment.

    `chat_model_override` is the positional CLI argument, which is the most
    specific source for the chat model. Precedence is: positional arg ->
    CHAT_MODEL -> provider default.
    """
    read = os_env if env is None else env

    chat_provider = _provider_or(read("CHAT_PROVIDER"), OLLAMA)
    embed_provider = _provider_or(read("EMBED_PROVIDER"), OLLAMA)
    ollama_url = _value_or(read("OLLAMA_URL"), OLLAMA_DEFAULT_BASE_URL)

    if chat_provider not in CHAT_PROVIDERS:
        raise ConfigError(
            f"unknown CHAT_PROVIDER {chat_provider!r}; valid values: {', '.join(CHAT_PROVIDERS)}"
        )
    if embed_provider not in EMBED_PROVIDERS:
        if embed_provider == ANTHROPIC:
            raise ConfigError(
                "EMBED_PROVIDER=anthropic is not supported: Anthropic offers no embeddings API. "
                "Keep EMBED_PROVIDER=ollama or use EMBED_PROVIDER=openai "
                "(chat and embeddings are selected independently)"
            )
        raise ConfigError(
            f"unknown EMBED_PROVIDER {embed_provider!r}; valid values: {', '.join(EMBED_PROVIDERS)}"
        )

    chat_model = _first_non_empty(chat_model_override, read("CHAT_MODEL"))
    if not chat_model:
        chat_model = {
            OLLAMA: OLLAMA_DEFAULT_CHAT_MODEL,
            ANTHROPIC: ANTHROPIC_DEFAULT_MODEL,
            OPENAI: OPENAI_DEFAULT_CHAT_MODEL,
        }[chat_provider]

    # OLLAMA_EMBED_MODEL stays a working alias for EMBED_MODEL — it is
    # documented in the README today and costs one line to honor — but only
    # while the embed provider is Ollama. Honoring it otherwise would send a
    # still-exported "nomic-embed-text" to OpenAI.
    embed_model = read("EMBED_MODEL")
    if embed_provider == OLLAMA:
        embed_model = _first_non_empty(embed_model, read("OLLAMA_EMBED_MODEL"))
    embed_model = embed_model.strip()
    if not embed_model:
        embed_model = {
            OLLAMA: OLLAMA_DEFAULT_EMBED_MODEL,
            OPENAI: OPENAI_DEFAULT_EMBED_MODEL,
        }[embed_provider]

    # Credentials are checked when the chat model is constructed, which
    # `chesser chat` does before its welcome banner. Doing it here instead would
    # make ingestion — which needs no chat provider at all — fail over a missing
    # chat credential.
    return Config(
        chat_provider=chat_provider,
        chat_model=chat_model,
        embed_provider=embed_provider,
        embed_model=embed_model,
        ollama_url=ollama_url,
        _anthropic_api_key=read(ANTHROPIC_API_KEY_ENV).strip(),
        _openai_api_key=read(OPENAI_API_KEY_ENV).strip(),
    )


def _provider_or(value: str, fallback: str) -> str:
    """Normalize a provider name, which is case-insensitive."""
    value = value.strip()
    return value.lower() if value else fallback


def _value_or(value: str, fallback: str) -> str:
    value = value.strip()
    return value or fallback


def _first_non_empty(*values: str) -> str:
    for value in values:
        stripped = value.strip()
        if stripped:
            return stripped
    return ""


# ---------- startup checks ----------


def preflight(warn: TextIO, *models: Any) -> None:
    """Run each adapter's reachability, credential, and model checks.

    An inconclusive result — a models endpoint a gateway does not implement,
    say — is written to `warn` and swallowed: the real call will report the
    truth, and blocking startup over an auxiliary call would break valid setups.
    Anything else propagates.
    """
    for model in models:
        if not isinstance(model, Preflighter):
            continue
        try:
            model.preflight()
        except LLMError as err:
            if err.kind is ErrorKind.PREFLIGHT_INCONCLUSIVE:
                print(f"Warning: startup check skipped: {err}", file=warn)
                continue
            raise


def check_index(database: DB, embedder: Embedder, adopt: bool, warn: TextIO) -> None:
    """Verify that the configured embedder matches the index it will query or
    extend.

    Two distinct failures hide here. The vector(N) column width is the obvious
    one. The subtler one is provenance: two 768-dimension models from different
    providers pass a width check while producing vectors in unrelated spaces, so
    cosine distance across them is meaningless and retrieval degrades without
    erroring.

    `adopt` records the current embedder when the index carries no stamp yet —
    which is every index built before provenance existed. Callers that write to
    the index (ingestion) adopt; read-only callers (chat) do not.
    """
    from chesser.db import IndexMeta

    dims = embedder.dimensions()
    if dims > 0:
        try:
            column_dims = database.embedding_dimensions()
        except Exception as err:
            print(f"Warning: could not read the embedding column width: {err}", file=warn)
        else:
            if column_dims > 0 and column_dims != dims:
                raise ConfigError(
                    f"embedding width mismatch: {embedder.name()}/{embedder.model()} produces "
                    f"{dims} dimensions but game_summaries.embedding is vector({column_dims})"
                )

    try:
        meta = database.get_index_meta()
    except Exception as err:
        print(f"Warning: could not read index provenance: {err}", file=warn)
        return

    if meta is None:
        if not adopt:
            return
        database.set_index_meta(
            IndexMeta(
                embed_provider=embedder.name(),
                embed_model=embedder.model(),
                dimensions=dims,
            )
        )
        return

    if meta.embed_provider != embedder.name() or meta.embed_model != embedder.model():
        raise ConfigError(
            f"embedding provider mismatch: the index was built with "
            f"{meta.embed_provider}/{meta.embed_model} but the configured embedder is "
            f"{embedder.name()}/{embedder.model()}. "
            "Vectors from different models are not comparable even at the same width. "
            f"Either restore EMBED_PROVIDER={meta.embed_provider} and "
            f"EMBED_MODEL={meta.embed_model}, or re-embed with: chesser data reembed"
        )
