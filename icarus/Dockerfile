# POC: builds icarus.jar from https://github.com/cin/icarus, branch poc/esop-4.1.12
# (esop 3.0.1 -> 4.1.12, see cin/icarus#1) instead of wget-ing a GitHub release --
# instaclustr/icarus is read-only to us, and no release exists for a fork build.
# Building it in-image (rather than depending on a host-local jar) keeps this
# Dockerfile buildable in CI with no local prerequisites.
FROM eclipse-temurin:11-jdk-noble AS builder
ARG ICARUS_FORK_REPO=https://github.com/cin/icarus.git
ARG ICARUS_FORK_REF=poc/esop-4.1.12

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
