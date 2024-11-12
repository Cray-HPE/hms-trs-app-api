// MIT License
//
// (C) Copyright [2021,2024] Hewlett Packard Enterprise Development LP
//
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included
// in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
// THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
// OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
// ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.

package trs_http_api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/pem"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	base "github.com/Cray-HPE/hms-base/v2"
	"github.com/sirupsen/logrus"
)

var svcName = "TestMe"

const (
	ERROR = 3
	INFO  = 2
	TRACE = 1
)
var logLevel = ERROR

func TestMain(m *testing.M) {
	flag.IntVar(&logLevel, "logLevel", ERROR, "set log level (0=ERROR, 1=INFO, 2=DEBUG)")
	flag.Parse()

	log.Printf("logLevel set to %v", logLevel)

	// Run the tests
	code := m.Run()

	// Exit
	os.Exit(code)
}

// Create a logger for trs_http_api (not unit tests)
func createLogger() *logrus.Logger {
	var level []logrus.Level

	switch logLevel {
	case ERROR:	level = []logrus.Level{logrus.ErrorLevel}
	case INFO:	level = []logrus.Level{logrus.InfoLevel}
	case TRACE:	level = []logrus.Level{logrus.TraceLevel}
	}

	trsLogger := logrus.New()

	trsLogger.SetFormatter(&logrus.TextFormatter{ FullTimestamp: true, })
	trsLogger.SetLevel(level[0])
	trsLogger.SetReportCaller(true)

	return trsLogger
}

func TestInit(t *testing.T) {
	tloc := &TRSHTTPLocal{}

	tloc.Init(svcName, createLogger())
	if (tloc.taskMap == nil) {
		t.Errorf("Init() failed to create task map")
	}
	if (tloc.clientMap == nil) {
		t.Errorf("Init() failed to create client map")
	}
	if (tloc.svcName != svcName) {
		t.Errorf("Init() failed to set service name")
	}
}

func TestCreateTaskList(t *testing.T) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())
	req,_ := http.NewRequest("GET","http://www.example.com",nil)
	tproto := HttpTask{Request: req,}
	base.SetHTTPUserAgent(req,tloc.svcName)
	tList := tloc.CreateTaskList(&tproto,5)

	if (len(tList) != 5) {
		t.Errorf("CreateTaskList() didn't create a correct array.")
	}
	for _,tsk := range(tList) {
		if (tsk.Request == nil) {
			t.Errorf("CreateTaskList() didn't create a proper Request.")
		}
		if (len(tsk.Request.Header) == 0) {
			t.Errorf("CreateTaskList() didn't create a proper Request header.")
		}
		vals,ok := tsk.Request.Header["User-Agent"]
		if (!ok) {
			t.Errorf("CreateTaskList() didn't copy User-Agent header.")
		}
		found := false
		for _,vr := range(vals) {
			if (vr == svcName) {
				found = true
				break
			}
		}
		if (!found) {
			t.Errorf("CreateTaskList() didn't copy User-Agent header.")
		}
	}
}

func hasUserAgentHeader(r *http.Request) bool {
    if (len(r.Header) == 0) {
        return false
    }

    _,ok := r.Header["User-Agent"]
    if (!ok) {
        return false
    }
    return true
}

var handlerLogger *testing.T

func launchHandler(w http.ResponseWriter, req *http.Request) {
	// Wait for all connections to be established so output looks nice
	time.Sleep(100 * time.Millisecond)

	handlerLogger.Logf("launchHandler running...")

	time.Sleep(1 * time.Second) // Simulate network and BMC delay

	if (!hasUserAgentHeader(req)) {
		w.Write([]byte(`{"Message":"No User-Agent Header"}`))
		w.WriteHeader(http.StatusInternalServerError)
		handlerLogger.Logf("launchHandler returning no User-Agent header...")
		return
	}
	w.Header().Set("Content-Type","application/json")
//	w.Header().Set("Connection","keep-alive")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))

	handlerLogger.Logf("launchHandler returning Message Ok...")
}

