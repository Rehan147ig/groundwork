from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import requests


def authorization_model() -> dict[str, Any]:
    return {
        "schema_version": "1.1",
        "type_definitions": [
            {"type": "user"},
            {
                "type": "group",
                "relations": {"member": {"this": {}}},
                "metadata": {
                    "relations": {
                        "member": {
                            "directly_related_user_types": [{"type": "user"}],
                        }
                    }
                },
            },
            {
                "type": "document",
                "relations": {"viewer": {"this": {}}},
                "metadata": {
                    "relations": {
                        "viewer": {
                            "directly_related_user_types": [
                                {"type": "user"},
                                {"type": "group", "relation": "member"},
                            ],
                        }
                    }
                },
            },
        ],
    }


@dataclass
class OpenFGAAuthorizer:
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
        self._post(f"/stores/{self.store_id}/authorization-models", authorization_model())
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
            if "already" not in exc.response.text.lower():
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
