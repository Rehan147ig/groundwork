from __future__ import annotations

import re
from time import time

from .models import Chunk, SourceDocument, stable_chunk


class SemanticChunker:
    def __init__(self, min_tokens: int = 256, max_tokens: int = 512, overlap_tokens: int = 50) -> None:
        self.min_tokens = min_tokens
        self.max_tokens = max_tokens
        self.overlap_tokens = overlap_tokens

    def chunk(self, source: SourceDocument) -> list[Chunk]:
        paragraphs = [p.strip() for p in re.split(r"\n\s*\n", source.body) if p.strip()]
        if not paragraphs:
            return []

        chunks: list[Chunk] = []
        current: list[str] = []
        current_tokens = 0
        offset = 0

        for paragraph in paragraphs:
            paragraph_tokens = token_count(paragraph)
            if current and current_tokens + paragraph_tokens > self.max_tokens:
                text = "\n\n".join(current)
                chunks.append(self._chunk(source, text, offset))
                offset += len(text)
                current = tail_overlap(current, self.overlap_tokens)
                current_tokens = token_count("\n\n".join(current))

            current.append(paragraph)
            current_tokens += paragraph_tokens

            if current_tokens >= self.min_tokens:
                text = "\n\n".join(current)
                chunks.append(self._chunk(source, text, offset))
                offset += len(text)
                current = tail_overlap(current, self.overlap_tokens)
                current_tokens = token_count("\n\n".join(current))

        if current:
            chunks.append(self._chunk(source, "\n\n".join(current), offset))

        return dedupe(chunks)

    def _chunk(self, source: SourceDocument, text: str, offset: int) -> Chunk:
        chunk_id, chunk_hash = stable_chunk(source.document_id, offset, text)
        return Chunk(
            tenant_id=source.tenant_id,
            region=source.region,
            document_id=source.document_id,
            chunk_id=chunk_id,
            chunk_hash=chunk_hash,
            text=text,
            metadata_prefix=f"Title: {source.title}\nSource: {source.source_uri}\nType: {source.source_type}",
            page=1,
            offset=offset,
            acl_scope=source.acl_scope,
            owner_acl_tags=source.owner_acl_tags,
            freshness_score=freshness_score(source.modified_at),
            data_subject_ids=source.data_subject_ids,
        )


def token_count(text: str) -> int:
    return len(re.findall(r"\S+", text))


def tail_overlap(paragraphs: list[str], overlap_tokens: int) -> list[str]:
    selected: list[str] = []
    total = 0
    for paragraph in reversed(paragraphs):
        selected.insert(0, paragraph)
        total += token_count(paragraph)
        if total >= overlap_tokens:
            break
    return selected


def freshness_score(modified_at: float) -> float:
    age_days = max(0.0, (time() - modified_at) / 86400)
    return max(0.0, min(1.0, 1.0 - age_days / 180))


def dedupe(chunks: list[Chunk]) -> list[Chunk]:
    seen: set[str] = set()
    out: list[Chunk] = []
    for chunk in chunks:
        if chunk.chunk_id in seen:
            continue
        seen.add(chunk.chunk_id)
        out.append(chunk)
    return out
