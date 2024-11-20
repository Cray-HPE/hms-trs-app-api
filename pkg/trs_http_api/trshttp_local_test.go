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

// Note for unit test and TRS loggers: Log level can be controlled by
// by modifying the 
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

// Create a logger for trs_http_api (not unit tests) so we can see what's
// going on in TRS when we hit errors
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
		t.Errorf("=====> ERROR: Init() failed to create task map <=====")
	}
	if (tloc.clientMap == nil) {
		t.Errorf("=====> ERROR: Init() failed to create client map <=====")
	}
	if (tloc.svcName != svcName) {
		t.Errorf("=====> ERROR: Init() failed to set service name <=====")
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
		t.Errorf("=====> ERROR: CreateTaskList() didn't create a correct array. <=====")
	}
	for _,tsk := range(tList) {
		if (tsk.Request == nil) {
			t.Errorf("=====> ERROR: CreateTaskList() didn't create a proper Request. <=====")
		}
		if (len(tsk.Request.Header) == 0) {
			t.Errorf("=====> ERROR: CreateTaskList() didn't create a proper Request header. <=====")
		}
		vals,ok := tsk.Request.Header["User-Agent"]
		if (!ok) {
			t.Errorf("=====> ERROR: CreateTaskList() didn't copy User-Agent header. <=====")
		}
		found := false
		for _,vr := range(vals) {
			if (vr == svcName) {
				found = true
				break
			}
		}
		if (!found) {
			t.Errorf("=====> ERROR: CreateTaskList() didn't copy User-Agent header. <=====")
		}
	}
}

// Check header for "User-Agent"
func hasUserAgentHeader(r *http.Request) bool {
    if (len(r.Header) == 0) {
        return false
    }

    _,ok := r.Header["User-Agent"]
	return ok
}

// Check header for "Trs-Fail-All-Retries"
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

// Check header for "Trs-Context-Timeout"
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

// launchHandler is the general handler used by the unittest http servers
// to handle incoming requests.  Depending on the request headers, it can:
//
//	* Return a 503 error for a specific request once
//	* Return a 503 error for a specific request multiple times so it burns
//	  through all its retries
//	* Stall indefinitly until a unit test signals to return a successful
//	  response
//	* Return a successful response immediately

var handlerLogger *testing.T	// Allows logging in the handlers
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

// stallHandler is handler used by the unittest http servers to simulate
// stalls in handling requests. It simply sits on a channel until the unit
// test in question signals it to return
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
			t.Errorf("=====> ERROR: tloc.SetSecurity() failed: %v <=====", err)
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
		t.Errorf("=====> ERROR: tloc.Launch failed: %v <=====",err)
	}

	nDone := 0
	nErr := 0
	for {
		tdone := <-tch
		nDone ++
		if (tdone == nil) {
			t.Errorf("=====> ERROR: Launch chan returned nil ptr. <=====")
		}
		if (tdone.Request == nil) {
			t.Errorf("=====> ERROR: Launch chan returned nil Request. <=====")
		} else if (tdone.Request.Response == nil) {
			t.Errorf("=====> ERROR: Launch chan returned nil Response. <=====")
		} else {
			if (tdone.Request.Response.StatusCode != http.StatusOK) {
				t.Errorf("=====> ERROR: Launch chan returned bad status: %v <=====",tdone.Request.Response.StatusCode)
				nErr ++
			}
			if ((tdone.Err != nil) && ((*tdone.Err) != nil)) {
				t.Errorf("=====> ERROR: Launch chan returned error: %v <=====",*tdone.Err)
			}
		}
		running, err := tloc.Check(&tList)
		if (err != nil) {
			t.Errorf("=====> ERROR: tloc.Check() failed: %v <=====",err)
		}
		if (nDone == len(tList)) {
			if (running) {
				t.Errorf("=====> ERROR: tloc.Check() says still running, but all tasks returned. <=====")
			}
			break
		}
	}

	if (nErr != 0) {
		t.Errorf("=====> ERROR: Got %d errors from Launch <=====",nErr)
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
		t.Errorf("=====> ERROR: tloc.Launch() failed: %v <=====",err)
	}
	time.Sleep(100 * time.Millisecond)

	nDone := 0
	nErr := 0
	for {
		tdone := <-tch
		nDone ++
		if (tdone == nil) {
			t.Errorf("=====> ERROR: Launch chan returned nil ptr. <=====")
		}
		stallCancel <- true
		running, err := tloc.Check(&tList)
		if (err != nil) {
			t.Errorf("=====> ERROR: tloc.Check() failed: %v <=====",err)
		}
		if (nDone == len(tList)) {
			if (running) {
				t.Errorf("=====> ERROR: Check() says still running, but all tasks returned. <=====")
			}
			break
		}
	}

	if (nErr != 0) {
		t.Errorf("=====> ERROR: Got %d errors from Launch <=====",nErr)
	}
	close(stallCancel)
}

