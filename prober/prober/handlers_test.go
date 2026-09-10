package prober

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cin/mr-cassop/prober/config"
	"github.com/cin/mr-cassop/prober/jolokia"
	"k8s.io/client-go/kubernetes"

	"github.com/julienschmidt/httprouter"
	"github.com/onsi/gomega"
	"go.uber.org/zap"
)

func TestHealthCheck(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testCases := []struct {
		name           string
		state          state
		remoteAddr     string
		broadcastAddr  string
		expectedBody   []byte
		expectedStatus int
		expectedState  state
	}{
		{
			name:          "healthcheck for a healthy node",
			remoteAddr:    "172.16.16.4",
			broadcastAddr: "10.134.3.4",
			state: state{
				nodes: map[string]nodeState{
					"/10.134.3.4": {
						SimpleStates:  map[string]string{"/10.134.3.4": "UP"},
						EndpointState: endpointState("/10.134.3.4", "NORMAL"),
					},
				},
				podIPs: map[string]string{
					"/10.134.3.4": "/172.16.16.4",
				},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/10.134.3.4": {
						SimpleStates:  map[string]string{"/10.134.3.4": "UP"},
						EndpointState: endpointState("/10.134.3.4", "NORMAL"),
					},
				},
				podIPs: map[string]string{
					"/10.134.3.4": "/172.16.16.4",
				},
			},
			expectedStatus: http.StatusOK,
			expectedBody:   []byte(`{"/10.134.3.4":"UP"}`),
		},
		{
			name:          "new node",
			remoteAddr:    "172.16.16.5",
			broadcastAddr: "10.134.3.5",
			state: state{
				nodes: map[string]nodeState{
					"/10.134.3.4": {
						SimpleStates:  map[string]string{"/10.134.3.4": "UP"},
						EndpointState: endpointState("/10.134.3.4", "NORMAL"),
					},
				},
				podIPs: map[string]string{},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/10.134.3.4": {
						SimpleStates:  map[string]string{"/10.134.3.4": "UP"},
						EndpointState: endpointState("/10.134.3.4", "NORMAL"),
					},
					"/10.134.3.5": {},
				},
				podIPs: map[string]string{
					"/10.134.3.5": "/192.0.2.1", // 192.x is default RemoteAddr from net/http/httptest/httptest_test.go
				},
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   []byte(`{"/10.134.3.4":"?","/10.134.3.5":"?"}`),
		},
		{
			name:          "node not ready",
			remoteAddr:    "172.16.16.5",
			broadcastAddr: "10.134.3.5",
			state: state{
				nodes: map[string]nodeState{
					"/10.134.3.4": {
						SimpleStates: map[string]string{
							"/10.134.3.4": "UP",
							"/10.134.3.5": "DOWN", // seen as down from this node
						},
						EndpointState: endpointState("/10.134.3.4", "NORMAL"),
					},
					"/10.134.3.5": {
						SimpleStates: map[string]string{
							"/10.134.3.4": "UP",
							"/10.134.3.5": "UP",
						},
						EndpointState: endpointState("/10.134.3.5", "NORMAL"),
					},
				},
				podIPs: map[string]string{
					"/10.134.3.5": "/172.16.16.5",
				},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/10.134.3.4": {
						SimpleStates: map[string]string{
							"/10.134.3.4": "UP",
							"/10.134.3.5": "DOWN",
						},
						EndpointState: endpointState("/10.134.3.4", "NORMAL"),
					},
					"/10.134.3.5": {
						SimpleStates: map[string]string{
							"/10.134.3.4": "UP",
							"/10.134.3.5": "UP",
						},
						EndpointState: endpointState("/10.134.3.5", "NORMAL"),
					},
				},
				podIPs: map[string]string{
					"/10.134.3.5": "/172.16.16.5",
				},
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   []byte(`{"/10.134.3.4":"DOWN","/10.134.3.5":"UP"}`),
		},
		{
			name:          "broadcast address is set",
			remoteAddr:    "172.16.16.213",
			broadcastAddr: "43.23.111.213",
			state: state{
				nodes: map[string]nodeState{
					"/43.23.111.212": {
						SimpleStates:  map[string]string{"/43.23.111.212": "UP", "/43.23.111.213": "UP"},
						EndpointState: endpointState("/43.23.111.212", "NORMAL"),
					},
					"/43.23.111.213": {
						SimpleStates:  map[string]string{"/43.23.111.212": "UP", "/43.23.111.213": "UP"},
						EndpointState: endpointState("/43.23.111.213", "NORMAL"),
					},
				},
				podIPs: map[string]string{
					"/43.23.111.212": "/172.16.16.212",
					"/43.23.111.213": "/172.16.16.213",
				},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/43.23.111.212": {
						SimpleStates:  map[string]string{"/43.23.111.212": "UP", "/43.23.111.213": "UP"},
						EndpointState: endpointState("/43.23.111.212", "NORMAL"),
					},
					"/43.23.111.213": {
						SimpleStates:  map[string]string{"/43.23.111.212": "UP", "/43.23.111.213": "UP"},
						EndpointState: endpointState("/43.23.111.213", "NORMAL"),
					},
				},
				podIPs: map[string]string{
					"/43.23.111.212": "/172.16.16.212",
					"/43.23.111.213": "/172.16.16.213",
				},
			},
			expectedStatus: http.StatusOK,
			expectedBody:   []byte(`{"/43.23.111.212":"UP","/43.23.111.213":"UP"}`),
		},
		{
			name:          "none of the nodes are ready",
			remoteAddr:    "172.16.16.213",
			broadcastAddr: "43.23.111.213",
			state: state{
				nodes: map[string]nodeState{
					"/43.23.111.212": {
						SimpleStates:  map[string]string{"/43.23.111.212": "DOWN", "/43.23.111.213": "DOWN"},
						EndpointState: endpointState("/43.23.111.212", "DOWN"),
					},
					"/43.23.111.213": {
						SimpleStates:  map[string]string{"/43.23.111.212": "DOWN", "/43.23.111.213": "DOWN"},
						EndpointState: endpointState("/43.23.111.213", "DOWN"),
					},
				},
				podIPs: map[string]string{
					"/43.23.111.212": "/172.16.16.212",
					"/43.23.111.213": "/172.16.16.213",
				},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/43.23.111.212": {
						SimpleStates:  map[string]string{"/43.23.111.212": "DOWN", "/43.23.111.213": "DOWN"},
						EndpointState: endpointState("/43.23.111.212", "DOWN"),
					},
					"/43.23.111.213": {
						SimpleStates:  map[string]string{"/43.23.111.212": "DOWN", "/43.23.111.213": "DOWN"},
						EndpointState: endpointState("/43.23.111.213", "DOWN"),
					},
				},
				podIPs: map[string]string{
					"/43.23.111.212": "/172.16.16.212",
					"/43.23.111.213": "/172.16.16.213",
				},
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   []byte(`{"/43.23.111.212":"DOWN","/43.23.111.213":"DOWN"}`),
		},
	}

	for _, testCase := range testCases {
		var testProber = NewProber(
			config.Config{},
			&jolokiaMock{},
			UserAuth{},
			&kubernetes.Clientset{},
			zap.NewNop().Sugar(),
		)

		testProber.state = testCase.state

		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/healthz/%s", testCase.broadcastAddr), nil)
		recorder := httptest.NewRecorder()
		router := httprouter.New()
		setupRoutes(router, testProber)

		router.ServeHTTP(recorder, request)
		b, err := io.ReadAll(recorder.Result().Body)
		t.Log(testCase.name)
		asserts.Expect(err).ToNot(gomega.HaveOccurred())
		asserts.Expect(string(b)).To(gomega.Equal(string(testCase.expectedBody)))
		asserts.Expect(testProber.state).To(gomega.Equal(testCase.expectedState))
		asserts.Expect(recorder.Code).To(gomega.Equal(testCase.expectedStatus))
	}
}