var retryNum = 0
func retryHandler(w http.ResponseWriter, req *http.Request) {
	if retryNum == 0 {
		//retryNum++

		// Wait for all connections to be established so output looks nice
		time.Sleep(100 * time.Millisecond)

		handlerLogger.Logf("retryHandler running...")

		time.Sleep(1 * time.Second) // Simulate network and BMC delay

		w.Header().Set("Content-Type","application/json")
		w.Header().Set("Retry-After","1")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"Message":"Service Unavailable"}`))

		handlerLogger.Logf("retryHandler returning Message Service Unavailable...")
	} else {
		// Wait for all connections to be established so output looks nice
		time.Sleep(100 * time.Millisecond)

		handlerLogger.Logf("launchHandler running...")

		time.Sleep(1 * time.Second) // Simulate network and BMC delay

		if (!hasUserAgentHeader(req)) {
			w.Write([]byte(`{"Message":"No User-Agent Header"}`))
			w.WriteHeader(http.StatusInternalServerError)
			handlerLogger.Logf("launchHandler returning no User-Agent header...")
			return
		}
		w.Header().Set("Content-Type","application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Message":"OK"}`))

		handlerLogger.Logf("launchHandler returning Message Ok...")
	}
}

var stallCancel chan bool

func stallHandler(w http.ResponseWriter, req *http.Request) {
	// Wait for all connections to be established so output looks nice
	time.Sleep(100 * time.Millisecond)

	handlerLogger.Logf("stallHandler running...")

	<-stallCancel

	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))

	handlerLogger.Logf("stallHandler returning Message Ok...")
}


func TestLaunch(t *testing.T) {
	testLaunch(t, 5, false)
}

func TestSecureLaunch(t *testing.T) {
	testLaunch(t, 1, true)
}

func testLaunch(t *testing.T, numTasks int, testSecureLaunch bool) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	var srv *httptest.Server
	if (testSecureLaunch == true) {
		srv = httptest.NewTLSServer(http.HandlerFunc(launchHandler))

		secInfo := TRSHTTPLocalSecurity{CACertBundleData:
				string(pem.EncodeToMemory(
					&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw},
				)),}

		err := tloc.SetSecurity(secInfo)
		if err != nil {
			t.Errorf("Error setting security info: %v", err)
			return
		}
	} else {
		srv = httptest.NewServer(http.HandlerFunc(launchHandler))
	}
	defer srv.Close()

	handlerLogger = t

	req,_ := http.NewRequest("GET",srv.URL,nil)
	tproto := HttpTask{Request: req, Timeout: 8*time.Second,}
	tList := tloc.CreateTaskList(&tproto, numTasks)

	tch,err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("Launch ERROR: %v",err)
	}

	nDone := 0
	nErr := 0
	for {
		tdone := <-tch
		nDone ++
		if (tdone == nil) {
			t.Errorf("Launch chan returned nil ptr.")
		}
		if (tdone.Request == nil) {
			t.Errorf("Launch chan returned nil Request.")
		} else if (tdone.Request.Response == nil) {
			t.Errorf("Launch chan returned nil Response.")
		} else {
			if (tdone.Request.Response.StatusCode != http.StatusOK) {
				t.Errorf("Launch chan returned bad status: %v",tdone.Request.Response.StatusCode)
				nErr ++
			}
			if ((tdone.Err != nil) && ((*tdone.Err) != nil)) {
				t.Errorf("Launch chan returned error: %v",*tdone.Err)
			}
		}
		running, err := tloc.Check(&tList)
		if (err != nil) {
			t.Errorf("ERROR with Check(): %v",err)
		}
		if (nDone == len(tList)) {
			if (running) {
				t.Errorf("ERROR, Check() says still running, but all tasks returned.")
			}
			break
		}
	}

	if (nErr != 0) {
		t.Errorf("Got %d errors from Launch",nErr)
	}
}

