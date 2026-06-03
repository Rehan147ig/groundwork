FROM python:3.13-slim
WORKDIR /app
ENV EMBEDDING_MODEL=sentence-transformers/all-MiniLM-L6-v2
ENV EMBEDDING_CACHE_DIR=/models/fastembed
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY groundwork_ingestion ./groundwork_ingestion
EXPOSE 8090
EXPOSE 8000
CMD ["uvicorn", "groundwork_ingestion.worker:app", "--host", "0.0.0.0", "--port", "8000"]
