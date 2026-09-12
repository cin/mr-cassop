FROM alpine AS build
ARG JOLOKIA_VERSION="2.6.2"

RUN wget -O jolokia.tar.gz https://github.com/jolokia/jolokia/releases/download/v${JOLOKIA_VERSION}/jolokia-assembly-${JOLOKIA_VERSION}-bin.tar.gz;
RUN tar -C /tmp/ -xzf jolokia.tar.gz;
RUN cp /tmp/jolokia-${JOLOKIA_VERSION}/agents/jolokia-agent-unsecured.war /tmp

FROM tomcat:10.1-jdk17-temurin
ENV JOLOKIA_JSR160_PROXY_ENABLED=true
ENV JOLOKIA_RESPONSE_TIMEOUT=30000
# Jolokia 2.6.2 denies every JSR-160 proxy target unless it matches an allow-listed
# pattern (see jsr160-proxy-allowed-patterns.txt for why - fixes an incomplete-denylist
# JNDI injection bypass). Without this, Prober/nodectl can no longer reach any
# Cassandra node's JMX through this proxy at all.
ENV JOLOKIA_JSR160_PROXY_ALLOWED_TARGETS=/opt/jolokia/jsr160-proxy-allowed-patterns.txt

RUN apt-get update && apt-get upgrade -y && apt-get clean

RUN groupadd --gid 901 jolokia && \
    useradd --uid 901 --gid 901 --create-home jolokia

COPY setenv.sh bin/
COPY jsr160-proxy-allowed-patterns.txt /opt/jolokia/jsr160-proxy-allowed-patterns.txt
COPY --from=build /tmp/jolokia-agent-unsecured.war $CATALINA_HOME/webapps/jolokia.war
RUN rm -rf /usr/local/tomcat/webapps.dist/manager \
    /usr/local/tomcat/webapps.dist/docs \
    /usr/local/tomcat/webapps.dist/examples \
    /usr/local/tomcat/webapps.dist/host-manager \
    /usr/local/tomcat/webapps.dist/ROOT && \
    chown -R jolokia:jolokia $CATALINA_HOME/webapps/

USER jolokia