func TestLaunchTimeout(t *testing.T) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())
	srv := httptest.NewServer(http.HandlerFunc(stallHandler))
	defer srv.Close()

	handlerLogger = t

	req,_ := http.NewRequest("GET",srv.URL,nil)
	tproto := HttpTask{
			Request: req,
			Timeout: 3*time.Second,
			CPolicy: ClientPolicy{
				retry: RetryPolicy{
						Retries: 1,
						BackoffTimeout: 3 * time.Second},
				},
			}
	tList := tloc.CreateTaskList(&tproto,1)
	stallCancel = make(chan bool, 1)

	tch,err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("Launch ERROR: %v",err)
	}
	time.Sleep(100 * time.Millisecond)

	nDone := 0
	nErr := 0
	for {
		tdone := <-tch
		nDone ++
		if (tdone == nil) {
			t.Errorf("Launch chan returned nil ptr.")
		}
		stallCancel <- true
		running, err := tloc.Check(&tList)
		if (err != nil) {
			t.Errorf("ERROR with Check(): %v",err)
		}
		if (nDone == len(tList)) {
			if (running) {
				t.Errorf("ERROR, Check() says still running, but all tasks returned.")
			}
			break
		}
	}

	if (nErr != 0) {
		t.Errorf("Got %d errors from Launch",nErr)
	}
	close(stallCancel)
}

