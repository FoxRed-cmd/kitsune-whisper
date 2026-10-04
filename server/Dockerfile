# syntax=docker/dockerfile:1

# One recipe builds both compose profiles; only the base image (and the CUDA
# extra) differ:
#
#   CPU: --build-arg BASE_IMAGE=python:3.11-slim
#   GPU: --build-arg BASE_IMAGE=nvidia/cuda:12.3.2-cudnn9-runtime-ubuntu22.04 \
#        --build-arg UV_SYNC_ARGS="--extra cuda"
ARG BASE_IMAGE=python:3.11-slim
FROM ${BASE_IMAGE}

# uv provisions Python 3.11 on the CUDA base (a runtime image with no Python),
# so the Debian CPU image and the Ubuntu GPU image share one dependency step.
COPY --from=ghcr.io/astral-sh/uv:0.12.19 /uv /uvx /bin/

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    UV_PYTHON=3.11 \
    UV_PROJECT_ENVIRONMENT=/opt/venv \
    KITSUNE_SERVER_HOST=0.0.0.0 \
    KITSUNE_SERVER_PORT=8000 \
    KITSUNE_SERVER_DOWNLOAD_ROOT=/models \
    PATH="/opt/venv/bin:$PATH"

WORKDIR /app

COPY pyproject.toml uv.lock ./
COPY kitsune_server ./kitsune_server

ARG UV_SYNC_ARGS=""
RUN uv sync --frozen --no-dev --no-editable ${UV_SYNC_ARGS}

EXPOSE 8000
VOLUME ["/models"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=300s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/health')"

ENTRYPOINT ["kitsune-server"]
CMD ["--config", "/config/kitsune.yaml"]
