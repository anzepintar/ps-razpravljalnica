# Razpravljalnica - Porazdeljeni sistemi


Generiranje razpravljalnica_grpc.pb.go in razpravljalnica.pb.go

```bash
protoc --go_out=./server --go-grpc_out=./server razpravljalnica.proto
protoc --go_out=./client --go-grpc_out=./client razpravljalnica.proto
```
