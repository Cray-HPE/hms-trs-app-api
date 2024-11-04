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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	base "github.com/Cray-HPE/hms-base/v2"
	"github.com/sirupsen/logrus"
)

var svcName = "TestMe"

// Create a logger with default log level of Error that can be overridden
// if debugging of a test is necessary.
func createLogger(level ...logrus.Level) *logrus.Logger {
	if len(level) == 0 {
		level = append(level, logrus.ErrorLevel)
	}

	trsLogger := logrus.New()

	trsLogger.SetFormatter(&logrus.TextFormatter{ FullTimestamp: true, })
	trsLogger.SetLevel(level[0])
	trsLogger.SetReportCaller(true)

	return trsLogger
}

func TestInit(t *testing.T) {
	tloc := &TRSHTTPLocal{}

	tloc.Init(svcName, createLogger(logrus.TraceLevel))
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
	tloc.Init(svcName, createLogger(logrus.TraceLevel))
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

func launchHandler(w http.ResponseWriter, req *http.Request) {
	if (!hasUserAgentHeader(req)) {
		w.Write([]byte(`{"Message":"No User-Agent Header"}`))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))
}

var stallCancel chan bool

func stallHandler(w http.ResponseWriter, req *http.Request) {
	<-stallCancel
	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Message":"OK"}`))
}


func TestLaunch(t *testing.T) {
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger(logrus.TraceLevel))

	srv := httptest.NewServer(http.HandlerFunc(launchHandler))
	defer srv.Close()

	req,_ := http.NewRequest("GET",srv.URL,nil)
	tproto := HttpTask{Request: req, Timeout: 8*time.Second,}
	tList := tloc.CreateTaskList(&tproto,5)

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
	tloc.Init(svcName, createLogger(logrus.TraceLevel))
	srv := httptest.NewServer(http.HandlerFunc(stallHandler))
	defer srv.Close()

	req,_ := http.NewRequest("GET",srv.URL,nil)
	tproto := HttpTask{Request: req, Timeout: 3*time.Second, RetryPolicy: RetryPolicy{Retries: 1, BackoffTimeout: 1 * time.Second,},}
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

func TestPCSUseCase(t *testing.T) {
	numNoStallTasks := 5
	numStallTasks := 5

	// Initialize the tloc
	tloc := &TRSHTTPLocal{}
	tloc.Init(svcName, createLogger(logrus.TraceLevel))

	// Create http servers.  One for tasks that complete, and one for tasks that stall
	noStallSrv := httptest.NewServer(http.HandlerFunc(launchHandler))
	stallSrv := httptest.NewServer(http.HandlerFunc(stallHandler))

	// Create http request proto for tasks that complete
	noStallReq, err := http.NewRequest(http.MethodGet, noStallSrv.URL, nil)
	if err != nil {
        t.Fatalf("Failed to create request: %v", err)
    }
	noStallProto := HttpTask{Request: noStallReq, Timeout: 8*time.Second, RetryPolicy: RetryPolicy{Retries: 5},}

	t.Logf("Creating completing task list with %v tasks and URL %v", numStallTasks, noStallSrv.URL)
	noStallList := tloc.CreateTaskList(&noStallProto, numNoStallTasks)

	// Create http request proto for tasks that stall
	stallReq, err := http.NewRequest("GET", stallSrv.URL, nil)
	if err != nil {
        t.Fatalf("Failed to create request: %v", err)
    }
	stallProto := HttpTask{Request: stallReq, Timeout: 8*time.Second, RetryPolicy: RetryPolicy{Retries: 5},}

	t.Logf("Creating stalling task list with %v tasks and URL %v", numStallTasks, stallSrv.URL)
	stallList := tloc.CreateTaskList(&stallProto, numStallTasks)

	// Launch both sets of tasks
	t.Logf("Launching tasks")
	tList := append(noStallList, stallList...)
	taskListChannel, err := tloc.Launch(&tList)
	if (err != nil) {
		t.Errorf("Launch ERROR: %v", err)
	}

	t.Logf("Waiting for normally completing tasks to complete")
	for i := 0; i < numNoStallTasks; i++ {
		<-taskListChannel
	}

	t.Logf("Checking normally completed tasks for closed response bodies")
	for _, tsk := range(noStallList) {
		if tsk.Request.Response != nil && tsk.Request.Response.Body != nil {
			tsk.Request.Response.Body = &CustomReadCloser{tsk.Request.Response.Body, false}
			if !tsk.Request.Response.Body.(*CustomReadCloser).WasClosed() {
				t.Errorf("Expected response body to be closed, but it was not")
			}
		}
	}

	// Cancel the entire task list, which will kill the stalled tasks
	t.Logf("Cancelling task list")
	tloc.Cancel(&tList)

	t.Logf("Waiting for stalled tasks to complete after cancel")
	for i := 0; i < numStallTasks; i++ {
		<-taskListChannel
	}

	t.Logf("Checking all tasks for canceled contexts")
	for _, tsk := range(tList) {
		select {
		case <-tsk.context.Done():
			if tsk.context.Err() != context.Canceled {
				t.Errorf("Expected context to be canceled, but got: %v", tsk.context.Err())
			}
		default:
			t.Errorf("Expected context to be done, but it is still active")
		}
	}

	t.Logf("Closing the task list channel")
	close(taskListChannel)

	t.Logf("Closing task list")
	tloc.Close(&tList)

	t.Logf("Checking that the task list was closed")
	if (len(tloc.taskMap) != 0) {
		t.Errorf("Expected task list map to be empty")
	}

	t.Logf("Closing servers")
	noStallSrv.CloseClientConnections()
	noStallSrv.Close()
	stallSrv.CloseClientConnections()	// needed due to stalled connections
	stallSrv.Close()
}