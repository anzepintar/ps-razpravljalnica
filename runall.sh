# Tmux navigation:

# Ctrl+b n - Next window
# Ctrl+b p - Previous window
# Ctrl+b [0-4] - Jump to window number
# Ctrl+b arrow - Move between panes
# Ctrl+b d - Detach (script keeps running)
# Ctrl+b w - Window list (interactive selection)
# tmux attach -t ps-razpravljalnica - Re-attach
# tmux kill-server
#
# Ali pa dodaj
# set -g mouse on
# v .config/tmux/tmux.conf

#!/bin/bash

SESSION="ps-razpravljalnica"

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    tmux kill-session -t $SESSION 2>/dev/null
    rm -rf runtime
    echo "Cleanup complete!"
}

# Check if cleanup flag is provided
if [ "$1" == "clean" ]; then
    cleanup
    exit 0
fi


# Build all components
echo "go build ..."
mkdir -p out
rm -rf runtime
go build -C control -o ../out/control .
go build -C server -o ../out/server .
go build -C client -o ../out/client .
echo "go build zaključen"

# Kill existing session if it exists
tmux kill-session -t $SESSION 2>/dev/null

# Create new session with raftadmin
tmux new-session -d -s $SESSION -n "raftadmin"

# Create new window for control plane nodes (3 panes)
tmux new-window -n "Nadzorni strežniki"

# Split window for control plane nodes (3 panes)
tmux select-window -t "Nadzorni strežniki"
tmux split-window -h
tmux split-window -h
# tmux select-layout even-horizontal

# Start control plane nodes
tmux select-pane -t 0
tmux send-keys "rm -rf runtime && ./out/control 127.0.0.1:6000 node1 --bootstrap-raft -t" C-m

tmux select-pane -t 1
tmux send-keys "./out/control 127.0.0.1:6001 node2 -t" C-m

tmux select-pane -t 2
tmux send-keys "./out/control 127.0.0.1:6002 node3 -t" C-m

echo "Zagnani nadzorni strežniki"

# Wait for bootstrap node to start
sleep 5

# Set up raftadmin commands in the first window
tmux select-window -t "raftadmin"
tmux send-keys "sleep 3 && raftadmin 127.0.0.1:6000 add_voter node2 127.0.0.1:6001 0" C-m
tmux send-keys "sleep 1 && raftadmin 127.0.0.1:6000 add_voter node3 127.0.0.1:6002 0" C-m

echo "Povezani nadzorni strežniki"

# Wait for cluster to form
sleep 5

# Create new window for data plane servers (3 panes)
tmux new-window -n "Podatkovni strežniki"
tmux select-window -t "Podatkovni strežniki"
tmux split-window -h
tmux split-window -h
tmux select-layout even-horizontal

tmux select-pane -t 0
tmux send-keys "./out/server 127.0.0.1:6000 -b 127.0.0.1:5000 -t" C-m

tmux select-pane -t 1
tmux send-keys "./out/server 127.0.0.1:6000 -b 127.0.0.1:5001 -t" C-m

tmux select-pane -t 2
tmux send-keys "./out/server 127.0.0.1:6000 -b 127.0.0.1:5002 -t" C-m

echo "Zagnani podatkovni strežniki"

# Wait for servers to start
sleep 5

# Create window for clients, split 50-50
tmux new-window -n "clients"
tmux select-window -t "clients"
tmux split-window -h

# Client 1 pane (left) with seed data
tmux select-pane -t 0
tmux send-keys "./out/client --entry 127.0.0.1:6000 create-user 'Janez'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 create-user 'Ana'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 create-user 'Franci'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 create-topic 'Avtomobili'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 create-topic 'Kolesarstvo'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 create-topic 'Letala'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=0 --user-id=0 'Iščem rabljen avto, priporočila?'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=0 --user-id=2 'Dizel manj kot 100000 km'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=1 --user-id=1 'Katero kolo priporočate?'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=1 --user-id=2 'Gorskega'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=2 --user-id=2 'Jutri letim z Airbusom'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=2 --user-id=2 'Včeraj sem letel iz Ljubljane v Madrid in to z ta velikim Airbusom!'" C-m
tmux send-keys "./out/client --entry 127.0.0.1:6000 post-message --topic-id=2 --user-id=0 'Kako je bilo?'" C-m
tmux send-keys "./out/client -e 127.0.0.1:6000 -t"

# Client 2 pane (right)
tmux select-pane -t 1
tmux send-keys "./out/client -e 127.0.0.1:6000 -t"

echo "Zaključen seed"

# Create readme window
tmux new-window -n "readme.md"
tmux select-window -t "readme.md"
tmux send-keys "cat README.md" C-m

sleep 2

# Za začetek
tmux select-window -t "Nadzorni strežniki"

# Attach to session
tmux attach-session -t $SESSION