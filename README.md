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
rm runtime -r
go build -C control -o ../out/control .
go build -C server -o ../out/server .
go build -C client -o ../out/client .
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
rm runtime -r
./out/control 127.0.0.1:6000 node1 --bootstrap-raft

./out/control 127.0.0.1:6001 node2

./out/control 127.0.0.1:6002 node3

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

## TMUX simulacija

```bash
./runall.sh

# Ctrl+b n - Next window
# Ctrl+b p - Previous window
# Ctrl+b [0-4] - Jump to window number
# Ctrl+b arrow - Move between panes
# Ctrl+b d - Detach (script keeps running)
# Ctrl+b w - Window list (interactive selection)
# tmux attach -t ps-razpravljalnica - Re-attach
# tmux kill-server

# po koncu
tmux kill-server
```



## Primeri uporabe:


### Build

```bash
mkdir -p out
rm runtime -r
go build -C control -o ../out/control .
go build -C server -o ../out/server .
go build -C client -o ../out/client .
```

### Testiranje

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

### Zagon

#### 1. Kontrolna ravnina (Raft cluster)

> vsak v svojem terminalu (split)

```bash
# bootstrap node
rm runtime -r
./out/control 127.0.0.1:6000 node1 --bootstrap-raft

./out/control 127.0.0.1:6001 node2 -t

./out/control 127.0.0.1:6002 node3

go install github.com/Jille/raftadmin/cmd/raftadmin@latest

raftadmin 127.0.0.1:6000 add_voter node2 127.0.0.1:6001 0
raftadmin 127.0.0.1:6000 add_voter node3 127.0.0.1:6002 0

```

#### 2. Data plane strežniki (Chain replication)

> vsak v svojem terminalu (split)

```bash
./out/server 127.0.0.1:6000 -b 127.0.0.1:5000

./out/server 127.0.0.1:6000 -b 127.0.0.1:5001 -t

./out/server 127.0.0.1:6000 -b 127.0.0.1:5002
```

### Seed

```bash
./out/client --entry 127.0.0.1:6000 create-user "Janez"
./out/client --entry 127.0.0.1:6000 create-user "Ana"
./out/client --entry 127.0.0.1:6000 create-user "Franci"
./out/client --entry 127.0.0.1:6000 create-topic "Avtomobili"
./out/client --entry 127.0.0.1:6000 create-topic "Kolesarstvo"
./out/client --entry 127.0.0.1:6000 create-topic "Letala"

./out/client --entry 127.0.0.1:6000 post-message --topic-id=0 --user-id=1 "Iščem rabljen avto, priporočila?"
./out/client --entry 127.0.0.1:6000 post-message --topic-id=0 --user-id=2 "Dizel manj kot 100000 km"
./out/client --entry 127.0.0.1:6000 post-message --topic-id=0 --user-id=2 "Katero kolo priporočate?"
./out/client --entry 127.0.0.1:6000 post-message --topic-id=0 --user-id=2 "Gorskega"
```

> preko tui:

```bash
./out/client -e 127.0.0.1:6000 -t
```

### Pisanje sporočil

### Ustvarjanje tem

### Všečkanje sporočil

### Dodajanje naročnin 

### Pogled na strežnike



### Redundanca podatkovne ravnine

#### Padec head

#### Padec tail

#### Padec vmesnega vozlišča

#### Dodajanje novega vozlišča


### Redundanca nadzorne ravnine

#### Padec leaderja

#### Dodajanje vozlišča



TODO:
- dobro preizkusi vse primere uporabe
    - izpadi strežnikov control - entry, non entry
    - izpadi strežnikov data - head, tail, subscription
    - dodajanje strežnikov in povezovanje na njih (če obdržijo vse podatke)
- odpravi shiftanje