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
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
)

type ModelsTS struct {
	suite.Suite
}

func GenerateStockHttpTask() (ht HttpTask) {
	req := GenerateStockRequest()
	ht = HttpTask{
		id:            uuid.New(),
		ServiceName:   "ServiceName",
		Request:       req,
		TimeStamp:     time.Now().String(),
		Err:           nil,
		Timeout:       0,
		CPolicy:       ClientPolicy{},
		context:       nil,
		contextCancel: nil,
	}
	return ht
}
func GenerateStockRequest() (req *http.Request) {
	req, _ = http.NewRequest("GET", "127.0.0.1", nil)
	return req
}

func GenerateStockResponse() (resp http.Response) {
	return resp
}

func (suite *ModelsTS) TestHttpTaskValidate_HappyPath() {
	ht := GenerateStockHttpTask()
	valid, err := ht.Validate()
	suite.Equal(true, valid)
	suite.Equal(nil, err)
}

func (suite *ModelsTS) TestHttpTaskValidate_ErrorPath_NilID() {
	ht := GenerateStockHttpTask()
	ht.id = uuid.Nil
	valid, err := ht.Validate()
	suite.Equal(false, valid)
	suite.True(err != nil)
}

func (suite *ModelsTS) TestHttpTaskValidate_ErrorPath_EmptyTimestamp() {
	ht := GenerateStockHttpTask()
	ht.TimeStamp = ""
	valid, err := ht.Validate()
	suite.Equal(false, valid)
	suite.True(err != nil)
}

func (suite *ModelsTS) TestHttpTaskValidate_ErrorPath_EmptyServiceName() {
	ht := GenerateStockHttpTask()
	ht.ServiceName = ""
	valid, err := ht.Validate()
	suite.Equal(false, valid)
	suite.True(err != nil)
}

func (suite *ModelsTS) TestHttpTaskValidate_ErrorPath_NilRequest() {
	ht := GenerateStockHttpTask()
	ht.Request = nil
	valid, err := ht.Validate()
	suite.Equal(false, valid)
	suite.True(err != nil)
}

func (suite *ModelsTS) TestToHttpKafkaTx_HappyPath() {
	ht := GenerateStockHttpTask()
	tx := ht.ToHttpKafkaTx()
	newHt := tx.ToHttpTask()
	tx2 := newHt.ToHttpKafkaTx()

	suite.Equal(tx, tx2)
}

func (suite *ModelsTS) TestToHttpTask_HappyPath() {
	ht := GenerateStockHttpTask()
	tx := ht.ToHttpKafkaTx()
	newHt := tx.ToHttpTask()
	tx2 := newHt.ToHttpKafkaTx()
	suite.Equal(tx, tx2)
}

func (suite *ModelsTS) TestToSerializedRequest_HappyPath() {
	req := GenerateStockRequest()
	sr := ToSerializedRequest(*req)
	req2 := sr.ToHttpRequest()
	sr2 := ToSerializedRequest(req2)

	suite.Equal(sr2, sr)
}

func (suite *ModelsTS) TestToHttpRequest_HappyPath() {
	req := GenerateStockRequest()
	sr := ToSerializedRequest(*req)
	req2 := sr.ToHttpRequest()
	sr2 := ToSerializedRequest(req2)

	suite.True(sr.Equal( sr2))
}

func (suite *ModelsTS) TestToHttpKafkaRx_HappyPath() {
	ht := GenerateStockResponse()

	sr := ToSerializedResponse(ht)
	ht1 := sr.ToHttpResponse()
	sr2 := ToSerializedResponse(ht1)
	suite.True(sr2.Equal(sr))
}

func TestModelApplicationSuite(t *testing.T) {
	suite.Run(t, new(ModelsTS))
}
