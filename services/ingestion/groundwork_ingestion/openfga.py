from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import requests


@dataclass
class OpenFGAAuthorizer:
    """OpenFGA client for the ingestion service.

    Scope intentionally narrow: this client discovers/creates the shared OpenFGA
    store and writes tuples (group memberships, per-document grants). It does NOT
    write the authorization model. ``services/query-runtime`` is the sole owner of
    the OpenFGA authorization model.

    Why: previously this module also POSTed an authorization model on first
    bootstrap. Because ingestion typically starts before query-runtime, its
    (older, 3-type) model won the race and overwrote query-runtime's newer
    4-type folder-inheriting model — making every folder-grant permission
    check silently fail closed. Removing the model write here makes ownership
    unambiguous and eliminates the race.
    """

    endpoint: str
    store_name: str = "groundwork_local"
    timeout: float = 2.0

    def __post_init__(self) -> None:
        self.endpoint = self.endpoint.rstrip("/")
        self.store_id: str | None = None
        self.ready = False

    def ensure(self) -> None:
        if self.ready:
            return
        self.store_id = self._ensure_store()
        # Default demo memberships. These are also written by query-runtime's
        # seedDefaultMemberships() when it provisions or upgrades the model, so
        # the runtime guarantees they exist even if this write is skipped below
        # because the model hasn't been written yet at bootstrap.
        self._write_tuples(
            [
                {"user": "user:finance_user", "relation": "member", "object": "group:finance"},
                {"user": "user:executive_user", "relation": "member", "object": "group:executive"},
                {"user": "user:security_user", "relation": "member", "object": "group:security"},
            ]
        )
        self.ready = True

    def grant_document(self, document_id: str, owner_acl_tags: list[str]) -> None:
        self.ensure()
        tuples = [
            {
                "user": f"group:{normalize_relation_part(tag)}#member",
                "relation": "viewer",
                "object": f"document:{document_id}",
            }
            for tag in owner_acl_tags
            if tag.strip()
        ]
        self._write_tuples(tuples)

    def _ensure_store(self) -> str:
        stores = self._get("/stores").get("stores", [])
        for store in stores:
            if store.get("name") == self.store_name:
                return str(store["id"])
        created = self._post("/stores", {"name": self.store_name})
        return str(created["id"])

    def _write_tuples(self, tuples: list[dict[str, str]]) -> None:
        if not tuples:
            return
        try:
            self._post(f"/stores/{self.store_id}/write", {"writes": {"tuple_keys": tuples}})
        except requests.HTTPError as exc:
            text = (exc.response.text if exc.response is not None else "").lower()
            # Tolerated:
            #   - "already" / "already exists" -> idempotent re-write.
            #   - "no authorization model" / "no writable authorization model" /
            #     "authorization_model_not_found" -> query-runtime has not yet
            #     written its model. The tuples here are best-effort default
            #     memberships that query-runtime also writes; skip and continue.
            tolerated = (
                "already" in text
                or "no authorization model" in text
                or "no writable authorization model" in text
                or "authorization_model_not_found" in text
            )
            if tolerated:
                return
            raise

    def _get(self, path: str) -> dict[str, Any]:
        response = requests.get(f"{self.endpoint}{path}", timeout=self.timeout)
        response.raise_for_status()
        return response.json()

    def _post(self, path: str, payload: dict[str, Any]) -> dict[str, Any]:
        response = requests.post(f"{self.endpoint}{path}", json=payload, timeout=self.timeout)
        response.raise_for_status()
        if not response.content:
            return {}
        return response.json()


def normalize_relation_part(value: str) -> str:
    return value.strip().lower().replace(" ", "_")
