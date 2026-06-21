FROM eclipse-temurin:11-jre-noble
ARG ICARUS_VERSION=3.0.0

RUN wget https://github.com/instaclustr/icarus/releases/download/icarus-${ICARUS_VERSION}/icarus.jar

RUN groupadd --gid 999 cassandra && \
    useradd --uid 999 --gid 999 --create-home cassandra

RUN apt-get update && apt-get -y upgrade && rm -rf /var/lib/apt/lists/*

USER cassandra

ENTRYPOINT ["java", "-jar", "icarus.jar", "icarus"]