// Test connection states using 'ss' utility
func testOpenConnections(t *testing.T, clientEstabExp int) {
	cmd := exec.Command( "ss", "--tcp", "--resolve", "--processes", "--all")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("error running ss utility: %v", err)
		return
	}

	srvrPorts := map[string]bool{}
	debugOutput := map[string][]string{}

	// Use a scanner to read output line-by-line
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "Recv-Q") {
			// Header line
			debugOutput["header"] = append(debugOutput["header"], line)
			continue
		} else if strings.Contains(line, "LISTEN") {
			// This is a server. LISTEN lines always comes first in the output.
			// Ignore anything that isn't our test process
			if !strings.Contains(line, "trs_http_api") {
				debugOutput["ignoredListen"] = append(debugOutput["ignoredListen"], line)
				continue
			}

			// Grab the port so we can filter on it later
			debugOutput["serverListen"] = append(debugOutput["serverListen"], line)

			re := regexp.MustCompile(`localhost:(\d+)`)

			match := re.FindStringSubmatch(line)
			if len(match) > 1 {
				srvrPorts[match[1]] = true
			} else {
				t.Errorf("Failed to find port in LISTEN line: %v", line)
			}
		} else {
			// Distinguish client connections from server connections
			re := regexp.MustCompile(`localhost:(\d+)\s+localhost:(\d+)`)

			match := re.FindStringSubmatch(line)
			if len(match) > 2 {
				srcPort := match[1]
				dstPort := match[2]

				if _, exists := srvrPorts[srcPort]; exists {
					// This is a server connection
					debugOutput["serverOther"] = append(debugOutput["serverOther"], line)
				} else {
					// This is might be a client connection.  Test to see
					// if it targets one of our server ports
					if _, exists := srvrPorts[dstPort]; exists {
						// It's one of our client connections
						if strings.Contains(line, "ESTAB") {
							debugOutput["clientEstab"] = append(debugOutput["clientEstab"], line)
						} else {
							debugOutput["clientOther"] = append(debugOutput["clientOther"], line)
						}
					} else {
						debugOutput["ignoredConn"] = append(debugOutput["ignoredConn"], line)
					}
				}
			} else {
				debugOutput["ignoredMisc"] = append(debugOutput["ignoredMisc"], line)
			}
		}
	}

	if (len(debugOutput["clientEstab"]) != clientEstabExp) {
		t.Errorf("Expected %v ESTABLISHED connections, but got %v:\n%s",
				 clientEstabExp, len(debugOutput["clientEstab"]), output)
	}

	if logLevel <= INFO {
		if len(debugOutput["header"]) > 0 {
			t.Logf("")
			for _,v := range(debugOutput["header"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["clientEstab"]) > 0 {
			sort.Strings(debugOutput["clientEstab"])

			t.Logf("Client ESTAB Connections: (%v)", len(debugOutput["clientEstab"]))
			t.Logf("")
			for _,v := range(debugOutput["clientEstab"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["clientOther"]) > 0 {
			sort.Strings(debugOutput["clientOther"])

			t.Logf("Client Other Connections: (%v)", len(debugOutput["clientOther"]))
			t.Logf("")
			for _,v := range(debugOutput["clientOther"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["serverListen"]) > 0 {
			sort.Strings(debugOutput["serverListen"])

			t.Logf("Server LISTEN Connections: (%v)", len(debugOutput["serverListen"]))
			t.Logf("")
			for _,v := range(debugOutput["serverListen"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["serverOther"]) > 0 {
			sort.Strings(debugOutput["serverOther"])

			t.Logf("Server Other Connections: (%v)", len(debugOutput["serverOther"]))
			t.Logf("")
			for _,v := range(debugOutput["serverOther"]) {
				t.Log(v)
			}
			t.Logf("")
		}
	}
	if logLevel <= TRACE {
		if len(debugOutput["ignoredConn"]) > 0 {
			sort.Strings(debugOutput["ignoredConn"])

			t.Logf("Ignored Connections: (%v)", len(debugOutput["ignoredConn"]))
			t.Logf("")
			for _,v := range(debugOutput["ignoredConn"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["ignoredListen"]) > 0 {
			sort.Strings(debugOutput["ignoredListen"])

			t.Logf("Ignored LISTEN Connections: (%v)", len(debugOutput["ignoredListen"]))
			t.Logf("")
			for _,v := range(debugOutput["ignoredListen"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["ignoredMisc"]) > 0 {
			sort.Strings(debugOutput["ignoredMisc"])

			t.Logf("Ignored Misc Output: (%v)", len(debugOutput["ignoredMisc"]))
			t.Logf("")
			for _,v := range(debugOutput["ignoredMisc"]) {
				t.Log(v)
			}
			t.Logf("")
		}
	}
}

// CustomConnState logs changes to connection states - Useful for debugging
func CustomConnState(conn net.Conn, state http.ConnState) {
	if logLevel >= ERROR {
		return
	}

	switch state {
	case http.StateNew:
		log.Printf("HTTP_SERVER(%v): Connection -> NEW    %v (State %v)",
				   conn.LocalAddr(), conn.RemoteAddr(), state)
	case http.StateActive:
		log.Printf("HTTP_SERVER(%v): Connection -> ACTIVE %v (State %v)",
				   conn.LocalAddr(), conn.RemoteAddr(), state)
	case http.StateIdle:
		log.Printf("HTTP_SERVER(%v): Connection -> IDLE   %v (State %v)",
				   conn.LocalAddr(), conn.RemoteAddr(), state)
	case http.StateClosed:
		log.Printf("HTTP_SERVER(%v): Connection -> CLOSED %v (State %v)",
				   conn.LocalAddr(), conn.RemoteAddr(), state)
	default:
		log.Printf("HTTP_SERVER(%v): Connection -> ?      %v (State %v)",
				   conn.LocalAddr(), conn.RemoteAddr(), state)
	}
}

// CustomReadCloser wraps an io.ReadCloser and tracks if it was closed.
// This is used to test if response bodies are being closed properly.
type CustomReadCloser struct {
    io.ReadCloser
	closed bool
}

func (c *CustomReadCloser) Close() error {
	c.closed = true
	return c.ReadCloser.Close()
}

func (c *CustomReadCloser) WasClosed() bool {
	return c.closed
}

// TestPCSUseCase tests the PCS use case of the TRS HTTP API.  It launches
// a mix of tasks that make http requests that complete successfully,
// requests that retry multiple times and fail to get good responses, and
// requests that waiting for a server response and thus hit their task
// timout which cancels their contexts.

func TestSuccessfulRequestsWithNoHttpTxPolicy(t *testing.T) {
	httpRetries      := 3
	pcsStatusTimeout := 30
	httpTimeout      := time.Duration(pcsStatusTimeout) * time.Second

	cPolicy := ClientPolicy{retry: RetryPolicy{Retries: httpRetries}}

	testSuccessfulRequests(t, httpTimeout, cPolicy)
}

func TestSuccessfulRequestsWithHttpTxPolicy(t *testing.T) {
/*
	httpRetries           := 3
	pcsStatusTimeout      := 30
	httpTimeout           := time.Duration(pcsStatusTimeout) * time.Second
	//idleConnTimeout     := time.Duration(pcsStatusTimeout * 15 / 10) * time.Second
	idleConnTimeout       := 90 * time.Second
	//idleConnTimeout       := 300 * time.Second
	responseHeaderTimeout :=  5 * time.Second
	//responseHeaderTimeout :=  50 * time.Second
	tLSHandshakeTimeout   := 10 * time.Second
	//tLSHandshakeTimeout   := 100 * time.Second
	DisableKeepAlives       := false

	cPolicy := ClientPolicy{
		retry: RetryPolicy{Retries: httpRetries},
		tx: HttpTxPolicy{
				Enabled:                true,
				MaxIdleConns:           100,
				MaxIdleConnsPerHost:    100,
				IdleConnTimeout:        idleConnTimeout,
				ResponseHeaderTimeout:  responseHeaderTimeout,
				TLSHandshakeTimeout:    tLSHandshakeTimeout,
				DisableKeepAlives:      DisableKeepAlives,
			},
	}
	testSuccessfulRequests(t, httpTimeout, cPolicy)
*/
}

func testSuccessfulRequests(t *testing.T, httpTimeout time.Duration, cPolicy ClientPolicy) {
	nTasks := 1

	// Initialize the task system
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	// Copy logger into global namespace for the http server handlers
	handlerLogger = t

	// Create http server.
	srv := httptest.NewServer(http.HandlerFunc(launchHandler))

	// Configure server to log changes to connection states
	srv.Config.ConnState = CustomConnState

	// Create an http request

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
        t.Fatalf("Failed to create request: %v", err)
    }
	req.Header.Set("Accept", "*/*")

	tListProto := HttpTask{
			Request: req,
			Timeout: httpTimeout,
			CPolicy: cPolicy, }

	t.Logf("Creating task list with %v tasks and URL %v", nTasks, srv.URL)
	tList := tloc.CreateTaskList(&tListProto, nTasks)

	t.Logf("Launching all tasks")
	taskListChannel, err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("Launch ERROR: %v", err)
	}

	// All connections should be in ESTAB(LISHED)
	time.Sleep(100 * time.Millisecond)		// Give time to staiblize
	t.Logf("Testing connections after Launch")
	testOpenConnections(t, (nTasks))

	t.Logf("Waiting for tasks to complete")
	for i := 0; i < (nTasks); i++ {
		<-taskListChannel
	}

	t.Logf("Closing the task list channel")
	close(taskListChannel)

	// All connections should still be in ESTAB(LISHED)
	time.Sleep(100 * time.Millisecond)		// Give time to staiblize
	t.Logf("Testing connections after tasks complete")
	testOpenConnections(t, nTasks)

	// Close the response bodies so connections stay open during ctx cancel
	t.Logf("Closing response bodies")
	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			// Must fully read the body in order to close the body so that
			// the underlying libraries/modules don't close the connection.
			// If body not fully conusmed they assume the connection had issues
			_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)

			tsk.Request.Response.Body.Close()
			tsk.Request.Response.Body = nil

			// Response headers can be  helpful for debug
			if logLevel <= TRACE {
				t.Logf("Response headers: %s", tsk.Request.Response.Header)
			}
		}
	}

	// Closing the body should not alter ESTAB(LISHED) connections
	time.Sleep(100 * time.Millisecond)		// Give time to staiblize
	t.Logf("Testing connections after response bodies closed")
	testOpenConnections(t, nTasks)

	// Now cancel the task list
	tloc.Cancel(&tList)

	// Cancelling the task list should not alter ESTAB(LISHED) connections
	time.Sleep(100 * time.Millisecond)		// Give time to staiblize
	t.Logf("Testing connections after task list cancelled")
	testOpenConnections(t, nTasks)

	t.Logf("Closing the task list")
	tloc.Close(&tList)

	t.Logf("Checking that the task list was closed")
	if (len(tloc.taskMap) != 0) {
		t.Errorf("Expected task list map to be empty")
	}

	// Closing the task list should not alter ESTAB(LISHED) connections
	time.Sleep(100 * time.Millisecond)		// Give time to staiblize
	t.Logf("Testing connections after task list closed")
	testOpenConnections(t, nTasks)

	t.Logf("Cleaning up task system")
	tloc.Cleanup()

	// Cleaking up the task list system should close all connections
	time.Sleep(100 * time.Millisecond)		// Give time to staiblize
	t.Logf("Testing connections after task list cleaned up")
	testOpenConnections(t, 0)

	t.Logf("Closing the server")
	srv.Close()
}

func TestPCSUseCaseWithHttpTxPolicy(t *testing.T) {
/*
	httpRetries           := 3
	pcsStatusTimeout      := 30
	httpTimeout           := time.Duration(pcsStatusTimeout) * time.Second
	//idleConnTimeout     := time.Duration(pcsStatusTimeout * 15 / 10) * time.Second
	idleConnTimeout       := 90 * time.Second
	//idleConnTimeout       := 300 * time.Second
	responseHeaderTimeout :=  5 * time.Second
	//responseHeaderTimeout :=  50 * time.Second
	tLSHandshakeTimeout   := 10 * time.Second
	//tLSHandshakeTimeout   := 100 * time.Second
	DisableKeepAlives       := false

	cPolicy := ClientPolicy{
		retry: RetryPolicy{Retries: httpRetries},
		tx: HttpTxPolicy{
				Enabled:                true,
				MaxIdleConns:           100,
				MaxIdleConnsPerHost:    100,
				IdleConnTimeout:        idleConnTimeout,
				ResponseHeaderTimeout:  responseHeaderTimeout,
				TLSHandshakeTimeout:    tLSHandshakeTimeout,
				DisableKeepAlives:      DisableKeepAlives,
			},
	}
	testPCSUseCase(t, httpTimeout, cPolicy)
*/
}

func testPCSUseCase(t *testing.T, httpTimeout time.Duration, cPolicy ClientPolicy) {
	//numSuccessTasks := 5
	numSuccessTasks := 1
	//numRetryTasks := 5
	numRetryTasks := 1
	//numStallTasks := 5
	numStallTasks := 0

	// Initialize the task system
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	// Copy logger into global namespace for the http servers
	handlerLogger = t

	// Create http servers.  One for eash request response we want to test
	// Because we're testing idle connections we need to configure them to
	// not close idle connections immediately
	successSrv := httptest.NewServer(http.HandlerFunc(launchHandler))
	retrySrv := httptest.NewServer(http.HandlerFunc(retryHandler))
	stallSrv := httptest.NewServer(http.HandlerFunc(stallHandler))

	var connTimes sync.Map
	successSrv.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			// Store the start time when the connection is new
			connTimes.Store(conn, time.Now())
			log.Printf("New connection %v started at %v", conn.RemoteAddr(), time.Now())
			log.Printf("     Local Address: %v, Remote Address: %v, State: %v", conn.LocalAddr(), conn.RemoteAddr(), state)
		case http.StateActive:
			log.Printf("Connection %v is now ACTIVE", conn.RemoteAddr())
		case http.StateIdle:
			log.Printf("Connection %v is now IDLE", conn.RemoteAddr())
		case http.StateClosed:
			// Calculate the duration of the connection's lifetime
			if startTime, ok := connTimes.Load(conn); ok {
				duration := time.Since(startTime.(time.Time))
				log.Printf("Connection %v closed after %v", conn.RemoteAddr(), duration)
				connTimes.Delete(conn)
			} else {
				log.Printf("Connection %v closed (no start time found)", conn.RemoteAddr())
			}
		default:
			log.Printf("UNHANDLED STATE: Connection %v changed state to %v", conn.RemoteAddr(), state)
		}
    }

	//successSrv.Start()
	//retrySrv.Start()
	//stallSrv.Start()

/*
	successSrv := &http.Server{
        Addr: "localhost:36411",
		Handler: http.HandlerFunc(launchHandler),
        IdleTimeout: 300 * time.Second,
        ReadTimeout: 300 * time.Second,
        WriteTimeout: 300 * time.Second,
		ConnState: func(conn net.Conn, state http.ConnState) {
			t.Logf("Connection %v changed state to %v", conn.RemoteAddr(), state)
		},
    }
	go successSrv.ListenAndServe()
	successReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:36411", nil)
*/

	// Create an http request for tasks that complete successfully

	successReq, err := http.NewRequest(http.MethodGet, successSrv.URL, nil)
	if err != nil {
        t.Fatalf("Failed to create request: %v", err)
    }
	successReq.Header.Set("Accept", "*/*")
//	successReq.Header.Set("Connection", "keep-alive")

	successProto := HttpTask{
			Request: successReq,
			Timeout: httpTimeout,
			CPolicy: cPolicy, }

	t.Logf("Creating success task list with %v tasks and URL %v", numSuccessTasks, successSrv.URL)
	successList := tloc.CreateTaskList(&successProto, numSuccessTasks)

	// Create an http request for tasks that retry muliple times and fail

	retryReq, err := http.NewRequest(http.MethodGet, retrySrv.URL, nil)
	if err != nil {
        t.Fatalf("Failed to create request: %v", err)
    }
	retryReq.Header.Set("Accept", "*/*")
//	successReq.Header.Set("Connection", "keep-alive")

	retryProto := HttpTask{
			Request: retryReq,
			Timeout: httpTimeout,
			CPolicy: cPolicy, }

	t.Logf("Creating retry task list with %v tasks and URL %v", numRetryTasks, retrySrv.URL)
	retryList := tloc.CreateTaskList(&retryProto, numRetryTasks)

/*
	// Create an http request for tasks that stall

	stallReq, err := http.NewRequest("GET", stallSrv.URL, nil)
	if err != nil {
        t.Fatalf("Failed to create request: %v", err)
    }
	stallReq.Header.Set("Accept", "STAR/STAR")
	stallReq.Header.Set("Connection", "keep-alive")

	stallProto := HttpTask{
			Request: stallReq,
			Timeout: httpTimeout,
			CPolicy: cPolicy, }

	t.Logf("Creating stalling task list with %v tasks and URL %v", numStallTasks, stallSrv.URL)
	stallList := tloc.CreateTaskList(&stallProto, numStallTasks)

	// Create a channel to signal the stalled server handlers to complete
	// We will need two sets for the initial call and then a retry
	stallCancel = make(chan bool, numStallTasks * 2)

	// Launch all three sets of tasks using a single list

	tList := append(successList, retryList...)
	tList = append(tList, stallList...)
*/
tList := append(successList, retryList...)

	t.Logf("Launching all tasks")
	taskListChannel, err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("Launch ERROR: %v", err)
	}

	// Wait for all connections to enter ESTABLISHED state so output looks nice
	time.Sleep(200 * time.Millisecond)

	// All connections should be in ESTABLISHED
	t.Logf("Testing open connections after Launch")
	testOpenConnections(t, (numSuccessTasks + numRetryTasks + numStallTasks))

	t.Logf("Waiting for normally completing tasks to complete")
	for i := 0; i < (numSuccessTasks + numRetryTasks); i++ {
		<-taskListChannel
	}

	// The only remaining connections should be for the stalled tasks
	// which should still be in ESTABLISHED
	t.Logf("Testing open connections after normally completing tasks completed")
	testOpenConnections(t, numStallTasks)

/*
	t.Logf("Waiting for stalled tasks to time out")
	for i := 0; i < numStallTasks; i++ {
		<-taskListChannel
	}

	// The stalled tasks timed out due to HTTPClient.Timeout because it was
	// sized to 90% of the task timeout.  These tasks will now retry so
	// sleep for the other 10% of the task timeout to allow them to be
	// cancelled due to their context timeing out.
	time.Sleep(httpTimeout / 10)
*/

for _, tsk := range(tList) {
	if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
		t.Logf("Response headers: %s", tsk.Request.Response.Header)
		t.Logf("Protocol: %s", tsk.Request.Response.Proto)
		t.Logf("discarding the body")
		_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)
		t.Logf("Closing response body for task %v", tsk.Request.URL)
		tsk.Request.Response.Body.Close()
		tsk.Request.Response.Body = nil

		time.Sleep(200 * time.Millisecond)
		t.Logf("")
		t.Logf("testing connections after close")
		testOpenConnections(t, 0)
	}
}
tloc.Cancel(&tList)
	// All connections should now be closed
	t.Logf("Testing open connections after stalled tasks completed")
	testOpenConnections(t, 0)

	t.Logf("Closing the task list channel")
	close(taskListChannel)

	// Currently only testing completing and timing out tasks so no
	// need to call tloc.Cancel()

	// Set up custom read closer to test if response bodies get closed
	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			tsk.Request.Response.Body = &CustomReadCloser{tsk.Request.Response.Body, false}
		}
	}

	t.Logf("Closing the task list")
	tloc.Close(&tList)

	t.Logf("Checking that the task list was closed")
	if (len(tloc.taskMap) != 0) {
		t.Errorf("Expected task list map to be empty")
	}

	// We never closed any tasks' response bodies because we want to test
	// that TRS does it for the caller if the caller forgets.
	t.Logf("Checking for closed response bodies")
	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			if !tsk.Request.Response.Body.(*CustomReadCloser).WasClosed() {
				t.Errorf("Expected response body to be closed, but it was not")
			}
		}
	}

	t.Logf("Checking for correct number of canceled and timed out contexts")
	canceledTasks := 0
	timedOutTasks := 0
	for _, tsk := range(tList) {
		select {
		case <-tsk.context.Done():
			if tsk.context.Err() == context.Canceled {
				canceledTasks++
			} else if tsk.context.Err() == context.DeadlineExceeded {
				timedOutTasks++
			} else {
				t.Errorf("Context was not canceled or timed out")
			}
		default:
			t.Errorf("Expected context to be done, but it is still active")
		}
	}
	if canceledTasks != (numSuccessTasks + numRetryTasks) {
		t.Errorf("Expected %v canceled tasks, but got %v", numSuccessTasks + numRetryTasks, canceledTasks)
	}
	if timedOutTasks != numStallTasks {
		t.Errorf("Expected %v timed out tasks, but got %v", numStallTasks, timedOutTasks)
	}

	t.Logf("Cleaning up task system")
	tloc.Cleanup()
/*

	// Cancel the stalled server handlers so we can close the servers. We
	// will need to do it once for the first set that timed out due to the
	// HTTPClient.Timeout and once for the second set that timed out due to
	// the context timeout.
	t.Logf("Signaling stalled handlers ")
	for i := 0; i < numStallTasks * 2; i++ {
		stallCancel <- true
	}
	close(stallCancel)
*/

	t.Logf("Closing servers")
	successSrv.Close()
	retrySrv.Close()
	stallSrv.Close()
}