func TestPing(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{},
		log:  zap.NewNop().Sugar(),
	}

	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	recorder := httptest.NewRecorder()
	router := httprouter.New()
	setupRoutes(router, testProber)

	router.ServeHTTP(recorder, request)
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())
	asserts.Expect(b).To(gomega.Equal([]byte("pong")))
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
}

func TestGetRegionReady(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{
			User:     "cassandra",
			Password: "cassandra",
		},
		log: zap.NewNop().Sugar(),
		state: state{
			regionReady: false,
		},
	}

	request := httptest.NewRequest(http.MethodGet, "/region-ready", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router := httprouter.New()
	setupRoutes(router, testProber)

	router.ServeHTTP(recorder, request)
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())
	asserts.Expect(b).To(gomega.Equal([]byte("false")))
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))

	testProber.state.regionReady = true
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	b, err = io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())
	asserts.Expect(b).To(gomega.Equal([]byte("true")))
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
}

func TestPutRegionReady(t *testing.T) {
	testCases := []struct {
		requestBody           io.Reader
		expectedCode          int
		expectedLocalDCsState bool
	}{
		{
			requestBody:           bytes.NewReader([]byte("true")),
			expectedCode:          http.StatusOK,
			expectedLocalDCsState: true,
		},
		{
			requestBody:           bytes.NewReader([]byte("false")),
			expectedCode:          http.StatusOK,
			expectedLocalDCsState: false,
		},
		{
			requestBody:           bytes.NewReader([]byte("invalid")),
			expectedCode:          http.StatusBadRequest,
			expectedLocalDCsState: false,
		},
		{
			requestBody:           failingReader("err"),
			expectedCode:          http.StatusInternalServerError,
			expectedLocalDCsState: false,
		},
	}

	for _, testCase := range testCases {
		asserts := gomega.NewWithT(t)
		testProber := &Prober{
			auth: UserAuth{
				User:     "cassandra",
				Password: "cassandra",
			},
			log:   zap.NewNop().Sugar(),
			state: state{},
		}

		router := httprouter.New()
		setupRoutes(router, testProber)

		request := httptest.NewRequest(http.MethodPut, "/region-ready", testCase.requestBody)
		request.SetBasicAuth("cassandra", "cassandra")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		asserts.Expect(recorder.Code).To(gomega.Equal(testCase.expectedCode))
		asserts.Expect(testProber.state.regionReady).To(gomega.Equal(testCase.expectedLocalDCsState))
	}
}

