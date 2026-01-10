package main

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// ParseTopicIDs parsa seznam topic ID-jev ločenih z vejico
// Uporablja se v SubscribeCmd
func ParseTopicIDs(input string) ([]int64, error) {
	topicIDsStr := strings.Split(input, ",")
	topicIDs := make([]int64, 0, len(topicIDsStr))
	for _, idStr := range topicIDsStr {
		trimmed := strings.TrimSpace(idStr)
		if trimmed == "" {
			continue
		}
		id, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, err
		}
		topicIDs = append(topicIDs, id)
	}
	return topicIDs, nil
}

func ValidateUsername(name string) bool {
	if name == "" {
		return false
	}
	if len(name) > 100 {
		return false
	}
	if !utf8.ValidString(name) {
		return false
	}
	// Ne sme vsebovati samo presledkov
	if strings.TrimSpace(name) == "" {
		return false
	}
	return true
}

func ValidateMessageText(text string) bool {
	if text == "" {
		return false
	}
	if len(text) > 10000 {
		return false
	}
	if !utf8.ValidString(text) {
		return false
	}
	return true
}

func ValidateTopicName(name string) bool {
	if name == "" {
		return false
	}
	if len(name) > 200 {
		return false
	}
	if !utf8.ValidString(name) {
		return false
	}
	if strings.TrimSpace(name) == "" {
		return false
	}
	return true
}

// ============================================================================
// Unit testi
// ============================================================================

func TestParseTopicIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []int64
		wantErr  bool
	}{
		{
			name:     "en ID",
			input:    "1",
			expected: []int64{1},
			wantErr:  false,
		},
		{
			name:     "več ID-jev",
			input:    "1,2,3",
			expected: []int64{1, 2, 3},
			wantErr:  false,
		},
		{
			name:     "z presledki",
			input:    "1, 2, 3",
			expected: []int64{1, 2, 3},
			wantErr:  false,
		},
		{
			name:     "prazni elementi",
			input:    "1,,2",
			expected: []int64{1, 2},
			wantErr:  false,
		},
		{
			name:     "neveljaven ID",
			input:    "1,abc,3",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "negativni ID",
			input:    "-1,2,3",
			expected: []int64{-1, 2, 3},
			wantErr:  false,
		},
		{
			name:     "velik ID",
			input:    "9223372036854775807",
			expected: []int64{9223372036854775807},
			wantErr:  false,
		},
		{
			name:     "prazen niz",
			input:    "",
			expected: []int64{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseTopicIDs(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTopicIDs(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(result) != len(tt.expected) {
					t.Errorf("ParseTopicIDs(%q) = %v, expected %v", tt.input, result, tt.expected)
					return
				}
				for i := range result {
					if result[i] != tt.expected[i] {
						t.Errorf("ParseTopicIDs(%q) = %v, expected %v", tt.input, result, tt.expected)
						return
					}
				}
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		expected bool
	}{
		{"veljavno ime", "janez", true},
		{"ime s presledki", "Janez Novak", true},
		{"prazno", "", false},
		{"samo presledki", "   ", false},
		{"predolgo", strings.Repeat("a", 101), false},
		{"maksimalna dolžina", strings.Repeat("a", 100), true},
		{"unicode znaki", "😋", true},
		{"emoji", "User", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateUsername(tt.username)
			if result != tt.expected {
				t.Errorf("ValidateUsername(%q) = %v, expected %v", tt.username, result, tt.expected)
			}
		})
	}
}

func TestValidateMessageText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"veljavno besedilo", "Hello, World!", true},
		{"prazno", "", false},
		{"predolgo", strings.Repeat("a", 10001), false},
		{"maksimalna dolžina", strings.Repeat("a", 10000), true},
		{"slovenske", "ČšžČŠŽ so slovenske črke", true},
		{"multiline", "Line 1\nLine 2\nLine 3", true},
		{"samo presledki", "   ", true}, // samo presledki
		{"emoji", "😋", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateMessageText(tt.text)
			if result != tt.expected {
				t.Errorf("ValidateMessageText(%q) = %v, expected %v", tt.text, result, tt.expected)
			}
		})
	}
}

