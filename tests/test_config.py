"""Ported from internal/config/config_test.go.

Backward compatibility lives in the precedence table, which is why it is the
highest-value test in the config layer: it is a table over an env map with no
network, and every row is a setup somebody may already have.
"""

from __future__ import annotations

import io

import pytest

from chesser.config import Config, ConfigError, resolve


def env_of(pairs: dict[str, str]):  # type: ignore[no-untyped-def]
    def read(key: str) -> str:
        return pairs.get(key, "")

    return read


PRECEDENCE_CASES = [
    pytest.param(
        {},
        "",
        ("ollama", "llama3.2", "ollama", "nomic-embed-text", "http://localhost:11434"),
        # The backward-compatibility case: a user with only the pre-existing
        # variables must see identical behavior.
        id="defaults are ollama end to end",
    ),
    pytest.param(
        {"OLLAMA_URL": "http://box:11434", "OLLAMA_EMBED_MODEL": "mxbai-embed-large"},
        "",
        ("ollama", "llama3.2", "ollama", "mxbai-embed-large", "http://box:11434"),
        id="pre-existing OLLAMA_ variables still apply",
    ),
    pytest.param(
        {"EMBED_MODEL": "bge-m3", "OLLAMA_EMBED_MODEL": "nomic-embed-text"},
        "",
        ("ollama", "llama3.2", "ollama", "bge-m3", "http://localhost:11434"),
        id="EMBED_MODEL outranks the OLLAMA_EMBED_MODEL alias",
    ),
    pytest.param(
        {"CHAT_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-test"},
        "",
        ("anthropic", "claude-opus-5", "ollama", "nomic-embed-text", "http://localhost:11434"),
        id="anthropic chat keeps embeddings local",
    ),
    pytest.param(
        {
            "CHAT_PROVIDER": "anthropic",
            "ANTHROPIC_API_KEY": "sk-test",
            "CHAT_MODEL": "claude-sonnet-5",
        },
        "",
        ("anthropic", "claude-sonnet-5", "ollama", "nomic-embed-text", "http://localhost:11434"),
        id="CHAT_MODEL overrides the provider default",
    ),
    pytest.param(
        {"CHAT_MODEL": "llama3.1"},
        "mistral",
        ("ollama", "mistral", "ollama", "nomic-embed-text", "http://localhost:11434"),
        # The positional argument is the most specific source, which is what
        # keeps `chesser chat <username> [model]` working.
        id="positional argument outranks CHAT_MODEL",
    ),
    pytest.param(
        {"CHAT_PROVIDER": "Anthropic", "ANTHROPIC_API_KEY": "sk-test"},
        "",
        ("anthropic", "claude-opus-5", "ollama", "nomic-embed-text", "http://localhost:11434"),
        id="provider names are case-insensitive",
    ),
    pytest.param(
        {"CHAT_PROVIDER": "openai", "OPENAI_API_KEY": "sk-test"},
        "",
        ("openai", "gpt-5-2025-08-07", "ollama", "nomic-embed-text", "http://localhost:11434"),
        id="openai chat keeps embeddings local",
    ),
    pytest.param(
        {"CHAT_PROVIDER": "openai", "EMBED_PROVIDER": "openai", "OPENAI_API_KEY": "sk-test"},
        "",
        (
            "openai",
            "gpt-5-2025-08-07",
            "openai",
            "text-embedding-3-small",
            "http://localhost:11434",
        ),
        # The configuration that removes Ollama from the prerequisite list
        # entirely: both providers hosted.
        id="openai for both chat and embeddings",
    ),
    pytest.param(
        {
            "CHAT_PROVIDER": "anthropic",
            "ANTHROPIC_API_KEY": "sk-ant",
            "EMBED_PROVIDER": "openai",
            "OPENAI_API_KEY": "sk-test",
        },
        "",
        (
            "anthropic",
            "claude-opus-5",
            "openai",
            "text-embedding-3-small",
            "http://localhost:11434",
        ),
        # The headline mix: hosted chat, local embeddings already indexed. It
        # must require no re-embedding.
        id="anthropic chat with openai embeddings",
    ),
    pytest.param(
        {
            "EMBED_PROVIDER": "openai",
            "OPENAI_API_KEY": "sk-test",
            "OLLAMA_EMBED_MODEL": "nomic-embed-text",
        },
        "",
        ("ollama", "llama3.2", "openai", "text-embedding-3-small", "http://localhost:11434"),
        # The alias is scoped to Ollama. A user who switches embed providers
        # with OLLAMA_EMBED_MODEL still exported must not send an Ollama model
        # name to OpenAI.
        id="OLLAMA_EMBED_MODEL does not leak into another embed provider",
    ),
    pytest.param(
        {
            "EMBED_PROVIDER": "openai",
            "OPENAI_API_KEY": "sk-test",
            "EMBED_MODEL": "text-embedding-3-large",
        },
        "",
        ("ollama", "llama3.2", "openai", "text-embedding-3-large", "http://localhost:11434"),
        id="EMBED_MODEL applies to a hosted embed provider",
    ),
]


