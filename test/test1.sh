#!/bin/bash

set -ex # x izpiše ukaze

SERVER_PID=0

cleanup() {
	kill "$SERVER_PID" 2>/dev/null || true
	wait "$SERVER_PID" 2>/dev/null || true
}

trap cleanup EXIT

# da je vse naloženo
cd ../server && go mod tidy && cd - > /dev/null
cd ../client && go mod tidy && cd - > /dev/null

mkdir -p ../out
pushd ../server > /dev/null
go build -o ../out/server ./main.go
popd > /dev/null
pushd ../client > /dev/null
go build -o ../out/client ./main.go
popd > /dev/null

../out/server &
SERVER_PID=$!

# čaka na server
sleep 1

cli() { ../out/client cli "$@"; }
cli create-user "Tonček"
cli create-user "Lojzek"
cli create-topic "General"
cli list-topics
cli post-message --topic-id=0 --user-id=0 "Živjo, jaz sem Tonček!"
cli post-message --topic-id=0 --user-id=1 "Živjo, jaz sem Lojzek!"
cli get-messages --topic-id=0
cli like --topic-id=0 --message-id=1 --user-id=1
cli update --topic-id=0 --message-id=1 --user-id=1 "Živjo, jaz sem Lojzek (urejeno)."
cli get-messages --topic-id=0
cli delete --topic-id=0 --message-id=1 --user-id=1
cli get-messages --topic-id=0

cleanup