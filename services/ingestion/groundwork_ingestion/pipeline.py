from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol

from .chunker import SemanticChunker
from .embeddings import EmbeddingEngine
from .indexes import AtomicDualIndexWriter, LexicalIndex, VectorIndex
from .models import SourceDocument, Tenant
from .residency import assert_region_allowed
from .retry import retry_with_backoff


class DocumentAuthorizer(Protocol):
    def grant_document(self, document_id: str, owner_acl_tags: list[str]) -> None:
        raise NotImplementedError


@dataclass(frozen=True)
class IngestionResult:
    tenant_id: str
    document_id: str
    region: str
    chunks_written: int
    min_freshness: float


class IngestionPipeline:
    def __init__(
        self,
        chunker: SemanticChunker,
        vector: VectorIndex,
        lexical: LexicalIndex,
        embeddings: EmbeddingEngine,
        authorizer: DocumentAuthorizer | None = None,
    ) -> None:
        self.chunker = chunker
        self.vector = vector
        self.lexical = lexical
        self.embeddings = embeddings
        self.authorizer = authorizer

    def ingest(self, tenant: Tenant, source: SourceDocument) -> IngestionResult:
        assert_region_allowed(tenant.residency, source.region)
        chunks = retry_with_backoff(lambda: self.chunker.chunk(source), label="semantic_chunking")
        vectors = retry_with_backoff(lambda: self.embeddings.embed([chunk.indexed_text for chunk in chunks]), label="local_embedding")
        chunks = [
            chunk.__class__(**{**chunk.__dict__, "embedding": vector})
            for chunk, vector in zip(chunks, vectors, strict=True)
        ]
        writer = AtomicDualIndexWriter(self.vector, self.lexical)
        retry_with_backoff(lambda: writer.write(chunks), label="atomic_dual_index")
        if self.authorizer is not None:
            try:
                retry_with_backoff(
                    lambda: self.authorizer.grant_document(source.document_id, source.owner_acl_tags),
                    label="openfga_document_grant",
                )
            except Exception:
                writer.rollback()
                raise
        return IngestionResult(
            tenant_id=source.tenant_id,
            document_id=source.document_id,
            region=source.region,
            chunks_written=len(chunks),
            min_freshness=min((chunk.freshness_score for chunk in chunks), default=0.0),
        )
