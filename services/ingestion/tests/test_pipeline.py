import unittest

from groundwork_ingestion.chunker import SemanticChunker
from groundwork_ingestion.embeddings import HashEmbeddingEngine
from groundwork_ingestion.indexes import InMemoryIndex
from groundwork_ingestion.models import Microsoft365OAuthSchema, SourceDocument, Tenant
from groundwork_ingestion.parser import parse_uploaded_bytes
from groundwork_ingestion.pipeline import IngestionPipeline
from groundwork_ingestion.payloads import uniform_payload
from groundwork_ingestion.residency import ResidencyViolation


class PipelineTests(unittest.TestCase):
    def test_ingestion_writes_chunks_to_both_indexes(self) -> None:
        body = "\n\n".join(
            f"Section {i}. Groundwork must enforce live ACL checks, immutable traces, and regional isolation before chunks reach the model prompt."
            for i in range(60)
        )
        tenant = Tenant(tenant_id="tenant_uk", name="UK Tenant", residency="uk", region="europe-west2")
        source = SourceDocument(
            tenant_id=tenant.tenant_id,
            region="europe-west2",
            document_id="doc_security",
            title="Security policy",
            body=body,
            source_uri="file://security-policy.md",
            source_type="markdown",
            acl_scope="platform",
        )
        vector = InMemoryIndex()
        lexical = InMemoryIndex()
        pipeline = IngestionPipeline(SemanticChunker(min_tokens=80, max_tokens=140, overlap_tokens=20), vector, lexical, HashEmbeddingEngine())

        result = pipeline.ingest(tenant, source)

        self.assertGreater(result.chunks_written, 1)
        self.assertEqual(len(vector.records), result.chunks_written)
        self.assertEqual(len(lexical.records), result.chunks_written)
        sample = next(iter(vector.records.values()))
        self.assertIn("chunk_hash", sample)
        self.assertIn("text", sample)
        self.assertEqual(sample["metadata"]["tenant_id"], "tenant_uk")
        self.assertEqual(sample["metadata"]["region"], "europe-west2")
        self.assertEqual(sample["metadata"]["source_scope"], "platform")
        self.assertEqual(len(sample["vector"]), 384)

    def test_residency_violation_blocks_ingestion(self) -> None:
        tenant = Tenant(tenant_id="tenant_eu", name="EU Tenant", residency="eu", region="europe-west1")
        source = SourceDocument(
            tenant_id=tenant.tenant_id,
            region="us-central1",
            document_id="doc_wrong_region",
            title="Wrong region",
            body="This should not be ingested into the wrong region.",
            source_uri="file://wrong.txt",
            source_type="text",
            acl_scope="platform",
        )
        pipeline = IngestionPipeline(SemanticChunker(), InMemoryIndex(), InMemoryIndex(), HashEmbeddingEngine())
        with self.assertRaises(ResidencyViolation):
            pipeline.ingest(tenant, source)

    def test_microsoft365_oauth_schema_contains_sharepoint_scopes(self) -> None:
        schema = Microsoft365OAuthSchema(
            tenant_id="contoso",
            client_id="client-id",
            redirect_uri="http://localhost:3000/api/connectors/microsoft/callback",
        )
        self.assertIn("Sites.Read.All", schema.scopes)
        self.assertIn("Files.Read.All", schema.scopes)
        self.assertIn("oauth2", schema.authorize_url)

    def test_uniform_payload_contract(self) -> None:
        source = SourceDocument(
            tenant_id="tenant_us",
            region="us",
            document_id="doc_contract",
            title="Contract",
            body="Groundwork stores uniform payload metadata for both Qdrant and Elasticsearch.",
            source_uri="upload://contract.txt",
            source_type="text",
            acl_scope="SharePoint",
            owner_acl_tags=["legal", "security"],
        )
        chunk = SemanticChunker(min_tokens=5, max_tokens=80, overlap_tokens=0).chunk(source)[0]
        payload = uniform_payload(chunk)
        self.assertIsInstance(payload["chunk_hash"], str)
        self.assertEqual(payload["text"], chunk.text)
        self.assertEqual(payload["metadata"]["tenant_id"], "tenant_us")
        self.assertEqual(payload["metadata"]["region"], "us")
        self.assertEqual(payload["metadata"]["source_scope"], "SharePoint")
        self.assertEqual(payload["metadata"]["owner_acl_tags"], ["legal", "security"])

    def test_uploaded_plaintext_parser(self) -> None:
        text, source_type = parse_uploaded_bytes("policy.txt", b"Security policy text")
        self.assertEqual(text, "Security policy text")
        self.assertEqual(source_type, "txt")


if __name__ == "__main__":
    unittest.main()
