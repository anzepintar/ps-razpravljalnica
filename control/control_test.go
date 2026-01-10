package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func ValidateNodeAddress(addr string) error {
	if addr == "" {
		return &ValidationError{Field: "address", Message: "Missing address"}
	}
	if len(addr) > 255 {
		return &ValidationError{Field: "address", Message: "Address too long"}
	}
	if !utf8.ValidString(addr) {
		return &ValidationError{Field: "address", Message: "Invalid UTF-8 in address"}
	}
	if strings.TrimSpace(addr) == "" {
		return &ValidationError{Field: "address", Message: "Address cannot be only whitespace"}
	}
	return nil
}

func ValidateNodeID(id int64) error {
	if id < 0 {
		return &ValidationError{Field: "node_id", Message: "Node ID cannot be negative"}
	}
	return nil
}

func ValidateRaftID(id string) error {
	if id == "" {
		return &ValidationError{Field: "raft_id", Message: "Missing Raft ID"}
	}
	if len(id) > 100 {
		return &ValidationError{Field: "raft_id", Message: "Raft ID too long"}
	}
	if strings.TrimSpace(id) == "" {
		return &ValidationError{Field: "raft_id", Message: "Raft ID cannot be only whitespace"}
	}
	return nil
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func TestValidateNodeAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"veljavni naslov", "localhost:6000", false},
		{"IP naslov", "192.168.1.1:8080", false},
		{"prazno", "", true},
		{"samo presledki", "   ", true},
		{"predolgo", strings.Repeat("a", 256), true},
		{"maksimalna dolžina", strings.Repeat("a", 255), false},
		{"unicode", "ŽČP:8080", false},
		{"emoji 😋", "😋server:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNodeAddress(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNodeAddress(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNodeID(t *testing.T) {
	tests := []struct {
		name    string
		id      int64
		wantErr bool
	}{
		{"veljavni ID 0", 0, false},
		{"veljavni ID pozitiven", 123, false},
		{"veljavni ID velik", 9223372036854775807, false},
		{"negativni ID", -1, true},
		{"zelo negativni ID", -9223372036854775808, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNodeID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNodeID(%d) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateRaftID(t *testing.T) {
	tests := []struct {
		name    string
		raftID  string
		wantErr bool
	}{
		{"veljavni ID", "node1", false},
		{"številčni ID", "12345", false},
		{"prazno", "", true},
		{"samo presledki", "   ", true},
		{"predolgo", strings.Repeat("a", 101), true},
		{"maksimalna dolžina", strings.Repeat("a", 100), false},
		{"posebni znaki", "node-1_test", false},
		{"emoji 😋", "😋node", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRaftID(tt.raftID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRaftID(%q) error = %v, wantErr %v", tt.raftID, err, tt.wantErr)
			}
		})
	}
}

func TestControlPlaneServer_IdToNode(t *testing.T) {
	srv := &ControlPlaneServer{
		Nodes: []Node{
			{id: 0, address: "localhost:8001"},
			{id: 1, address: "localhost:8002"},
			{id: 5, address: "localhost:8003"},
		},
	}

	tests := []struct {
		name        string
		id          int64
		wantNil     bool
		wantAddress string
	}{
		{"obstaja id 0", 0, false, "localhost:8001"},
		{"obstaja id 1", 1, false, "localhost:8002"},
		{"obstaja id 5", 5, false, "localhost:8003"},
		{"ne obstaja id 2", 2, true, ""},
		{"ne obstaja id 100", 100, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := srv.idToNode(tt.id)
			if tt.wantNil && result != nil {
				t.Errorf("idToNode(%d) = %v, want nil", tt.id, result)
			}
			if !tt.wantNil && result == nil {
				t.Errorf("idToNode(%d) = nil, want non-nil", tt.id)
			}
			if !tt.wantNil && result != nil && result.address != tt.wantAddress {
				t.Errorf("idToNode(%d).address = %q, want %q", tt.id, result.address, tt.wantAddress)
			}
		})
	}
}

func TestNodeStruct(t *testing.T) {
	node := Node{id: 42, address: "localhost:9000"}

	if node.id != 42 {
		t.Errorf("Node.id = %d, want 42", node.id)
	}
	if node.address != "localhost:9000" {
		t.Errorf("Node.address = %q, want %q", node.address, "localhost:9000")
	}
}

func TestSnapshotStruct(t *testing.T) {
	nodes := []Node{
		{id: 0, address: "localhost:8001"},
		{id: 1, address: "localhost:8002"},
	}
	snapshot := Snapshot{idx: 5, nodes: nodes}

	if snapshot.idx != 5 {
		t.Errorf("Snapshot.idx = %d, want 5", snapshot.idx)
	}
	if len(snapshot.nodes) != 2 {
		t.Errorf("len(Snapshot.nodes) = %d, want 2", len(snapshot.nodes))
	}
}

func TestSnapshotRelease(t *testing.T) {
	snapshot := &Snapshot{idx: 0, nodes: nil}
	snapshot.Release()
}

func FuzzValidateNodeAddress(f *testing.F) {
	f.Add("localhost:6000")
	f.Add("")
	f.Add("   ")
	f.Add(strings.Repeat("a", 255))
	f.Add(strings.Repeat("a", 256))
	f.Add("192.168.1.1:8080")
	f.Add("😋:8080")
	f.Add("\x00\x01\x02")

	f.Fuzz(func(t *testing.T, addr string) {
		err := ValidateNodeAddress(addr)

		if addr == "" && err == nil {
			t.Error("prazen naslov bi moral vrniti napako")
		}

		if len(addr) > 255 && err == nil {
			t.Error("predolg naslov bi moral vrniti napako")
		}

		if strings.TrimSpace(addr) == "" && err == nil {
			t.Error("naslov samo s presledki bi moral vrniti napako")
		}
	})
}

func FuzzValidateNodeID(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(1))
	f.Add(int64(-1))
	f.Add(int64(9223372036854775807))
	f.Add(int64(-9223372036854775808))

	f.Fuzz(func(t *testing.T, id int64) {
		err := ValidateNodeID(id)

		if id < 0 && err == nil {
			t.Error("negativni ID bi moral vrniti napako")
		}

		if id >= 0 && err != nil {
			t.Errorf("nenegativni ID %d ne bi smel vrniti napake", id)
		}
	})
}

func FuzzValidateRaftID(f *testing.F) {
	f.Add("node1")
	f.Add("")
	f.Add("   ")
	f.Add(strings.Repeat("a", 100))
	f.Add(strings.Repeat("a", 101))
	f.Add("😋")
	f.Add("\x00\x01\x02")

	f.Fuzz(func(t *testing.T, id string) {
		err := ValidateRaftID(id)

		if id == "" && err == nil {
			t.Error("prazen Raft ID bi moral vrniti napako")
		}

		if len(id) > 100 && err == nil {
			t.Error("predolg Raft ID bi moral vrniti napako")
		}

		if strings.TrimSpace(id) == "" && err == nil {
			t.Error("Raft ID samo s presledki bi moral vrniti napako")
		}
	})
}
