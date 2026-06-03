from __future__ import annotations

from dataclasses import dataclass, field
from hashlib import sha256
from time import time


@dataclass(frozen=True)
class Tenant:
    tenant_id: str
    name: str
    residency: str
    region: str
    kms_key_ref: str | None = None


@dataclass(frozen=True)
class SourceDocument:
    tenant_id: str
    region: str
    document_id: str
    title: str
    body: str
    source_uri: str
    source_type: str
    acl_scope: str
    owner_acl_tags: list[str] = field(default_factory=list)
    data_subject_ids: list[str] = field(default_factory=list)
    modified_at: float = field(default_factory=time)


@dataclass(frozen=True)
class Chunk:
    tenant_id: str
    region: str
    document_id: str
    chunk_id: str
    chunk_hash: str
    text: str
    metadata_prefix: str
    page: int
    offset: int
    acl_scope: str
    owner_acl_tags: list[str]
    freshness_score: float
    data_subject_ids: list[str]
    embedding: list[float] = field(default_factory=list)
    soft_deleted: bool = False

    @property
    def indexed_text(self) -> str:
        return f"{self.metadata_prefix}\n\n{self.text}".strip()


@dataclass(frozen=True)
class Microsoft365OAuthSchema:
    tenant_id: str
    client_id: str
    redirect_uri: str
    scopes: tuple[str, ...] = (
        "offline_access",
        "openid",
        "profile",
        "Sites.Read.All",
        "Files.Read.All",
        "User.Read.All",
        "Group.Read.All",
    )

    @property
    def authorize_url(self) -> str:
        scope = "%20".join(self.scopes)
        return (
            f"https://login.microsoftonline.com/{self.tenant_id}/oauth2/v2.0/authorize"
            f"?client_id={self.client_id}&response_type=code&redirect_uri={self.redirect_uri}&scope={scope}"
        )


def stable_chunk(document_id: str, offset: int, text: str) -> tuple[str, str]:
    digest = sha256(f"{document_id}:{offset}:{text}".encode("utf-8")).hexdigest()
    return f"chk_{digest[:20]}", digest
