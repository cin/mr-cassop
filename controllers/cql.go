package controllers

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
)

func newCassandraConfig(cc *v1alpha1.CassandraCluster, adminRole string, adminPwd string, logr *zap.SugaredLogger) *gocql.ClusterConfig {
	cassCfg := gocql.NewCluster(fmt.Sprintf("%s.%s.svc.cluster.local", names.DCService(cc.Name, cc.Spec.DCs[0].Name), cc.Namespace))
	cassCfg.Authenticator = &gocql.PasswordAuthenticator{
		Username: adminRole,
		Password: adminPwd,
	}

	cassCfg.Timeout = 6 * time.Second
	cassCfg.ConnectTimeout = 6 * time.Second
	cassCfg.Consistency = gocql.LocalQuorum
	cassCfg.ReconnectionPolicy = &gocql.ConstantReconnectionPolicy{
		MaxRetries: 3,
		Interval:   time.Second * 1,
	}

	if logr == nil {
		logr = zap.NewNop().Sugar()
	}
	cassCfg.Logger = &gocqlLoggerWrapper{SugaredLogger: logr}

	if cc.Spec.Encryption.Client.Enabled {
		cassCfg.SslOpts = &gocql.SslOptions{
			CertPath: fmt.Sprintf("%s/%s", names.OperatorClientTLSDir(cc),
				cc.Spec.Encryption.Client.NodeTLSSecret.CrtFileKey),
			KeyPath: fmt.Sprintf("%s/%s", names.OperatorClientTLSDir(cc),
				cc.Spec.Encryption.Client.NodeTLSSecret.FileKey),
			CaPath: fmt.Sprintf("%s/%s", names.OperatorClientTLSDir(cc),
				cc.Spec.Encryption.Client.NodeTLSSecret.CACrtFileKey),
			EnableHostVerification: false,
		}
	}

	return cassCfg
}

type gocqlLoggerWrapper struct {
	*zap.SugaredLogger
}

func (w *gocqlLoggerWrapper) Print(v ...interface{}) {
	w.SugaredLogger.Debug(v)
}

func (w *gocqlLoggerWrapper) Printf(format string, v ...interface{}) {
	w.SugaredLogger.Debugf(format, v...)
}

func (w *gocqlLoggerWrapper) Println(v ...interface{}) {
	w.SugaredLogger.Debug(v...)
}

func (w *gocqlLoggerWrapper) Error(msg string, fields ...gocql.LogField) {
	w.SugaredLogger.Errorw(msg, logFields(fields)...)
}

func (w *gocqlLoggerWrapper) Warning(msg string, fields ...gocql.LogField) {
	w.SugaredLogger.Warnw(msg, logFields(fields)...)
}

func (w *gocqlLoggerWrapper) Info(msg string, fields ...gocql.LogField) {
	w.SugaredLogger.Infow(msg, logFields(fields)...)
}

func (w *gocqlLoggerWrapper) Debug(msg string, fields ...gocql.LogField) {
	w.SugaredLogger.Debugw(msg, logFields(fields)...)
}

func logFields(fields []gocql.LogField) []interface{} {
	if len(fields) == 0 {
		return nil
	}

	args := make([]interface{}, 0, len(fields)*2)
	for _, field := range fields {
		args = append(args, field.Name, field.Value.Any())
	}
	return args
}
