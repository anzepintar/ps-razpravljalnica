# Tmux navigation:

# Ctrl+b n - Next window
# Ctrl+b p - Previous window
# Ctrl+b [0-4] - Jump to window number
# Ctrl+b arrow - Move between panes
# Ctrl+b d - Detach (script keeps running)
# Ctrl+b w - Window list (interactive selection)
# tmux attach -t ps-razpravljalnica - Re-attach
# tmux kill-server


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

# Create new session with first control plane node
tmux new-session -d -s $SESSION -n "Nadzorni strežniki"

# Split window for control plane nodes (3 panes)
tmux split-window -h
tmux split-window -h
tmux select-layout even-horizontal

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

# Create new window for raft admin commands
tmux new-window -n "raftadmin"
tmux send-keys "sleep 3 && raftadmin 127.0.0.1:6000 add_voter node2 127.0.0.1:6001 0" C-m
tmux send-keys "sleep 1 && raftadmin 127.0.0.1:6000 add_voter node3 127.0.0.1:6002 0" C-m

echo "Povezani nadzorni strežniki"

# Wait for cluster to form
sleep 5

# Create new window for data plane servers (3 panes)
tmux new-window -n "Podatkovni strežniki"
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

# Create new window for seeding data
tmux new-window -n "Odjemalec"
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
tmux send-keys "./out/client -e 127.0.0.1:6000 -t"

echo "Zaključen seed"

sleep 5

# Select the cluster window to start
tmux select-window -t "Podatkovni strežniki"

# Attach to session
tmux attach-session -t $SESSION