@pytest.mark.parametrize(("env", "positional", "want"), PRECEDENCE_CASES)
def test_resolve_precedence(
    env: dict[str, str], positional: str, want: tuple[str, str, str, str, str]
) -> None:
    cfg = resolve(env_of(env), positional)
    got = (
        cfg.chat_provider,
        cfg.chat_model,
        cfg.embed_provider,
        cfg.embed_model,
        cfg.ollama_url,
    )
    assert got == want


@pytest.mark.parametrize(
    ("env", "want_text"),
    [
        pytest.param(
            {"CHAT_PROVIDER": "gemini"},
            ["CHAT_PROVIDER", "ollama", "anthropic", "openai"],
            id="unknown chat provider lists valid values",
        ),
        pytest.param(
            {"EMBED_PROVIDER": "voyage"},
            ["EMBED_PROVIDER", "ollama", "openai"],
            id="unknown embed provider lists valid values",
        ),
        pytest.param(
            {"EMBED_PROVIDER": "anthropic"},
            ["no embeddings API", "EMBED_PROVIDER=ollama", "EMBED_PROVIDER=openai"],
            # Anthropic has no embeddings API, so this is explained rather than
            # silently falling back.
            id="anthropic embeddings are refused with an explanation",
        ),
    ],
)
def test_resolve_errors(env: dict[str, str], want_text: list[str]) -> None:
    with pytest.raises(ConfigError) as excinfo:
        resolve(env_of(env), "")
    message = str(excinfo.value)
    for want in want_text:
        assert want in message, f"error = {message!r}, want it to mention {want!r}"


def test_summary_never_leaks_the_key() -> None:
    """The key must never reach a message or the startup banner."""
    cfg = resolve(
        env_of({"CHAT_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-ant-secret-value"}), ""
    )
    summary = cfg.summary()
    assert "sk-ant-secret-value" not in summary
    assert "sent to a third party" in summary
    assert cfg.uses_hosted_provider()

    # repr is the other way a secret escapes — into a log line or a traceback.
    assert "sk-ant-secret-value" not in repr(cfg)


def test_local_only_setup_says_nothing_about_egress() -> None:
    cfg = resolve(env_of({}), "")
    assert not cfg.uses_hosted_provider(), (
        "the default configuration must stay account-free and local"
    )
    assert "third party" not in cfg.summary()


def test_preflight_swallows_an_inconclusive_check_and_reraises_anything_else() -> None:
    from chesser.config import preflight
    from chesser.llm.errors import ErrorKind, LLMError

    class Inconclusive:
        def preflight(self) -> None:
            raise LLMError("fake", "chat", ErrorKind.PREFLIGHT_INCONCLUSIVE, "no models endpoint")

    class Fatal:
        def preflight(self) -> None:
            raise LLMError("fake", "chat", ErrorKind.UNAUTHORIZED, "bad key")

    class NotAPreflighter:
        pass

    warn = io.StringIO()
    preflight(warn, Inconclusive(), NotAPreflighter())
    assert "startup check skipped" in warn.getvalue()

    with pytest.raises(LLMError) as excinfo:
        preflight(io.StringIO(), Fatal())
    assert excinfo.value.kind is ErrorKind.UNAUTHORIZED


def test_config_is_constructible_without_an_sdk_installed() -> None:
    """resolve() and its error messages must not depend on a vendor SDK.

    Nothing about naming a provider requires importing its client, and making
    startup do so would turn an unrelated install problem into an unresolvable
    configuration error.
    """
    cfg = Config(
        chat_provider="anthropic",
        chat_model="claude-opus-5",
        embed_provider="ollama",
        embed_model="nomic-embed-text",
        ollama_url="http://localhost:11434",
    )
    assert cfg.uses_hosted_provider()
