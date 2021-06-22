1. install protoc
2. `go install github.com/gogo/protobuf/protoc-gen-gofast`
3. `protoc -I=. -I=$GOPATH/src --gofast_out=. log.proto`
