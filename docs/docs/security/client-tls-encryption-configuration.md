---
title: Client TLS Encryption configuration
slug: /client-tls-encryption-configuration
---

## Cassandra Client TLS Encryption configuration

By default, the Client TLS Encryption is disabled.

## Client TLS Encryption Field Specification Reference

| Field                                                      | Description                                                                                                                                                                                | Is Required | Default          |
|------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------|------------------|
| `encryption.client                                 `       | Client TLS Encryption configuration.                                                                                                                                                       | `N`         |                  |
| `encryption.client.enabled                         `       | Enables Client Encryption.                                                                                                                                                                 | `N`         | `false`          |
| `encryption.client.optional                        `       | If enabled both encrypted and unencrypted connections are allowed.                                                                                                                         | `N`         | `false`          |
| `encryption.client.requireClientAuth               `       | Enables or disables Two-Way TLS authentication. If enabled the server verifies the certificate used by the client. The client certificate should be signed by a CA that the server trusts. | `N`         | `true`           |
| `encryption.client.caTLSSecret`                            | CA TLS Secret fields configuration.                                                                                                                                                        | `N`         | ``               |
| `encryption.client.caTLSSecret.name`                       | CA TLS Secret name which stores TLS CA data.                                                                                                                                               | `N`         | ``               |
| `encryption.client.caTLSSecret.fileKey`                    | CA TLS Secret fields which holds TLS CA key file.                                                                                                                                          | `N`         | `ca.key`         |
| `encryption.client.caTLSSecret.crtFileKey`                 | CA TLS Secret fields which holds TLS CA certificate file.                                                                                                                                  | `N`         | `ca.crt`         |
| `encryption.client.nodeTLSSecret`                          | Node TLS Secret name which stores TLS Node data.                                                                                                                                           | `N`         | ``               |
| `encryption.client.nodeTLSSecret.name`                     | Name of Node TLS Secret.                                                                                                                                                                   | `N`         | ``               |
| `encryption.client.nodeTLSSecret.keystoreFileKey`          | Node TLS Secret field which holds Keystore file. Keystore should contain keypair chains.                                                                                                   | `N`         | `keystore.p12`   |
| `encryption.client.nodeTLSSecret.keystorePasswordKey`      | Node TLS Secret field which holds password for Keystore. The password must match that one is used when generating the Keystore.                                                            | `N`         | `keystore.password`      |
| `encryption.client.nodeTLSSecret.truststoreFileKey`        | Node TLS Secret field which holds Truststore file. Truststore should contain chain of trusted CA certificates.                                                                             | `N`         | `truststore.p12` |
| `encryption.client.nodeTLSSecret.truststorePasswordKey`    | Node TLS Secret field which holds password for Truststore. The password must match that one is used when generating the Truststore.                                                        | `N`         | `truststore.password`      |
| `encryption.client.nodeTLSSecret.generateKeystorePassword` | Node TLS Secret field which holds password to encrypt keystore and truststore during generating. It's used only in case encryption.client.caTLSSecret.name is provided.                    | `N`         | `cassandra`      |
| `encryption.client.nodeTLSSecret.caFileKey`                | Node TLS Secret field which holds ca certificate file.                                                                                                                                     | `N`         | `ca.crt`         |
| `encryption.client.nodeTLSSecret.crtFileKey`               | Node TLS Secret field which holds TLS certificate file.                                                                                                                                    | `N`         | `tls.crt`        |
| `encryption.client.nodeTLSSecret.fileKey`            | Node TLS Secret field which holds TLS private key file.                                                                                                                                            | `N`         | `tls.key`        |
| `encryption.client.protocol`                               | Cryptographic protocol.                                                                                                                                                                    | `N`         | `TLS`            |
| `encryption.client.algorithm`                              | Key exchange or key agreement method.                                                                                                                                                      | `N`         | `SunX509`        |
| `encryption.client.storeType`                              | Archive format of Keystore.                                                                                                                                                                | `N`         | `PKCS12`            |
| `encryption.client.cipherSuites`                           | The list of cipher suites for the server to support, in order of preference.                                                                                                               | `N`         | ``               |

## JMX connections and hostname verification

When client encryption is enabled, JMX is encrypted too. The node certificate is shared by every node (the one the
operator generates, and the one `utils/tls.sh` generates, is `CN=localhost` with a `DNS:localhost` SAN), but the
components that talk to a node's JMX port, Jolokia in the Prober pod and Reaper, connect by pod IP. A hostname check
could never pass against such a certificate, so the operator starts Jolokia with
`-Djdk.rmi.ssl.client.enableEndpointIdentification=false`.

The connection is still authenticated: the node certificate must chain to the cluster CA, and with
`requireClientAuth: true` (the default) the node verifies the client certificate as well. Only the hostname/IP match is
skipped. `nodetool` inside a Cassandra pod connects to `127.0.0.1` and is not affected.

:::caution Known limitation: Reaper

Reaper does not start when `encryption.client.enabled` is `true`. Its Cassandra driver fails with
`Expected class com.datastax.oss.driver.internal.core.ssl.JdkSslHandlerFactory (specified by advanced.ssl-engine-factory.class)
to be a subtype of com.datastax.oss.driver.api.core.ssl.SslEngineFactory`. This happens with Reaper `4.2.5` and `5.0.1`
and comes from the `ssl: type: jdk` section that Reaper generates for native-protocol SSL, not from an operator setting.
Cassandra, the Prober and the operator are not affected: the cluster itself becomes ready, but the Reaper pod stays
unready, so repairs through Reaper are unavailable on a cluster with client TLS until this is fixed upstream.

:::

## Grant access to external clients

To grant access to C* nodes via CQL and/or JMX protocols the TLS Secret for client should be generated (signed) using the same CA.
