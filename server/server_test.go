package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// pomožne funkcije za validacijo (izvožene za teste)

func ValidateTopicName(name string) error {
	if len(name) == 0 {
		return &ValidationError{Field: "topic_name", Message: "Missing Topic name"}
	}
	if len(name) > 200 {
		return &ValidationError{Field: "topic_name", Message: "Topic name too long"}
	}
	if !utf8.ValidString(name) {
		return &ValidationError{Field: "topic_name", Message: "Invalid UTF-8 in topic name"}
	}
	return nil
}

func ValidateUserName(name string) error {
	if len(name) == 0 {
		return &ValidationError{Field: "user_name", Message: "Missing User name"}
	}
	if len(name) > 100 {
		return &ValidationError{Field: "user_name", Message: "User name too long"}
	}
	if !utf8.ValidString(name) {
		return &ValidationError{Field: "user_name", Message: "Invalid UTF-8 in user name"}
	}
	if strings.TrimSpace(name) == "" {
		return &ValidationError{Field: "user_name", Message: "User name cannot be only whitespace"}
	}
	return nil
}

func ValidateMessageText(text string) error {
	if len(text) == 0 {
		return &ValidationError{Field: "message_text", Message: "Empty message not permitted"}
	}
	if len(text) > 10000 {
		return &ValidationError{Field: "message_text", Message: "Message too long"}
	}
	if !utf8.ValidString(text) {
		return &ValidationError{Field: "message_text", Message: "Invalid UTF-8 in message"}
	}
	return nil
}

