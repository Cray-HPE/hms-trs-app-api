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
	"sync/atomic"
	"testing"
	"time"

	base "github.com/Cray-HPE/hms-base/v2"
	"github.com/sirupsen/logrus"
)

var svcName = "TestMe"

var logLevel logrus.Level	// use this for more than logrus

func TestMain(m *testing.M) {
	var logLevelInt int

	flag.IntVar(&logLevelInt, "logLevel", int(logrus.ErrorLevel),
		"set log level (0=Panic, 1=Fatal, 2=Error 3=Warn, 4=Info, 5=Debug, 6=Trace)")
	flag.Parse()

	logLevel = logrus.Level(logLevelInt)

	log.Printf("logLevel set to %v", logLevel)

	// Run the tests
	code := m.Run()

	// Exit
	os.Exit(code)
}

// Create a logger for trs_http_api (not unit tests)
func createLogger() *logrus.Logger {
	trsLogger := logrus.New()

	trsLogger.SetFormatter(&logrus.TextFormatter{ FullTimestamp: true, })
	trsLogger.SetLevel(logrus.Level(logLevel))
	trsLogger.SetReportCaller(true)

	return trsLogger
}

func TestInit(t *testing.T) {
	tloc := &TRSHTTPLocal{}

	tloc.Init(svcName, createLogger())
	if (tloc.taskMap == nil) {
		t.Errorf("===> ERROR: Init() failed to create task map")
	}
	if (tloc.clientMap == nil) {
		t.Errorf("===> ERROR: Init() failed to create client map")
	}
	if (tloc.svcName != svcName) {
		t.Errorf("===> ERROR: Init() failed to set service name")
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
		t.Errorf("===> ERROR: CreateTaskList() didn't create a correct array.")
	}
	for _,tsk := range(tList) {
		if (tsk.Request == nil) {
			t.Errorf("===> ERROR: CreateTaskList() didn't create a proper Request.")
		}
		if (len(tsk.Request.Header) == 0) {
			t.Errorf("===> ERROR: CreateTaskList() didn't create a proper Request header.")
		}
		vals,ok := tsk.Request.Header["User-Agent"]
		if (!ok) {
			t.Errorf("===> ERROR: CreateTaskList() didn't copy User-Agent header.")
		}
		found := false
		for _,vr := range(vals) {
			if (vr == svcName) {
				found = true
				break
			}
		}
		if (!found) {
			t.Errorf("===> ERROR: CreateTaskList() didn't copy User-Agent header.")
		}
	}
}

func hasUserAgentHeader(r *http.Request) bool {
    if (len(r.Header) == 0) {
        return false
    }

    _,ok := r.Header["User-Agent"]
	return ok
}

func hasTRSAlwaysRetryHeader(r *http.Request) bool {
    if (len(r.Header) == 0) {
        return false
    }

	_,ok := r.Header["Trs-Fail-All-Retries"]

	if ok == true {
		if (logLevel >= logrus.DebugLevel) {
			handlerLogger.Logf("Received Trs-Fail-All-Retries header")
		}
	}
	return ok
}

func hasTRSStallHeader(r *http.Request) bool {
    if (len(r.Header) == 0) {
        return false
    }

	_,ok := r.Header["Trs-Context-Timeout"]

	if ok == true {
		if (logLevel >= logrus.DebugLevel) {
			handlerLogger.Logf("Received Trs-Context-Timeout header")
		}
	}

	return ok
}

var handlerLogger *testing.T
var handlerSleep int    = 2 // time to sleep to simulate network/BMC delays
var retrySleep int      = 0 // time to sleep before returning 503 for retry
var nRetries int32      = 0 // how many retries before returning success
var nHttpTimeouts int   = 0 // how many context timeouts

func launchHandler(w http.ResponseWriter, req *http.Request) {
	if (logLevel >= logrus.TraceLevel) {
		handlerLogger.Logf("launchHandler received an HTTP %v.%v request",
						   req.ProtoMajor, req.ProtoMinor)
	}

	// Distinguish between limited retries that will succeed and retries
	// that should continually fail and exceed their retry limit
	singletonRetry := false
	itHasTRSAlwaysRetryHeader := hasTRSAlwaysRetryHeader(req)
	if !itHasTRSAlwaysRetryHeader {
		singletonRetry = atomic.AddInt32(&nRetries, -1) >= 0
	}

	if singletonRetry || itHasTRSAlwaysRetryHeader {
		if (logLevel >= logrus.DebugLevel) {
			handlerLogger.Logf("launchHandler 503 running (sleep for %vs)...", retrySleep)
		}
		if singletonRetry {
			// Only update for tasks not retrying forever
			nRetries--
		}

		// Delay retry based on test requirement
		time.Sleep(time.Duration(retrySleep) * time.Second)

		w.Header().Set("Content-Type","application/json")
		w.Header().Set("Retry-After","1")
		//w.Header().Set("Connection","keep-alive")
		//w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"Message":"Service Unavailable"}`))

		if (logLevel >= logrus.DebugLevel) {
			handlerLogger.Logf("retryHandler returning Message Service Unavailable...")
		}
	} else if hasTRSStallHeader(req) {
		stallHandler(w, req)
	} else {
		if (logLevel >= logrus.DebugLevel) {
			handlerLogger.Logf("launchHandler running (sleep for %vs)...", handlerSleep)
		}

		// Simulate network/BMC delays
		time.Sleep(time.Duration(handlerSleep) * time.Second)

		if (!hasUserAgentHeader(req)) {
			w.Write([]byte(`{"Message":"No User-Agent Header"}`))
			//w.Header().Set("Connection","keep-alive")
			w.WriteHeader(http.StatusInternalServerError)

			if (logLevel >= logrus.DebugLevel) {
				handlerLogger.Logf("launchHandler returning no User-Agent header...")
			}
			return
		}
		w.Header().Set("Content-Type","application/json")
		//w.Header().Set("Connection","keep-alive")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Message":"OK"}`))

		if (logLevel >= logrus.DebugLevel) {
			handlerLogger.Logf("launchHandler returning Message Ok...")
		}
	}

}

