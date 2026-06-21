FROM alpine AS build
ARG JOLOKIA_VERSION="2.6.0"

RUN wget -O jolokia.tar.gz https://github.com/rhuss/jolokia/releases/download/v${JOLOKIA_VERSION}/jolokia-${JOLOKIA_VERSION}-bin.tar.gz;
RUN tar -C /tmp/ -xzf jolokia.tar.gz;
RUN cp /tmp/jolokia-${JOLOKIA_VERSION}/agents/jolokia-agent-unsecured.war /tmp

FROM tomcat:10.1-jdk17-temurin
ENV JOLOKIA_JSR160_PROXY_ENABLED=true
ENV JOLOKIA_RESPONSE_TIMEOUT=30000

RUN apt-get update && apt-get upgrade -y && apt-get clean

RUN groupadd --gid 901 jolokia && \
    useradd --uid 901 --gid 901 --create-home jolokia

COPY setenv.sh bin/
COPY --from=build /tmp/jolokia-agent-unsecured.war $CATALINA_HOME/webapps/jolokia.war
RUN rm -rf /usr/local/tomcat/webapps.dist/manager \
    /usr/local/tomcat/webapps.dist/docs \
    /usr/local/tomcat/webapps.dist/examples \
    /usr/local/tomcat/webapps.dist/host-manager \
    /usr/local/tomcat/webapps.dist/ROOT && \
    chown -R jolokia:jolokia $CATALINA_HOME/webapps/

USER jolokia
