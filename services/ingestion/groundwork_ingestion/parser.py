from __future__ import annotations

from pathlib import Path


class UnsupportedFileType(ValueError):
    pass


def parse_local_file(path: str) -> str:
    file_path = Path(path)
    suffix = file_path.suffix.lower()
    if suffix in {".txt", ".md", ".csv"}:
        return file_path.read_text(encoding="utf-8", errors="replace")
    if suffix == ".pdf":
        # Production adapter: run OCR and digital PDF extraction here.
        return file_path.read_bytes().decode("utf-8", errors="replace")
    if suffix in {".docx", ".pptx"}:
        # Production adapter: parse Word XML or slide-by-slide PPTX content here.
        return file_path.read_bytes().decode("utf-8", errors="replace")
    raise UnsupportedFileType(f"unsupported source type: {suffix}")


def parse_uploaded_bytes(filename: str, data: bytes) -> tuple[str, str]:
    suffix = Path(filename).suffix.lower()
    if suffix in {".txt", ".md", ".csv"}:
        return data.decode("utf-8", errors="replace"), suffix.removeprefix(".")
    if suffix == ".pdf":
        return data.decode("utf-8", errors="replace"), "pdf"
    if suffix in {".docx", ".pptx"}:
        return data.decode("utf-8", errors="replace"), suffix.removeprefix(".")
    raise UnsupportedFileType(f"unsupported source type: {suffix}")
