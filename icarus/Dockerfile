FROM eclipse-temurin:11-jre-noble

# POC: icarus.jar here is a local build from https://github.com/cin/icarus, branch
# poc/esop-4.1.12 (esop 3.0.1 -> 4.1.12, see cin/icarus#1), not a GitHub release --
# instaclustr/icarus is read-only to us. Rebuild it with:
#   git clone -b poc/esop-4.1.12 https://github.com/cin/icarus.git
#   cd icarus && ./mvnw -q -DskipTests -Dmaven.javadoc.skip=true clean package
#   cp target/icarus.jar <this dir>/icarus.jar
COPY icarus.jar .

RUN groupadd --gid 999 cassandra && \
    useradd --uid 999 --gid 999 --create-home cassandra

RUN apt-get update && apt-get -y upgrade && rm -rf /var/lib/apt/lists/*

USER cassandra

ENTRYPOINT ["java", "-jar", "icarus.jar", "icarus"]