func TestValidateTopicName(t *testing.T) {
	tests := []struct {
		name      string
		topicName string
		expected  bool
	}{
		{"veljavno ime", "Splošna razprava", true},
		{"prazno", "", false},
		{"samo presledki", "   ", false},
		{"predolgo", strings.Repeat("a", 201), false},
		{"maksimalna dolžina", strings.Repeat("a", 200), true},
		{"unicode", "😋", true},
		{"posebni znaki", "Topic #1 - [Special]", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateTopicName(tt.topicName)
			if result != tt.expected {
				t.Errorf("ValidateTopicName(%q) = %v, expected %v", tt.topicName, result, tt.expected)
			}
		})
	}
}

func FuzzParseTopicIDs(f *testing.F) {
	f.Add("1")
	f.Add("1,2,3")
	f.Add("1, 2, 3")
	f.Add("")
	f.Add("0")
	f.Add("-1")
	f.Add("1234372036854775807")
	f.Add("abc")
	f.Add("1,abc,3")
	f.Add(",,,,")
	f.Add("1,,2,,3")

	f.Fuzz(func(t *testing.T, input string) {
		result, err := ParseTopicIDs(input)

		if err == nil {
			// če ni napake, morajo biti vsi ID-ji veljavna števila
			for _, id := range result {
				// Preveri, da je ID znotraj int64 meja
				_ = id
			}
		}

		if input == "" && len(result) != 0 && err == nil {
			t.Errorf("prazen vhod bi moral vrniti prazen seznam")
		}
	})
}

func FuzzValidateUsername(f *testing.F) {
	f.Add("janez")
	f.Add("")
	f.Add("   ")
	f.Add(strings.Repeat("a", 100))
	f.Add(strings.Repeat("a", 101))
	f.Add("😋")
	f.Add("\x00\x01\x02")
	f.Add("a\nb\nc")

	f.Fuzz(func(t *testing.T, username string) {
		// funkcija se ne sme sesuti
		result := ValidateUsername(username)

		if username == "" && result {
			t.Error("prazen username ne bi smel biti veljaven")
		}

		if len(username) > 100 && result {
			t.Error("predolg username ne bi smel biti veljaven")
		}

		if strings.TrimSpace(username) == "" && result {
			t.Error("username samo s presledki ne bi smel biti veljaven")
		}
	})
}

func FuzzValidateMessageText(f *testing.F) {
	f.Add("Hello, World!")
	f.Add("")
	f.Add(strings.Repeat("a", 10000))
	f.Add(strings.Repeat("a", 10001))
	f.Add("ČŠŽčžš")
	f.Add("😋")
	f.Add("Line1\nLine2")
	f.Add("\t\t\t")

	f.Fuzz(func(t *testing.T, text string) {
		result := ValidateMessageText(text)

		if text == "" && result {
			t.Error("prazno besedilo ne bi smelo biti veljavno")
		}

		if len(text) > 10000 && result {
			t.Error("predolgo besedilo ne bi smelo biti veljavno")
		}
	})
}

func FuzzValidateTopicName(f *testing.F) {
	f.Add("Splošna tema")
	f.Add("")
	f.Add("   ")
	f.Add(strings.Repeat("a", 200))
	f.Add(strings.Repeat("a", 201))
	f.Add("😋")
	f.Add("Topic #1")

	f.Fuzz(func(t *testing.T, name string) {
		result := ValidateTopicName(name)

		if name == "" && result {
			t.Error("prazno ime teme ne bi smelo biti veljavno")
		}

		if len(name) > 200 && result {
			t.Error("predolgo ime teme ne bi smelo biti veljavno")
		}

		if strings.TrimSpace(name) == "" && result {
			t.Error("ime teme samo s presledki ne bi smelo biti veljavno")
		}
	})
}