func ValidateID(id int64, fieldName string) error {
	if id < 0 {
		return &ValidationError{Field: fieldName, Message: fieldName + " cannot be negative"}
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

// Unit testi

func TestValidateTopicName(t *testing.T) {
	tests := []struct {
		name      string
		topicName string
		wantErr   bool
	}{
		{"veljavno ime", "Splošna razprava", false},
		{"prazno", "", true},
		{"predolgo", strings.Repeat("a", 201), true},
		{"maksimalna dolžina", strings.Repeat("a", 200), false},
		{"unicode", "😋", false},
		{"posebni znaki", "Topic #1 - [Special]", false},
		{"emoji", "😋 Tema", false},
		{"presledki dovoljeni", "  Topic  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTopicName(tt.topicName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTopicName(%q) error = %v, wantErr %v", tt.topicName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateUserName(t *testing.T) {
	tests := []struct {
		name     string
		userName string
		wantErr  bool
	}{
		{"veljavno ime", "janez", false},
		{"ime s presledki", "Janez Novak", false},
		{"prazno", "", true},
		{"samo presledki", "   ", true},
		{"predolgo", strings.Repeat("a", 101), true},
		{"maksimalna dolžina", strings.Repeat("a", 100), false},
		{"unicode znaki", "😋", false},
		{"emoji", "😋User", false},
		{"tab in newline", "User\tName\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserName(tt.userName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUserName(%q) error = %v, wantErr %v", tt.userName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMessageText(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{"veljavno besedilo", "Hello, World!", false},
		{"prazno", "", true},
		{"predolgo", strings.Repeat("a", 10001), true},
		{"maksimalna dolžina", strings.Repeat("a", 10000), false},
		{"slovenske", "ČŠŽčšž so slovenske črke", false},
		{"multiline", "Line 1\nLine 2\nLine 3", false},
		{"samo presledki", "   ", false},
		{"emoji", "😋", false},
		{"samo tab", "\t\t\t", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMessageText(tt.text)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMessageText(%q) error = %v, wantErr %v", tt.text, err, tt.wantErr)
			}
		})
	}
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		name      string
		id        int64
		fieldName string
		wantErr   bool
	}{
		{"veljavni ID 0", 0, "user_id", false},
		{"veljavni ID pozitiven", 123, "topic_id", false},
		{"veljavni ID velik", 9223372036854775807, "message_id", false},
		{"negativni ID", -1, "user_id", true},
		{"zelo negativni ID", -9223372036854775808, "topic_id", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateID(tt.id, tt.fieldName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateID(%d, %q) error = %v, wantErr %v", tt.id, tt.fieldName, err, tt.wantErr)
			}
		})
	}
}

func TestValidationErrorMessage(t *testing.T) {
	err := &ValidationError{Field: "test_field", Message: "test message"}
	if err.Error() != "test message" {
		t.Errorf("ValidationError.Error() = %q, want %q", err.Error(), "test message")
	}
}

// Testi za MessageBoardServer pomožne metode

func TestMessageBoardServer_TopicExist(t *testing.T) {
	srv := &MessageBoardServer{
		topics: []Topic{
			{name: "Topic 1", messages: map[int64]Message{}},
			{name: "Topic 2", messages: map[int64]Message{}},
		},
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
	}{
		{"obstaja prvi", 0, false},
		{"obstaja drugi", 1, false},
		{"ne obstaja", 2, true},
		{"ne obstaja velik", 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.topicExist(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("topicExist(%d) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestMessageBoardServer_UserExist(t *testing.T) {
	srv := &MessageBoardServer{
		users: []User{
			{name: "User 1"},
			{name: "User 2"},
			{name: "User 3"},
		},
	}

	tests := []struct {
		name    string
		id      int64
		wantErr bool
	}{
		{"obstaja prvi", 0, false},
		{"obstaja zadnji", 2, false},
		{"ne obstaja", 3, true},
		{"ne obstaja velik", 1000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.userExist(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("userExist(%d) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestMessageBoardServer_MsgExist(t *testing.T) {
	srv := &MessageBoardServer{
		topics: []Topic{
			{
				name: "Topic 1",
				messages: map[int64]Message{
					0: {user: 0, text: "msg 0"},
					5: {user: 1, text: "msg 5"},
				},
			},
		},
	}

	tests := []struct {
		name      string
		topicID   int64
		messageID int64
		wantErr   bool
	}{
		{"obstaja msg 0", 0, 0, false},
		{"obstaja msg 5", 0, 5, false},
		{"ne obstaja msg 1", 0, 1, true},
		{"ne obstaja msg 100", 0, 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.msgExist(tt.topicID, tt.messageID)
			if (err != nil) != tt.wantErr {
				t.Errorf("msgExist(%d, %d) error = %v, wantErr %v", tt.topicID, tt.messageID, err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// Fuzz testi
// ============================================================================

func FuzzValidateTopicName(f *testing.F) {
	f.Add("Splošna razprava")
	f.Add("")
	f.Add("   ")
	f.Add(strings.Repeat("a", 200))
	f.Add(strings.Repeat("a", 201))
	f.Add("Topic #1")
	f.Add("\x00\x01\x02")
	f.Add("😋")

	f.Fuzz(func(t *testing.T, name string) {
		err := ValidateTopicName(name)

		if name == "" && err == nil {
			t.Error("prazno ime teme bi moralo vrniti napako")
		}

		if len(name) > 200 && err == nil {
			t.Error("predolgo ime teme bi moralo vrniti napako")
		}
	})
}

func FuzzValidateUserName(f *testing.F) {
	f.Add("janez")
	f.Add("")
	f.Add("   ")
	f.Add(strings.Repeat("a", 100))
	f.Add(strings.Repeat("a", 101))
	f.Add("😋")
	f.Add("\x00\x01\x02")
	f.Add("a\nb\nc")
	f.Add("\t\t\t")

	f.Fuzz(func(t *testing.T, name string) {
		err := ValidateUserName(name)

		if name == "" && err == nil {
			t.Error("prazno ime bi moralo vrniti napako")
		}

		if len(name) > 100 && err == nil {
			t.Error("predolgo ime bi moralo vrniti napako")
		}

		if strings.TrimSpace(name) == "" && err == nil {
			t.Error("ime samo s presledki bi moralo vrniti napako")
		}
	})
}

func FuzzValidateMessageText(f *testing.F) {
	f.Add("Hello, World!")
	f.Add("")
	f.Add(strings.Repeat("a", 10000))
	f.Add(strings.Repeat("a", 10001))
	f.Add("ČšžČŠŽ")
	f.Add("😋")
	f.Add("Line1\nLine2")
	f.Add("\t\t\t")
	f.Add("\x00\x01\x02")

	f.Fuzz(func(t *testing.T, text string) {
		err := ValidateMessageText(text)

		if text == "" && err == nil {
			t.Error("prazno besedilo bi moralo vrniti napako")
		}

		if len(text) > 10000 && err == nil {
			t.Error("predolgo besedilo bi moralo vrniti napako")
		}
	})
}

func FuzzValidateID(f *testing.F) {
	// Seed korpus
	f.Add(int64(0))
	f.Add(int64(1))
	f.Add(int64(-1))
	f.Add(int64(9223372036854775807))
	f.Add(int64(-9223372036854775808))
	f.Add(int64(123456789))

	f.Fuzz(func(t *testing.T, id int64) {
		err := ValidateID(id, "test_id")

		if id < 0 && err == nil {
			t.Error("negativni ID bi moral vrniti napako")
		}

		if id >= 0 && err != nil {
			t.Errorf("nenegativni ID %d ne bi smel vrniti napake", id)
		}
	})
}
