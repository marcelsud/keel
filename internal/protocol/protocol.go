package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
)

const (
	Prefix = "keel.v1"
	Events = "KEEL_EVENTS"
	Jobs   = "KEEL_JOBS"
	Bucket = "KEEL_DATA"
)

var Operations = []string{"kv_get", "kv_set", "kv_cas", "kv_delete", "collection_insert", "collection_list", "collection_get", "collection_update", "collection_delete", "publish", "enqueue", "consumer"}
var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func ValidName(name string) bool { return identifier.MatchString(name) }

func Encode(value string) string {
	if value == "" {
		return "_"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func ActionSubject(service, action string) string {
	return Prefix + ".actions." + service + "." + action
}

func ConsumerName(kind, topic, name string) string {
	return fmt.Sprintf("c_%x", sha256.Sum256([]byte(kind+"\x00"+topic+"\x00"+name)))
}

type Request struct {
	Namespace      string          `json:"namespace,omitempty"`
	Key            string          `json:"key,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	Revision       uint64          `json:"revision,omitempty"`
	Collection     string          `json:"collection,omitempty"`
	ID             string          `json:"id,omitempty"`
	Document       json.RawMessage `json:"document,omitempty"`
	IfNeverExisted bool            `json:"ifNeverExisted,omitempty"`
	Topic          string          `json:"topic,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	Name           string          `json:"name,omitempty"`
}

type Value struct {
	Found    bool            `json:"found"`
	Value    json.RawMessage `json:"value,omitempty"`
	Revision uint64          `json:"revision,omitempty"`
}

type Consumer struct {
	Stream  string `json:"stream"`
	Name    string `json:"consumer"`
	Subject string `json:"subject"`
}

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string        { return e.Message }
func BadRequest(message string) error { return &Error{Code: 400, Message: message} }
