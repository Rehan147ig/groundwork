from __future__ import annotations

import os
from contextlib import asynccontextmanager
from hashlib import sha256
from typing import Any

from fastapi import FastAPI, File, Form, HTTPException, UploadFile

from .bootstrap import bootstrap_storage
from .chunker import SemanticChunker
from .embeddings import create_embedding_engine
from .indexes import ElasticsearchLexicalIndex, InMemoryIndex, QdrantVectorIndex
from .models import SourceDocument, Tenant
from .openfga import OpenFGAAuthorizer
from .parser import UnsupportedFileType, parse_uploaded_bytes
from .pipeline import IngestionPipeline
from .residency import ResidencyViolation, residency_from_region


MODEL_NAME = os.getenv("EMBEDDING_MODEL", "sentence-transformers/all-MiniLM-L6-v2")
MODEL_CACHE = os.getenv("EMBEDDING_CACHE_DIR", "/models/fastembed")
QDRANT_URL = os.getenv("QDRANT_URL", "")
QDRANT_COLLECTION = os.getenv("QDRANT_COLLECTION", "groundwork_chunks")
ELASTICSEARCH_URL = os.getenv("ELASTICSEARCH_URL", "")
ELASTICSEARCH_INDEX = os.getenv("ELASTICSEARCH_INDEX", "groundwork_chunks")
BOOTSTRAP_STORAGE = os.getenv("BOOTSTRAP_STORAGE", "true").lower() == "true"
OPENFGA_URL = os.getenv("OPENFGA_URL", "")
OPENFGA_STORE_NAME = os.getenv("OPENFGA_STORE_NAME", "groundwork_local")

EMBEDDINGS = create_embedding_engine(MODEL_NAME, MODEL_CACHE)


def build_pipeline() -> IngestionPipeline:
    if QDRANT_URL and ELASTICSEARCH_URL:
        vector = QdrantVectorIndex(QDRANT_URL, QDRANT_COLLECTION)
        lexical = ElasticsearchLexicalIndex(ELASTICSEARCH_URL, ELASTICSEARCH_INDEX)
    else:
        vector = InMemoryIndex()
        lexical = InMemoryIndex()
    authorizer = OpenFGAAuthorizer(OPENFGA_URL, OPENFGA_STORE_NAME) if OPENFGA_URL else None
    return IngestionPipeline(SemanticChunker(), vector, lexical, EMBEDDINGS, authorizer)


PIPELINE = build_pipeline()


@asynccontextmanager
async def lifespan(app: FastAPI):
    if BOOTSTRAP_STORAGE and isinstance(PIPELINE.vector, QdrantVectorIndex) and isinstance(PIPELINE.lexical, ElasticsearchLexicalIndex):
        app.state.bootstrap = bootstrap_storage(PIPELINE.vector, PIPELINE.lexical)
    if PIPELINE.authorizer is not None:
        PIPELINE.authorizer.ensure()
    yield


app = FastAPI(title="Groundwork Ingestion Runtime", lifespan=lifespan)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": "groundwork-ingestion"}


@app.post("/embed")
def embed(payload: dict[str, Any]) -> dict[str, object]:
    text = payload.get("text")
    if isinstance(text, str):
        return {"embedding": EMBEDDINGS.embed([text])[0]}

    texts = payload.get("texts")
    if isinstance(texts, list) and all(isinstance(item, str) for item in texts):
        return {"model": MODEL_NAME, "dimension": EMBEDDINGS.dimension, "vectors": EMBEDDINGS.embed(texts)}

    raise HTTPException(status_code=400, detail='payload must include "text" string')


@app.post("/v1/ingest/file")
async def ingest_file(
    file: UploadFile = File(...),
    tenant_id: str = Form(...),
    region: str = Form(...),
    source_scope: str = Form(...),
    owner_acl_tags: str = Form(""),
) -> dict[str, object]:
    raw = await file.read()
    try:
        body, source_type = parse_uploaded_bytes(file.filename or "upload.txt", raw)
        residency = residency_from_region(region)
        tenant = Tenant(tenant_id=tenant_id, name=tenant_id, residency=residency, region=region)
        source = SourceDocument(
            tenant_id=tenant_id,
            region=region,
            document_id=document_id(tenant_id, file.filename or "upload", raw),
            title=file.filename or "uploaded-source",
            body=body,
            source_uri=f"upload://{file.filename or 'source'}",
            source_type=source_type,
            acl_scope=source_scope,
            owner_acl_tags=parse_acl_tags(owner_acl_tags),
        )
        result = PIPELINE.ingest(tenant, source)
    except UnsupportedFileType as exc:
        raise HTTPException(status_code=415, detail=str(exc)) from exc
    except ResidencyViolation as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc

    return {
        "tenant_id": result.tenant_id,
        "document_id": result.document_id,
        "region": result.region,
        "chunks_written": result.chunks_written,
        "min_freshness": result.min_freshness,
    }


def parse_acl_tags(value: str) -> list[str]:
    return [tag.strip() for tag in value.split(",") if tag.strip()]


def document_id(tenant_id: str, filename: str, raw: bytes) -> str:
    digest = sha256(tenant_id.encode("utf-8") + b":" + filename.encode("utf-8") + b":" + raw).hexdigest()
    return f"doc_{digest[:20]}"
