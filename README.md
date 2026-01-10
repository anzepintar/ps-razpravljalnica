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

## Testiranje

```bash
# Vsi unit testi v projektu
cd client && go test -v && cd ..
cd server && go test -v && cd ..
cd control && go test -v && cd ..

# Fuzz testi za client
go test -fuzz=FuzzParseTopicIDs -fuzztime=30s ./client

# Fuzz testi za server
go test -fuzz=FuzzValidateUserName -fuzztime=30s ./server

# Fuzz testi za control
go test -fuzz=FuzzValidateNodeAddress -fuzztime=30s ./control
```

## Zagon

### 1. Kontrolna ravnina (Raft cluster)

Za cluster z več nodi (3-node cluster):
```bash
# bootstrap node
./out/control localhost:6000 node1 --bootstrap-raft

./out/control localhost:6001 node2 localhost:6000

./out/control localhost:6002 node3 localhost:6000 localhost:6001
```

### 2. Data plane strežniki (Chain replication)

Zaženemo enega ali več strežnikov. Vsak strežnik se registrira pri kontrolni ravnini:
```bash
# Head
./out/server localhost:6000 -b localhost:5000

./out/server localhost:6000 -b localhost:5001

# Tail
./out/server localhost:6000 -b localhost:5002
```

Argumenti:
- Prvi argument: naslov kontrolne ravnine (entry point)
- `-b`: naslov na katerem strežnik posluša

### 3. Odjemalec

Odjemalec se poveže na kontrolno ravnino (entry point) in od tam dobi naslove Head/Tail strežnikov.

**CLI**
```bash
# Pomoč
./out/client cli --help

# Z drugačnim entry pointom
./out/client --entry localhost:6000 cli create-user "Janez"
```

**TUI:**
```bash
./out/client
```

## Delovanje

- **Writes** (CreateUser, CreateTopic, PostMessage, ...) → HEAD
- **Reads** (ListTopics, GetMessages, GetUser) → TAIL
- Če karkoli faila → client snitcha kontrolni ravnini

## Testiranje subscriptions

**Naročnik:**
```bash
./out/client cli create-user --name "test"
./out/client cli create-topic --name "testna tema"

./out/client cli subscribe --user-id 0 --topic-ids 0
```

**Pošiljatelj:**
```bash
./out/client cli post-message --user-id 0 --topic-id 0 --text "sporocilotest1"

./out/client cli like-message --user-id 0 --topic-id 0 --message-id 0

./out/client cli update-message --user-id 0 --topic-id 0 --message-id 0 --text "sporocilotest2"

./out/client cli delete-message --user-id 0 --topic-id 0 --message-id 0
```