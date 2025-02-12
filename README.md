# Task Runner Service (TRS)

This repo contains a GO package which serves as the API which enables
services to use the Task Runner Service (TRS).

## Overview

The goal of TRS is to reduce duplication and maintenance costs by
providing a single highly scalable http communication mechanism for
all HMS services. It's an engine that performs REST-based tasks. It
takes as input requests to perform some number of tasks. It performs
the tasks and returns the results. Applications utilize TRS as a GO
library.

There are two modes of TRS operation:

- "Local" - Uses retryablehttp within goroutines
- "Remote" - Uses Kafka

Remote mode was created to increase scalability but has not yet been
adopted due to its added complexity.  Unless local mode shows itself
to be inadequate, remote mode will likely remain a dormant feature.
Testing as shown that local mode should scale beyond 24,000 concurrent
tasks.

The remainder of this top level readme will document local mode.

## Typical High Level Use Model

The typical use model will be:

1. Declare an instance of `TRSHTTPLocal{}` object (we will call it `obj`)

1. Call `obj.Init()` to initialize a local HTTP task system

1. Create a task list by calling `obj.CreateTaskArray()`.  Prior to
   calling this library function, create a source task descriptor and
   populate its fields appropriately.  Its contents will be used to
   populate each task in the new task list array.  After the task list
   has been created, each entry may then be customized by the caller.

   Notable fields to set before calling `obj.CreateTaskArray()`:

    - `ServiceName` – the name of your service
    - `Request` – the HTTP operation to perform
    - `Timeout` – overall operation timeout
    - `CPolicy` - if any default policies are not sufficient
    - `forceInsecure` - if insecure communication required

1. If desired, iterate through the returned task list and customize
   any individual tasks necessary.  Sometimes HTTP requests may need
   to be tailored to each individual task.

1. Launch the array of tasks by calling `obj.Launch()`. This function
   returns a Go channel which can be listened on for responses as each
   task completes.

1. Wait for completion by either monitoring the Go channel returned by
   `obj.Launch()` or make periodic calls to `obj.Check()` to check on
   the status of the task array.

1. When the tasks are all finished, call `obj.Close()` and immediately
   afterwards close the Go channel that was returned by `obj.Launch()`.

1. Additional task lists may be created and launched.

1. When no futher task lists need to be created and launched, clean up
   the local HTTP task system by calling `obj.Cleanup()`.

## Sample Application

Following is a very rough pseudo-go coded example of the scenarios listed above.

**Parallel GET operations returning data**

```Go
...
var source trsapi.HttpTask
var tloc trsapi.TRSHTTPLocal
logger *logrus.Logger

logger = logrus.New()
logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true,})
logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true,})
tloc.Init(myServiceName, logger)

source.Request = http.NewRequest("GET","http://www.example.com",nil)
source.Timeout = 10 * time.Second  // each task should take no more than 10 seconds to complete
source.RetryPolicy.Retries = 5     //max 5 retries on failure
source.RetryPolicy.BackoffTimeout = 6 * time.Second //max back-off for retries

taskArray := tloc.CreateTaskList(&source,100)  //create set of 100 HTTP tasks

// Set information specific to each task in the set

for ii,_:= range(taskArray) {
    taskArray[ii].Request.URL,_ = neturl.Parse(targetURL)  //target URL
}

rchan,err := tloc.Launch(&taskArray)   //execute tasks
if (err != nil) {
    //lib func failed for some reason
}

// Monitor the chan for responses

nDone := 0

for {
    tdone := <-rchan
    log.Printf("Task complete, URL: %s, return data: '%s'\n",tdone.URL,string(tdone.Response.Payload))
    nDone ++
    if (nDone == len(taskArray)) {
               break
        }
}

log.Printf("DONE!\n")
```

## API Reference

### type TrsAPI Interface

```Go
type TrsAPI interface {
	Init(serviceName string, logger *logrus.Logger) error
	SetSecurity(params interface{}) error
	CreateTaskList(source *HttpTask, numTasks int) []HttpTask
	Launch(taskList *[]HttpTask) (chan *HttpTask, error)
	Check(taskList *[]HttpTask) (running bool, err error)
	Cancel(taskList *[]HttpTask)
	Close(taskList *[]HttpTask)
	Alive() (ok bool, err error)
	Cleanup()
}
```

### func Init()

```Go
// Initialize the local HTTP task system.
//
// serviceName: Name of the service or application
// logger:      The logger to use; if nil, a default logger is created
//
// Return:      Nil on success; error string on failure

func (tloc *TRSHTTPLocal) Init(serviceName string, logger *logrus.Logger) error
```

### func SetSecurity()

```Go
type TRSHTTPLocalSecurity struct {
	CACertBundleData string
	ClientCertData string
	ClientKeyData string
}
```

