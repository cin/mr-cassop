ARG CASSANDRA_VERSION=4.1.12
ARG GO_VERSION=1.24.13
ARG GOSU_VERSION=1.19
FROM golang:${GO_VERSION}-bookworm AS gosu-builder
ARG GOSU_VERSION

RUN set -eux; \
  GOBIN=/out go install github.com/tianon/gosu@${GOSU_VERSION}; \
  /out/gosu --version

FROM cassandra:${CASSANDRA_VERSION}
ARG JMX_EXPORTER_VERSION=1.6.0
ENV INSTACLUSTR_CASSANDRA_EXPORTER_VERSION=0.9.14
ENV DATASTAX_CASSANDRA_EXPORTER_VERSION=0.3.6

COPY --from=gosu-builder /out/gosu /usr/local/bin/gosu

RUN set -eux; \
  apt-get update && apt-get install -y --no-install-recommends \
    curl \
    dnsutils \
    jq \
    net-tools \
    s3cmd \
    httpie \
    wget; \
  mkdir -p /prometheus; \
  wget https://github.com/prometheus/jmx_exporter/releases/download/${JMX_EXPORTER_VERSION}/jmx_prometheus_javaagent-${JMX_EXPORTER_VERSION}.jar -O /prometheus/jmx_prometheus_javaagent.jar; \
  wget https://github.com/instaclustr/cassandra-exporter/releases/download/v${INSTACLUSTR_CASSANDRA_EXPORTER_VERSION}/cassandra-exporter-agent-${INSTACLUSTR_CASSANDRA_EXPORTER_VERSION}.jar -O /prometheus/cassandra-exporter-agent.jar; \
  # Cassandra 4.1+ needs the "-4.1-beta1" build - the default build throws
  # "NoSuchFieldError: scheduledTasks" from UnixSocketClient.initCollectdHealthCheck
  # on startup against 4.1's internals. See datastax/metric-collector-for-apache-cassandra#79.
  wget https://github.com/datastax/metric-collector-for-apache-cassandra/releases/download/v${DATASTAX_CASSANDRA_EXPORTER_VERSION}/datastax-mcac-agent-${DATASTAX_CASSANDRA_EXPORTER_VERSION}-4.1-beta1.tar.gz -O /tmp/datastax-mcac-agent.tgz; \
  tar zxf /tmp/datastax-mcac-agent.tgz -C /prometheus; \
  mv /prometheus/datastax-mcac-agent-* /prometheus/datastax-mcac-agent; \
  rm -rf /var/lib/apt/lists/* /tmp/datastax-mcac-agent.tgz /prometheus/datastax-mcac-agent/config; \
  mkdir /home/cassandra; \
  chown cassandra:cassandra /home/cassandra /opt/cassandra /prometheus; \
  ln -sf /proc/mounts /etc/mtab; \
  # This image runs on JDK 11+, so /etc/cassandra/jvm11-server.options is the file that's
  # actually loaded. It ships with CMS active by default - CMS was deprecated in JDK 9 and
  # is removed entirely in JDK 14+, so it's a dead end for any future JDK upgrade, not just
  # a suboptimal GC choice. Switch to G1 instead: comment out the CMS block, uncomment G1's
  # own recommended settings, and lower InitiatingHeapOccupancyPercent from the stock 70
  # (Cassandra's comment only calls that out for heaps >= 16GB - 25 is the widely
  # recommended value for the much smaller heaps most Cassandra pods actually run).
  sed -i \
    -e 's/^-XX:+UseConcMarkSweepGC$/#-XX:+UseConcMarkSweepGC/' \
    -e 's/^-XX:+CMSParallelRemarkEnabled$/#-XX:+CMSParallelRemarkEnabled/' \
    -e 's/^-XX:CMSInitiatingOccupancyFraction=75$/#-XX:CMSInitiatingOccupancyFraction=75/' \
    -e 's/^-XX:+UseCMSInitiatingOccupancyOnly$/#-XX:+UseCMSInitiatingOccupancyOnly/' \
    -e 's/^-XX:CMSWaitDuration=10000$/#-XX:CMSWaitDuration=10000/' \
    -e 's/^-XX:+CMSParallelInitialMarkEnabled$/#-XX:+CMSParallelInitialMarkEnabled/' \
    -e 's/^-XX:+CMSEdenChunksRecordAlways$/#-XX:+CMSEdenChunksRecordAlways/' \
    -e 's/^-XX:+CMSClassUnloadingEnabled$/#-XX:+CMSClassUnloadingEnabled/' \
    -e 's/^#-XX:+UseG1GC$/-XX:+UseG1GC/' \
    -e 's/^#-XX:+ParallelRefProcEnabled$/-XX:+ParallelRefProcEnabled/' \
    -e 's/^#-XX:G1RSetUpdatingPauseTimePercent=5$/-XX:G1RSetUpdatingPauseTimePercent=5/' \
    -e 's/^#-XX:MaxGCPauseMillis=300$/-XX:MaxGCPauseMillis=300/' \
    -e 's/^#-XX:InitiatingHeapOccupancyPercent=70$/-XX:InitiatingHeapOccupancyPercent=25/' \
    /etc/cassandra/jvm11-server.options

WORKDIR /home/cassandra

USER cassandra

COPY .bashrc ./
