#
# MIT License
#
# (C) Copyright 2022,2024 Hewlett Packard Enterprise Development LP
#
# Permission is hereby granted, free of charge, to any person obtaining a
# copy of this software and associated documentation files (the "Software"),
# to deal in the Software without restriction, including without limitation
# the rights to use, copy, modify, merge, publish, distribute, sublicense,
# and/or sell copies of the Software, and to permit persons to whom the
# Software is furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included
# in all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
# THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
# OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
# ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
# OTHER DEALINGS IN THE SOFTWARE.
#

all:  unittest integration
.PHONY:  unittest integration

unittest:
	# There are three different versions of go in the unit test VM.
	# Versions may change over time but can be discovered with:
	#
	#	ls /opt/hostedtoolcache/go
	#
	# Here's what's available as of Nov 20, 2024:
	#
	#/opt/hostedtoolcache/go/1.21.13/x64/bin/go test -v ./pkg/trs_http_api/... -cover -logLevel=2
	#/opt/hostedtoolcache/go/1.22.9/x64/bin/go test -v ./pkg/trs_http_api/... -cover -logLevel=2
	#/opt/hostedtoolcache/go/1.23.3/x64/bin/go test -v ./pkg/trs_http_api/... -cover -logLevel=2
	# 
	# -logLevel values: 0=Panic, 1=Fatal, 2=Error 3=Warn, 4=Info, 5=Debug, 6=Trace"
	
	go test -v ./pkg/trs_http_api/... -cover -logLevel=2
	
	# no -v -tags musl

integration:
	go run ./Test
	# no -v -tags musl
