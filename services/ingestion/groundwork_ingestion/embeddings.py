from __future__ import annotations

from hashlib import sha256
from typing import Protocol


class EmbeddingEngine(Protocol):
    dimension: int

    def embed(self, texts: list[str]) -> list[list[float]]:
        raise NotImplementedError


class FastEmbedEngine:
    def __init__(self, model_name: str = "sentence-transformers/all-MiniLM-L6-v2", cache_dir: str | None = None) -> None:
        from fastembed import TextEmbedding

        kwargs = {"model_name": model_name}
        if cache_dir:
            kwargs["cache_dir"] = cache_dir
        self.model = TextEmbedding(**kwargs)
        self.dimension = 384

    def embed(self, texts: list[str]) -> list[list[float]]:
        return [vector.tolist() for vector in self.model.embed(texts)]


class HashEmbeddingEngine:
    """Deterministic local fallback used for tests and offline bootstrap before model weights exist."""

    dimension = 384

    def embed(self, texts: list[str]) -> list[list[float]]:
        return [hash_embedding(text, self.dimension) for text in texts]


def create_embedding_engine(model_name: str, cache_dir: str | None = None, allow_hash_fallback: bool = True) -> EmbeddingEngine:
    try:
        return FastEmbedEngine(model_name=model_name, cache_dir=cache_dir)
    except Exception:
        if allow_hash_fallback:
            return HashEmbeddingEngine()
        raise


def hash_embedding(text: str, dimension: int = 384) -> list[float]:
    vector: list[float] = []
    for index in range(dimension):
        digest = sha256(f"{text}:{index}".encode("utf-8")).digest()
        vector.append((digest[0] / 127.5) - 1.0)
    return vector
