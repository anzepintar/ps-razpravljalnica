# Razpravljalnica

## Generiranje proto

```bash
protoc --go_out=./server --go-grpc_out=./server razpravljalnica.proto
protoc --go_out=./client --go-grpc_out=./client razpravljalnica.proto
protoc --go_out=./control --go-grpc_out=./control razpravljalnica.proto
```

## Build

```bash
mkdir -p out
go build -C server -o ../out/server .
go build -C client -o ../out/client .
go build -C control -o ../out/control .
```

## Zagon

Kontrolna ravnina:
```bash
./out/control localhost:6000 node1 --bootstrap-raft
```

Strežnik:
```bash
./out/server localhost:6000 -b localhost:5000
```

Odjemalec (CLI):
```bash
./out/client cli --help
```

Odjemalec (TUI) - ko bo:
```bash
./out/client
```