func TestGetSeeds(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{
			User:     "cassandra",
			Password: "cassandra",
		},
		log: zap.NewNop().Sugar(),
		state: state{
			seeds: []string{"seed1", "seed2"},
		},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/seeds", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())
	asserts.Expect(b).To(gomega.BeEquivalentTo([]byte("[\"seed1\",\"seed2\"]")))
}

func TestPutSeeds(t *testing.T) {
	asserts := gomega.NewWithT(t)

	testCases := []struct {
		requestBody       io.Reader
		expectedCode      int
		expectedSeedState []string
	}{
		{
			requestBody:       bytes.NewReader([]byte("[\"seed1\",\"seed2\"]")),
			expectedCode:      http.StatusOK,
			expectedSeedState: []string{"seed1", "seed2"},
		},
		{
			requestBody:       bytes.NewReader([]byte("invalid")),
			expectedCode:      http.StatusBadRequest,
			expectedSeedState: nil,
		},
		{
			requestBody:       failingReader("err"),
			expectedCode:      http.StatusInternalServerError,
			expectedSeedState: nil,
		},
	}

	for _, testCase := range testCases {
		testProber := &Prober{
			auth: UserAuth{
				User:     "cassandra",
				Password: "cassandra",
			},
			log:   zap.NewNop().Sugar(),
			state: state{},
		}

		router := httprouter.New()
		setupRoutes(router, testProber)

		request := httptest.NewRequest(http.MethodPut, "/seeds", testCase.requestBody)
		request.SetBasicAuth("cassandra", "cassandra")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		asserts.Expect(recorder.Code).To(gomega.Equal(testCase.expectedCode))
		asserts.Expect(testProber.state.seeds).To(gomega.Equal(testCase.expectedSeedState))
	}
}

