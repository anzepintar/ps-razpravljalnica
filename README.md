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

Argumenti:
- Prvi argument: naslov na katerem control node posluša
- Drugi argument: Raft ID
- Ostali argumenti: naslovi drugih control nodov
- `--bootstrap-raft`: inicializira Raft cluster (samo za prvi node)
- `-t`: omogoči TUI vmesnik

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
- `-t`: omogoči TUI vmesnik

### 3. Odjemalec

Odjemalec se poveže na kontrolno ravnino (entry point) in od tam dobi naslove Head/Tail strežnikov.

**CLI:**
```bash
# Pomoč
./out/client --help

# Z drugačnim entry pointom
./out/client --entry localhost:6000 create-user "Janez"
```

**TUI:**
```bash
./out/client -t

# Z drugačnim entry pointom
./out/client -e localhost:6000 -t
```

## Delovanje

- **Writes** (CreateUser, CreateTopic, PostMessage, ...) → HEAD
- **Reads** (ListTopics, GetMessages, GetUser) → TAIL
- Če karkoli faila → client snitcha kontrolni ravnini

## TUI vmesniki

Vsi trije komponenti (client, server, control) imajo TUI vmesnik, ki se zažene z `-t` flagom.

### Client TUI
```bash
./out/client -t
```
- **Login screen**: Ustvari novega uporabnika ali se prijavi z obstoječim ID
- **Topics panel**: Seznam tem, navigacija z puščicami
- **Messages panel**: Sporočila v trenutni temi
- **Input field**: Pisanje novih sporočil
- **Tipke**: `Tab` = preklop panelov, `t` = nova tema, `r` = osveži, `l` = like, `d` = delete, `e` = edit, `q` = izhod

### Server TUI
```bash
./out/server localhost:6000 -b localhost:5000 -t
```
- **Users table**: Seznam registriranih uporabnikov (ID, ime)
- **Topics table**: Seznam tem (ID, ime, število sporočil)
- **Chain info**: Informacije o chain replication (self, prev, next, control plane)
- **Logs panel**: Logi strežnika
- **Tipke**: `Tab` = preklop panelov, `q` = izhod

### Control TUI
```bash
./out/control localhost:6000 node1 --bootstrap-raft -t
```
- **Nodes table**: Seznam data plane nodov v chain (pozicija, ID, naslov, vloga HEAD/TAIL)
- **Raft info**: Informacije o Raft clustru (state, leader, term, log index, seznam serverjev)
- **Logs panel**: Logi kontrolne ravnine
- **Tipke**: `Tab` = preklop panelov, `q` = izhod

## Testiranje subscriptions

**Naročnik:**
```bash
./out/client create-user "test"
./out/client create-topic "testna tema"

./out/client subscribe --user-id 0 --topic-ids 0
```

**Pošiljatelj:**
```bash
./out/client post --user-id 0 --topic-id 0 "sporocilotest1"

./out/client like --user-id 0 --topic-id 0 --message-id 0

./out/client update --user-id 0 --topic-id 0 --message-id 0 "sporocilotest2"

./out/client delete --user-id 0 --topic-id 0 --message-id 0
```