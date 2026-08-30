"""The Chess.com game payload and the fields chesser derives from it."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

_PGN_HEADER = re.compile(r'\[(\w+) "([^"]*)"\]')
_OPENING_URL = re.compile(r"/openings/([^/]+)")
# Matches "-4." or "..." — the point where a variation's move list begins.
_VARIATION_SPLIT = re.compile(r"(-\d+\.|\.\.\.)")


@dataclass(slots=True)
class Player:
    uuid: str = ""
    username: str = ""
    rating: int = 0
    result: str = ""

    @classmethod
    def from_json(cls, raw: dict[str, Any]) -> Player:
        return cls(
            uuid=str(raw.get("uuid", "")),
            username=str(raw.get("username", "")),
            rating=int(raw.get("rating", 0) or 0),
            result=str(raw.get("result", "")),
        )


@dataclass(slots=True)
class Game:
    uuid: str = ""
    url: str = ""
    pgn: str = ""
    time_control: str = ""
    end_time: int = 0
    rated: bool = False
    accuracies: dict[str, float] = field(default_factory=dict)
    tcn: str = ""
    initial_setup: str = ""
    fen: str = ""
    time_class: str = ""
    # eco is the Chess.com *openings URL*, not the ECO code. The code lives in
    # the PGN header; this field is what opening_name() parses.
    eco: str = ""
    white: Player = field(default_factory=Player)
    black: Player = field(default_factory=Player)

    @classmethod
    def from_json(cls, raw: dict[str, Any]) -> Game:
        return cls(
            uuid=str(raw.get("uuid", "")),
            url=str(raw.get("url", "")),
            pgn=str(raw.get("pgn", "")),
            time_control=str(raw.get("time_control", "")),
            end_time=int(raw.get("end_time", 0) or 0),
            rated=bool(raw.get("rated", False)),
            accuracies={k: float(v) for k, v in (raw.get("accuracies") or {}).items()},
            tcn=str(raw.get("tcn", "")),
            initial_setup=str(raw.get("initial_setup", "")),
            fen=str(raw.get("fen", "")),
            time_class=str(raw.get("time_class", "")),
            eco=str(raw.get("eco", "")),
            white=Player.from_json(raw.get("white") or {}),
            black=Player.from_json(raw.get("black") or {}),
        )

    def game_result(self) -> str:
        if self.white.result == "win":
            return "white"
        if self.black.result == "win":
            return "black"
        return "draw"

    def _pgn_header(self, name: str) -> str:
        for key, value in _PGN_HEADER.findall(self.pgn):
            if key == name:
                return str(value)
        return ""

    def eco_code(self) -> str:
        return self._pgn_header("ECO")

    def opening_name(self) -> str:
        """Extract the opening name from the Chess.com openings URL.

        "https://www.chess.com/openings/Pirc-Defense-Main-Line-Kholmov-System-4...Bg7"
        becomes "Pirc Defense Main Line Kholmov System".
        """
        match = _OPENING_URL.search(self.eco)
        if match is None:
            return ""
        # Go's regexp.Split with n=2 keeps the capture group out of the result
        # and yields at most two pieces; re.split keeps the group, so take the
        # first piece either way.
        name = _VARIATION_SPLIT.split(match.group(1), maxsplit=1)[0]
        return name.replace("-", " ")

    def termination_type(self) -> str:
        return self._pgn_header("Termination")
