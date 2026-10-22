"""
embed-server/server.py

OpenAI-compatible /v1/embeddings endpoint backed by the local BGE model.
Meilisearch's REST embedder calls this to compute vectors for indexed documents.

Model dimensions: 768 (bge-base-en-v1.5)
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import uvicorn
from fastapi import FastAPI
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

MODEL_PATH: str = os.getenv("MODEL_PATH", "/model")

# Loaded once on startup — keeps latency low for Meilisearch callbacks.
_model: SentenceTransformer | None = None


def get_model() -> SentenceTransformer:
    global _model
    if _model is None:
        _model = SentenceTransformer(str(Path(MODEL_PATH)))
    return _model


app = FastAPI(title="BGE Embed Server")


@app.on_event("startup")  # type: ignore[untyped-decorator]
async def preload() -> None:
    """Preload model at container startup so first embed call is not slow."""
    get_model()


@app.get("/health")  # type: ignore[untyped-decorator]
def health() -> dict[str, str]:
    return {"status": "ok"}


class EmbedRequest(BaseModel):  # type: ignore[misc]
    input: list[str] | str
    model: str = "bge-base-en-v1.5"


@app.post("/v1/embeddings")  # type: ignore[untyped-decorator]
def embed(req: EmbedRequest) -> Any:
    """
    Returns OpenAI-format embeddings.

    Meilisearch REST embedder config:
      url:               http://embed-server:8080/v1/embeddings
      inputField:        ["input"]
      pathToEmbeddings:  ["data"]
      embeddingObject:   ["embedding"]
      dimensions:        768
    """
    inputs = req.input if isinstance(req.input, list) else [req.input]
    vecs = get_model().encode(inputs, normalize_embeddings=True).tolist()
    return {
        "object": "list",
        "model": req.model,
        "data": [
            {"object": "embedding", "index": i, "embedding": v}
            for i, v in enumerate(vecs)
        ],
    }


if __name__ == "__main__":
    uvicorn.run("server:app", host="0.0.0.0", port=8080)