///////////////////////////////////////////////////////////////////////////
//
// Documenting treatment of connections due to handling of response bodies here
// as its as good of place as any to put this information.
//
// If response body is closed
//
//		And body was drained before closure:
//
//			* Go client connection state: open, idle, reusable
//			* OS connection state:        open, idle, reusable
//			* Istio connection state:     open, idle, reusable
//
//		And body was NOT drained before closure
//
//			* Go client connection state: closed
//			* OS connection state:        closed
//			* Istio connection state:     closed
//
// If response body is NOT closed
//
//		And body was drained
//
//			* Go client connection state: open, unusable (resource leak) (dirty)
//			* OS connection state:        open, unusable (resource leak) (dirty)
//			* Istio connection state:     open, unusable (resource leak) (dirty)
//
//			* Marked "dirty" and could get cleaned up any time since the
//			  body was drained
//
//			* If/wen IdleConnTimeout is exceeded (by default is 0 which means
//			  no timeout in place), it will be closed and:
//
//			  	* I couldn't find a definitive answer if a minimal
//				  resource leak (which would NOT include body data) would
//			      be permanent or not in the Go client
//				* Prior OS resource leak should now be freed (not sure I believe)
//				* Prior Istio resource leak should now be freed (not sure I believe)
//
//		And body was NOT drained
//
//			* Go client connection state: open, unusable (resource leak)
//			* OS connection state:        open, unusable (resource leak)
//			* Istio connection state:     open, unusable (resource leak)
//
//			* If/wen IdleConnTimeout is exceeded (by default is 0 which means
//			  no timeout in place), it will be closed and:
//
//				* Go client resource leak (including body data) will remain
//				* Prior OS resource leak should now be freed (not sure I believe)
//				* Prior Istio resource leak should now be freed (not sure I believe)
//
///////////////////////////////////////////////////////////////////////////

// CustomConnState is a hook into httptest http servers that the unit tests
// below spin up.  It allows is to log changes to connection states.  This
// is critical when debugging connection state issues

func CustomConnState(conn net.Conn, state http.ConnState) {
	if logLevel >= logrus.DebugLevel {
		log.Printf("HTTP_SERVER %v Connection -> %v\t%v",
				   conn.LocalAddr(), state, conn.RemoteAddr())
	}
}

// testOpenConnections is called with an argument representing the number
// of connections in the ESTAB(LISTHED) that we hope to find at the current
// moment. It does this by executing the 'ss' tool in the unit test vm and
// using the output to count various connection states.