var stallCancel chan bool

func stallHandler(w http.ResponseWriter, req *http.Request) {
	// Wait for all connections to be established so output looks nice
	time.Sleep(100 * time.Millisecond)

	if (logLevel >= logrus.DebugLevel) {
		handlerLogger.Logf("stallHandler running (sleep for %vms)...", 100)
	}

	<-stallCancel

	w.Header().Set("Content-Type","application/json")
	//w.Header().Set("Connection","keep-alive")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))

	if (logLevel >= logrus.DebugLevel) {
		handlerLogger.Logf("stallHandler returning Message Ok...")
	}
}


func TestLaunch(t *testing.T) {
	testLaunch(t, 5, false, false)
}

func TestSecureLaunch(t *testing.T) {
	testLaunch(t, 1, true, false)
}

func TestSecureLaunchBadCert(t *testing.T) {
	// Despite cert being bad, TRS should retry using the insecure
	// client and succeed
	testLaunch(t, 1, true, true)
}

func testLaunch(t *testing.T, numTasks int, testSecureLaunch bool, useBadCert bool) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	var srv *httptest.Server
	if (testSecureLaunch == true) {
		srv = httptest.NewTLSServer(http.HandlerFunc(launchHandler))

		secInfo := TRSHTTPLocalSecurity{CACertBundleData: string("BAD CERT")}

		if (useBadCert != true) {
			secInfo = TRSHTTPLocalSecurity{CACertBundleData:
				string(pem.EncodeToMemory(
					&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw},
				)),}
		}

		err := tloc.SetSecurity(secInfo)
		if err != nil {
			t.Errorf("===> ERROR: tloc.SetSecurity() failed: %v", err)
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
		t.Errorf("===> ERROR: tloc.Launch failed: %v",err)
	}

	nDone := 0
	nErr := 0
	for {
		tdone := <-tch
		nDone ++
		if (tdone == nil) {
			t.Errorf("===> ERROR: Launch chan returned nil ptr.")
		}
		if (tdone.Request == nil) {
			t.Errorf("===> ERROR: Launch chan returned nil Request.")
		} else if (tdone.Request.Response == nil) {
			t.Errorf("===> ERROR: Launch chan returned nil Response.")
		} else {
			if (tdone.Request.Response.StatusCode != http.StatusOK) {
				t.Errorf("===> ERROR: Launch chan returned bad status: %v",tdone.Request.Response.StatusCode)
				nErr ++
			}
			if ((tdone.Err != nil) && ((*tdone.Err) != nil)) {
				t.Errorf("===> ERROR: Launch chan returned error: %v",*tdone.Err)
			}
		}
		running, err := tloc.Check(&tList)
		if (err != nil) {
			t.Errorf("===> ERROR: tloc.Check() failed: %v",err)
		}
		if (nDone == len(tList)) {
			if (running) {
				t.Errorf("===> ERROR: tloc.Check() says still running, but all tasks returned.")
			}
			break
		}
	}

	if (nErr != 0) {
		t.Errorf("===> ERROR: Got %d errors from Launch",nErr)
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
				Retry: RetryPolicy{
						Retries: 1,
						BackoffTimeout: 3 * time.Second},
				},
			}
	tList := tloc.CreateTaskList(&tproto,1)
	stallCancel = make(chan bool, 1)

	tch,err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("===> ERROR: tloc.Launch() failed: %v",err)
	}
	time.Sleep(100 * time.Millisecond)

	nDone := 0
	nErr := 0
	for {
		tdone := <-tch
		nDone ++
		if (tdone == nil) {
			t.Errorf("===> ERROR: Launch chan returned nil ptr.")
		}
		stallCancel <- true
		running, err := tloc.Check(&tList)
		if (err != nil) {
			t.Errorf("===> ERROR: tloc.Check() failed: %v",err)
		}
		if (nDone == len(tList)) {
			if (running) {
				t.Errorf("===> ERROR: Check() says still running, but all tasks returned.")
			}
			break
		}
	}

	if (nErr != 0) {
		t.Errorf("===> ERROR: Got %d errors from Launch",nErr)
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
				t.Errorf("===> ERROR: Failed to find port in LISTEN line: %v", line)
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
		t.Errorf("===> ERROR: Expected %v ESTABLISH(ED) connections, but got %v",
				 clientEstabExp, len(debugOutput["clientEstab"]))
		if logLevel == logrus.TraceLevel {
			t.Logf("Full 'ss' output:\n%s", output)
		}
	}

	if logLevel >= logrus.DebugLevel {
		if len(debugOutput["header"]) > 0 {
			t.Logf("")
			for _,v := range(debugOutput["header"]) {
				t.Log(v)
			}
			t.Logf("")
		}
	}
	if logLevel >= logrus.InfoLevel {
		if len(debugOutput["clientEstab"]) > 0 {
			sort.Strings(debugOutput["clientEstab"])

			t.Logf("- Client ESTAB Connections: (%v)", len(debugOutput["clientEstab"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(debugOutput["clientEstab"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
		if len(debugOutput["clientOther"]) > 0 {
			sort.Strings(debugOutput["clientOther"])

			t.Logf("- Client Other Connections: (%v)", len(debugOutput["clientOther"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(debugOutput["clientOther"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
		if len(debugOutput["serverListen"]) > 0 {
			sort.Strings(debugOutput["serverListen"])

			t.Logf("- Server LISTEN Connections: (%v)", len(debugOutput["serverListen"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(debugOutput["serverListen"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
		if len(debugOutput["serverOther"]) > 0 {
			sort.Strings(debugOutput["serverOther"])

			t.Logf("- Server Other Connections: (%v)", len(debugOutput["serverOther"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(debugOutput["serverOther"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
	}
	if logLevel == logrus.TraceLevel {
		if len(debugOutput["ignoredConn"]) > 0 {
			sort.Strings(debugOutput["ignoredConn"])

			t.Logf("- Ignored Connections: (%v)", len(debugOutput["ignoredConn"]))
			t.Logf("")
			for _,v := range(debugOutput["ignoredConn"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["ignoredListen"]) > 0 {
			sort.Strings(debugOutput["ignoredListen"])

			t.Logf("- Ignored LISTEN Connections: (%v)", len(debugOutput["ignoredListen"]))
			t.Logf("")
			for _,v := range(debugOutput["ignoredListen"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(debugOutput["ignoredMisc"]) > 0 {
			sort.Strings(debugOutput["ignoredMisc"])

			t.Logf("- Ignored Misc Output: (%v)", len(debugOutput["ignoredMisc"]))
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
	if logLevel >= logrus.DebugLevel {
		log.Printf("HTTP_SERVER %v Connection -> %v\t%v",
				   conn.LocalAddr(), state, conn.RemoteAddr())
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

type testConnsArg struct {
	tListProto             *HttpTask // Initialization to pass to tloc.CreateTaskList()
	srvHandler             func(http.ResponseWriter, *http.Request) // response handler to use
	nTasks                 int       // Number of tasks to create
	nSkipDrainBody         int       // Number of response bodies to skip draining before closing
	nSkipCloseBody         int       // Number of response bodies to skip closing
	nSuccessRetries        int32     // Number of retries to succeed
	nFailRetries           int       // Number of retries to fail
	testIdleConnTimeout    bool 	 // Test idle connection timeout
	nHttpTimeouts          int       // Number of context timeouts
	openAtStart            int       // Expected number of ESTAB connections at beginning
	openAfterTasksComplete int       // Expected number of ESTAB connections after all tasks complete
	openAfterBodyClose     int       // Expected number of ESTAB connections after closing response bodies
	skipCancel             bool      // Skip cancel and go directly to Close()
	openAfterCancel        int       // Expected number of ESTAB connections after cancelling tasks
	openAfterClose         int       // Expected number of ESTAB connections after closing task list
	runSecondTaskList	   bool      // Run a second task list after the first with same server
}

func logConnTestHeader(t *testing.T, a testConnsArg) {
	t.Logf("============================================================")

	if logLevel < logrus.ErrorLevel {
		return 
	}

	t.Logf("   nTasks:              %v", a.nTasks)
	t.Logf("   nSkipDrainBody:      %v", a.nSkipDrainBody)
	t.Logf("   nSkipCloseBody:      %v", a.nSkipCloseBody)
	t.Logf("   nSuccessRetries:     %v", a.nSuccessRetries)
	t.Logf("   nFailRetries:        %v", a.nFailRetries)
	t.Logf("   nHttpTimeouts:       %v", a.nHttpTimeouts)
	t.Logf("")
	t.Logf("   testIdleConnTimeout: %v", a.testIdleConnTimeout)
	t.Logf("   runSecondTaskList:   %v", a.runSecondTaskList)
	t.Logf("")
	t.Logf("   Conns open after:    start:         %v", a.openAtStart)
	t.Logf("                        tasksComplete: %v", a.openAfterTasksComplete)
	t.Logf("                        bodyClose:     %v", a.openAfterBodyClose)
	t.Logf("                        cancel:        %v (skip = %v)", a.openAfterCancel, a.skipCancel)
	t.Logf("                        close:         %v", a.openAfterClose)
	t.Logf("")
	t.Logf("   rtPolicy:            httpRetries:         %v", a.tListProto.CPolicy.Retry.Retries)
	t.Logf("")

	if a.tListProto.CPolicy.Tx.Enabled == true {
		t.Logf("")
		t.Logf("   txPolicy:            MaxIdleConns:        %v", a.tListProto.CPolicy.Tx.MaxIdleConns)
		t.Logf("                        MaxIdleConnsPerHost: %v", a.tListProto.CPolicy.Tx.MaxIdleConnsPerHost)
		t.Logf("                        IdleConnTimeout:     %v", a.tListProto.CPolicy.Tx.IdleConnTimeout)
	}

	t.Logf("============================================================")
}

// TestConnsWithHttpTxPolicy tests connection use by TRS callers that do
// NOT configure the http transport.

func TestConnsWithNoHttpTxPolicy(t *testing.T) {
	httpRetries             := 3
	pcsStatusTimeout        := 30
	pcsTimeToNextStatusPoll := 30	// pmSampleInterval
	ctxTimeout              := time.Duration(pcsStatusTimeout) * time.Second

	// Default prototype to initialize each task in the task list with
	// Can customize prior to each test
	defaultTListProto := &HttpTask{
		Timeout: ctxTimeout,
		CPolicy: ClientPolicy {
			Retry: RetryPolicy{Retries: httpRetries},
		},
	}

	// Initialize argument structure (will be modified each test)
	a := testConnsArg{
		tListProto:             defaultTListProto,
		srvHandler:             launchHandler,	// always returns success
	}

	t.Logf("ctxTimeout              = %v", ctxTimeout)
	t.Logf("idleConnTimeout         = %v", idleConnTimeout)
	t.Logf("pcsTimeToNextStatusPoll = %v", pcsTimeToNextStatusPoll)
	t.Logf("MaxIdleConns            = 100 (default)")
	t.Logf("MaxIdleConnsPerHost     = 2   (default)")
	t.Logf("httpRetries             = %v", httpRetries)

	// 10 requests: no issues

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = 10
	a.openAfterBodyClose     = 2	// MaxIdleConnsPerHost
	a.openAfterCancel        = 2	// MaxIdleConnsPerHost
	a.openAfterClose         = 2	// MaxIdleConnsPerHost

	testConns(t, a)

	// 2 requests, 1 skipped body close

	a.nTasks                 = 2	// MaxIdleConnsPerHost
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 1
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 2 requests, 1 skipped body drain
	//
	// If a body is closed but not drained first, the connection is marked
	// as "dirty".  This marks it for lazy closure in the network layer but
	// the connection stays open with status "idle", though it cannot be
	// used.  If a tool like 'ss' starts interrogating the details of
	// connections on a system, this can kick the network layer into closing
	// it.  We run 'ss' in this test so this after closing bodies, so that
	// will actually close any connections with undrained bodies.

	a.nTasks                 = 2	// MaxIdleConnsPerHost
	a.nSkipDrainBody         = 1
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks - a.nSkipDrainBody	// the ss call after body close does it
	a.openAfterCancel        = a.nTasks - a.nSkipDrainBody
	a.openAfterClose         = a.nTasks - a.nSkipDrainBody

	testConns(t, a)

	// 2 requests: 1 request retries once before success

	a.nTasks                 = 2	// MaxIdleConnsPerHost
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 1
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 2 requests, 1 request exhausts retries and fails

	a.nTasks                 = 2	// MaxIdleConnsPerHost
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 1
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	retrySleep = 0	// 0 seconds so retries complete first

	testConns(t, a)

	// 2 requests, 1 http timeout

	//a.nTasks                 = 2	// MaxIdleConnsPerHost
	a.nTasks                 = 2	// MaxIdleConnsPerHost
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 1
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = a.nTasks - a.nHttpTimeouts
	a.openAfterBodyClose     = a.nTasks - a.nHttpTimeouts
	a.openAfterCancel        = a.nTasks - a.nHttpTimeouts
	a.openAfterClose         = a.nTasks - a.nHttpTimeouts

	testConns(t, a)

	// 10 requests, 2 skipped body closes, 3 successful retries, 2 retry failures

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 2
	a.nSuccessRetries        = 3
	a.nFailRetries           = 2
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = 2	// MaxIdleConnsPerHost
	a.openAfterCancel        = 2	// MaxIdleConnsPerHost
	a.openAfterClose         = 2	// MaxIdleConnsPerHost

	testConns(t, a)
}

// TestBasicConnectionBehavior tests the connection behavior that we code
// to in TRS.  This includes the use of an http transport configuration.

func TestBasicConnectionBehaviorWithHttpTxPolicy(t *testing.T) {
	// PCS defaults
	httpRetries             := 3
	pcsTimeToNextStatusPoll := 30	// pmSampleInterval
	pcsStatusTimeout        := 30
	pcsMaxIdleConns         := 1000
	pcsMaxIdleConnsPerHost  := 4

	// Overrides for test purposes
	// Override defaults for testing purposes
	pcsMaxIdleConns        = 10
	pcsMaxIdleConnsPerHost = 10


	// Timeout placed on the context for the http request
	ctxTimeout := time.Duration(pcsStatusTimeout) * time.Second

	// idleConnTimeout is the time after which idle connections are closed.
	// In PCS we want them to stay open between polling intervals so they
	// can be reused for the next poll.  Thus, we set it to the worst case
	// time it takes for one poll (pcsStatusTimeout) plus the time until
	// the next poll (pcsStatusPollInterval).  We add an additional 50% to
	// this for a buffer (ie. multiply by 150%).
	idleConnTimeout := time.Duration(
		(pcsStatusTimeout + pcsTimeToNextStatusPoll) * 15 / 10) * time.Second

	// Default prototype to initialize each task in the task list with
	// Can customize prior to each test
	defaultTListProto := &HttpTask{
		Timeout: ctxTimeout,

		CPolicy: ClientPolicy {
			Retry:
				RetryPolicy {
					Retries: httpRetries,
				},
			Tx:
				HttpTxPolicy {
					Enabled:                  true,
					MaxIdleConns:             pcsMaxIdleConns,
					MaxIdleConnsPerHost:      pcsMaxIdleConnsPerHost,
					IdleConnTimeout:          idleConnTimeout,
					// ResponseHeaderTimeout: responseHeaderTimeout,
					// TLSHandshakeTimeout:   tLSHandshakeTimeout,
					// DisableKeepAlives:     DisableKeepAlives,
			},
		},
	}

	// Initialize argument structure (will be modified each test)
	a := testConnsArg{
		tListProto:             defaultTListProto,
		srvHandler:             launchHandler,	// always returns success
	}

	t.Logf("ctxTimeout              = %v", ctxTimeout)
	t.Logf("idleConnTimeout         = %v", idleConnTimeout)
	t.Logf("pcsTimeToNextStatusPoll = %v", pcsTimeToNextStatusPoll)
	t.Logf("MaxIdleConns            = %v", maxIdleConns)
	t.Logf("MaxIdleConnsPerHost     = %v", maxIdleConnsPerHost)
	t.Logf("httpRetries             = %v", httpRetries)

	// 10 requests: No issues so all conns should be open

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	a.runSecondTaskList = true // second run should not open any new connections

	testConns(t, a)

	a.runSecondTaskList = false

	// 10 requests: 2 skipped body closures

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 2
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 10 requests: 2 skipped body drains
	//
	// If a body is closed but not drained first, the connection is marked
	// as "dirty".  This marks it for lazy closure in the network layer but
	// the connection stays open with status "idle", though it cannot be
	// used.  If a tool like 'ss' starts interrogating the details of
	// connections on a system, this can kick the network layer into closing
	// it.  We run 'ss' in this test so this after closing bodies, so that
	// will actually close any connections with undrained bodies.

	a.nTasks                 = 10
	a.nSkipDrainBody         = 2
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks - a.nSkipDrainBody	// the ss call after body close does it
	a.openAfterCancel        = a.nTasks - a.nSkipDrainBody
	a.openAfterClose         = a.nTasks - a.nSkipDrainBody

	testConns(t, a)

	// 10 requests: 2 retries that both succeed

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 2
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 10 requests: 2 exhaust all retries and fail BEFORE successes complete

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 1
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	retrySleep = 0	// 0 seconds so retries complete first

	testConns(t, a)

	retrySleep = 0					// set back to default

	// 10 requests: 2 exhaust all retries and fail AFTER successes complete
	//              We close the successful tasks response bodies before the
	//              failed retry tasks complete

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 1
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	retrySleep = 4	// 4 seconds so retries complete last

	// We want to run a 2nd task list with the same server to make sure that
	// failures from prior task list don't impact future ones.  This second
	// task list should succeed everything.
	a.runSecondTaskList = true

	testConns(t, a)

	a.runSecondTaskList = false	// set back to default

	retrySleep = 0				// set back to default

	// 10 requests that we run these twice to test IdleConnTimeout:
	//
	//	* 1st run with 2 http timeouts.  2 connections close and go
	//	  into CLOSE-WAIT or FIN-WAIT-2.  After IdleConnTimeout we verify
	//	  that the other 8 connections are now closed to ensure that our
	//    IdleConnTimeout logic is working correctly.
	//
	//	* 2nd run with 10 successful requests so that all connections are
	//	  in ESTAB(LISHED).  After IdleConnTimeout we verify that those
	//    10 connections get closed
	//
	// THESE TESTS WILL TAKE A LOT OF TIME TO COMPLETE!!!!

	a.nTasks                 = 10
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 2 // so we can look at CLOSE-WAIT and FIN-WAIT-2 in traces
	a.testIdleConnTimeout    = true
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks - a.nHttpTimeouts
	a.openAfterBodyClose     = a.nTasks - a.nHttpTimeouts
	a.openAfterCancel        = a.nTasks - a.nHttpTimeouts
	a.openAfterClose         = a.nTasks - a.nHttpTimeouts

	a.runSecondTaskList = true // second run should not open any new connections

	testConns(t, a)

	a.runSecondTaskList    = true	// set back to default
	a.testIdleConnTimeout  = false	// set back to default
}

// TestLargeConnectionPools tests very large connection pools

func TestLargeConnectionPools(t *testing.T) {
	httpRetries             := 3
	pcsTimeToNextStatusPoll := 30	// pmSampleInterval
	pcsStatusTimeout        := 30
	pcsMaxIdleConns         := 1000
	pcsMaxIdleConnsPerHost  := 1000

	// Timeout placed on the context for the http request
	ctxTimeout := time.Duration(pcsStatusTimeout) * time.Second,

	// idleConnTimeout is the time after which idle connections are closed.
	// In PCS we want them to stay open between polling intervals so they
	// can be reused for the next poll.  Thus, we set it to the worst case
	// time it takes for one poll (pcsStatusTimeout) plus the time until
	// the next poll (pcsStatusPollInterval).  We add an additional 50% to
	// this for a buffer (ie. multiply by 150%).
	idleConnTimeout := time.Duration(
		(pcsStatusTimeout + pcsTimeToNextStatusPoll) * 15 / 10) * time.Second

	// Default prototype to initialize each task in the task list with
	// Can customize prior to each test
	defaultTListProto := &HttpTask{
		Timeout: ctxTimeout

		CPolicy: ClientPolicy {
			Retry:
				RetryPolicy {
					Retries: httpRetries,
				},
			Tx:
				HttpTxPolicy {
					Enabled:                  true,
					MaxIdleConns:             pcsMaxIdleConns,
					MaxIdleConnsPerHost:      pcsMaxIdleConnsPerHost,
					IdleConnTimeout:          idleConnTimeout,
					// ResponseHeaderTimeout: responseHeaderTimeout,
					// TLSHandshakeTimeout:   tLSHandshakeTimeout,
					// DisableKeepAlives:     DisableKeepAlives,
			},
		},
	}

	// Initialize argument structure (will be modified each test)
	a := testConnsArg{
		tListProto:             defaultTListProto,
		srvHandler:             launchHandler,	// always returns success
	}

	t.Logf("ctxTimeout              = %v", ctxTimeout)
	t.Logf("idleConnTimeout         = %v", idleConnTimeout)
	t.Logf("pcsTimeToNextStatusPoll = %v", pcsTimeToNextStatusPoll)
	t.Logf("MaxIdleConns            = %v", maxIdleConns)
	t.Logf("MaxIdleConnsPerHost     = %v", maxIdleConnsPerHost)
	t.Logf("httpRetries             = %v", httpRetries)

	// 1000 requests: No issues so all conns should be open

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 1000 requests: 500 skipped body closures

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 500
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 1000 requests: 500 skipped body drains
	//
	// If a body is closed but not drained first, the connection is marked
	// as "dirty".  This marks it for lazy closure in the network layer but
	// the connection stays open with status "idle", though it cannot be
	// used.  If a tool like 'ss' starts interrogating the details of
	// connections on a system, this can kick the network layer into closing
	// it.  We run 'ss' in this test so this after closing bodies, so that
	// will actually close any connections with undrained bodies.

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 500
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks - a.nSkipDrainBody	// the ss call after body close does it
	a.openAfterCancel        = a.nTasks - a.nSkipDrainBody
	a.openAfterClose         = a.nTasks - a.nSkipDrainBody

	testConns(t, a)

	// 1000 requests: 500 retries that succeed

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 500
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	testConns(t, a)

	// 1000 requests: 500 exhaust all retries and fail BEFORE successes complete

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 500
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	retrySleep = 0	// 0 seconds so retries complete first

	testConns(t, a)

	retrySleep = 0					// set back to default

	// 100 requests: 500 exhaust all retries and fail AFTER successes complete
	//               We close the successful tasks response bodies before the
	//               failed retry tasks complete

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 500
	a.nHttpTimeouts          = 0
	a.testIdleConnTimeout    = false
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks
	a.openAfterBodyClose     = a.nTasks
	a.openAfterCancel        = a.nTasks
	a.openAfterClose         = a.nTasks

	retrySleep = 4	// 4 seconds so retries complete last

	// We want to run a 2nd task list with the same server to make sure that
	// failures from prior task list don't impact future ones.  This second
	// task list should succeed everything.
	a.runSecondTaskList = true

	testConns(t, a)

	a.runSecondTaskList = false	// set back to default

	retrySleep = 0				// set back to default

	// 1000 requests that we run these twice to test IdleConnTimeout:
	//
	//	* 1st run with 500 http timeouts.  500 connections close and go
	//	  into CLOSE-WAIT or FIN-WAIT-2.  After IdleConnTimeout we verify
	//	  that the other 8 connections are now closed to ensure that our
	//    IdleConnTimeout logic is working correctly.
	//
	//	* 2nd run with 1000 successful requests so that all connections are
	//	  in ESTAB(LISHED).  After IdleConnTimeout we verify that those
	//    10 connections get closed
	//
	// THESE TESTS WILL TAKE A LOT OF TIME TO COMPLETE!!!!

	a.nTasks                 = 1000
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nHttpTimeouts          = 500 // so we can look at CLOSE-WAIT and FIN-WAIT-2 in traces
	a.testIdleConnTimeout    = true
	a.openAtStart            = 0
	a.openAfterTasksComplete = a.nTasks - a.nHttpTimeouts
	a.openAfterBodyClose     = a.nTasks - a.nHttpTimeouts
	a.openAfterCancel        = a.nTasks - a.nHttpTimeouts
	a.openAfterClose         = a.nTasks - a.nHttpTimeouts

	a.runSecondTaskList = true // second run should not open any new connections

	testConns(t, a)

	a.runSecondTaskList    = true	// set back to default
	a.testIdleConnTimeout  = false	// set back to default
}

const sleepTimeToStabilizeConns = 250 * time.Millisecond

// WARNING: testConns()/runTaskList() is not capable of testing retries and
//          timeouts within the same call.  Please use different tests to
//          test each
func testConns(t *testing.T, a testConnsArg) {
	logConnTestHeader(t, a)

	// Initialize the task system
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	// Copy logger into global namespace for the http server handlers
	handlerLogger = t

	// Create http server.
	srv := httptest.NewServer(http.HandlerFunc(a.srvHandler))

	// Configure server to log changes to connection states
	srv.Config.ConnState = CustomConnState

	// Run the primary task list test
	runTaskList(t, tloc, a, srv)

	// If a second task list is requested, run it
	if (a.runSecondTaskList) {
		// Overwrite args as a 2nd task list should always succeed everything
		// The only impact might be open connections at the start (or not)

		// a.nTasks stays the same
		// a.testIdleConnTimeout stays the same

		a.nSkipDrainBody         = 0
		a.nSkipCloseBody         = 0
		a.nSuccessRetries        = 0
		a.nHttpTimeouts          = 0
		a.nFailRetries           = 0

		if (a.testIdleConnTimeout) {
			a.openAtStart        = 0 // should have all timeed out
		} else {
			a.openAtStart        = a.openAfterClose // carry forward at end of last run
		}

		a.openAfterTasksComplete = a.nTasks
		a.openAfterBodyClose     = a.nTasks
		a.openAfterCancel        = a.nTasks
		a.openAfterClose         = a.nTasks

		t.Logf("===================> RUNNING SECOND TASK LIST <===================")

		runTaskList(t, tloc, a, srv)
	}

	t.Logf("Calling tloc.Cleanup to clean up task system")
	tloc.Cleanup()

	// Cleaking up the task list system should close all connections
	time.Sleep(sleepTimeToStabilizeConns)
	t.Logf("Testing connections after task list cleaned up")
	testOpenConnections(t, 0)

	t.Logf("Closing the server")
	srv.Close()
}

// runTaskList runs a task list from CreateTaskList() through Close()
// It assumes a server is already running

func runTaskList(t *testing.T, tloc *TRSHTTPLocal, a testConnsArg, srv *httptest.Server) {

	t.Logf("Testing connections at start")
	testOpenConnections(t, a.openAtStart)

	// Create an http request
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
        t.Fatalf("===> ERROR: Failed to create request: %v", err)
    }
	req.Header.Set("Accept", "*/*")
	//req.Header.Set("Connection","keep-alive")

	a.tListProto.Request = req
	t.Logf("Calling tloc.CreateTaskList() to create %v tasks for URL %v", a.nTasks, srv.URL)
	tList := tloc.CreateTaskList(a.tListProto, a.nTasks)

	// Configure any requested retries (put at beginning of list)
	nRetries = a.nSuccessRetries
	for i := 0; i < a.nFailRetries; i++ {
		tList[i].Request.Header.Set("Trs-Fail-All-Retries", "true")

		if (logLevel == logrus.DebugLevel) {
			t.Logf("Set request header %v for task %v",
					 tList[i].Request.Header, tList[i].GetID())
		}
	}

	// Configure any requested http timeouts (put at end of list)
	nHttpTimeouts = a.nHttpTimeouts
	for i := len(tList) - 1; i > len(tList) - 1 - a.nHttpTimeouts; i-- {
		tList[i].Request.Header.Set("Trs-Context-Timeout", "true")

		if (logLevel == logrus.DebugLevel) {
			t.Logf("Set request header %v for task %v",
					 tList[i].Request.Header, tList[i].GetID())
		}

		// Create a channel to signal the stalled server handlers to complete
		stallCancel = make(chan bool, a.nHttpTimeouts * 2)
	}

	// All connections should be in ESTAB(LISHED) and should stay there
	// until response bodies are closed or Cancel is called
	t.Logf("Calling tloc.Launch() to launch all tasks")
	taskListChannel, err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("===> ERROR: tloc.Launch() failed: %v", err)
	}

	time.Sleep(2 * time.Second)	// Can take some time for all to get established
	t.Logf("Testing connections after Launch")
	testOpenConnections(t, (a.nTasks))

	// If asked, here we attempt to close task bodies for tasks that have
	// already completed, prior to tasks that will fail retries.  We do this
	// to test if the completed tasks have their connections closed
	tasksToWaitFor := a.nTasks
	if a.nFailRetries > 0 && retrySleep > 0 {
		t.Logf("Waiting for %v non-retry tasks to complete", a.nTasks - a.nFailRetries)
		for i := 0; i < (a.nTasks - a.nFailRetries); i++ {
			<-taskListChannel
			tasksToWaitFor--
		}

		t.Logf("Closing non-retry response bodies early before retry failures")
		for _, tsk := range(tList) {
			if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
				// Must fully read the body in order to close the body so that
				// the underlying libraries/modules don't close the connection.
				// If body not fully conusmed they assume the connection had issues
				_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)

				tsk.Request.Response.Body.Close()
				tsk.Request.Response.Body = nil

				if logLevel == logrus.TraceLevel {
					// Response headers can be  helpful for debug
					t.Logf("Response headers: %s", tsk.Request.Response.Header)
				}
			}
		}

		// All connections should still be in ESTAB(LISHED)
		time.Sleep(time.Duration(a.nTasks) * time.Millisecond)
		t.Logf("Testing connections after non-retry request bodies closed")
		testOpenConnections(t, a.nTasks)
	}

	// Here we attempt to close task bodies and cancel context for tasks that
	// have already completed, prior to tasks that will time.  We do this to
	// test if the completed tasks have their connections closed before the
	// HTTPClient.Timeout expires

	if a.nHttpTimeouts > 0 {
		t.Logf("Waiting for %v non-timeout tasks to complete", a.nTasks - a.nHttpTimeouts)
		for i := 0; i < (a.nTasks - a.nHttpTimeouts); i++ {
			<-taskListChannel
			tasksToWaitFor--
		}

		t.Logf("Closing non-timeout response bodies early and canceling their contexts")
		for i := 0; i < len(tList) - a.nHttpTimeouts; i++ {
			if tList[i].Request.Response != nil && tList[i].Request.Response.Body != nil {
				// Must fully read the body in order to close the body so that
				// the underlying libraries/modules don't close the connection.
				// If body not fully conusmed they assume the connection had issues
				_, _ = io.Copy(io.Discard, tList[i].Request.Response.Body)

				tList[i].Request.Response.Body.Close()
				tList[i].Request.Response.Body = nil

				if logLevel == logrus.TraceLevel {
					// Response headers can be  helpful for debug
					t.Logf("Response headers: %s", tList[i].Request.Response.Header)
				}
			}
			tList[i].contextCancel()
		}

		// All connections should still be in ESTAB(LISHED)
		time.Sleep(time.Duration(a.nTasks) * time.Millisecond)
		t.Logf("Testing connections after non-timeout request bodies closed")
		testOpenConnections(t, a.nTasks)
	}

	t.Logf("Waiting for %d tasks to complete", tasksToWaitFor)
	for i := 0; i < tasksToWaitFor; i++ {
		<-taskListChannel
	}

	t.Logf("Closing the task list channel")
	close(taskListChannel)

	// All connections should still be in ESTAB(LISHED)
	time.Sleep(sleepTimeToStabilizeConns)
	t.Logf("Testing connections after tasks complete")
	testOpenConnections(t, a.openAfterTasksComplete)

	// Set up custom read closer to test if all response bodies get closed
	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			tsk.Request.Response.Body = &CustomReadCloser{tsk.Request.Response.Body, false}
		}
	}

	// Now close the response bodies so connections stay open after we call
	// tloc.Cancel().  We sometimes skip one to test that tloc.Close()
	// closes it for us and the connection associated with it.  Note that
	// if a response body is not drained before closure, the connection
	// stays open but is marked "dirty".  If a tool like 'ss' inspects all
	// our open connections, this act causes the "dirty" connections to
	// close.  Thus, we do want to test for that case too.
	nBodyClosesSkipped := 0
	nBodyDrainSkipped := 0
	t.Logf("Closing response bodies (skip body=%v drain=%v)", a.nSkipCloseBody, a.nSkipDrainBody)
	for _, tsk := range(tList) {
		if nBodyClosesSkipped < a.nSkipCloseBody {
			nBodyClosesSkipped++
			if logLevel == logrus.DebugLevel {
				t.Logf("Skipping closing response body for task %v", tsk.GetID())
			}
			continue
		}
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			// Must fully drain the body in before closing the body so that
			// the underlying libraries/modules don't close the connection.
			// If body not drained before closure the connection stays open
			// but is marked "dirty".  Here we dirty connections if requested
			if nBodyDrainSkipped < a.nSkipDrainBody {
				nBodyDrainSkipped++
				if logLevel == logrus.DebugLevel {
					t.Logf("Skipping draining response body for task %v", tsk.GetID())
				}
			} else {
				_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)
			}

			tsk.Request.Response.Body.Close()
			tsk.Request.Response.Body = nil

			if logLevel == logrus.TraceLevel {
				// Response headers can be  helpful for debug
				t.Logf("Response headers: %s", tsk.Request.Response.Header)
			}
		}
	}

	// Closing the body affects the number of ESTAB(LISHED) connections
	// based on the Transport configuration
	time.Sleep(sleepTimeToStabilizeConns)
	t.Logf("Testing connections after response bodies closed")
	testOpenConnections(t, a.openAfterBodyClose)

	if a.skipCancel {
		t.Logf("Skipping tloc.Cancel()")
	} else {
		// tloc.Cancel() cancels the contexts for all of the tasks in the task list
		t.Logf("Calling tloc.Cancel() to cancel all tasks")
		tloc.Cancel(&tList)

		// Cancelling the task list should not alter existing ESTAB(LISHED)
		// connections except for connections where a response body was not
		// previously closed.  The lower level libraries assume this means
		// that there's a problem with the connection if the body was not closed.
		time.Sleep(sleepTimeToStabilizeConns)
		t.Logf("Testing connections after task list cancelled")
		testOpenConnections(t, a.openAfterCancel)
	}

	// tloc.Close() cancels all contexts, closes any reponse bodies left
	// open, and removes all of the tasks from the task list
	t.Logf("Calling tloc.Close() to close out the task list")
	tloc.Close(&tList)

	// Closing the task list should not alter existing ESTAB(LISHED) connections
	time.Sleep(sleepTimeToStabilizeConns)
	t.Logf("Testing connections after task list closed")
	testOpenConnections(t, a.openAfterClose)

	// Verify that tloc.Close() did indeed close the response bodies that
	// we left open to test it
	t.Logf("Checking for closed response bodies")
	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			if !tsk.Request.Response.Body.(*CustomReadCloser).WasClosed() {
				t.Errorf("===> ERROR: Expected response body for %v to be closed, but it was not", tsk.GetID())
			}
		}
	}

	t.Logf("Checking that the task list was closed")
	if (len(tloc.taskMap) != 0) {
		t.Errorf("===> ERROR: Expected task list map to be empty")
	}

	if (a.nHttpTimeouts > 0) {
		t.Logf("Signaling stalled handlers ")
		for i := 0; i < a.nHttpTimeouts * 2; i++ {
			stallCancel <- true
		}
	}

	if (a.testIdleConnTimeout) {
		// TODO: Should also comfirm no client "other" connections as well
		t.Logf("Testing connections after idleConnTimeout")
		time.Sleep(a.tListProto.CPolicy.Tx.IdleConnTimeout)
		testOpenConnections(t, 0)
	}
}