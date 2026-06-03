from __future__ import annotations

from typing import Any

from .models import Chunk


def uniform_payload(chunk: Chunk) -> dict[str, Any]:
    return {
        "document_id": chunk.document_id,
        "chunk_id": chunk.chunk_id,
        "chunk_hash": chunk.chunk_hash,
        "text": chunk.text,
        "metadata_prefix": chunk.metadata_prefix,
        "page": chunk.page,
        "offset": chunk.offset,
        "freshness_score": chunk.freshness_score,
        "soft_deleted": chunk.soft_deleted,
        "metadata": {
            "tenant_id": chunk.tenant_id,
            "region": chunk.region,
            "source_scope": chunk.acl_scope,
            "owner_acl_tags": chunk.owner_acl_tags,
        },
    }
