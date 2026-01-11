# Documentation

## Client

### Startup
1. Get at least one control plane address from user
2. Store in array
3. `GetClusterStateClient` -> control[0]
    - populate array
    - store head
    - store tail

  > this sometimes needs to be updated in case of chain changes

### Writes
- Server

    > Writes should always go to chain head
    > Reads should usually be done from chain tail
    > Subscriptions should be from the node provided in the response

    - `CreateUser`
    - `CreateTopic`
    - `PostMessage`
    - `UpdateMessage`
    - `DeleteMessage`
    - `LikeMessage`
    - `GetSubscipitonNone` (Technically a write, since it modifies state)
- Control

    > Writes should always go to leader
    > Client has no way to know who is the leader other than trying and failing
    > The recomended approach is addressing with multi:///node1, node2, ...
    > <https://github.com/Jille/grpc-multi-resolver>

    > skim also: <https://github.com/Jille/raft-grpc-leader-rpc>

    ```go
    import "github.com/grpc-ecosystem/go-grpc-middleware/retry"

    retryOpts := []grpc_retry.CallOption{
        grpc_retry.WithBackoff(grpc_retry.BackoffExponential(100 * time.Millisecond)),
        grpc_retry.WithMax(5), // Give up after 5 retries.
    }
    grpc.Dial(..., grpc.WithUnaryInterceptor(grpc_retry.UnaryClientInterceptor(retryOpts...)))
    ```

    - `Snitch`

        Client should call snitch when a write or read fails

### Reads
- Server
    - `ListTopics`
    - `GetMessages`
    - `SubscribeTopic` (Not always a tail)
    - `GetUser`
- Control
    - `GetClusterStateClient`

## Server

### Startup
- get args
    - only required arg is address of at least one control plane node
- `register`
- sync state from head
    - `GetClusterStateClient` -> control -> global head
    - `GetWholeState` -> global head -> store state

## Control

### Startup
- First time setup
```bash
go install github.com/Jille/raftadmin/cmd/raftadmin@latest
./control --bootstrap-raft <bind-address@1> <node-name@1>
./control <bind-address@2> <node-name@2>
# ...

raftadmin <bind-address@1> add_voter <node-name@2> <bind-address@2> 0
# ...
```
- Later runs
State is stored in cwd/runtime/node_name directory.
So all nodes should be rerun withwout the `--bootstrap-raft` argument.
