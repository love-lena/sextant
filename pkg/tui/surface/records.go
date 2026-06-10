package surface

import (
	"encoding/json"

	"github.com/love-lena/sextant/pkg/wire"
)

// The lexicon $type discriminators the surfaces read and write. They match the
// records in protocol/lexicons; a surface renders a record it recognises and
// falls back to a raw view for one it does not.
const (
	typeChatMessage = "chat.message"
	typeDocument    = "document"
)

// chatMessage is the chat.message lexicon a message-stream renders and a compose
// publishes: a line of text, optionally replying to another frame. The author is
// the frame author (bus-stamped), never a record field, so it is not represented
// here. See protocol/lexicons/chat.message.json.
type chatMessage struct {
	Type    string `json:"$type"`
	Text    string `json:"text"`
	ReplyTo string `json:"replyTo,omitempty"`
}

// parseChatMessage decodes a frame record as a chat.message, returning false for
// a record that is not one (a different $type, or undecodable). The stream uses
// the false case to fall back to a raw render rather than dropping the line.
func parseChatMessage(record wire.Lexicon) (chatMessage, bool) {
	if len(record) == 0 {
		return chatMessage{}, false
	}
	var m chatMessage
	if err := json.Unmarshal(record, &m); err != nil {
		return chatMessage{}, false
	}
	if m.Type != typeChatMessage {
		return chatMessage{}, false
	}
	return m, true
}

// marshalChatMessage builds the chat.message record a compose publishes: the
// $type plus the typed text, with an optional replyTo. The bus stamps the author
// and id; the client supplies only this record.
func marshalChatMessage(text, replyTo string) (json.RawMessage, error) {
	return json.Marshal(chatMessage{Type: typeChatMessage, Text: text, ReplyTo: replyTo})
}

// document is the document lexicon the artifact surface renders: a titled
// Markdown body. See protocol/lexicons/document.json.
type document struct {
	Type  string `json:"$type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// parseDocument decodes an artifact record as a document, returning false for a
// record that is not one. The reader uses the false case to show the raw record
// rather than rendering an empty page.
func parseDocument(record wire.Lexicon) (document, bool) {
	if len(record) == 0 {
		return document{}, false
	}
	var d document
	if err := json.Unmarshal(record, &d); err != nil {
		return document{}, false
	}
	if d.Type != typeDocument {
		return document{}, false
	}
	return d, true
}
