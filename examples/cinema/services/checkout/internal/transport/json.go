package transport

import (
	"encoding/json"
	"github.com/marcelsud/keel/sdk"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func Parse(raw json.RawMessage, message proto.Message) error {
	if err := protojson.Unmarshal(raw, message); err != nil {
		return &sdk.Error{Code: 400, Message: "invalid request fields"}
	}
	return nil
}

func Reply(message proto.Message, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	data, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(message)
	return json.RawMessage(data), err
}