func TestGetDCs(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{
			User:     "cassandra",
			Password: "cassandra",
		},
		log: zap.NewNop().Sugar(),
		state: state{
			dcs: []dc{{Name: "dc1", Replicas: 3}, {Name: "dc2", Replicas: 4}},
		},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/dcs", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())
	asserts.Expect(b).To(gomega.BeEquivalentTo([]byte("[{\"name\":\"dc1\",\"replicas\":3},{\"name\":\"dc2\",\"replicas\":4}]")))
}

func TestPutDCs(t *testing.T) {
	asserts := gomega.NewWithT(t)

	testCases := []struct {
		requestBody  io.Reader
		expectedCode int
		expectedDCs  []dc
	}{
		{
			requestBody:  bytes.NewReader([]byte("[{\"name\":\"dc1\",\"replicas\":3},{\"name\":\"dc2\",\"replicas\":4}]")),
			expectedCode: http.StatusOK,
			expectedDCs:  []dc{{Name: "dc1", Replicas: 3}, {Name: "dc2", Replicas: 4}},
		},
		{
			requestBody:  bytes.NewReader([]byte("invalid")),
			expectedCode: http.StatusBadRequest,
			expectedDCs:  nil,
		},
		{
			requestBody:  failingReader("err"),
			expectedCode: http.StatusInternalServerError,
			expectedDCs:  nil,
		},
	}

	for _, testCase := range testCases {
		testProber := &Prober{
			auth: UserAuth{
				User:     "cassandra",
				Password: "cassandra",
			},
			log:   zap.NewNop().Sugar(),
			state: state{},
		}

		router := httprouter.New()
		setupRoutes(router, testProber)

		request := httptest.NewRequest(http.MethodPut, "/dcs", testCase.requestBody)
		request.SetBasicAuth("cassandra", "cassandra")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		asserts.Expect(recorder.Code).To(gomega.Equal(testCase.expectedCode))
		asserts.Expect(testProber.state.dcs).To(gomega.Equal(testCase.expectedDCs))
	}
}

func TestGetNodes(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{
			User:     "cassandra",
			Password: "cassandra",
		},
		log: zap.NewNop().Sugar(),
		state: state{
			nodes: map[string]nodeState{
				"/10.134.3.4": {
					SimpleStates:  map[string]string{"/10.134.3.4": "UP"},
					EndpointState: endpointState("/10.134.3.4", "NORMAL"),
				},
			},
		},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())

	var got map[string]nodeState
	asserts.Expect(json.Unmarshal(b, &got)).To(gomega.Succeed())
	asserts.Expect(got).To(gomega.Equal(testProber.state.nodes))
}

func TestGetNodesRequiresAuth(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{
			User:     "cassandra",
			Password: "cassandra",
		},
		log: zap.NewNop().Sugar(),
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusUnauthorized))
}

func TestGetTools(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/tools", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())

	var got []jolokia.StatDef
	asserts.Expect(json.Unmarshal(b, &got)).To(gomega.Succeed())
	names := make([]string, len(got))
	for i, def := range got {
		names[i] = def.Name
	}
	asserts.Expect(names).To(gomega.ContainElements("info", "describecluster", "compactionstats", "tpstats"))
}