```Go
// Set up the local HTTP task system to use a CA cert bundle and optional
// client cert/key data.  This is used to set up a secure connection to the
// target system. The CA cert bundle is required.  The client cert/key data
// is optional.
//
// inParams: Ptr to a TRSHTTPLocalSecurity struct containing the CA cert
//           bundle and optional client cert/key data.
//
// Return:   Nil on success; error string on failure

func (tloc *TRSHTTPLocal) SetSecurity(inParams interface{}) error {
```

### func CreateTaskList()

```Go
type RetryPolicy struct {
	Retries        int           // number of retries to attempt (default is 3)
	BackoffTimeout time.Duration // base backoff timeout between retries (default is 5 seconds)
}
```

```Go
type HttpTxPolicy struct {
	Enabled                 bool          // policy enabled (default is false)
	MaxIdleConns            int           // max idle connections across all hosts (default is 100)
	MaxIdleConnsPerHost     int           // max idle connections per host (default is 2)
	IdleConnTimeout         time.Duration // duration an idle connection remains open (default is unlimited)
	ResponseHeaderTimeout   time.Duration // max wait time for a host's response header (default is unlimited)
	TLSHandshakeTimeout     time.Duration // max duration for the TLS handshake (default is 10 seconds)
	DisableKeepAlives       bool          // disable HTTP keep-alives if true (default is false)
}
```

```Go
type ClientPolicy struct {
	Retry    RetryPolicy   // task's retry policy
	Tx       HttpTxPolicy  // task's transport policy
}
```

```Go
type HttpTask struct {
	id            uuid.UUID          // message id
	ServiceName   string             // name of the service
	Request       *http.Request      // the http request
	TimeStamp     string             // time the request was created/sent RFC3339Nano
	Err           *error             // any error associated with the request
	Timeout       time.Duration      // task's context timeout
	CPolicy       ClientPolicy       // task's retry and transport policies
	Ignore        bool               // if true, trs will ignore this task
	context       context.Context    // task's context
	contextCancel context.CancelFunc // task's context cancellation function
	forceInsecure bool               // if true, force insecure communication
}
```

```Go
// Create an array of task descriptors.  Data is copied from the source task
// into each element of the returned array.  Per-task data should then be
// populated by the caller after control returns.
//
// source:   Pounter to a task descriptor populated with relevant data.  The
//           only fields that should be set by the caller are:
//               - ServiceName
//               - Request
//               - Timeout
//               - CPolicy
//               - Ignore
//               - forceInsecure
// numTasks: Number of elements in the array to be returne
//
// Return:   Array of populated task descriptors

func (tloc *TRSHTTPLocal) CreateTaskList(source *HttpTask, numTasks int) []HttpTask
```

### func Launch()

```Go
// Launch an array of tasks.  This is non-blocking.  The caller can use
// either the returned Go channel or Check() to check for compleetion.  The
// caller should close all HTTP response bodies after they are received and
// processed and close the returned Go channel when done.
//
// taskList:  Ptr to a list of HTTP tasks to launch
//
// Return:
//
// chan:  A Go channel of *HttpTxTask, sized by task list, which caller can
//        use to get notified of each task's completion. The caller MUST
//        CLOSE THE CHANNEL WHEN DONE even if using Done() to check for
//		  completion.
// error: Nil on success; error string on failure

func (tloc *TRSHTTPLocal) Launch(taskList *[]HttpTask) (chan *HttpTask, error)
```

### func Check()

```Go
// Check the status of the launched task list.  This is an alternative to
// waiting on the task-complete channel returned by Launch().
//
// taskList:  Pointer to the task list
//
// Return:
//
// bool:  True or false if the task list is still running
// error: Nil on success; error string on failure

func (tloc *TRSHTTPLocal) Check(taskList *[]HttpTask) (bool, error)
```

### func Cancel()

```Go
// Cancel a currently-running task set.  Note that this won't kill
// the individual in-flight tasks, but just kills the overall operation.
// Thus, for tasks with no time-out which are hung, it could result in
// a resource leak.   But this can be used to at least wrestle control
// over a task set.
//
// taskList:  Pointer to the task list to cancel

func (tloc *TRSHTTPLocal) Cancel(taskList *[]HttpTask)
```

### func Close()

```Go
// Close out a task list transaction.  This frees up resources so it
// should not be skipped.
//
// taskList:  Pointer to the task list to close

func (tloc *TRSHTTPLocal) Close(taskList *[]HttpTask)
```

### func Alive()

```Go
// Check the health of the local HTTP task launch system.
//
// Return:
//
// bool:  True or false if alive and operational
// error: Error message associated with non-alive/functional state

func (tloc *TRSHTTPLocal) Alive()
```

### func Cleanup()

```Go
// Clean up a local HTTP task system.  This is called when after it will no
// longer be used.

func (tloc *TRSHTTPLocal) Cleanup()
```