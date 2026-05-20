# Embedded by trond — `build.image_strategy: jar-wrap` Phase 5d.
#
# Trond first builds the JAR via its standard Phase 1-2 path (full
# cache reuse + per-platform pinned JDK builder), then runs
# `docker build` against THIS Dockerfile inside a per-cache-key
# context directory containing the JAR. The result is a runnable
# Docker image that bakes in:
#
#   - the SAME pinned eclipse-temurin runtime that compiled the JAR
#     (no version drift between compile-time and runtime JVM), and
#   - the FullNode JAR at a predictable path, with a vanilla
#     `java -jar` ENTRYPOINT so compose / k8s / docker run all "just
#     work" with no command override required.
#
# Placeholders substituted by image_wrap.go before docker build:
#   {{BASE_IMAGE}} — full ref @ digest of the pinned runtime JDK.
#   {{JAR_NAME}}   — basename of the JAR file in the build context.
FROM {{BASE_IMAGE}}

WORKDIR /opt/tron
COPY {{JAR_NAME}} /opt/tron/FullNode.jar

# Default heap is intentionally small so the image is portable; the
# operator's intent.yaml (rendered via trond's RenderCompose) is
# expected to override JAVA_OPTS / args at runtime.
ENV JAVA_OPTS="-Xmx1g"

# ENTRYPOINT — not CMD — so docker compose's `command:` adds
# arguments after `java -jar /opt/tron/FullNode.jar`, which is
# exactly what java-tron expects (-c <config.conf>, --witness, ...).
ENTRYPOINT ["sh", "-c", "exec java $JAVA_OPTS -jar /opt/tron/FullNode.jar \"$@\"", "--"]
