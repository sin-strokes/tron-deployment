# Embedded by trond — `build.image_strategy: jar-wrap` Phase 5d.
#
# Trond first builds the JAR via its standard Phase 1-2 path (full
# cache reuse + per-platform pinned JDK builder), then runs
# `docker build` against THIS Dockerfile inside a per-cache-key
# context directory containing the JAR.
#
# Placeholders substituted by image_wrap.go before docker build:
#   {{BASE_IMAGE}}        — full ref @ digest of the pinned runtime JDK.
#   {{JAR_NAME}}          — basename of the JAR file in the build context.
#   {{ARCH_TRIPLET}}      — Debian arch triplet for the tcmalloc lib path
#                           (x86_64-linux-gnu or aarch64-linux-gnu).
#   {{SOURCE_REVISION}}   — git sha of java-tron at build time.
#   {{CACHE_KEY}}         — trond cache key (lets ops grep `docker images`
#                           for the trond build that produced an image).
#   {{BUILD_TIME}}        — RFC 3339 UTC timestamp.

FROM {{BASE_IMAGE}}

# tcmalloc — java-tron's allocator pressure under sustained RPS is
# a known footgun on glibc malloc; tron-docker's upstream image
# applies the same LD_PRELOAD. Install the minimal variant (smaller
# layer) and pin to the arch-specific library path.
#
# This RUN sits BEFORE the COPY so the apt layer is cached across
# JAR-only rebuilds — only the artifact layer rebuilds when the
# source changes.
RUN apt-get update -qq \
 && apt-get install -qq -y --no-install-recommends libtcmalloc-minimal4 \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*
ENV LD_PRELOAD="/usr/lib/{{ARCH_TRIPLET}}/libtcmalloc_minimal.so.4"
ENV TCMALLOC_RELEASE_RATE=10

WORKDIR /opt/tron
COPY {{JAR_NAME}} /opt/tron/FullNode.jar

# Default heap is intentionally small so the image is portable; the
# operator's intent.yaml (rendered via trond's RenderCompose) is
# expected to override JAVA_OPTS / args at runtime.
ENV JAVA_OPTS="-Xmx1g"

# OCI image annotations + a trond-specific label for traceability —
# any docker image inspector can answer "what trond build produced
# this image" without round-tripping through the manifest file.
LABEL org.opencontainers.image.title="java-tron"
LABEL org.opencontainers.image.source="https://github.com/tronprotocol/java-tron"
LABEL org.opencontainers.image.revision="{{SOURCE_REVISION}}"
LABEL org.opencontainers.image.created="{{BUILD_TIME}}"
LABEL org.opencontainers.image.vendor="trond build pipeline"
LABEL trond.cache_key="{{CACHE_KEY}}"

# ENTRYPOINT — not CMD — so docker compose's `command:` adds
# arguments after `java -jar /opt/tron/FullNode.jar`, which is
# exactly what java-tron expects (-c <config.conf>, --witness, ...).
ENTRYPOINT ["sh", "-c", "exec java $JAVA_OPTS -jar /opt/tron/FullNode.jar \"$@\"", "--"]
