/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"github.com/go-logr/zapr"
	"github.com/gocql/gocql"
	operatorCfg "github.com/ibm/cassandra-operator/controllers/config"
	"github.com/ibm/cassandra-operator/controllers/cql"
	"github.com/ibm/cassandra-operator/controllers/logger"
	"github.com/ibm/cassandra-operator/controllers/nodetool"
	"github.com/ibm/cassandra-operator/controllers/prober"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"

	dbv1alpha1 "github.com/ibm/cassandra-operator/api/v1alpha1"
	"github.com/ibm/cassandra-operator/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	Version = "undefined"
	scheme  = runtime.NewScheme()
)

const leaderElectionID = "cassandra-operator-leader-election-lock"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(dbv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	operatorConfig, err := operatorCfg.LoadConfig()
	if err != nil {
		fmt.Printf("unable to load operator config: %s", err.Error())
		os.Exit(1)
	}

	logr := logger.NewLogger(operatorConfig.LogFormat, operatorConfig.LogLevel)

	logr.Infof("Version: %s", Version)
	logr.Infof("Leader election enabled: %t", operatorConfig.LeaderElectionEnabled)
	logr.Infof("Log level: %s", operatorConfig.LogLevel.String())
	logr.Infof("Prometheus metrics exporter port: %d", operatorConfig.MetricsPort)

	logr = logr.With(logger.FieldOperatorVersion, Version)

	ctrl.SetLogger(zapr.NewLogger(logr.Desugar()))

	restCfg := ctrl.GetConfigOrDie()

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		logr.With(zap.Error(err), "unable to create client")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                  scheme,
		MetricsBindAddress:      fmt.Sprintf(":%d", operatorConfig.MetricsPort),
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: operatorConfig.Namespace,
	})
	if err != nil {
		logr.With(zap.Error(err)).Error("unable to start manager")
		os.Exit(1)
	}

	cassandaReconciler := &controllers.CassandraClusterReconciler{
		Client:       mgr.GetClient(),
		Log:          logr,
		Scheme:       mgr.GetScheme(),
		Cfg:          *operatorConfig,
		Clientset:    clientset,
		RESTConfig:   restCfg,
		ProberClient: func(host string) prober.Client { return prober.NewProberClient(host) },
		CqlClient:    func(cluster *gocql.ClusterConfig) (cql.Client, error) { return cql.NewCQLClient(cluster) },
		NodetoolClient: func(clientset *kubernetes.Clientset, config *rest.Config) nodetool.Client {
			return nodetool.NewNodetoolClient(clientset, config)
		},
	}

	err = controllers.SetupCassandraReconciler(cassandaReconciler, mgr, logr)
	if err != nil {
		logr.With(zap.Error(err)).Error("unable to create controller", "controller", "CassandraCluster")
		os.Exit(1)
	}

	logr.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logr.With(zap.Error(err)).Error("problem running manager")
		os.Exit(1)
	}
}