func TestGetStat(t *testing.T) {
	asserts := gomega.NewWithT(t)
	wantResult := jolokia.StatsResult{Groups: []jolokia.StatGroup{{Rows: []jolokia.StatRow{
		{Label: "Operation mode", Value: "NORMAL"},
	}}}}
	mock := &jolokiaMock{
		stats: map[string]map[string]jolokia.StatsResult{
			"/172.16.16.4": {"info": wantResult},
		},
	}
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
		state: state{
			podIPs: map[string]string{"/10.134.3.4": "/172.16.16.4"},
		},
		jolokia: mock,
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/stats?name=info&ip=10.134.3.4", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())

	var got jolokia.StatsResult
	asserts.Expect(json.Unmarshal(b, &got)).To(gomega.Succeed())
	asserts.Expect(got).To(gomega.Equal(wantResult))
}

func TestGetStatMissingName(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/stats?ip=10.134.3.4", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusBadRequest))
}

func TestGetStatMissingIP(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/stats?name=info", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusBadRequest))
}

func TestGetStatUnknownNode(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth:  UserAuth{User: "cassandra", Password: "cassandra"},
		log:   zap.NewNop().Sugar(),
		state: state{podIPs: map[string]string{}},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodGet, "/stats?name=info&ip=10.134.3.9", nil)
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusNotFound))
}

func TestPutSettings(t *testing.T) {
	asserts := gomega.NewWithT(t)
	mock := &jolokiaMock{
		setSettingsErrs: map[string]map[string]string{
			"/172.16.16.4": {"Trace probability": "invalid value"},
		},
	}
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
		state: state{
			podIPs: map[string]string{"/10.134.3.4": "/172.16.16.4"},
		},
		jolokia: mock,
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	body := `{"Compaction throughput (MB/s)":"128","Trace probability":"0.1"}`
	request := httptest.NewRequest(http.MethodPut, "/settings?ip=10.134.3.4", strings.NewReader(body))
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusOK))
	b, err := io.ReadAll(recorder.Result().Body)
	asserts.Expect(err).ToNot(gomega.HaveOccurred())

	var got map[string]string
	asserts.Expect(json.Unmarshal(b, &got)).To(gomega.Succeed())
	asserts.Expect(got).To(gomega.Equal(map[string]string{"Trace probability": "invalid value"}))

	asserts.Expect(mock.setSettingsCalls).To(gomega.HaveLen(1))
	asserts.Expect(mock.setSettingsCalls[0].ip).To(gomega.Equal("/172.16.16.4"))
	asserts.Expect(mock.setSettingsCalls[0].changes).To(gomega.Equal(map[string]string{
		"Compaction throughput (MB/s)": "128",
		"Trace probability":            "0.1",
	}))
}

func TestPutSettingsEmptyBody(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
		state: state{
			podIPs: map[string]string{"/10.134.3.4": "/172.16.16.4"},
		},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodPut, "/settings?ip=10.134.3.4", strings.NewReader(`{}`))
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusBadRequest))
}

func TestPutSettingsInvalidJSON(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth: UserAuth{User: "cassandra", Password: "cassandra"},
		log:  zap.NewNop().Sugar(),
		state: state{
			podIPs: map[string]string{"/10.134.3.4": "/172.16.16.4"},
		},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodPut, "/settings?ip=10.134.3.4", strings.NewReader(`not json`))
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusBadRequest))
}

func TestPutSettingsUnknownNode(t *testing.T) {
	asserts := gomega.NewWithT(t)
	testProber := &Prober{
		auth:  UserAuth{User: "cassandra", Password: "cassandra"},
		log:   zap.NewNop().Sugar(),
		state: state{podIPs: map[string]string{}},
	}
	router := httprouter.New()
	setupRoutes(router, testProber)

	request := httptest.NewRequest(http.MethodPut, "/settings?ip=10.134.3.9", strings.NewReader(`{"Trace probability":"0.1"}`))
	request.SetBasicAuth("cassandra", "cassandra")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	asserts.Expect(recorder.Code).To(gomega.Equal(http.StatusNotFound))
}

type failingReader string

func (f failingReader) Read(_ []byte) (n int, err error) { return 0, errors.New(string(f)) }
