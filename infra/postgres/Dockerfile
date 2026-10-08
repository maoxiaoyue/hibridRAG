# Custom PostgreSQL 18.3 image with pg_jieba (Chinese FTS tokenizer).
#
# Build:
#   docker build -t <registry>/mcp-postgres:18.3-jieba -f infra/postgres/Dockerfile .
# Push:
#   docker push <registry>/mcp-postgres:18.3-jieba
# Deploy (single-node StatefulSet `postgres-std`):
#   kubectl -n <ns> set image statefulset/postgres-std postgres=<registry>/mcp-postgres:18.3-jieba
#   kubectl -n <ns> rollout status statefulset/postgres-std
# Activate (inside DB):
#   CREATE EXTENSION pg_jieba;
#
# Notes:
#   - Tries the v1.1.1 release first; falls back to master if it fails (PG 18
#     compatibility is not yet promised by upstream tags as of 2026-05).
#   - Uses official postgres:18.3 (debian-bookworm based). server-dev-18 is
#     pulled from PGDG via the bundled apt source already configured in the
#     base image.

FROM postgres:18.3

# Build deps
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        build-essential \
        cmake \
        git \
        ca-certificates \
        postgresql-server-dev-18; \
    rm -rf /var/lib/apt/lists/*

# Build pg_jieba — try tagged release, fall back to master on failure
RUN set -eux; \
    BUILD_DIR=/tmp/pg_jieba; \
    git clone --recurse-submodules https://github.com/jaiminpan/pg_jieba.git ${BUILD_DIR}; \
    cd ${BUILD_DIR}; \
    if git checkout v1.1.1 2>/dev/null && git submodule update --init --recursive; then \
        echo "Building pg_jieba v1.1.1"; \
    else \
        echo "v1.1.1 unavailable, staying on master"; \
        git checkout master; \
        git submodule update --init --recursive; \
    fi; \
    mkdir build && cd build; \
    cmake .. -DCMAKE_BUILD_TYPE=Release; \
    make -j"$(nproc)"; \
    make install; \
    cd / && rm -rf ${BUILD_DIR}

# Strip build deps to keep image lean
RUN set -eux; \
    apt-get purge -y --auto-remove \
        build-essential cmake git postgresql-server-dev-18; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

# Smoke test at build time: confirm pg_jieba.control got installed
RUN test -f "$(pg_config --sharedir)/extension/pg_jieba.control" \
    || (echo "pg_jieba install verification failed" && exit 1)
