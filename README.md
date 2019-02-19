# grpcapi

### Protoset Files

Protoset files contain binary encoded `google.protobuf.FileDescriptorSet` protos. To create
a protoset file, invoke `protoc` with the `*.proto` files that define the service:

```shell
protoc --proto_path=. \
    --descriptor_set_out=myservice.protoset \
    --include_imports \
    my/custom/server/service.proto
```
