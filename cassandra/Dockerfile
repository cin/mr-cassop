ARG CASSANDRA_VERSION=5.0.9
ARG GO_VERSION=1.26
ARG GOSU_VERSION=1.19
FROM golang:${GO_VERSION}-trixie AS gosu-builder
ARG GOSU_VERSION

RUN set -eux; \
  GOBIN=/out go install github.com/tianon/gosu@${GOSU_VERSION}; \
  /out/gosu --version

FROM cassandra:${CASSANDRA_VERSION}
ARG JMX_EXPORTER_VERSION=1.6.0
# Cassandra 5 requires JDK 17 (see the JDK 17 comment below), which lifts the
# class-file-version ceiling that previously pinned this to 2.3.0 on the JDK
# 11 4.1.x image -- kept in sync with jolokia/Dockerfile's JOLOKIA_VERSION now.
ARG JOLOKIA_JSON_VERSION=2.6.2

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
  rm -rf /var/lib/apt/lists/*; \
  # jolokia-json.jar on Cassandra's own classpath (anything under
  # /opt/cassandra/lib is picked up automatically) -- required for any JMX
  # operation invoked with a Map/List argument (e.g. StorageServiceMBean.
  # repairAsync's options map) to work through Jolokia's JSR-160 proxy mode.
  # Without it, such a call fails with "ClassNotFoundException:
  # org.jolokia.json.JSONObject" on the Cassandra side -- Jolokia's own docs
  # confirm this is expected, not a bug: the proxy target JVM needs this jar
  # itself, not just the Jolokia server doing the proxying.
  wget https://repo1.maven.org/maven2/org/jolokia/jolokia-json/${JOLOKIA_JSON_VERSION}/jolokia-json-${JOLOKIA_JSON_VERSION}.jar -O /opt/cassandra/lib/jolokia-json-${JOLOKIA_JSON_VERSION}.jar; \
  mkdir /home/cassandra; \
  chown cassandra:cassandra /home/cassandra /opt/cassandra /prometheus /opt/cassandra/lib/jolokia-json-${JOLOKIA_JSON_VERSION}.jar; \
  ln -sf /proc/mounts /etc/mtab; \
  # Cassandra 5 requires JDK 17, so /etc/cassandra/jvm17-server.options is the file that's
  # actually loaded (jvm11-server.options no longer applies). It ships with CMS active by
  # default - CMS was deprecated in JDK 9 and is removed entirely in JDK 14+, so it's a dead
  # end on JDK 17, not just a suboptimal GC choice. Switch to G1 instead: comment out the CMS
  # block, uncomment G1's own recommended settings, and lower InitiatingHeapOccupancyPercent
  # from the stock 70 (Cassandra's comment only calls that out for heaps >= 16GB - 25 is the
  # widely recommended value for the much smaller heaps most Cassandra pods actually run).
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
    /etc/cassandra/jvm17-server.options

WORKDIR /home/cassandra

USER cassandra

COPY .bashrc ./