func testOpenConnections(t *testing.T, clientEstabExp int) {
	// Set up 'ss' command and arguments
	cmd := exec.Command( "ss", "--tcp", "--resolve", "--processes", "--all")

	// Execute the command
	fullOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("=====> ERROR: error running ss utility: %v <=====", err)
		return
	}

	// srvrPorts - Map of every server port for trs_http_api
	srvrPorts := map[string]bool{}

	// output - Map used to collect different buckets of output that we care about
	output := map[string][]string{}

	// Use a scanner to parse through fullOutput line-by-line
	scanner := bufio.NewScanner(bytes.NewReader(fullOutput))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "Recv-Q") {
			// Header line. Save it as it helps when reading output
			output["header"] = append(output["header"], line)
			continue
		} else if strings.Contains(line, "LISTEN") {
			// This is a server. LISTEN lines always comes first in the output.
			// Ignore anything that isn't our test process
			if !strings.Contains(line, "trs_http_api") {
				output["ignoredListen"] = append(output["ignoredListen"], line)
				continue
			}

			// Grab the port so we can filter on it later
			output["serverListen"] = append(output["serverListen"], line)

			re := regexp.MustCompile(`localhost:(\d+)`)

			match := re.FindStringSubmatch(line)
			if len(match) > 1 {
				srvrPorts[match[1]] = true
			} else {
				t.Errorf("=====> ERROR: Failed to find port in LISTEN line: %v <=====", line)
			}
		} else {
			// This is a connection. We now need to distinguish client
			// connections from server connections
			re := regexp.MustCompile(`localhost:(\d+)\s+localhost:(\d+)`)

			match := re.FindStringSubmatch(line)
			if len(match) > 2 {
				srcPort := match[1]
				dstPort := match[2]

				if _, exists := srvrPorts[srcPort]; exists {
					// This is a server connection
					output["serverOther"] = append(output["serverOther"], line)
				} else {
					// This is might be a client connection.  Test to see
					// if it targets one of our server ports
					if _, exists := srvrPorts[dstPort]; exists {
						// It's one of our client connections
						if strings.Contains(line, "ESTAB") {
							output["clientEstab"] = append(output["clientEstab"], line)
						} else {
							output["clientOther"] = append(output["clientOther"], line)
						}
					} else {
						// An unrelated connection.  Ignore
						output["ignoredConn"] = append(output["ignoredConn"], line)
					}
				}
			} else {
				// An unrelated line of output.  Ignore
				output["ignoredMisc"] = append(output["ignoredMisc"], line)
			}
		}
	}

	// This is the critical test for this routine
	if (len(output["clientEstab"]) != clientEstabExp) {
		t.Errorf("=====> ERROR: Expected %v ESTABLISH(ED) connections, but got %v <=====",
				 clientEstabExp, len(output["clientEstab"]))
		if logLevel == logrus.TraceLevel {
			t.Logf("Full 'ss' output:\n%s", fullOutput)
		}
	}

	// All other output as follows is for debug only

	if logLevel >= logrus.InfoLevel {
		if len(output["header"]) > 0 {
			t.Logf("")
			for _,v := range(output["header"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(output["clientEstab"]) > 0 {
			sort.Strings(output["clientEstab"])

			t.Logf("- Client ESTAB Connections: (%v)", len(output["clientEstab"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(output["clientEstab"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
	}
	if logLevel >= logrus.DebugLevel {
		if len(output["clientOther"]) > 0 {
			sort.Strings(output["clientOther"])

			t.Logf("- Client Other Connections: (%v)", len(output["clientOther"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(output["clientOther"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
	}
	if logLevel >= logrus.InfoLevel {
		if len(output["serverListen"]) > 0 {
			sort.Strings(output["serverListen"])

			t.Logf("- Server LISTEN Connections: (%v)", len(output["serverListen"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(output["serverListen"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
	}
	if logLevel >= logrus.DebugLevel {
		if len(output["serverOther"]) > 0 {
			sort.Strings(output["serverOther"])

			t.Logf("- Server Other Connections: (%v)", len(output["serverOther"]))

			if logLevel > logrus.InfoLevel {
				t.Logf("")
				for _,v := range(output["serverOther"]) {
					t.Log(v)
				}
				t.Logf("")
			}
		}
	}
	if logLevel == logrus.TraceLevel {
		if len(output["ignoredConn"]) > 0 {
			sort.Strings(output["ignoredConn"])

			t.Logf("- Ignored Connections: (%v)", len(output["ignoredConn"]))
			t.Logf("")
			for _,v := range(output["ignoredConn"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(output["ignoredListen"]) > 0 {
			sort.Strings(output["ignoredListen"])

			t.Logf("- Ignored LISTEN Connections: (%v)", len(output["ignoredListen"]))
			t.Logf("")
			for _,v := range(output["ignoredListen"]) {
				t.Log(v)
			}
			t.Logf("")
		}
		if len(output["ignoredMisc"]) > 0 {
			sort.Strings(output["ignoredMisc"])

			t.Logf("- Ignored Misc Output: (%v)", len(output["ignoredMisc"]))
			t.Logf("")
			for _,v := range(output["ignoredMisc"]) {
				t.Log(v)
			}
			t.Logf("")
		}
	}
}

// CustomReadCloser wraps an io.ReadCloser so we can track if response
// bodies get closed in the unit tests below.

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

// testConnsArg is the struct used to hold arguments to the lower layer
// connection tests so that that code can be reused for many tests

type testConnsArg struct {
	tListProto             *HttpTask // Initialization to pass to tloc.CreateTaskList()
	srvHandler             func(http.ResponseWriter, *http.Request) // response handler to use
	nTasks                 int       // Number of tasks to create
	maxIdleConnsPerHost    int       // Value of MaxIdleConnsPerHost
	nSuccessRetries        int32     // Number of retries to succeed
	nFailRetries           int       // Number of retries to fail
	nSkipDrainBody         int       // Number of response bodies to skip draining before closing
	nSkipCloseBody         int       // Number of response bodies to skip closing
	nHttpTimeouts          int       // Number of context timeouts
	testIdleConnTimeout    bool 	 // Test idle connection timeout
	runSecondTaskList	   bool      // Run a second task list after the first with same server
	openAtStart            int       // Expected number of ESTAB connections at beginning
	openAfterLaunch        int       // Expected number of ESTAB connections after Launch()
	openAfterTasksComplete int       // Expected number of ESTAB connections after all tasks complete
	openAfterBodyClose     int       // Expected number of ESTAB connections after closing response bodies
	skipCancel             bool      // Skip cancel and go directly to Close()
	openAfterCancel        int       // Expected number of ESTAB connections after cancelling tasks
	openAfterClose         int       // Expected number of ESTAB connections after closing task list
}

// logConnTestHeader prints header for each connection related test

func logConnTestHeader(t *testing.T, a testConnsArg) {
	t.Logf("")
	t.Logf("--------------------------------------------------")
	t.Logf("-             Sub-Test Configuration             -")
	t.Logf("--------------------------------------------------")
	t.Logf("")

	if logLevel < logrus.ErrorLevel {
		return 
	}

	t.Logf("   nTasks:              %v", a.nTasks)
	t.Logf("   nSuccessRetries:     %v", a.nSuccessRetries)
	t.Logf("   nFailRetries:        %v", a.nFailRetries)
	t.Logf("   nSkipDrainBody:      %v", a.nSkipDrainBody)
	t.Logf("   nSkipCloseBody:      %v", a.nSkipCloseBody)
	t.Logf("   nHttpTimeouts:       %v", a.nHttpTimeouts)
	t.Logf("")
	t.Logf("   testIdleConnTimeout: %v", a.testIdleConnTimeout)
	t.Logf("   runSecondTaskList:   %v", a.runSecondTaskList)
	t.Logf("")
	t.Logf("   Conns open after:    start:         %v", a.openAtStart)
	t.Logf("                        launch:        %v", a.openAfterLaunch)
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

	t.Logf("")
}

///////////////////////////////////////////////////////////////////////////
//
// MANY CONNECTION TESTS BELOW ARE MARKED AS SKIP BECAUSE:
//
//	* The unit test framework in GitHub only allows 10 minutes for ALL
//	  unit tests to complete
//
//	* Connection and task counts of more than 1000 sometimes do not match
//	  what one would expect to see based on observations at smaller
//	  connection and task counts. That is not to say that things are not
//	  behaving correctly. It's more likely the case that at higher counts
//	  the underlying logic and timing of that logic is not as simplistic
//	  as it is at smaller counts for outside observers.  Trying to write
//	  tests to precisely match what's going on in the black box at the
//	  system level is nearly impossible. The counts that the unit tests
//	  do detect at these higher counts are within a very reasonable small
//	  deviation from what one would expect. For these reasons, the tests
//	  at higher counts are not enabled by default.  When changes to TRS
//	  are made in the future, developers should re-enable them and
//	  reconfirm that the resultant behavior is still within a reasonable
//	  deviation.
//
///////////////////////////////////////////////////////////////////////////

// TestConnsWithNoHttpTxPolicy* tests use by TRS users that do NOT
// configure the http transport.  This would be the case for TRS users
// if they updated to the latest TRS without configuring the transport,
// which is a newer feature of TRS.

func TestConnsWithNoHttpTxPolicy_Idle(t *testing.T) {
	nTasks  := 2	// default MaxIdleConnsPerHost
	nIssues := 1

	testConnsWithNoHttpTxPolicy(t, nTasks, nIssues)
}

func TestConnsWithNoHttpTxPolicy_ModeratlyBusy(t *testing.T) {

	nTasks  := 1000
	nIssues := 1

	testConnsWithNoHttpTxPolicy(t, nTasks, nIssues)
}

func TestConnsWithNoHttpTxPolicy_Busy(t *testing.T) {

	//t.Skip()	/***************** REMOVE TO RUN TEST *****************/

	nTasks  := 4000
	nIssues := 200

	testConnsWithNoHttpTxPolicy(t, nTasks, nIssues)
}

func TestConnsWithNoHttpTxPolicy_VeryBusy(t *testing.T) {

	t.Skip()	/***************** REMOVE TO RUN TEST *****************/

	nTasks  := 8000
	nIssues := 40

	testConnsWithNoHttpTxPolicy(t, nTasks, nIssues)
}

// testConnsWithNoHttpTxPolicy() is the next call level down for the above
// tests that don't configure an HttpTxPolicy

func testConnsWithNoHttpTxPolicy(t *testing.T, nTasks int, nIssues int) {
	httpRetries             := 3
	pcsStatusTimeout        := 30
	ctxTimeout              := time.Duration(pcsStatusTimeout) * time.Second
	maxIdleConnsPerHost     := 2		// default when not using HttpTxPolicy
	//maxIdleConns            := 100	// default when using HttpTxPolicy
	//pcsTimeToNextStatusPoll := 30		// pmSampleInterval

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
		maxIdleConnsPerHost:    maxIdleConnsPerHost,
		tListProto:             defaultTListProto,
		srvHandler:             launchHandler,	// always returns success
	}

	testConnsPrep(t, a, nTasks, nIssues)
}

// TestConnsWithHttpTxPolicy* tests use by TRS users that do configure
// the http transport.  We will use the defaults used by the PCS status
// configuration for most of these.

func TestConnsWithHttpTxPolicy_PcsSmallIdle(t *testing.T) {
	nTasks              := 4
	nIssues             := 4
	maxIdleConnsPerHost := 4	// PCS default when using HttpTxPolicy
	maxIdleConns        := 1000	// PCS default when using HttpTxPolicy
	pcsStatusTimeout    := 30   // PCS default

	testConnsWithHttpTxPolicy(t, nTasks, nIssues, maxIdleConnsPerHost, maxIdleConns, pcsStatusTimeout)
}

func TestConnsWithHttpTxPolicy_PcsSmallModeratlyBusy(t *testing.T) {
	nTasks              := 1000
	nIssues             := 10
	maxIdleConnsPerHost := 4	// PCS default when using HttpTxPolicy
	maxIdleConns        := 1000	// PCS default when using HttpTxPolicy
	pcsStatusTimeout    := 30   // PCS default

	testConnsWithHttpTxPolicy(t, nTasks, nIssues, maxIdleConnsPerHost, maxIdleConns, pcsStatusTimeout)
}

func TestConnsWithHttpTxPolicy_PcsSimulatedMedium(t *testing.T) {
	nTasks              := 1000
	nIssues             := 4
	maxIdleConnsPerHost := 1000 // Simulate more servers and larger connection pool
	maxIdleConns        := 1000	// PCS default when using HttpTxPolicy
	pcsStatusTimeout    := 30   // PCS default

	//t.Skip()	/***************** REMOVE TO RUN TEST *****************/

	testConnsWithHttpTxPolicy(t, nTasks, nIssues, maxIdleConnsPerHost, maxIdleConns, pcsStatusTimeout)
}

func TestConnsWithHttpTxPolicy_PcsSmallBusy(t *testing.T) {

	t.Skip()	/***************** REMOVE TO RUN TEST *****************/

	nTasks              := 4000
	nIssues             := 10
	maxIdleConnsPerHost := 1000	// Simulate more servers and larger connection pool
	maxIdleConns        := 1000	// PCS default when using HttpTxPolicy
	pcsStatusTimeout    := 30   // PCS default

	testConnsWithHttpTxPolicy(t, nTasks, nIssues, maxIdleConnsPerHost, maxIdleConns, pcsStatusTimeout)
}

func TestConnsWithHttpTxPolicy_PcsLargeBusy(t *testing.T) {

	t.Skip()	/***************** REMOVE TO RUN TEST *****************/

	nTasks              := 8000
	nIssues             := 10
	maxIdleConnsPerHost := 8000  // We're only using one Host server so pretend
	maxIdleConns        := 8000  // 8000 requests / 4 per host = 2000 BMCs
	pcsStatusTimeout    := 60    // Increase for pitiful unit test vm 

	testConnsWithHttpTxPolicy(t, nTasks, nIssues, maxIdleConnsPerHost, maxIdleConns, pcsStatusTimeout)
}

func TestConnsWithHttpTxPolicy_PcsHugeBusy(t *testing.T) {

	t.Skip()	/***************** REMOVE TO RUN TEST *****************/

	nTasks               := 24000  // TRS can handle larger but unit test vm can't
	nIssues              := 2
	maxIdleConnsPerHost  := 24000  // We're only using one Host server so pretend
	maxIdleConns         := 24000  // 24000 requests / 4 per host = 6000 BMCs
	pcsStatusTimeout     := 60     // Larger so we have more time to sleep to wait

	testConnsWithHttpTxPolicy(t, nTasks, nIssues, maxIdleConnsPerHost, maxIdleConns, pcsStatusTimeout)
}

// testConnsWithHttpTxPolicy() is the next call level down for the above
// tests that do configure the http transport

func testConnsWithHttpTxPolicy(t *testing.T, nTasks int, nIssues int,
	                           maxIdleConnsPerHost int, maxIdleConns int,
							   pcsStatusTimeout int) {
	httpRetries             := 3
	ctxTimeout              := time.Duration(pcsStatusTimeout) * time.Second
	pcsTimeToNextStatusPoll := 30	// pmSampleInterval

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
					MaxIdleConns:             maxIdleConns,
					MaxIdleConnsPerHost:      maxIdleConnsPerHost,
					IdleConnTimeout:          idleConnTimeout,
					// ResponseHeaderTimeout: responseHeaderTimeout,
					// TLSHandshakeTimeout:   tLSHandshakeTimeout,
					// DisableKeepAlives:     DisableKeepAlives,
			},
		},
	}

	// Initialize argument structure (will be modified each test)
	a := testConnsArg{
		maxIdleConnsPerHost:    maxIdleConnsPerHost,
		tListProto:             defaultTListProto,
		srvHandler:             launchHandler,	// always returns success
	}

	testConnsPrep(t, a, nTasks, nIssues)
}

// testConnsPrep is the next call level down for ALL conection related
// unit tests.  It configures each of the sub-tests to send down to yet
// another call level

func testConnsPrep(t *testing.T, a testConnsArg, nTasks int, nIssues int) {

	t.Logf("")
	t.Logf("============================================================")
	t.Logf("=                  Test Configuration                    ===")
	t.Logf("============================================================")
	t.Logf("")
	t.Logf("nTasks                  = %v", nTasks)
	t.Logf("nIssues                 = %v", nIssues)
	t.Logf("Retries                 = %v", a.tListProto.CPolicy.Retry.Retries)
	t.Logf("ctxTimeout              = %v", a.tListProto.Timeout)

	if a.tListProto.CPolicy.Tx.Enabled {
		t.Logf("idleConnTimeout         = %v", a.tListProto.CPolicy.Tx.IdleConnTimeout)
		t.Logf("MaxIdleConns            = %v", a.tListProto.CPolicy.Tx.MaxIdleConns)
		t.Logf("MaxIdleConnsPerHost     = %v", a.tListProto.CPolicy.Tx.MaxIdleConnsPerHost)
	} else {
		t.Logf("idleConnTimeout         = 0   (default = unlimited)")
		t.Logf("MaxIdleConnsPerHost     = 2   (default)")
		t.Logf("MaxIdleConns            = 100 (default)")
	}

	t.Logf("")

	///////////////////////////////////////////////////////
	// All successes

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks
	a.openAfterTasksComplete = a.openAfterLaunch
	a.openAfterBodyClose     = a.maxIdleConnsPerHost
	a.openAfterCancel        = a.maxIdleConnsPerHost
	a.openAfterClose         = a.maxIdleConnsPerHost

	testConns(t, a)

	///////////////////////////////////////////////////////
	// Successful retries

	a.nTasks                 = nTasks
	a.nSuccessRetries        = int32(nIssues)
	a.nFailRetries           = 0
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks
	a.openAfterTasksComplete = a.openAfterLaunch
	a.openAfterBodyClose     = a.maxIdleConnsPerHost
	a.openAfterCancel        = a.maxIdleConnsPerHost
	a.openAfterClose         = a.maxIdleConnsPerHost

	testConns(t, a)

	///////////////////////////////////////////////////////
	// Failed retries due to retries exceeded that complete before the
	// successful reception of a response

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = nIssues
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0

	// Connections for failed retries will be subject to maxIdleConnsPerHost

	openAfter := a.nFailRetries
	if openAfter > a.maxIdleConnsPerHost {
		openAfter = a.maxIdleConnsPerHost
	}

	openAfter = openAfter + (nTasks - nIssues)

	a.openAfterLaunch        = openAfter
	a.openAfterTasksComplete = a.openAfterLaunch
	a.openAfterBodyClose     = a.maxIdleConnsPerHost
	a.openAfterCancel        = a.maxIdleConnsPerHost
	a.openAfterClose         = a.maxIdleConnsPerHost

	retrySleep   = 0	// 0 seconds so retries complete first

	if a.nTasks < 1000 && a.nFailRetries < 10 {
		handlerSleep = 5	// slow down the others
	} else {
		handlerSleep = 20	// slow down the others
	}

	testConns(t, a)

	handlerSleep = 2	// Set back to default
	retrySleep   = 0	// Set back to default

	///////////////////////////////////////////////////////
	// Failed retries due to retries exceeded that completes after the
	// successful reception of a response

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = nIssues
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks
	a.openAfterTasksComplete = a.maxIdleConnsPerHost	// successful tasks closed bodies already
	a.openAfterBodyClose     = a.maxIdleConnsPerHost
	a.openAfterCancel        = a.maxIdleConnsPerHost
	a.openAfterClose         = a.maxIdleConnsPerHost

	if a.nTasks < 1000 && a.nFailRetries < 10 {
		retrySleep = 10	// So retries complete after
	} else {
		retrySleep = 20	// So retries complete after
	}

	testConns(t, a)

	retrySleep = 0	// Set back to default

	///////////////////////////////////////////////////////
	// Body Drain: skip
	// Body Close: yes
	//
	// Even though we close the body, if it was not drained first the
	// connection gets closed

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nSkipDrainBody         = nIssues
	a.nSkipCloseBody         = 0
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks
	a.openAfterTasksComplete = a.openAfterLaunch

	// This test closes drained bodies so account for that along with the
	// max number of connections allowed per host

	openAfter = a.nTasks - a.nSkipDrainBody
	if openAfter > a.maxIdleConnsPerHost {
		openAfter = a.maxIdleConnsPerHost
	}

	a.openAfterBodyClose     = openAfter
	a.openAfterCancel        = openAfter
	a.openAfterClose         = openAfter

	testConns(t, a)

	///////////////////////////////////////////////////////
	// Body Drain: yes
	// Body Close: skip
	//
	// Connection stays open if the body is never closed and is not reusable
	// for any other requests (unless body is later drained/closed)
	//
 	// However, because it was marked "dirty" it can get cleaned up by
	// the system at any time. I've seen that using 'ss' in the tests
	// can force this to happen immediately after tasks complete if the
	// number of open connections is greater than maxIdleConnsPerHost

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = nIssues
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks
	a.openAfterTasksComplete = a.openAfterLaunch
	a.openAfterBodyClose     = a.maxIdleConnsPerHost
	a.openAfterCancel        = a.maxIdleConnsPerHost
	a.openAfterClose         = a.maxIdleConnsPerHost

	testConns(t, a)

	///////////////////////////////////////////////////////
	// Body Drain: skip
	// Body Close: skip
	//
	// Like the above test case, an open body stays open if the body is
	// never closed and is not reusable for any other requests (unless
	// body is later drained/closed).
	//
	// Unlike the case above, the connection is not marked "dirty" and is
	// not closed unless the caller closes the body or IdleConnTimeout
	// causes it to close.

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nSkipDrainBody         = nIssues
	a.nSkipCloseBody         = nIssues
	a.nHttpTimeouts          = 0

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks
	a.openAfterTasksComplete = a.openAfterLaunch

	// Truncate the good connections down to MaxIdleConnsPerHost
	// plus whatever connections are yucky

	openAfter = a.nTasks - a.nSkipDrainBody
	if openAfter > a.maxIdleConnsPerHost {
		openAfter = a.maxIdleConnsPerHost
	}
	openAfter = openAfter + a.nSkipDrainBody

	a.openAfterBodyClose     = openAfter
	a.openAfterCancel        = openAfter

	// Unclosed bodies will now be closed by tloc.Close()

	if openAfter > a.maxIdleConnsPerHost - a.nSkipDrainBody {
		a.openAfterClose     = a.maxIdleConnsPerHost
	} else {
		a.openAfterClose     = openAfter
	}

	testConns(t, a)

	///////////////////////////////////////////////////////
	// Timeouts.  The http timeout will trigger first if HttpTxPolicy is
	// configured by the caller.  If HtppTxPolicy is not configured, only
	// the context timeout is set (because default IdleConnTimeout is 0
	// which means no http timeout).

	a.nTasks                 = nTasks
	a.nSuccessRetries        = 0
	a.nFailRetries           = 0
	a.nSkipDrainBody         = 0
	a.nSkipCloseBody         = 0
	a.nHttpTimeouts          = nIssues

	a.openAtStart            = 0
	a.openAfterLaunch        = a.nTasks

	// Timed out connections will close but we also need to account for
	// the max idle connections allowed per host

	openAfter = a.openAfterLaunch - a.nHttpTimeouts
	if openAfter > a.maxIdleConnsPerHost {
		openAfter = a.maxIdleConnsPerHost
	}

	a.openAfterTasksComplete = openAfter
	a.openAfterBodyClose     = openAfter
	a.openAfterCancel        = openAfter
	a.openAfterClose         = openAfter

	// We run a second task list through the client to verify that the prior
	// task list, which had issues, has no effect on subsequent task list
	// execution

	a.runSecondTaskList = true

	testConns(t, a)

	a.runSecondTaskList = false
}

const sleepTimeToStabilizeConns = 250 * time.Millisecond

// testConns runs tloc.Init() to set up a task list service.  It starts
// the http server and creates the retryablehttp clients.  It can issue
// multiple sets of task list requests to one final lower layer if the
// upper layers request it.
//
// WARNING: testConns()/runTaskList() is not capable of testing retries and
//          timeouts within the same call.  Please use different tests to
//          test each

func testConns(t *testing.T, a testConnsArg) {
	logConnTestHeader(t, a)

	// Initialize the task system
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger())

	// Copy logger into global namespace so that the http server handlers
	// can use it
	handlerLogger = t

	// Create http server.  We should probably make an addition later to
	// create multiple servers.
	srv := httptest.NewServer(http.HandlerFunc(a.srvHandler))

	// Configure server to log changes to connection states
	srv.Config.ConnState = CustomConnState

	// Run the primary task list test
	runTaskList(t, tloc, a, srv)

	// If a second task list is requested, run it
	if (a.runSecondTaskList) {
		// Overwrite prior args.  Any second task list run should always
		// always succeed everything as we're testing to see if any problems
		// caused by the prior run can affect a future run. The only valid
		// impact we should see are open connections from the prior run,
		// which is actually a good thing!

		// a.nTasks              stays the same
		// a.testIdleConnTimeout stays the same

		a.nSkipDrainBody         = 0	// We want no issues
		a.nSkipCloseBody         = 0	// We want no issues
		a.nSuccessRetries        = 0	// We want no issues
		a.nHttpTimeouts          = 0	// We want no issues
		a.nFailRetries           = 0	// We want no issues

		// If we tested that exceeding IdleConnTimeout closes all connections
		// during the first task list run, we should have no open collections
		// at the start of this run

		if (a.testIdleConnTimeout) {
			// They should have all timeed out and closed
			a.openAtStart        = 0
		} else {
			// What was open at the end of the last run should still be open
			a.openAtStart        = a.openAfterClose
		}

		// Carry forward the same number of tasks.  Since there will be no
		// issues or errors in the second run, open connections should be
		// truncated down to MaxIdleConnsPerHost after bodies get closed

		a.openAfterLaunch        = a.nTasks
		a.openAfterTasksComplete = a.openAfterLaunch
		a.openAfterBodyClose     = a.maxIdleConnsPerHost
		a.openAfterCancel        = a.maxIdleConnsPerHost
		a.openAfterClose         = a.maxIdleConnsPerHost

		t.Logf("===================> RUNNING SECOND TASK LIST <===================")

		runTaskList(t, tloc, a, srv)
	}

	t.Logf("Calling tloc.Cleanup to clean up task system")
	tloc.Cleanup()

	// Cleaning up the task list system should close all connections.  Verify

	time.Sleep(sleepTimeToStabilizeConns)
	t.Logf("Testing connections after task list cleaned up")
	testOpenConnections(t, 0)

	t.Logf("Closing the server")
	srv.Close()
}

// runTaskList runs a task list from CreateTaskList() through to Close()
// It assumes a server is already running

func runTaskList(t *testing.T, tloc *TRSHTTPLocal, a testConnsArg, srv *httptest.Server) {
	// Verify correct number of open conections at start

	t.Logf("Testing connections at start")
	testOpenConnections(t, a.openAtStart)

	// Create an http request

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
        t.Fatalf("=====> ERROR: Failed to create request: %v <=====", err)
    }

	// Set any necessary headers

	req.Header.Set("Accept", "*/*")
	//req.Header.Set("Connection","keep-alive")

	// Create the task list

	a.tListProto.Request = req
	t.Logf("Calling tloc.CreateTaskList() to create %v tasks for URL %v", a.nTasks, srv.URL)
	tList := tloc.CreateTaskList(a.tListProto, a.nTasks)

	// Configure any requested retries and put at start of task list

	nRetries = a.nSuccessRetries	// this signals the handler

	for i := 0; i < a.nFailRetries; i++ {
		// Let handler know if it should fail all retries by this request
		// as apposed to just failing it once

		tList[i].Request.Header.Set("Trs-Fail-All-Retries", "true")

		// TODO: Could put nRetries into header to reduce complexity

		if (logLevel == logrus.DebugLevel) {
			t.Logf("Set request header %v for task %v",
					 tList[i].Request.Header, tList[i].GetID())
		}
	}

	// Configure any requested http timeouts and put at end of task list

	nHttpTimeouts = a.nHttpTimeouts	// this signals the handler

	for i := len(tList) - 1; i > len(tList) - 1 - a.nHttpTimeouts; i-- {
		// This header is what identifies this request to the handler

		tList[i].Request.Header.Set("Trs-Context-Timeout", "true")

		// TODO: Could put nHttpTimeouts into header to reduce complexity

		if (logLevel == logrus.DebugLevel) {
			t.Logf("Set request header %v for task %v",
					 tList[i].Request.Header, tList[i].GetID())
		}

		// Create a channel which will allow us to later signal the stalled
		// server handlers to return the response

		stallCancel = make(chan bool, a.nHttpTimeouts * 2)
	}

	// Launch the task list

	t.Logf("Calling tloc.Launch() to launch all tasks")
	taskListChannel, err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("=====> ERROR: tloc.Launch() failed: %v <=====", err)
	}

	// Pause so that all tasks can be started and open their connections

	if a.nTasks <= 1000 {
		time.Sleep(2 * time.Second)
	} else if a.nTasks <= 4000 {
		time.Sleep(10 * time.Second)
	} else if a.nTasks <= 8000 {
		time.Sleep(20 * time.Second)
	} else {
		time.Sleep(30 * time.Second)
	}

	// All tasks should now be runnind and all connections should be in
	// the ESTAB(LISHED) state

	t.Logf("Testing connections after Launch")
	testOpenConnections(t, a.openAfterLaunch)

	// If asked, here we attempt to close response bodies for tasks that have
	// already completed, prior to tasks that will fail retries.  This is
	// only done if we were asked to test retries.  We do it to verify that
	// the completed tasks had their connections closed

	tasksToWaitFor := a.nTasks
	if a.nFailRetries > 0 && retrySleep > 0 {
		t.Logf("Waiting for %v non-retry tasks to complete", a.nTasks - a.nFailRetries)

		nWaitedFor := 0
		for i := 0; i < (a.nTasks - a.nFailRetries); i++ {
			<-taskListChannel
			tasksToWaitFor--
			nWaitedFor++
		}

		t.Logf("Draining/closing non-retry response bodies early before retry failures")

		for _, tsk := range(tList) {
			if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
				_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)

				tsk.Request.Response.Body.Close()

				if logLevel == logrus.TraceLevel {
					// Response headers can be  helpful for debug
					t.Logf("Response headers: %s", tsk.Request.Response.Header)
				}
			}
		}

		// Wait for underlying system to perform actions on connections
		time.Sleep(sleepTimeToStabilizeConns)

		t.Logf("Testing connections after non-retry request bodies closed (oabc=%v nfr=%v)",
			    a.openAfterBodyClose, a.nFailRetries)

		oConns := nWaitedFor
		if nWaitedFor > a.maxIdleConnsPerHost {
			oConns = a.maxIdleConnsPerHost
		}
		oConns += a.nFailRetries

		testOpenConnections(t, oConns)
	}

	// Here we attempt to close task bodies and cancel context for tasks that
	// have already completed, prior to tasks that will timeout.  This is
	// only done if we were asked to test timeouts.  We do it to verify that
	// the completed tasks had their connections closed at the right time

	if a.nHttpTimeouts > 0 {
		t.Logf("Waiting for %v non-timeout tasks to complete", a.nTasks - a.nHttpTimeouts)

		nWaitedFor := 0
		for i := 0; i < (a.nTasks - a.nHttpTimeouts); i++ {
			<-taskListChannel
			tasksToWaitFor--
			nWaitedFor++
		}

		t.Logf("Draining/closing non-timeout response bodies early and canceling their contexts")

		for i := 0; i < len(tList) - a.nHttpTimeouts; i++ {
			if tList[i].Request.Response != nil && tList[i].Request.Response.Body != nil {
				_, _ = io.Copy(io.Discard, tList[i].Request.Response.Body)

				tList[i].Request.Response.Body.Close()

				if logLevel == logrus.TraceLevel {
					// Response headers can be  helpful for debug
					t.Logf("Response headers: %s", tList[i].Request.Response.Header)
				}
			}
			tList[i].contextCancel()
		}

		// Wait for underlying system to perform actions on connections

		time.Sleep(sleepTimeToStabilizeConns)
		t.Logf("Testing connections after non-timeout request bodies closed")

		oConns := nWaitedFor
		if nWaitedFor > a.maxIdleConnsPerHost {
			oConns = a.maxIdleConnsPerHost
		}
		oConns += a.nHttpTimeouts

		testOpenConnections(t, oConns)
	}

	// Now wait for ALL tasks to complete

	t.Logf("Waiting for %d tasks to complete", tasksToWaitFor)
	for i := 0; i < tasksToWaitFor; i++ {
		<-taskListChannel
	}

	// Close the task list channel to prevent resource leakage

	t.Logf("Closing the task list channel")
	close(taskListChannel)

	// Wait for underlying system to perform actions on connections

	if a.nTasks <= 4000 {
		time.Sleep(sleepTimeToStabilizeConns)
	} else {
		time.Sleep(5 * time.Second)
	}

	t.Logf("Testing connections after tasks complete")
	testOpenConnections(t, a.openAfterTasksComplete)

	// Set up custom read closer to test response body closure

	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			tsk.Request.Response.Body = &CustomReadCloser{tsk.Request.Response.Body, false}
		}
	}

	// Now drain and close response bodies per configurated request.

	t.Logf("Draining/Closing response bodies (skipClose=%v skipDrain=%v)", a.nSkipCloseBody, a.nSkipDrainBody)

	nBodyClosesSkipped := 0
	nBodyDrainSkipped := 0

	for _, tsk := range(tList) {
		// Skip closing any requested response bodies

		if nBodyClosesSkipped < a.nSkipCloseBody {
			if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
				// May also want to skip drainind the body before skipping closure
				if nBodyDrainSkipped < a.nSkipDrainBody {

					nBodyDrainSkipped++

					if logLevel == logrus.DebugLevel {
						t.Logf("Skipping draining response body for task %v", tsk.GetID())
					}
				} else {
					_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)
				}

				nBodyClosesSkipped++

				if logLevel == logrus.DebugLevel {
					t.Logf("Skipping closing response body for task %v", tsk.GetID())
				}
				continue
			}
		}

		// Close and (maybe) drain the remaining response bodies

		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			// Skip draining the body if requested
			if nBodyDrainSkipped < a.nSkipDrainBody {

				nBodyDrainSkipped++

				if logLevel == logrus.DebugLevel {
					t.Logf("Skipping draining response body for task %v", tsk.GetID())
				}
			} else {
				_, _ = io.Copy(io.Discard, tsk.Request.Response.Body)
			}

			// Do the close

			tsk.Request.Response.Body.Close()

			if logLevel == logrus.TraceLevel {
				// Response headers can be  helpful for debug
				t.Logf("Closed response body for task %v with response headers: %s",
					   tsk.GetID(), tsk.Request.Response.Header)
			}
		}
	}

	// Closing the body may affect the state of open connections

	// Wait for underlying system to perform actions on connections
	time.Sleep(sleepTimeToStabilizeConns)

	t.Logf("Testing connections after response bodies closed")
	testOpenConnections(t, a.openAfterBodyClose)

	// TRS users are not required to call tloc.Cancel() so lets test both ways
	if a.skipCancel {
		t.Logf("Skipping tloc.Cancel()")
	} else {
		// tloc.Cancel() cancels the contexts for all of the tasks in the task list
		t.Logf("Calling tloc.Cancel() to cancel all tasks (skipCancel=false)")
		tloc.Cancel(&tList)

		// Cancelling the task list should not alter connection state

		time.Sleep(sleepTimeToStabilizeConns)

		t.Logf("Testing connections after task list cancelled")
		testOpenConnections(t, a.openAfterCancel)
	}

	// tloc.Close() cancels all contexts, closes any reponse bodies left
	// open, and removes all of the tasks from the task list

	t.Logf("Calling tloc.Close() to close out the task list")
	tloc.Close(&tList)

	// Closing the task list should not alter connection state

	time.Sleep(sleepTimeToStabilizeConns)

	t.Logf("Testing connections after task list closed")
	testOpenConnections(t, a.openAfterClose)

	// Verify that tloc.Close() did indeed close the response bodies that
	// we left open to verify that it WOULD close them

	t.Logf("Checking for closed response bodies")

	for _, tsk := range(tList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			if !tsk.Request.Response.Body.(*CustomReadCloser).WasClosed() {
				t.Errorf("=====> ERROR: Expected response body for %v to be closed, but it was not <=====", tsk.GetID())
			}
		}
	}

	// Verify task list was closed

	t.Logf("Checking that the task list was closed")

	if (len(tloc.taskMap) != 0) {
		t.Errorf("=====> ERROR: Expected task list map to be empty <=====")
	}

	// If we stalled any requests, they are still stalled in the server
	// handler.  Lets release them now so that we can cleanly stop the
	// servers

	if (a.nHttpTimeouts > 0) {
		t.Logf("Signaling stalled handlers ")
		for i := 0; i < a.nHttpTimeouts * 2; i++ {
			stallCancel <- true
		}
	}

	// We likely have many connections open and idle.  If requested, pause
	// until IdleConnTimeout expires so that we verify they then close

	if (a.testIdleConnTimeout) {
		// TODO: Should also comfirm no client "other" connections as well
		t.Logf("Testing connections after idleConnTimeout")

		time.Sleep(a.tListProto.CPolicy.Tx.IdleConnTimeout)

		testOpenConnections(t, 0)
	}
}