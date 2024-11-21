// MIT License
//
// (C) Copyright [2020-2021] Hewlett Packard Enterprise Development LP
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

package main

// This is for manual testing only, not to be used in the library package.

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"os"
	trsapi "github.com/Cray-HPE/hms-trs-app-api/v2/pkg/trs_http_api"
	"strconv"
	"strings"
	"time"
)

type PayloadData struct {
	Data int
}

func main() {

	logy := logrus.New()

	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logy.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	var source trsapi.HttpTask


	var envstr string
	var tloc trsapi.TrsAPI
	//var worker interface{}


	deadline := 10
	numops := 2
	opType := "GET"
	cancelTime := -1
	useChan := false

	envstr = os.Getenv("IMPLEMENTATION")
	if envstr == "REMOTE" {
		worker := &trsapi.TRSHTTPRemote{}
		worker.Logger = logy
		tloc = worker
	} else {
		worker := &trsapi.TRSHTTPLocal{}
		worker.Logger = logy
		tloc = worker
	}


	envstr = os.Getenv("DEADLINE")
	if envstr != "" {
		deadline, _ = strconv.Atoi(envstr)
	}
	envstr = os.Getenv("CANCEL")
	if envstr != "" {
		cancelTime, _ = strconv.Atoi(envstr)
		logrus.Printf("Canceltime: %d", cancelTime)
	}
	envstr = os.Getenv("NUMOPS")
	if envstr != "" {
		numops, _ = strconv.Atoi(envstr)
	}

	envstr = os.Getenv("CHAN")
	if envstr != "" {
		useChan = true
	}

	envstr = os.Getenv("OPTYPE")
	if envstr != "" {
		if envstr == "GET" {
			opType = "GET"
		} else if envstr == "PUT" {
			opType = "PUT"
		} else {
			opType = "GET"
		}
	}
	envstr = os.Getenv("LOG_LEVEL")
	if envstr != "" {
		logLevel := strings.ToUpper(envstr)
		logrus.Infof("Setting log level to: %d\n", envstr)

		switch logLevel {

		case "TRACE":
			logrus.SetLevel(logrus.TraceLevel)
		case "DEBUG":
			logrus.SetLevel(logrus.DebugLevel)
		case "INFO":
			logrus.SetLevel(logrus.InfoLevel)
		case "WARN":
			logrus.SetLevel(logrus.WarnLevel)
		case "ERROR":
			logrus.SetLevel(logrus.ErrorLevel)
		case "FATAL":
			logrus.SetLevel(logrus.FatalLevel)
		case "PANIC":
			logrus.SetLevel(logrus.PanicLevel)
		default:
			logrus.SetLevel(logrus.ErrorLevel)
		}

		//Set the kafka level to the same level.
		logy.SetLevel(logrus.GetLevel())
	}



	source.Request, _ = http.NewRequest(opType, "http://www.example.org", nil)

	source.Request.Method = opType
	source.Timeout = time.Second * time.Duration(deadline)

	ierr := tloc.Init("test", logy)
	if ierr != nil {
		logrus.Printf("Init() failed: '%v'\n", ierr)
		os.Exit(1)
	}

	// if its remote, then you can change the kafka verbo
	//worker.KafkaInstance.Logger.SetLevel(logrus.TraceLevel)

	taskArray := tloc.CreateTaskList(&source, numops)

	for ii := 0; ii < numops; ii++ {
			//TODO make this several types of things to try!
			logrus.Trace(ii, taskArray[ii].GetID())
			taskArray[ii].Request.URL, _ = url.Parse(fmt.Sprintf("http://www.example.org/v1/EP/%d", ii))


	}
	rchan, err := tloc.Launch(&taskArray)
	if err != nil {
		logrus.Printf("Launch() return error: '%v'\n", err)
	}

	if useChan {
		logrus.Printf("using CHAN")
		goterr := false
		nDone := 0
		nErr := 0
		for {
			tdone := <-rchan
			//body, err := ioutil.ReadAll(tdone.Request.Response.Body)
			//logrus.Printf(" --> Task Finished (%s), status: %d\t, (%s)", tdone.Request.URL.String(), tdone.Request.Response.StatusCode, string(body))
			logrus.Printf(" --> Task Finished ID: %s, URL: (%s), status: %d\n", tdone.GetID().String(), tdone.Request.URL.String(), tdone.Request.Response.StatusCode)
			nDone++
			if tdone.Request.Response.StatusCode != 0 {
				nErr++
			}
			running, err := tloc.Check(&taskArray)
			if err != nil {
				goterr = true
			}
			if nDone == len(taskArray) {
				if running {
					logrus.Printf("ERROR, Check() says still running, but all tasks returned.\n")
				}
				break
			}
		}

		logrus.Printf("Tasks completed, goterr: %t, numErr: %d\n", goterr, nErr)

	} else {
		logrus.Printf("NOT using CHAN")

		//Check for results.  If a cancel time was specified, keep track of time
		//and call the Cancel() method at the right time.

		startTime := time.Now()
		goterr := false
		var ix int
		for ix = 0; ix < 1000000; ix++ {
			if cancelTime > 0 {
				elapsed := time.Since(startTime)
				logrus.Printf("EL: %f, CT: %f", elapsed.Seconds(), float64(cancelTime))
				if elapsed.Seconds() >= float64(cancelTime) {
					logrus.Printf("CANCELING OPERATION!\n")
					tloc.Cancel(&taskArray)
					break
				}
			}
			running, err := tloc.Check(&taskArray)
			if err != nil {
				goterr = true
			}
			if !running {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		logrus.Printf("Check loop ran %d times, goterr: %t\n", ix, goterr)
	}

	//wait for the kafka bus to come back
	tloc.Check(&taskArray)
	time.Sleep(2 * time.Second)
	tloc.Close(&taskArray)
	time.Sleep(2 * time.Second)
	tloc.Cleanup()
	time.Sleep(2 * time.Second)



	logrus.Printf("Goodbye")

	os.Exit(0)
}
