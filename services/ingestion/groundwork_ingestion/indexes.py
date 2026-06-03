from __future__ import annotations

from uuid import UUID

from .models import Chunk
from .payloads import uniform_payload
from .retry import retry_with_backoff


class IndexWriteError(RuntimeError):
    pass


class VectorIndex:
    def upsert(self, chunk: Chunk, vector: list[float]) -> None:
        raise NotImplementedError

    def delete(self, tenant_id: str, chunk_id: str) -> None:
        raise NotImplementedError


class LexicalIndex:
    def upsert(self, chunk: Chunk) -> None:
        raise NotImplementedError

    def delete(self, tenant_id: str, chunk_id: str) -> None:
        raise NotImplementedError


class InMemoryIndex(VectorIndex, LexicalIndex):
    def __init__(self) -> None:
        self.records: dict[tuple[str, str], dict[str, object]] = {}

    def upsert(self, chunk: Chunk, vector: list[float] | None = None) -> None:
        payload = uniform_payload(chunk)
        if vector is not None:
            payload["vector"] = vector
        self.records[(chunk.tenant_id, chunk.chunk_id)] = payload

    def delete(self, tenant_id: str, chunk_id: str) -> None:
        self.records.pop((tenant_id, chunk_id), None)


class QdrantVectorIndex(VectorIndex):
    def __init__(self, url: str, collection: str, vector_size: int = 384) -> None:
        from qdrant_client import QdrantClient

        self.url = url
        self.collection = collection
        self.vector_size = vector_size
        self.client = QdrantClient(url=url)

    def ensure_schema(self) -> None:
        from qdrant_client.http.models import (
            Distance,
            HnswConfigDiff,
            ScalarQuantization,
            ScalarQuantizationConfig,
            ScalarType,
            VectorParams,
        )

        def operation() -> None:
            collections = self.client.get_collections().collections
            exists = any(collection.name == self.collection for collection in collections)
            if not exists:
                self.client.create_collection(
                    collection_name=self.collection,
                    vectors_config=VectorParams(size=self.vector_size, distance=Distance.COSINE, on_disk=True),
                    hnsw_config=HnswConfigDiff(on_disk=True),
                    quantization_config=ScalarQuantization(
                        scalar=ScalarQuantizationConfig(type=ScalarType.INT8, always_ram=False)
                    ),
                )

        retry_with_backoff(operation, attempts=8, base_delay=0.5, label="qdrant_schema_bootstrap")

    def upsert(self, chunk: Chunk, vector: list[float]) -> None:
        from qdrant_client.http.models import PointStruct

        payload = uniform_payload(chunk)
        point = PointStruct(id=point_id(chunk), vector=vector, payload=payload)
        self.client.upsert(collection_name=self.collection, points=[point])

    def delete(self, tenant_id: str, chunk_id: str) -> None:
        from qdrant_client.http.models import FieldCondition, Filter, MatchValue

        self.client.delete(
            collection_name=self.collection,
            points_selector=Filter(
                must=[
                    FieldCondition(key="metadata.tenant_id", match=MatchValue(value=tenant_id)),
                    FieldCondition(key="chunk_id", match=MatchValue(value=chunk_id)),
                ],
            ),
        )


class ElasticsearchLexicalIndex(LexicalIndex):
    def __init__(self, url: str, index: str) -> None:
        from elasticsearch import Elasticsearch

        self.url = url
        self.index = index
        self.client = Elasticsearch(url)

    def ensure_schema(self) -> None:
        mappings = {
            "mappings": {
                "properties": {
                    "chunk_hash": {"type": "keyword"},
                    "text": {"type": "text"},
                    "metadata": {
                        "properties": {
                            "tenant_id": {"type": "keyword"},
                            "region": {"type": "keyword"},
                            "source_scope": {"type": "keyword"},
                            "owner_acl_tags": {"type": "keyword"},
                        }
                    },
                }
            }
        }

        def operation() -> None:
            if not self.client.indices.exists(index=self.index):
                self.client.indices.create(index=self.index, **mappings)

        retry_with_backoff(operation, attempts=8, base_delay=1.5, label="elasticsearch_schema_bootstrap")

    def upsert(self, chunk: Chunk) -> None:
        self.client.index(index=self.index, id=chunk.chunk_id, document=uniform_payload(chunk))

    def delete(self, tenant_id: str, chunk_id: str) -> None:
        self.client.delete_by_query(
            index=self.index,
            query={
                "bool": {
                    "filter": [
                        {"term": {"metadata.tenant_id": tenant_id}},
                        {"term": {"chunk_id": chunk_id}},
                    ]
                }
            },
        )


class AtomicDualIndexWriter:
    def __init__(self, vector: VectorIndex, lexical: LexicalIndex) -> None:
        self.vector = vector
        self.lexical = lexical
        self.written: list[Chunk] = []

    def write(self, chunks: list[Chunk]) -> None:
        for chunk in chunks:
            try:
                self.vector.upsert(chunk, chunk.embedding)
                self.lexical.upsert(chunk)
                self.written.append(chunk)
            except Exception as exc:  # noqa: BLE001
                self.rollback()
                raise IndexWriteError(f"failed atomic dual-index write for {chunk.chunk_id}") from exc

    def rollback(self) -> None:
        for chunk in reversed(self.written):
            self.vector.delete(chunk.tenant_id, chunk.chunk_id)
            self.lexical.delete(chunk.tenant_id, chunk.chunk_id)
        self.written.clear()


def point_id(chunk: Chunk) -> str:
    return str(UUID(hex=chunk.chunk_hash[:32]))
