# POC: builds icarus.jar from https://github.com/cin/icarus, tag icarus-4.1.13
# (esop 4.1.12 -> 4.1.13, see cin/icarus#2) instead of wget-ing a GitHub release --
# instaclustr/icarus is read-only to us, so we pin to our fork's own release tag.
# Building it in-image (rather than depending on a host-local jar) keeps this
# Dockerfile buildable in CI with no local prerequisites.
FROM eclipse-temurin:11-jdk-noble AS builder
ARG ICARUS_FORK_REPO=https://github.com/cin/icarus.git
ARG ICARUS_FORK_REF=icarus-4.1.13

RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /build
RUN git clone --branch "${ICARUS_FORK_REF}" --depth 1 "${ICARUS_FORK_REPO}" .
RUN ./mvnw -B -q -DskipTests -Dmaven.javadoc.skip=true clean package

FROM eclipse-temurin:11-jre-noble
COPY --from=builder /build/target/icarus.jar .

RUN groupadd --gid 999 cassandra && \
    useradd --uid 999 --gid 999 --create-home cassandra

RUN apt-get update && apt-get -y upgrade && rm -rf /var/lib/apt/lists/*

USER cassandra

ENTRYPOINT ["java", "-jar", "icarus.jar", "icarus"]
