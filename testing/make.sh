#!/usr/bin/env bash
protoc -I ./proto helloworld.proto --go_out=plugins=grpc:helloworld
protoc --proto_path=. \
    --descriptor_set_out=helloworld.protoset \
    --include_imports \
    proto/helloworld.proto
