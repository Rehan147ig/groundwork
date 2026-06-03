from __future__ import annotations

from dataclasses import dataclass

from .indexes import ElasticsearchLexicalIndex, QdrantVectorIndex


@dataclass(frozen=True)
class BootstrapResult:
    qdrant_collection: str
    elasticsearch_index: str
    vector_size: int


def bootstrap_storage(qdrant: QdrantVectorIndex, elasticsearch: ElasticsearchLexicalIndex) -> BootstrapResult:
    qdrant.ensure_schema()
    elasticsearch.ensure_schema()
    return BootstrapResult(
        qdrant_collection=qdrant.collection,
        elasticsearch_index=elasticsearch.index,
        vector_size=qdrant.vector_size,
    )
