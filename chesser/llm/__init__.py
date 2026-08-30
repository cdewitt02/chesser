"""Provider-neutral LLM interfaces and the per-provider adapters.

The adapters are not imported here: each pulls in a vendor SDK, and
`chesser.config` must be able to resolve and report on a configuration without
loading all three. Import `chesser.llm.ollama` and friends directly.
"""

from chesser.llm.base import (
    FINISH_CONTENT_FILTER,
    FINISH_LENGTH,
    FINISH_OTHER,
    FINISH_STOP,
    ROLE_ASSISTANT,
    ROLE_USER,
    ChatModel,
    ChatRequest,
    ChatResponse,
    Embedder,
    Message,
    Preflighter,
    StreamingChatModel,
    Usage,
    embed_one,
    validate_messages,
)
from chesser.llm.errors import ConsumerError, ErrorKind, LLMError, classify_status

__all__ = [
    "FINISH_CONTENT_FILTER",
    "FINISH_LENGTH",
    "FINISH_OTHER",
    "FINISH_STOP",
    "ROLE_ASSISTANT",
    "ROLE_USER",
    "ChatModel",
    "ChatRequest",
    "ChatResponse",
    "ConsumerError",
    "Embedder",
    "ErrorKind",
    "LLMError",
    "Message",
    "Preflighter",
    "StreamingChatModel",
    "Usage",
    "classify_status",
    "embed_one",
    "validate_messages",
]
