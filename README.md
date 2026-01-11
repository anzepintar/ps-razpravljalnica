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
./out/control 127.0.0.1:6000 node1 --bootstrap-raft

./out/control 127.0.0.1:6001 node2 127.0.0.1:6000

./out/control 127.0.0.1:6002 node3 127.0.0.1:6000 127.0.0.1:6001

go install github.com/Jille/raftadmin/cmd/raftadmin@latest

raftadmin 127.0.0.1:6000 add_voter node2 127.0.0.1:6001 0
raftadmin 127.0.0.1:6000 add_voter node3 127.0.0.1:6002 0

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
./out/server 127.0.0.1:6000 -b 127.0.0.1:5000

./out/server 127.0.0.1:6000 -b 127.0.0.1:5001

# Tail
./out/server 127.0.0.1:6000 -b 127.0.0.1:5002
```

Argumenti:
- Prvi argument: naslov kontrolne ravnine (entry point)
- `-b`: naslov na katerem strežnik posluša
- `-t`: omogoči TUI vmesnik

### 3. Odjemalec

Odjemalec se poveže na kontrolno ravnino (entry point) in od tam dobi naslove Head/Tail strežnikov.

Cli je namenjem testiranju komunikacije med strežnikom in odjemalcem ter nima implementiranih funkcij za mitigacijo izpadov strežnikov.

**TUI:**
```bash
./out/client -t

# Z drugačnim entry pointom
./out/client -e 127.0.0.1:6000 -t
```

Argumenti:
- Prvi argument: naslov kontrolne ravnine (entry point)
- `-e`: entry point strežnik
- `-t`: omogoči TUI vmesnik

**CLI:**
```bash
# Pomoč
./out/client --help

# Z drugačnim entry pointom
./out/client --entry 127.0.0.1:6000 create-user "Janez"
```


## Delovanje

- **Writes** (CreateUser, CreateTopic, PostMessage, ...) → HEAD
- **Reads** (ListTopics, GetMessages, GetUser) → TAIL
- Če karkoli faila → client snitcha kontrolni ravnini
- **Subscriptions** -> 50% možnosti entry point, 50% da naprej, če pride to repa vzame rep.



TODO:

- subscription se sesuje, če ugasneš strežnik na katerem je subscription - trenutno se client sesuje
- trenutno subscription omogoča pisanje - trenutno piše v temo 0
- testiranje clienta v primeru da preneha delovati entry point - trenutno se sesuje
- če odstraniš raft leaderja, ali potem kdo drug postane leader - trenutno se mi zdi, da ostane isti raft leader (prevote denied, kljub temu, da leader ne obstaja več)?
